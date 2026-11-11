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
)

func proposalIDs(p rec.Proposal) []string {
	ids := []string{}
	for _, m := range p.Members {
		ids = append(ids, m.ID)
	}
	return ids
}
func currentProposalContext(ctx context.Context, tx *sql.Tx, account string, p rec.Proposal) (model.WorkContext, error) {
	c, e := loadWorkContext(ctx, tx, account, proposalIDs(p), nil, nil, true)
	if e != nil {
		return c, rec.ErrStale
	}
	for key, want := range p.Expected {
		if got, ok := c.Expected[key]; !ok || got != want {
			return c, model.ErrConflict
		}
	}
	pool, e := recommendationPoolSubset(ctx, tx, []string{c.Editions[0].LibraryID}, "", "", proposalIDs(p))
	if e != nil {
		return c, e
	}
	current := map[string]rec.InputEdition{}
	for _, v := range pool.Editions {
		current[v.Book.ID] = v
	}
	for id, want := range p.Evidence {
		v, ok := current[id]
		if !ok || rec.EvidenceFingerprint(v) != want {
			return c, rec.ErrStale
		}
	}
	for _, m := range p.Members {
		if m.Role == "new" {
			if e = managementGuard(ctx, tx, m.ID); e != nil {
				return c, rec.ErrStale
			}
			w := m.WorkID
			for _, ed := range c.Editions {
				if ed.ID == m.ID {
					w = ed.WorkID
				}
			}
			for _, work := range c.Works {
				if work.ID == w && len(work.EditionIDs) != 1 {
					return c, rec.ErrStale
				}
			}
		}
	}
	return c, nil
}
func sameMembers(a, b []string) bool {
	a = slices.Clone(a)
	b = slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
func resolveSelection(p rec.Proposal, ids []string, c model.WorkContext) (rec.Proposal, error) {
	if !sameMembers(ids, p.NewIDs) {
		for _, id := range ids {
			if !slices.Contains(p.NewIDs, id) {
				return p, model.ErrInput
			}
		}
	}
	if p.Kind == "create_work" && len(ids) < 2 || p.Kind == "add_to_work" && len(ids) < 1 {
		return p, model.ErrInput
	}
	p.NewIDs = slices.Clone(ids)
	slices.Sort(p.NewIDs)
	members := p.Members[:0]
	p.Evidence = cloneEvidence(p.Evidence)
	for _, m := range p.Members {
		if m.Role == "existing" || slices.Contains(ids, m.ID) {
			members = append(members, m)
		} else {
			delete(p.Evidence, m.ID)
			delete(p.Expected, "edition:"+m.ID)
			delete(p.Expected, "work:"+m.WorkID)
		}
	}
	p.Members = members
	if p.Kind == "create_work" {
		retained := ""
		for _, w := range c.Works {
			for _, m := range p.Members {
				if w.ID == m.WorkID && (retained == "" || slices.Contains(p.NewIDs, w.PreferredEditionID) && w.ID < p.Target) {
					retained = w.ID
				}
			}
		}
		if !slices.ContainsFunc(p.Members, func(m rec.ProposalMember) bool { return m.WorkID == p.Target }) {
			p.Target = retained
		}
	}
	for _, w := range c.Works {
		if w.ID == p.Target {
			p.TargetName = w.Name
		}
	}
	rec.SuggestPresets(p.Members)
	all := proposalIDs(p)
	p.Key = rec.ProposalKey(p.Kind, p.Target, all, p.Evidence)
	p.ID = rec.Fingerprint(p.Key)
	p.State = "pending"
	p.Primary = false
	p.Revision = 1
	scoped := proposalContext(c, p)
	p.Expected = scoped.Expected
	preview, e := planProposal(scoped, p)
	if e != nil {
		return p, e
	}
	p.Effects = preview.Effects
	p.Blocked = false
	return p, nil
}
func cloneEvidence(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
func (s *Store) ResolveWorkProposal(ctx context.Context, id string, in rec.ProposalSelection) (rec.Proposal, error) {
	if !validProposalMembers(in.IDs) {
		return rec.Proposal{}, model.ErrInput
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return rec.Proposal{}, e
	}
	defer tx.Rollback()
	if e = enhancementGate(ctx, tx, model.EnhancementWork); e != nil {
		return rec.Proposal{}, e
	}
	p, e := scanProposal(scopeRow(ctx, tx, "SELECT plan_json,state,revision,primary_plan FROM work_proposals WHERE id=?", id))
	if e != nil {
		return p, e
	}
	if e = guardImportPlan(ctx, tx, id, "versions", "resolve"); e != nil {
		return p, e
	}
	if p.Revision != in.Revision || p.State != "pending" {
		return p, model.ErrConflict
	}
	c, e := currentProposalContext(ctx, tx, "", p)
	if e != nil {
		return p, e
	}
	plans := []rec.Proposal{p}
	if e = refreshProposalLabelViews(ctx, tx, plans); e != nil {
		return p, e
	}
	p = plans[0]
	if sameMembers(in.IDs, p.NewIDs) {
		return p, nil
	}
	if p.Kind == "create_work" && !slices.ContainsFunc(p.Members, func(m rec.ProposalMember) bool { return m.WorkID == p.Target && slices.Contains(in.IDs, m.ID) }) {
		rawIDs, _ := json.Marshal(in.IDs)
		if e = scopeRow(ctx, tx, "SELECT e.work_id FROM editions e JOIN files f ON f.edition_id=e.id WHERE e.id IN(SELECT value FROM json_each(?)) ORDER BY f.indexed_at,e.work_id LIMIT 1", string(rawIDs)).Scan(&p.Target); e != nil {
			return p, e
		}
	}
	resolved, e := resolveSelection(p, in.IDs, c)
	if e != nil {
		return p, e
	}
	var jobID *string
	var library string
	if e = scopeRow(ctx, tx, "SELECT job_id,library_id FROM work_proposals WHERE id=?", p.ID).Scan(&jobID, &library); e != nil {
		return p, e
	}
	batch, err := importJobScope(ctx, tx, p.JobID)
	if err != nil {
		return p, err
	}
	if batch != "" {
		resolved.ID = "import-work:" + rec.Fingerprint([]string{batch, resolved.Key})
		if e = linkImportProposal(ctx, tx, batch, "versions", resolved.ID); e != nil {
			return p, e
		}
	}
	raw, _ := json.Marshal(resolved)
	write, e := tx.ExecContext(ctx, `INSERT INTO work_proposals(id,job_id,library_id,proposal_key,kind,target_work_id,tier,score,primary_plan,state,plan_json,selection_plan,created_at) VALUES(?,?,?,?,?,?,?,?,0,'pending',?,1,unixepoch()) ON CONFLICT(id) DO UPDATE SET plan_json=excluded.plan_json,state='pending',selection_plan=1,revision=work_proposals.revision+1 WHERE work_proposals.state NOT IN('accepted','dismissed')`, resolved.ID, jobID, library, resolved.Key, resolved.Kind, resolved.Target, resolved.Tier, resolved.Score, string(raw))
	if e != nil {
		return p, e
	}
	if n, _ := write.RowsAffected(); n != 1 {
		return p, model.ErrConflict
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM work_proposal_members WHERE proposal_id=?", resolved.ID); e != nil {
		return p, e
	}
	for i, m := range resolved.Members {
		if _, e = tx.ExecContext(ctx, "INSERT INTO work_proposal_members VALUES(?,?,?,?)", resolved.ID, m.ID, i, m.Role); e != nil {
			return p, e
		}
	}
	if e = tx.Commit(); e != nil {
		return p, e
	}
	return s.WorkProposal(ctx, resolved.ID)
}
func (s *Store) DecideWorkProposal(ctx context.Context, account, id, action string, in rec.ProposalSelection) (rec.Proposal, error) {
	if action == "accept" && !validProposalMembers(in.IDs) {
		return rec.Proposal{}, model.ErrInput
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return rec.Proposal{}, e
	}
	defer tx.Rollback()
	if action == "accept" {
		if e = enhancementGate(ctx, tx, model.EnhancementWork); e != nil {
			return rec.Proposal{}, e
		}
	}
	p, e := scanProposal(scopeRow(ctx, tx, "SELECT plan_json,state,revision,primary_plan FROM work_proposals WHERE id=?", id))
	if e != nil {
		return p, e
	}
	if e = guardImportPlan(ctx, tx, id, "versions", action); e != nil {
		return p, e
	}
	ids := in.IDs
	if action == "accept" {
		for _, member := range ids {
			if !slices.Contains(p.NewIDs, member) {
				return p, model.ErrInput
			}
		}
		if p.Kind == "create_work" && len(ids) < 2 {
			return p, model.ErrInput
		}
	}
	if action == "accept" && p.State == "accepted" {
		var raw string
		if e = scopeRow(ctx, tx, "SELECT accepted_members_json FROM work_proposal_decisions WHERE proposal_key=?", p.Key).Scan(&raw); e != nil {
			return p, e
		}
		var accepted []string
		_ = json.Unmarshal([]byte(raw), &accepted)
		if !sameMembers(ids, accepted) {
			return p, model.ErrConflict
		}
		return p, nil
	}
	if p.Revision != in.Revision {
		return p, model.ErrConflict
	}
	if action == "undo" {
		if e = undoWorkProposal(ctx, tx, account, &p); e != nil {
			return p, e
		}
	} else {
		c, e := currentProposalContext(ctx, tx, account, p)
		if e != nil {
			return p, e
		}
		switch action {
		case "accept":
			if p.State != "pending" || p.Blocked {
				return p, model.ErrConflict
			}
			if !sameMembers(ids, p.NewIDs) {
				return p, rec.ErrSelection
			}
			before, _ := json.Marshal(c)
			result, e := planProposal(c, p)
			if e != nil {
				return p, e
			}
			if p.Kind == "create_work" {
				command, _ := proposalCommand(p, c)
				command.Expected = p.Expected
				result, e = changeWorkTx(ctx, tx, account, command, false, organization.PlanWork)
			} else {
				e = persistWorkContext(ctx, tx, account, c, &result.Context)
				if e == nil {
					e = checkOrganization(ctx, tx)
				}
			}
			if e != nil {
				return p, e
			}
			after, _ := json.Marshal(result.Context)
			resultRaw, _ := json.Marshal(result)
			selected, _ := json.Marshal(ids)
			if _, e = tx.ExecContext(ctx, `INSERT INTO work_proposal_decisions(proposal_key,proposal_id,account_id,decision,before_json,after_json,accepted_members_json,result_json,revision,updated_at) VALUES(?,?,?,'accepted',?,?,?,?,1,unixepoch()) ON CONFLICT(proposal_key) DO UPDATE SET proposal_id=excluded.proposal_id,account_id=excluded.account_id,decision='accepted',before_json=excluded.before_json,after_json=excluded.after_json,accepted_members_json=excluded.accepted_members_json,result_json=excluded.result_json,revision=work_proposal_decisions.revision+1,updated_at=unixepoch()`, p.Key, p.ID, account, string(before), string(after), string(selected), string(resultRaw)); e != nil {
				return p, e
			}
			p.State = "accepted"
			p.CanUndo = true
			if _, e = tx.ExecContext(ctx, "UPDATE work_proposals SET state='superseded',revision=revision+1 WHERE state='pending' AND id<>? AND (target_work_id=? OR id IN(SELECT proposal_id FROM work_proposal_members WHERE edition_id IN("+placeholders(len(ids))+")))", append([]any{p.ID, p.Target}, stringArgs(ids)...)...); e != nil {
				return p, e
			}
		case "dismiss":
			if p.State != "pending" {
				return p, model.ErrConflict
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO work_proposal_decisions(proposal_key,proposal_id,account_id,decision,revision,updated_at) VALUES(?,?,?,'dismissed',1,unixepoch()) ON CONFLICT(proposal_key) DO UPDATE SET decision='dismissed',revision=work_proposal_decisions.revision+1,updated_at=unixepoch()`, p.Key, p.ID, account); e != nil {
				return p, e
			}
			p.State = "dismissed"
		case "restore":
			if p.State != "dismissed" {
				return p, model.ErrConflict
			}
			if _, e = tx.ExecContext(ctx, "DELETE FROM work_proposal_decisions WHERE proposal_key=? AND decision='dismissed'", p.Key); e != nil {
				return p, e
			}
			p.State = "pending"
		default:
			return p, model.ErrInput
		}
	}
	if _, e = tx.ExecContext(ctx, "UPDATE work_proposals SET state=?,revision=revision+1 WHERE id=?", p.State, p.ID); e != nil {
		return p, e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE work_discovery_runs SET refresh_required=1 WHERE job_id=(SELECT job_id FROM work_proposals WHERE id=?) AND phase='finished'", p.ID); e != nil {
		return p, e
	}
	if e = tx.Commit(); e != nil {
		return p, e
	}
	return s.WorkProposal(ctx, id)
}
func (s *Store) RebuildDiscoveryPending(ctx context.Context) (bool, error) {
	var job string
	e := scopeRow(ctx, s.Read, "SELECT job_id FROM work_discovery_runs WHERE refresh_required=1 AND phase='finished' ORDER BY created_at,job_id LIMIT 1").Scan(&job)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	prepared, e := s.prepareWorkProposals(ctx, job)
	if e != nil {
		return false, e
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	if e = backgroundEnhancementGate(ctx, tx, model.EnhancementWork); e != nil {
		return false, e
	}
	var pending int
	if e = scopeRow(ctx, tx, "SELECT refresh_required FROM work_discovery_runs WHERE job_id=?", job).Scan(&pending); e != nil {
		return false, e
	}
	if pending != 1 {
		return false, nil
	}
	if e = publishWorkProposals(ctx, tx, prepared); e != nil {
		return false, e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE work_discovery_runs SET refresh_required=0 WHERE job_id=?", job); e != nil {
		return false, e
	}
	return true, tx.Commit()
}

// undo restores only organization fields, preserving current metadata, labels,
// personal state and files. All revision counters advance rather than roll back.
func undoWorkProposal(ctx context.Context, tx *sql.Tx, account string, p *rec.Proposal) error {
	if p.State != "accepted" {
		return model.ErrConflict
	}
	var beforeRaw, afterRaw string
	if e := scopeRow(ctx, tx, "SELECT before_json,after_json FROM work_proposal_decisions WHERE proposal_key=? AND decision='accepted'", p.Key).Scan(&beforeRaw, &afterRaw); e != nil {
		return e
	}
	var before, after model.WorkContext
	if json.Unmarshal([]byte(beforeRaw), &before) != nil || json.Unmarshal([]byte(afterRaw), &after) != nil {
		return model.ErrInput
	}
	oldW, newW := map[string]model.Work{}, map[string]model.Work{}
	for _, w := range before.Works {
		oldW[w.ID] = w
	}
	for _, w := range after.Works {
		newW[w.ID] = w
	}
	changed := map[string]bool{}
	for id, w := range oldW {
		a, ok := newW[id]
		if !ok || a.Revision != w.Revision {
			changed[id] = true
		}
	}
	for id, w := range newW {
		b, ok := oldW[id]
		if !ok || b.Revision != w.Revision {
			changed[id] = true
		}
	}
	for id := range changed {
		want := int64(0)
		if w, ok := newW[id]; ok {
			want = w.Revision
		}
		var actual int64
		e := scopeRow(ctx, tx, "SELECT revision FROM works WHERE id=?", id).Scan(&actual)
		if errors.Is(e, sql.ErrNoRows) {
			actual = 0
		} else if e != nil {
			return e
		}
		if actual != want {
			return model.ErrConflict
		}
	}
	changedSeries := map[string]model.Series{}
	seriesIDs := []string{}
	for _, old := range before.Series {
		for _, next := range after.Series {
			if old.ID == next.ID && (old.Name != next.Name || !slices.Equal(old.WorkIDs, next.WorkIDs)) {
				changedSeries[old.ID] = old
				seriesIDs = append(seriesIDs, old.ID)
			}
		}
	}
	ids := []string{}
	for _, ed := range after.Editions {
		ids = append(ids, ed.ID)
	}
	current, e := loadWorkContext(ctx, tx, account, ids, nil, seriesIDs, true)
	if e != nil {
		return model.ErrConflict
	}
	// Work revisions above guard membership, preferred and naming. Edition
	// organization revisions also advance for manual label edits, which Undo
	// deliberately preserves. Verify ownership rather than rejecting those edits.
	currentOwners := map[string]string{}
	for _, ed := range current.Editions {
		currentOwners[ed.ID] = ed.WorkID
	}
	for _, ed := range after.Editions {
		if currentOwners[ed.ID] != ed.WorkID {
			return model.ErrConflict
		}
		if e = managementGuard(ctx, tx, ed.ID); e != nil {
			return e
		}
	}
	for _, s := range after.Series {
		if _, changed := changedSeries[s.ID]; !changed {
			continue
		}
		if current.Expected["series:"+s.ID] != s.Revision {
			return model.ErrConflict
		}
	}
	restore := current
	restore.Works = slices.Clone(current.Works)
	for id, w := range oldW {
		if !changed[id] {
			continue
		}
		actual := int64(0)
		restore.Works = slices.DeleteFunc(restore.Works, func(v model.Work) bool {
			if v.ID == id {
				actual = v.Revision
				return true
			}
			return false
		})
		w.Revision = max(w.Revision, newW[id].Revision, actual) + 1
		restore.Works = append(restore.Works, w)
	}
	oldE := map[string]model.WorkEdition{}
	for _, ed := range before.Editions {
		oldE[ed.ID] = ed
	}
	restore.Editions = slices.Clone(current.Editions)
	for i := range restore.Editions {
		ed := &restore.Editions[i]
		if old, ok := oldE[ed.ID]; ok && old.WorkID != ed.WorkID {
			ed.WorkID = old.WorkID
			ed.Revision++
		}
	}
	restore.Series = slices.Clone(current.Series)
	for i := range restore.Series {
		if old, changed := changedSeries[restore.Series[i].ID]; changed {
			rev := restore.Series[i].Revision
			restore.Series[i] = old
			restore.Series[i].Revision = rev + 1
		}
	}
	if e = persistWorkContext(ctx, tx, account, current, &restore); e != nil {
		return e
	}
	if e = checkOrganization(ctx, tx); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE work_proposal_decisions SET decision='undone',revision=revision+1,updated_at=unixepoch() WHERE proposal_key=?", p.Key); e != nil {
		return e
	}
	p.State = "undone"
	p.CanUndo = false
	return nil
}

func (s *Store) WorkProposalAlternatives(ctx context.Context, id string, page, size int) (rec.ProposalPage, error) {
	out := rec.ProposalPage{Items: []rec.Proposal{}, Page: page, PageSize: size}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	p, e := scanProposal(scopeRow(ctx, tx, "SELECT plan_json,state,revision,primary_plan FROM work_proposals WHERE id=?", id))
	if e != nil {
		return out, e
	}
	ids, _ := json.Marshal(p.NewIDs)
	where := ` FROM work_proposals p WHERE p.id<>? AND p.state='pending' AND EXISTS(SELECT 1 FROM work_proposal_members m WHERE m.proposal_id=p.id AND m.role='new' AND m.edition_id IN(SELECT value FROM json_each(?)))`
	args := []any{id, string(ids)}
	if e = scopeRow(ctx, tx, "SELECT count(*)"+where, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, "SELECT plan_json,state,revision,primary_plan"+where+" ORDER BY CASE tier WHEN 'strong' THEN 0 ELSE 1 END,score DESC,p.id LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		p, e := scanProposal(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Items = append(out.Items, p)
	}
	e = rows.Err()
	rows.Close()
	if e == nil {
		e = refreshProposalLabelViews(ctx, tx, out.Items)
	}
	if e == nil {
		e = tx.Commit()
	}
	return out, e
}

func validProposalMembers(ids []string) bool {
	if len(ids) < 1 || len(ids) > 100 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || len(id) > 128 || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
