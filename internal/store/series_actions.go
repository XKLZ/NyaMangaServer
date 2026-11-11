package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	rec "nyamangaserver.local/server/internal/recommendation"
	"slices"
	"strings"
)

func currentSeriesPlan(ctx context.Context, tx *sql.Tx, account string, p rec.SeriesProposal) (model.WorkContext, error) {
	for key, want := range p.Expected {
		table := "works"
		id, ok := strings.CutPrefix(key, "work:")
		if !ok {
			table = "series"
			id, ok = strings.CutPrefix(key, "series:")
			if !ok {
				return model.WorkContext{}, model.ErrInput
			}
		}
		var revision int64
		if e := scopeRow(ctx, tx, "SELECT revision FROM "+table+" WHERE id=?", id).Scan(&revision); e != nil || revision != want {
			return model.WorkContext{}, model.ErrConflict
		}
	}
	selected := []string{}
	for id := range p.Inputs {
		selected = append(selected, id)
	}
	if len(selected) == 0 {
		return model.WorkContext{}, rec.ErrStale
	}
	pool, e := recommendationPoolSubset(ctx, tx, []string{p.LibraryID}, "", "", selected)
	if e != nil {
		return model.WorkContext{}, e
	}
	if len(pool.Editions) != len(p.Inputs) {
		return model.WorkContext{}, rec.ErrStale
	}
	for _, ed := range pool.Editions {
		if rec.SeriesInput(ed) != p.Inputs[ed.Book.ID] {
			return model.WorkContext{}, rec.ErrStale
		}
		if e = managementGuard(ctx, tx, ed.Book.ID); e != nil {
			return model.WorkContext{}, e
		}
	}
	series := []string{}
	if p.Kind == "add_to_series" {
		series = append(series, p.Target)
	}
	c, e := loadWorkContext(ctx, tx, account, nil, p.Order, series, false)
	if e != nil {
		return c, e
	}
	if p.Kind == "add_to_series" {
		target, err := loadSeries(ctx, tx, p.Target)
		if err != nil {
			return c, err
		}
		if !slices.Equal(target.WorkIDs, p.ExistingIDs) {
			return c, model.ErrConflict
		}
	}
	for _, w := range c.Works {
		if !slices.Contains(p.Order, w.ID) {
			continue
		}
		if w.LibraryID != p.LibraryID {
			return c, model.ErrLibraryConflict
		}
		if slices.Contains(p.NewIDs, w.ID) && w.SeriesID != "" {
			return c, model.ErrSeriesConflict
		}
	}
	return c, nil
}
func (s *Store) ResolveSeries(ctx context.Context, id string, in rec.SeriesSelection) (rec.SeriesProposal, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return rec.SeriesProposal{}, e
	}
	defer tx.Rollback()
	if e = enhancementGate(ctx, tx, model.EnhancementSeries); e != nil {
		return rec.SeriesProposal{}, e
	}
	p, e := scanSeriesProposal(scopeRow(ctx, tx, "SELECT "+seriesProposalColumns+" FROM series_proposals WHERE id=?", id))
	if e != nil {
		return p, e
	}
	if e = guardImportPlan(ctx, tx, id, "series", "resolve"); e != nil {
		return p, e
	}
	if p.State != "pending" || p.Revision != in.Revision {
		return p, model.ErrConflict
	}
	if len(in.IDs) == 0 || (p.Kind == "create_series" && len(in.IDs) < 2) {
		return p, model.ErrInput
	}
	for _, i := range in.IDs {
		if !slices.Contains(p.NewIDs, i) {
			return p, model.ErrInput
		}
	}
	if _, e = currentSeriesPlan(ctx, tx, "", p); e != nil {
		return p, e
	}
	order := in.Order
	if order == nil {
		order = []string{}
		for _, i := range p.Order {
			if slices.Contains(p.ExistingIDs, i) || slices.Contains(in.IDs, i) {
				order = append(order, i)
			}
		}
	}
	if !sameMembers(order, append(slices.Clone(p.ExistingIDs), in.IDs...)) || !slices.Equal(existingSeriesOrder(order, p.ExistingIDs), p.ExistingIDs) {
		return p, model.ErrInput
	}
	name := p.Name
	if in.Name != nil {
		if p.Kind == "add_to_series" && *in.Name != name {
			return p, model.ErrInput
		}
		name = strings.TrimSpace(*in.Name)
	}
	if name == "" {
		return p, model.ErrInput
	}
	if sameMembers(in.IDs, p.NewIDs) && slices.Equal(order, p.Order) && name == p.Name {
		return p, nil
	}
	if p.OrderBasis != "" && !slices.Equal(order, filterSeriesOrder(p.Order, p.ExistingIDs, in.IDs)) {
		p.OrderBasis = "manual"
		p.OrderUncertain = false
	}
	p.NameRequired = false
	p.NewIDs = slices.Clone(in.IDs)
	p.Order = slices.Clone(order)
	p.Name = name
	p.MemberCount = len(order)
	var poolRaw string
	if e = scopeRow(ctx, tx, "SELECT pool_json FROM series_discovery_runs WHERE job_id=?", p.JobID).Scan(&poolRaw); e != nil {
		return p, e
	}
	var pool rec.Pool
	if json.Unmarshal([]byte(poolRaw), &pool) != nil {
		return p, model.ErrInput
	}
	inputs := map[string]string{}
	for _, ed := range pool.Editions {
		if slices.Contains(order, ed.WorkID) {
			inputs[ed.Book.ID] = p.Inputs[ed.Book.ID]
		}
	}
	p.Inputs = inputs
	expected := map[string]int64{}
	for key, rev := range p.Expected {
		if strings.HasPrefix(key, "series:") || slices.Contains(order, strings.TrimPrefix(key, "work:")) {
			expected[key] = rev
		}
	}
	p.Expected = expected
	p.Key = rec.Fingerprint([]any{rec.SeriesAlgorithm, p.Kind, p.Target, sortedSeriesIDs(order), p.Inputs, p.Name, p.Order})
	if p.Presentation != "" {
		p.Key = rec.Fingerprint([]any{p.Key, p.Presentation})
	}
	batch, scopeErr := importJobScope(ctx, tx, p.JobID)
	if scopeErr != nil {
		return p, scopeErr
	}
	p.ID = "series-plan:" + p.Key
	if batch != "" {
		p.ID = "import-series:" + rec.Fingerprint([]string{batch, p.Key})
		if e = linkImportProposal(ctx, tx, batch, "series", p.ID); e != nil {
			return p, e
		}
	}
	p.Revision = 1
	old, err := scanSeriesProposal(scopeRow(ctx, tx, "SELECT "+seriesProposalColumns+" FROM series_proposals WHERE id=?", p.ID))
	if err == nil {
		if old.State == "accepted" || old.State == "dismissed" {
			return p, model.ErrConflict
		}
		p.Revision = old.Revision + 1
	} else if !errors.Is(err, model.ErrNotFound) {
		return p, err
	}
	if e = saveSeriesPlan(ctx, tx, p, true); e != nil {
		return p, e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE series_proposals SET state='superseded',revision=revision+1 WHERE id=?", id); e != nil {
		return p, e
	}
	if e = tx.Commit(); e != nil {
		return p, e
	}
	return s.SeriesProposal(ctx, p.ID)
}

