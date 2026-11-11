package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
	"nyamangaserver.local/server/internal/textkey"
	"slices"
	"strings"
)

func scanSeriesProposal(row scanner) (rec.SeriesProposal, error) {
	var raw, state, key, match, scope string
	var revision int64
	var primary bool
	e := row.Scan(&raw, &state, &revision, &primary, &key, &match, &scope)
	if errors.Is(e, sql.ErrNoRows) {
		return rec.SeriesProposal{}, model.ErrNotFound
	}
	if e != nil {
		return rec.SeriesProposal{}, e
	}
	var v rec.StoredSeriesProposal
	if e = json.Unmarshal([]byte(raw), &v); e != nil {
		return v.Proposal, e
	}
	p := v.Proposal
	p.Presentation = v.Presentation
	p.Expected = v.Expected
	p.Inputs = v.Inputs
	p.Key = match
	if p.Key == "" {
		p.Key = key
	}
	p.StorageKey, p.Scope = key, scope
	p.State = state
	p.Revision = revision
	p.Primary = primary
	p.CanUndo = state == "accepted"
	return p, nil
}

const seriesProposalColumns = "plan_json,state,revision,primary_plan,proposal_key,match_key,scope_key"

func storedSeries(p rec.SeriesProposal) string {
	return seriesJSON(rec.StoredSeriesProposal{Presentation: p.Presentation, Proposal: p, Expected: p.Expected, Inputs: p.Inputs})
}

func seriesPlan(raw rec.SeriesRawPlan, r rec.SeriesRun, primary bool) (rec.SeriesProposal, error) {
	p := rec.SeriesProposal{JobID: r.JobID, LibraryID: r.LibraryID, Kind: raw.Kind, Name: strings.TrimSpace(raw.Name), Tier: raw.Tier, Risks: raw.Risks, Primary: primary, State: "pending", Revision: 1, NewIDs: slices.Clone(raw.NewIDs), ExistingIDs: []string{}, Expected: map[string]int64{}, Inputs: map[string]string{}}
	if raw.Kind == "join_series" {
		p.Kind = "add_to_series"
	}
	if p.Kind != "create_series" && p.Kind != "add_to_series" {
		return p, model.ErrInput
	}
	if len(raw.WorkIDs) < 2 || len(raw.WorkIDs) > 1000 || len(raw.NewIDs) == 0 || len(slices.Compact(sortedSeriesIDs(raw.WorkIDs))) != len(raw.WorkIDs) {
		return p, model.ErrInput
	}
	members := map[string]model.Work{}
	all := map[string]bool{}
	for _, w := range r.Context.Works {
		members[w.ID] = w
	}
	if p.Kind == "add_to_series" {
		for _, s := range r.Context.Series {
			if s.ID == raw.Target {
				p.Target = s.ID
				p.Name = s.Name
				p.ExistingIDs = slices.Clone(s.WorkIDs)
				p.Expected["series:"+s.ID] = s.Revision
			}
		}
		if p.Target == "" {
			return p, model.ErrNotFound
		}
	}
	for _, id := range raw.WorkIDs {
		w, ok := members[id]
		if !ok {
			return p, model.ErrNotFound
		}
		all[id] = true
		if w.LibraryID != p.LibraryID {
			return p, model.ErrLibraryConflict
		}
		existing := slices.Contains(p.ExistingIDs, id)
		if (!existing && w.SeriesID != "") || (existing && w.SeriesID != p.Target) {
			return p, model.ErrSeriesConflict
		}
		if !existing && !slices.Contains(p.NewIDs, id) {
			return p, model.ErrInput
		}
		p.Expected["work:"+id] = w.Revision
	}
	if len(p.ExistingIDs)+len(p.NewIDs) != len(all) {
		return p, model.ErrInput
	}
	for _, id := range append(slices.Clone(p.ExistingIDs), p.NewIDs...) {
		if !all[id] {
			return p, model.ErrInput
		}
	}
	for _, ed := range r.Pool.Editions {
		if all[ed.WorkID] {
			p.Inputs[ed.Book.ID] = rec.SeriesInput(ed)
		}
	}
	if p.Kind == "create_series" && len(p.NewIDs) < 2 {
		return p, model.ErrInput
	}
	p.Presentation = r.Presentation
	if p.Name == "" && r.Presentation == "" {
		p.Name = "待命名系列"
	}
	name := []rune(p.Name)
	if len(name) > 200 {
		p.Name = string(name[:200])
	}
	order := slices.Clone(raw.Order.Suggested)
	if !sameMembers(order, raw.WorkIDs) {
		p.OrderUncertain = true
		order = slices.Clone(p.NewIDs)
		slices.SortFunc(order, func(a, b string) int {
			if c := bytes.Compare(textkey.Natural(members[a].Name), textkey.Natural(members[b].Name)); c != 0 {
				return c
			}
			return strings.Compare(a, b)
		})
		order = append(slices.Clone(p.ExistingIDs), order...)
	}
	if p.Kind == "add_to_series" && !slices.Equal(existingSeriesOrder(order, p.ExistingIDs), p.ExistingIDs) {
		return p, model.ErrInput
	}
	p.Order = order
	if r.Presentation == rec.SeriesPresentationVersion {
		works := []rec.SeriesPresentationWork{}
		for _, id := range sortedSeriesIDs(raw.WorkIDs) {
			w := rec.SeriesPresentationWork{ID: id, Titles: []string{}}
			for _, ed := range r.Pool.Editions {
				if ed.WorkID == id {
					w.Titles = append(w.Titles, ed.Book.TitleOriginal, ed.Book.Title)
				}
			}
			works = append(works, w)
		}
		display := rec.PresentSeries(works, p.ExistingIDs)
		p.Order = display.Order
		p.OrderBasis = display.Basis
		p.OrderUncertain = display.Uncertain
		if p.Kind == "create_series" {
			p.Name = display.Name
			p.NameRequired = display.NameRequired
		}
		if len([]rune(p.Name)) > 200 {
			p.Name = string([]rune(p.Name)[:200])
		}
	}
	p.MemberCount = len(order)
	p.Key = rec.Fingerprint([]any{rec.SeriesAlgorithm, p.Kind, p.Target, sortedSeriesIDs(raw.WorkIDs), p.Inputs})
	if p.Presentation != "" {
		p.Key = rec.Fingerprint([]any{p.Key, p.Presentation})
	}
	p.ID = "series-plan:" + p.Key
	if p.Kind == "create_series" {
		p.Target = "series:" + p.Key[:32]
	}
	return p, nil
}
func existingSeriesOrder(order, existing []string) []string {
	out := []string{}
	for _, id := range order {
		if slices.Contains(existing, id) {
			out = append(out, id)
		}
	}
	return out
}