type seriesUndo struct {
	Exists        bool             `json:"exists"`
	Series        model.Series     `json:"series"`
	WorkRevisions map[string]int64 `json:"work_revisions"`
}

func seriesSnapshot(ctx context.Context, tx *sql.Tx, p rec.SeriesProposal) (seriesUndo, error) {
	out := seriesUndo{WorkRevisions: map[string]int64{}}
	series, e := loadSeries(ctx, tx, p.Target)
	if errors.Is(e, model.ErrNotFound) {
		out.Exists = false
	} else if e != nil {
		return out, e
	} else {
		out.Exists = true
		out.Series = series
	}
	for _, id := range p.Order {
		var revision int64
		if e = scopeRow(ctx, tx, "SELECT revision FROM works WHERE id=?", id).Scan(&revision); e != nil {
			return out, e
		}
		out.WorkRevisions[id] = revision
	}
	return out, nil
}
func seriesCommand(p rec.SeriesProposal, c model.WorkContext) model.WorkCommand {
	action := "series_create"
	if p.Kind == "add_to_series" {
		action = "series_members"
	}
	return model.WorkCommand{Action: action, ID: p.Target, LibraryID: p.LibraryID, Name: p.Name, WorkIDs: p.Order, Expected: c.Expected}
}
func (s *Store) DecideSeries(ctx context.Context, account, id, action string, in rec.SeriesSelection) (rec.SeriesProposal, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return rec.SeriesProposal{}, e
	}
	defer tx.Rollback()
	if action == "accept" {
		if e = enhancementGate(ctx, tx, model.EnhancementSeries); e != nil {
			return rec.SeriesProposal{}, e
		}
	}
	p, e := scanSeriesProposal(scopeRow(ctx, tx, "SELECT "+seriesProposalColumns+" FROM series_proposals WHERE id=?", id))
	if e != nil {
		return p, e
	}
	if e = guardImportPlan(ctx, tx, id, "series", action); e != nil {
		return p, e
	}
	if action == "accept" && p.State == "accepted" {
		if !sameMembers(in.IDs, p.NewIDs) || (in.Order != nil && !slices.Equal(in.Order, p.Order)) || (in.Name != nil && strings.TrimSpace(*in.Name) != p.Name) {
			return p, model.ErrConflict
		}
		return p, nil
	}
	if p.Revision != in.Revision {
		return p, model.ErrConflict
	}
	if in.Customize && action == "accept" {
		if p.Kind != "create_series" || p.State != "pending" || len(in.IDs) < 2 || in.Name == nil || strings.TrimSpace(*in.Name) == "" || len([]rune(strings.TrimSpace(*in.Name))) > 200 {
			return p, model.ErrInput
		}
		seen := map[string]bool{}
		for _, id := range in.IDs {
			if seen[id] {
				return p, model.ErrInput
			}
			seen[id] = true
			if !slices.Contains(p.NewIDs, id) {
				return p, model.ErrInput
			}
		}
		if !sameMembers(in.Order, in.IDs) {
			return p, rec.ErrSelection
		}
		// Validate the complete original frozen snapshot before narrowing it.
		if _, e = currentSeriesPlan(ctx, tx, account, p); e != nil {
			return p, e
		}
		p.NewIDs = slices.Clone(in.IDs)
		p.Order = slices.Clone(in.Order)
		p.Name = strings.TrimSpace(*in.Name)
		p.NameRequired = false
		p.MemberCount = len(p.Order)
		p.OrderBasis = "manual"
		p.OrderUncertain = false
	}
	decision := ""
	switch action {
	case "accept":
		if p.NameRequired || strings.TrimSpace(p.Name) == "" {
			return p, model.ErrInput
		}
		if p.State != "pending" || !sameMembers(in.IDs, p.NewIDs) || (in.Name != nil && !in.Customize) {
			return p, rec.ErrSelection
		}
		if in.Order != nil {
			if (p.Kind != "add_to_series" && !in.Customize) || !sameMembers(in.Order, p.Order) || !slices.Equal(existingSeriesOrder(in.Order, p.ExistingIDs), p.ExistingIDs) {
				return p, rec.ErrSelection
			}
			p.Order = slices.Clone(in.Order)
			p.OrderBasis, p.OrderUncertain = "manual", false
		}
		c, err := currentSeriesPlan(ctx, tx, account, p)
		if err != nil {
			return p, err
		}
		before, err := seriesSnapshot(ctx, tx, p)
		if err != nil {
			return p, err
		}
		if p.Kind == "create_series" && before.Exists {
			return p, model.ErrConflict
		}
		if _, e = changeWorkTx(ctx, tx, account, seriesCommand(p, c), false, organization.PlanWork); e != nil {
			return p, e
		}
		after, err := seriesSnapshot(ctx, tx, p)
		if err != nil {
			return p, err
		}
		if e = saveSeriesDecision(ctx, tx, p, account, "accepted", seriesJSON(before), seriesJSON(after)); e != nil {
			return p, e
		}
		p.State = "accepted"
	case "undo":
		if p.State != "accepted" {
			return p, model.ErrConflict
		}
		var b, a string
		if e = scopeRow(ctx, tx, "SELECT before_json,after_json FROM series_proposal_decisions WHERE proposal_key=? AND proposal_id=? AND decision='accepted'", p.StorageKey, p.ID).Scan(&b, &a); e != nil {
			return p, e
		}
		var before, after seriesUndo
		if json.Unmarshal([]byte(b), &before) != nil || json.Unmarshal([]byte(a), &after) != nil {
			return p, model.ErrInput
		}
		current, err := seriesSnapshot(ctx, tx, p)
		if err != nil {
			return p, err
		}
		if !current.Exists || current.Series.Revision != after.Series.Revision || !slices.Equal(current.Series.WorkIDs, after.Series.WorkIDs) || !sameExpected(current.WorkRevisions, after.WorkRevisions) {
			return p, model.ErrConflict
		}
		c, err := loadWorkContext(ctx, tx, account, nil, p.Order, []string{p.Target}, false)
		if err != nil {
			return p, err
		}
		command := model.WorkCommand{Action: "series_delete", ID: p.Target, Expected: c.Expected}
		if before.Exists {
			command.Action = "series_members"
			command.WorkIDs = before.Series.WorkIDs
		}
		if _, e = changeWorkTx(ctx, tx, account, command, false, organization.PlanWork); e != nil {
			return p, e
		}
		p.State = "undone"
		decision = "undone"
	case "dismiss":
		if p.State != "pending" {
			return p, model.ErrConflict
		}
		p.State = "dismissed"
		decision = "dismissed"
	case "restore":
		if p.State != "dismissed" {
			return p, model.ErrConflict
		}
		if _, e = currentSeriesPlan(ctx, tx, account, p); e != nil {
			return p, e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM series_proposal_decisions WHERE proposal_key=? AND proposal_id=? AND decision='dismissed'", p.StorageKey, p.ID); e != nil {
			return p, e
		}
		if e = saveSeriesFeedback(ctx, tx, p, account, "restored"); e != nil {
			return p, e
		}
		p.State = "pending"
	default:
		return p, model.ErrInput
	}
	if decision != "" {
		if e = saveSeriesDecision(ctx, tx, p, account, decision, nil, nil); e != nil {
			return p, e
		}
		if decision == "dismissed" {
			if e = saveSeriesFeedback(ctx, tx, p, account, "dismissed"); e != nil {
				return p, e
			}
		}
	}
	if action == "accept" && in.Order != nil {
		if _, e = tx.ExecContext(ctx, "UPDATE series_proposals SET plan_json=?,customized=CASE WHEN ? THEN 1 ELSE customized END WHERE id=?", storedSeries(p), in.Customize, id); e != nil {
			return p, e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM series_proposal_members WHERE proposal_id=?", id); e != nil {
			return p, e
		}
		for i, work := range p.Order {
			role := "new"
			if slices.Contains(p.ExistingIDs, work) {
				role = "existing"
			}
			if _, e = tx.ExecContext(ctx, "INSERT INTO series_proposal_members(proposal_id,work_id,role,position) VALUES(?,?,?,?)", id, work, role, i); e != nil {
				return p, e
			}
		}
	}
	if _, e = tx.ExecContext(ctx, "UPDATE series_proposals SET state=?,revision=revision+1 WHERE id=?", p.State, id); e != nil {
		return p, e
	}
	if action == "accept" || action == "undo" {
		if _, e = tx.ExecContext(ctx, `UPDATE series_proposals SET state='superseded',revision=revision+1 WHERE id<>? AND state='pending' AND (target_series_id=? OR id IN(SELECT proposal_id FROM series_proposal_members WHERE work_id IN(SELECT value FROM json_each(?))))`, id, p.Target, seriesJSON(p.Order)); e != nil {
			return p, e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE series_discovery_runs SET refresh_required=1 WHERE job_id=(SELECT job_id FROM series_discovery_runs WHERE library_id=? AND phase='finished' ORDER BY created_at DESC,rowid DESC LIMIT 1)`, p.LibraryID); e != nil {
		return p, e
	}
	if e = tx.Commit(); e != nil {
		return p, e
	}
	return s.SeriesProposal(ctx, id)
}

func filterSeriesOrder(order, existing, selected []string) []string {
	out := []string{}
	for _, id := range order {
		if slices.Contains(existing, id) || slices.Contains(selected, id) {
			out = append(out, id)
		}
	}
	return out
}