func saveSeriesPlan(ctx context.Context, tx *sql.Tx, p rec.SeriesProposal, custom bool) error {
	var e error
	p, e = identifySeriesPlan(ctx, tx, p)
	if e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `INSERT INTO series_proposals(id,job_id,library_id,proposal_key,kind,target_series_id,tier,primary_plan,customized,revision,state,plan_json,created_at,match_key,scope_key) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,unixepoch(),?,?) ON CONFLICT(id) DO UPDATE SET job_id=excluded.job_id,primary_plan=excluded.primary_plan,customized=excluded.customized,revision=excluded.revision,state=excluded.state,plan_json=excluded.plan_json`, p.ID, p.JobID, p.LibraryID, p.StorageKey, p.Kind, p.Target, p.Tier, p.Primary, custom, p.Revision, p.State, storedSeries(p), p.Key, p.Scope); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, "DELETE FROM series_proposal_members WHERE proposal_id=?", p.ID); e != nil {
		return e
	}
	for i, id := range p.Order {
		role := "new"
		if slices.Contains(p.ExistingIDs, id) {
			role = "existing"
		}
		if _, e := tx.ExecContext(ctx, "INSERT INTO series_proposal_members VALUES(?,?,?,?)", p.ID, id, i, role); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) PublishSeries(ctx context.Context, j model.Job, r rec.SeriesRun, result rec.SeriesResult, refresh bool) (publishErr error) {
	defer func() { publishErr = seriesStorageError(publishErr) }()
	if result.Algorithm != rec.SeriesAlgorithm || len(result.Proposals) > 10000 || len(result.Pending) > len(r.Pool.Works) {
		return model.ErrInput
	}
	// Construct outside the write transaction. Labels and historical feedback are
	// never passed to the clustering engine or used to alter its thresholds.
	plans := []rec.SeriesProposal{}
	seen := map[string]int{}
	taken := map[string]bool{}
	for _, raw := range result.Proposals {
		p, e := seriesPlan(raw, r, true)
		if e != nil {
			return e
		}
		for _, id := range p.Order {
			if taken[id] {
				return model.ErrInput
			}
			taken[id] = true
		}
		seen[p.Key] = len(plans)
		plans = append(plans, p)
	}
	pendingSeen := map[string]bool{}
	for n := range result.Pending {
		pending := &result.Pending[n]
		if taken[pending.WorkID] || pendingSeen[pending.WorkID] || len(pending.Options) > 3 {
			return model.ErrInput
		}
		pendingSeen[pending.WorkID] = true
		for m := range pending.Options {
			option := &pending.Options[m]
			raw := rec.SeriesRawPlan{Kind: option.Kind, WorkIDs: option.WorkIDs, Tier: option.Tier, Risks: option.Risks, Order: option.Order}
			if option.Kind == "join_series" {
				raw.Target = option.Target
				raw.NewIDs = []string{pending.WorkID}
			} else {
				raw.NewIDs = slices.Clone(option.WorkIDs)
				for _, w := range r.Context.Works {
					if w.ID == pending.WorkID {
						raw.Name = w.Name
						break
					}
				}
			}
			p, e := seriesPlan(raw, r, false)
			if e != nil {
				return e
			}
			if at, ok := seen[p.Key]; ok {
				option.ProposalID = plans[at].ID
			} else {
				seen[p.Key] = len(plans)
				plans = append(plans, p)
				option.ProposalID = p.ID
			}
		}
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = enhancementGate(ctx, tx, model.EnhancementSeries); e != nil {
		return e
	}
	if refresh {
		if e = backgroundEnhancementGate(ctx, tx, model.EnhancementSeries); e != nil {
			return e
		}
	}
	if !refresh {
		if e = guard(ctx, tx, j); e != nil {
			return e
		}
		var next, total int
		if e = scopeRow(ctx, tx, "SELECT next_row,json_array_length(pool_json,'$.works') FROM series_discovery_runs WHERE job_id=?", j.ID).Scan(&next, &total); e != nil {
			return e
		}
		if next != total {
			return model.ErrConflict
		}
	} else {
		var n int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM series_discovery_runs d JOIN jobs j ON j.id=d.job_id WHERE d.job_id=? AND d.refresh_required=1 AND d.phase='finished' AND j.state='completed' AND j.generation=?", j.ID, j.Generation).Scan(&n); e != nil {
			return e
		}
		if n != 1 {
			return model.ErrConflict
		}
	}
	token, e := discoveryToken(ctx, tx, r.LibraryID)
	if e != nil {
		return e
	}
	if token != r.Token {
		return rec.ErrStale
	}
	batch, scopeErr := importJobScope(ctx, tx, j.ID)
	if scopeErr != nil {
		return scopeErr
	}
	scope := seriesScope(batch)
	keys := []string{}
	customRows, err := scopeRows(ctx, tx, "SELECT "+seriesProposalColumns+" FROM series_proposals WHERE library_id=? AND scope_key=? AND customized=1 AND state='pending'", r.LibraryID, scope)
	if err != nil {
		return err
	}
	customs := []rec.SeriesProposal{}
	for customRows.Next() {
		p, err := scanSeriesProposal(customRows)
		if err != nil {
			customRows.Close()
			return err
		}
		customs = append(customs, p)
	}
	err = customRows.Err()
	customRows.Close()
	if err != nil {
		return err
	}
	customTaken := map[string]bool{}
	for _, p := range customs {
		if _, err := currentSeriesPlan(ctx, tx, "", p); err == nil {
			keys = append(keys, p.ID)
			for _, id := range p.Order {
				customTaken[id] = true
			}
		}
	}
	targets := map[string]bool{}
	if batch != "" {
		targets, scopeErr = importTargetIDs(ctx, tx, batch, "series")
		if scopeErr != nil {
			return scopeErr
		}
	}
	for _, p := range plans {
		p.Scope = scope
		if batch != "" {
			if !involves(p.NewIDs, targets) {
				continue
			}
			p.ID = "import-series:" + rec.Fingerprint([]string{batch, p.Key})
			if e = linkImportProposal(ctx, tx, batch, "series", p.ID); e != nil {
				return e
			}
		}
		keys = append(keys, p.ID)
		p, e = identifySeriesPlan(ctx, tx, p)
		if e != nil {
			return e
		}
		for _, id := range p.Order {
			if customTaken[id] {
				p.Primary = false
				break
			}
		}
		var decision string
		err := scopeRow(ctx, tx, "SELECT decision FROM series_proposal_decisions WHERE proposal_key=? AND proposal_id=?", p.StorageKey, p.ID).Scan(&decision)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if decision == "dismissed" {
			p.State = "dismissed"
		}
		if decision == "undone" {
			p.State = "undone"
		}
		old, err := scanSeriesProposal(scopeRow(ctx, tx, "SELECT "+seriesProposalColumns+" FROM series_proposals WHERE id=?", p.ID))
		if err != nil && !errors.Is(err, model.ErrNotFound) {
			return err
		}
		feedback, legacy, feedbackErr := seriesFeedback(ctx, tx, p)
		if feedbackErr != nil {
			return feedbackErr
		}
		// A legacy fallback cannot overwrite a restored existing record. New
		// explicit feedback is authoritative across scopes; undo stays local.
		if decision != "undone" && (feedback != "" && (!legacy || errors.Is(err, model.ErrNotFound))) {
			if feedback == "dismissed" {
				p.State = "dismissed"
			} else {
				p.State = "pending"
			}
		}
		if err == nil {
			if old.State == "accepted" {
				continue
			}
			var customized bool
			if e = scopeRow(ctx, tx, "SELECT customized FROM series_proposals WHERE id=?", p.ID).Scan(&customized); e != nil {
				return e
			}
			if customized && old.State == "pending" && sameExpected(old.Expected, p.Expected) {
				continue
			}
			p.Revision = old.Revision
			a, b := old, p
			a.JobID = ""
			b.JobID = ""
			a.Revision = 0
			b.Revision = 0
			a.Primary = false
			b.Primary = false
			if storedSeries(a) != storedSeries(b) {
				p.Revision++
			}
		}
		if e = saveSeriesPlan(ctx, tx, p, false); e != nil {
			return e
		}
	}
	rawKeys := seriesJSON(keys)
	if batch == "" {
		if _, e = tx.ExecContext(ctx, "UPDATE series_proposals SET state='superseded',revision=revision+1 WHERE library_id=? AND scope_key=? AND state='pending' AND id NOT IN(SELECT value FROM json_each(?))", r.LibraryID, scope, rawKeys); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM series_pending_works WHERE job_id=?", j.ID); e != nil {
		return e
	}
	for _, pending := range result.Pending {
		if _, e = tx.ExecContext(ctx, "INSERT INTO series_pending_works VALUES(?,?,?)", j.ID, pending.WorkID, seriesJSON(pending)); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, "UPDATE series_discovery_runs SET phase='finished',refresh_required=0,diagnostics_json=? WHERE job_id=?", seriesJSON(map[string]int{"unavailable_series_count": len(result.Unavailable)}), j.ID); e != nil {
		return e
	}
	return tx.Commit()
}
func sameExpected(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func (s *Store) SeriesProposal(ctx context.Context, id string) (rec.SeriesProposal, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return rec.SeriesProposal{}, e
	}
	defer tx.Rollback()
	p, e := scanSeriesProposal(scopeRow(ctx, tx, "SELECT "+seriesProposalColumns+" FROM series_proposals WHERE id=?", id))
	if e != nil {
		return p, e
	}
	items := []rec.SeriesProposal{p}
	if e = enrichSeriesProposalCovers(ctx, tx, items); e != nil {
		return p, e
	}
	if e = enrichSeriesNewPreviews(ctx, tx, items); e != nil {
		return p, e
	}
	return items[0], tx.Commit()
}
func (s *Store) SeriesProposals(ctx context.Context, job, library, kind, state string, page, size int, filters ...rec.SeriesProposalFilter) (rec.SeriesProposalPage, error) {
	out := rec.SeriesProposalPage{Items: []rec.SeriesProposal{}, Page: page, PageSize: size}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	filter := rec.SeriesProposalFilter{}
	if len(filters) > 0 {
		filter = filters[0]
	}
	where := " FROM series_proposals WHERE (?='' OR job_id=?) AND (?='' OR library_id=?) AND (?='' OR kind=?) AND state=? AND (state<>'pending' OR primary_plan=1 OR ?) AND (?='' OR target_series_id=?)"
	args := []any{job, job, library, library, kind, kind, state, filter.Alternatives, filter.TargetSeriesID, filter.TargetSeriesID}
	if filter.OnlyAlternatives {
		where += " AND primary_plan=0"
	}
	if filter.ImportBatchID != "" {
		where += " AND EXISTS(SELECT 1 FROM import_proposals ip WHERE ip.proposal_id=series_proposals.id AND ip.batch_id=? AND ip.stage='series')"
		args = append(args, filter.ImportBatchID)
	}
	if e = scopeRow(ctx, tx, "SELECT count(*)"+where, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, "SELECT "+seriesProposalColumns+where+" ORDER BY CASE tier WHEN 'strong' THEN 0 ELSE 1 END,id LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		p, err := scanSeriesProposal(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, p)
	}
	e = rows.Err()
	rows.Close()
	if e == nil {
		e = enrichSeriesProposalCovers(ctx, tx, out.Items)
	}
	if e == nil {
		e = enrichSeriesNewPreviews(ctx, tx, out.Items)
	}
	if e == nil {
		e = tx.Commit()
	}
	return out, e
}
func (s *Store) SeriesMembers(ctx context.Context, id string, page, size int) (rec.SeriesMemberPage, error) {
	out := rec.SeriesMemberPage{Items: []rec.SeriesMember{}, Page: page, PageSize: size}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	p, e := scanSeriesProposal(scopeRow(ctx, tx, "SELECT "+seriesProposalColumns+" FROM series_proposals WHERE id=?", id))
	if e != nil {
		return out, e
	}
	out.Revision = p.Revision
	out.Total = len(p.Order)
	rows, e := scopeRows(ctx, tx, `SELECT pm.work_id,COALESCE(w.name_override,eb.title,f.filename_title,'作品已不在当前索引'),COALESCE(w.preferred_edition_id,''),(SELECT count(*) FROM editions e WHERE e.work_id=pm.work_id),pm.role='existing',COALESCE(f.content_revision,0),COALESCE(cs.revision,0),COALESCE(eb.title,f.filename_title,''),COALESCE(json_extract(eb.fields_json,'$.title_original'),'') FROM series_proposal_members pm LEFT JOIN works w ON w.id=pm.work_id LEFT JOIN files f ON f.edition_id=w.preferred_edition_id LEFT JOIN effective_editions eb ON eb.edition_id=w.preferred_edition_id LEFT JOIN cover_selections cs ON cs.edition_id=w.preferred_edition_id WHERE pm.proposal_id=? ORDER BY pm.position LIMIT ? OFFSET ?`, id, size, (page-1)*size)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var m rec.SeriesMember
		if e = rows.Scan(&m.WorkID, &m.Name, &m.EditionID, &m.EditionCount, &m.Existing, &m.ContentRevision, &m.CoverRevision, &m.Title, &m.TitleOriginal); e != nil {
			rows.Close()
			return out, e
		}
		out.Items = append(out.Items, m)
	}
	e = rows.Err()
	rows.Close()
	if e == nil {
		e = tx.Commit()
	}
	return out, e
}
func (s *Store) SeriesPending(ctx context.Context, job string, page, size int) (rec.SeriesPendingPage, error) {
	out := rec.SeriesPendingPage{Items: []rec.SeriesPending{}, Page: page, PageSize: size}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var exists int
	if e = scopeRow(ctx, tx, "SELECT count(*),COALESCE(max(json_extract(diagnostics_json,'$.unavailable_series_count')),0) FROM series_discovery_runs WHERE job_id=?", job).Scan(&exists, &out.Unavailable); e != nil {
		return out, e
	}
	if exists == 0 {
		return out, model.ErrNotFound
	}
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM series_pending_works WHERE job_id=?", job).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, `SELECT sw.options_json,COALESCE(w.name_override,eb.title,f.filename_title,'作品已不在当前索引'),COALESCE(w.preferred_edition_id,''),COALESCE(f.content_revision,0),COALESCE(cs.revision,0),COALESCE(eb.title,f.filename_title,''),COALESCE(json_extract(eb.fields_json,'$.title_original'),'') FROM series_pending_works sw LEFT JOIN works w ON w.id=sw.work_id LEFT JOIN files f ON f.edition_id=w.preferred_edition_id LEFT JOIN effective_editions eb ON eb.edition_id=w.preferred_edition_id LEFT JOIN cover_selections cs ON cs.edition_id=w.preferred_edition_id WHERE sw.job_id=? ORDER BY sw.work_id LIMIT ? OFFSET ?`, job, size, (page-1)*size)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var raw string
		var p rec.SeriesPending
		if e = rows.Scan(&raw, &p.Name, &p.EditionID, &p.ContentRevision, &p.CoverRevision, &p.Title, &p.TitleOriginal); e != nil {
			rows.Close()
			return out, e
		}
		name, edition, content, cover, title, original := p.Name, p.EditionID, p.ContentRevision, p.CoverRevision, p.Title, p.TitleOriginal
		if e = json.Unmarshal([]byte(raw), &p); e != nil {
			rows.Close()
			return out, e
		}
		p.Name = name
		p.Title, p.TitleOriginal = title, original
		p.EditionID = edition
		p.ContentRevision = content
		p.CoverRevision = cover
		out.Items = append(out.Items, p)
	}
	e = rows.Err()
	rows.Close()
	if e == nil {
		e = tx.Commit()
	}
	return out, e
}
