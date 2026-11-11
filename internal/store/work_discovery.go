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
	"time"
)

func (s *Store) CreateDiscoveryJob(ctx context.Context, id, library string, parent ...string) (*model.Job, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = enhancementGate(ctx, tx, model.EnhancementWork); e != nil {
		return nil, e
	}
	l, e := scanLibrary(scopeRow(ctx, tx, "SELECT "+libraryColumns+" FROM libraries WHERE id=?", library))
	if e != nil {
		return nil, e
	}
	if l.Disabled {
		return nil, model.ErrDisabled
	}
	if l.TrashedAt != nil {
		return nil, model.ErrTrashed
	}
	pool, e := recommendationPool(ctx, tx, []string{library})
	if e != nil {
		return nil, e
	}
	n := len(pool.Editions)
	if n < 2 {
		return nil, model.ErrInput
	}
	if int64(n)*int64(n-1)/2 > rec.MaxDiscoveryComparisons {
		return nil, model.ErrCapacity
	}
	raw, _ := json.Marshal(pool)
	if len(raw) > 16<<20 || len(raw)+n*5300 >= rec.MaxMessage {
		return nil, model.ErrCapacity
	}
	now := time.Now().Unix()
	if _, e = tx.ExecContext(ctx, "INSERT INTO jobs(id,type,mode,library_id,state,stage,generation,total,created_at,updated_at) VALUES(?,'work_recommendation','library',?,'queued','preparing',1,?,?,?)", id, library, n, now, now); e != nil {
		if strings.Contains(e.Error(), "UNIQUE") {
			return nil, model.ErrJobActive
		}
		return nil, e
	}
	if len(parent) > 0 && parent[0] != "" {
		if _, e = tx.ExecContext(ctx, "UPDATE jobs SET parent_job_id=? WHERE id=?", parent[0], id); e != nil {
			return nil, e
		}
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO recommendation_batches(job_id,pool_json,algorithm_version,created_at) VALUES(?,?,?,?)", id, string(raw), rec.Algorithm, now); e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO work_discovery_runs(job_id,library_id,evidence_version,created_at) VALUES(?,?,?,?)", id, library, rec.EvidenceVersion, now); e != nil {
		return nil, e
	}
	if s.Version >= 11 {
		full := len(parent) > 1 && parent[1] == "full"
		var base *string
		if !full {
			var b string
			err := scopeRow(ctx, tx, `SELECT d.job_id FROM work_discovery_runs d JOIN jobs j ON j.id=d.job_id JOIN recommendation_batches b ON b.job_id=d.job_id WHERE d.library_id=? AND d.next_row>0 AND j.state IN('completed','failed','cancelled') AND d.execution_version=? AND d.evidence_version=? AND b.algorithm_version=? ORDER BY d.next_row DESC,d.created_at DESC,d.rowid DESC LIMIT 1`, library, rec.DiscoveryExecutionVersion, rec.EvidenceVersion, rec.Algorithm).Scan(&b)
			if err == nil {
				base = &b
			} else if !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
		}
		if _, e = tx.ExecContext(ctx, "UPDATE work_discovery_runs SET execution_version=?,base_job_id=? WHERE job_id=?", rec.DiscoveryExecutionVersion, base, id); e != nil {
			return nil, e
		}
		baselineState := "no_compatible_baseline"
		if full {
			baselineState = "full_recompute_requested"
		} else if base != nil {
			baselineState = "reusing_checkpoint"
		}
		if _, e = tx.ExecContext(ctx, "UPDATE recommendation_batches SET summary_json=json_set(summary_json,'$.discovery_baseline',?) WHERE job_id=?", baselineState, id); e != nil {
			return nil, e
		}

	}
	for i, v := range pool.Editions {
		input, _ := json.Marshal(v)
		if _, e = tx.ExecContext(ctx, "INSERT INTO job_resources VALUES(?,?,1)", "recommendation:edition:"+v.Book.ID, id); e != nil {
			if strings.Contains(e.Error(), "UNIQUE") {
				return nil, model.ErrJobActive
			}
			return nil, e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO job_items(job_id,ordinal,state,edition_id,content_revision,input_json) VALUES(?,?,'pending',?,?,?)", id, i+1, v.Book.ID, v.ContentRevision, string(input)); e != nil {
			return nil, e
		}
	}
	if e = armEnhancement(ctx, tx, model.EnhancementWork); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s.Job(ctx, id)
}
func (s *Store) DiscoveryRun(ctx context.Context, id string) (*rec.DiscoveryRun, error) {
	var v rec.DiscoveryRun
	e := scopeRow(ctx, s.Read, "SELECT job_id,library_id,next_row,phase,evidence_version FROM work_discovery_runs WHERE job_id=?", id).Scan(&v.JobID, &v.LibraryID, &v.NextRow, &v.Phase, &v.EvidenceVersion)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e == nil && s.Version >= 11 {
		e = scopeRow(ctx, s.Read, "SELECT execution_version,reused_pairs,computed_pairs,reused_features,prepared_features FROM work_discovery_runs WHERE job_id=?", id).Scan(&v.ExecutionVersion, &v.ReusedPairs, &v.ComputedPairs, &v.ReusedFeatures, &v.PreparedFeatures)
	}
	if e != nil {
		return &v, e
	}
	var raw, resources string
	var base *string
	baseExpression := "NULL"
	if s.Version >= 11 {
		baseExpression = "d.base_job_id"
	}
	if e = scopeRow(ctx, s.Read, "SELECT "+baseExpression+",b.summary_json,b.resources_json FROM work_discovery_runs d JOIN recommendation_batches b USING(job_id) WHERE d.job_id=?", id).Scan(&base, &raw, &resources); e != nil {
		return &v, e
	}
	var summary struct {
		Baseline  string `json:"discovery_baseline"`
		Discovery struct {
			Stage  string `json:"stage"`
			Reason string `json:"reason"`
			Row    int    `json:"row"`
		} `json:"discovery_diagnostic"`
	}
	if e = json.Unmarshal([]byte(raw), &summary); e != nil {
		return &v, e
	}
	if e = json.Unmarshal([]byte(resources), &v.Resources); e != nil {
		return &v, e
	}
	v.FailureStage, v.FailureReason, v.FailureRow = summary.Discovery.Stage, summary.Discovery.Reason, summary.Discovery.Row
	v.BaselineState = summary.Baseline
	if v.BaselineState == "" {
		v.BaselineState = "no_compatible_baseline"
	}
	if base != nil {
		v.BaselineState = "reusing_checkpoint"
	}
	return &v, nil
}
func (s *Store) SaveDiscoveryDiagnostic(ctx context.Context, j model.Job, stage, reason string, row int) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	if reason == "" {
		if _, e = tx.ExecContext(ctx, "UPDATE recommendation_batches SET summary_json=json_remove(summary_json,'$.discovery_diagnostic') WHERE job_id=?", j.ID); e != nil {
			return e
		}
		return tx.Commit()
	}
	raw, _ := json.Marshal(map[string]any{"stage": stage, "reason": reason, "row": row})
	if _, e = tx.ExecContext(ctx, "UPDATE recommendation_batches SET summary_json=json_set(summary_json,'$.discovery_diagnostic',json(?)) WHERE job_id=?", string(raw), j.ID); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) SaveDiscoveryBlock(ctx context.Context, j model.Job, b rec.PairBlock) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	var next, n, count, bytes int
	var version string
	if e = scopeRow(ctx, tx, "SELECT d.next_row,json_array_length(r.pool_json,'$.editions'),d.evidence_version,d.edge_count,d.evidence_bytes FROM work_discovery_runs d JOIN recommendation_batches r USING(job_id) WHERE d.job_id=?", j.ID).Scan(&next, &n, &version, &count, &bytes); e != nil {
		return e
	}
	if b.Reused < 0 || b.Reused > b.Compared || version != rec.EvidenceVersion || b.Row != next || b.NextRow != next+1 || next >= n || b.Compared != n-next-1 || len(b.HardBits) != (b.Compared+7)/8 {
		return model.ErrInput
	}
	addedBytes := 0
	for _, edge := range b.Edges {
		addedBytes += len(edge.Signal)
	}
	if count+len(b.Edges) > rec.MaxDiscoveryEdges || bytes+addedBytes > rec.MaxDiscoveryEvidenceBytes {
		return model.ErrCapacity
	}
	seen := map[int]bool{}
	for _, v := range b.Edges {
		if v.A != b.Row || v.B <= v.A || v.B >= n || seen[v.B] || (v.Tier != "strong" && v.Tier != "supplementary") || len(v.Signal) > 16<<10 {
			return model.ErrInput
		}
		seen[v.B] = true
		if _, e = tx.ExecContext(ctx, "INSERT INTO work_pair_evidence VALUES(?,?,?,?,?,?)", j.ID, v.A, v.B, v.Tier, v.Score, string(v.Signal)); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO work_pair_blocks VALUES(?,?,?)", j.ID, b.Row, b.HardBits); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE work_discovery_runs SET next_row=?,phase=?,edge_count=edge_count+?,evidence_bytes=evidence_bytes+? WHERE job_id=?", b.NextRow, map[bool]string{true: "proposals", false: "pairs"}[b.NextRow == n], len(b.Edges), addedBytes, j.ID); e != nil {
		return e
	}
	if s.Version >= 11 {
		if _, e = tx.ExecContext(ctx, "UPDATE work_discovery_runs SET reused_pairs=reused_pairs+?,computed_pairs=computed_pairs+?,base_job_id=CASE WHEN ? THEN NULL ELSE base_job_id END WHERE job_id=?", b.Reused, b.Compared-b.Reused, b.NextRow == n, j.ID); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, "UPDATE job_items SET state='completed' WHERE job_id=? AND ordinal=?", j.ID, b.NextRow); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE jobs SET done=?,stage='comparing',updated_at=unixepoch() WHERE id=?", b.NextRow, j.ID); e != nil {
		return e
	}
	return tx.Commit()
}
func discoveryGraph(ctx context.Context, q queryer, job string, pool rec.Pool) (rec.PairGraph, error) {
	g := rec.PairGraph{IDs: []string{}, Edges: []rec.PairEvidence{}, Hard: map[int][]byte{}}
	for _, v := range pool.Editions {
		g.IDs = append(g.IDs, v.Book.ID)
	}
	rows, e := scopeRows(ctx, q, "SELECT a,b,tier,score,signal_json FROM work_pair_evidence WHERE job_id=? ORDER BY a,b", job)
	if e != nil {
		return g, e
	}
	for rows.Next() {
		var v rec.PairEvidence
		var raw string
		if e = rows.Scan(&v.A, &v.B, &v.Tier, &v.Score, &raw); e != nil {
			rows.Close()
			return g, e
		}
		v.Signal = json.RawMessage(raw)
		g.Edges = append(g.Edges, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return g, e
	}
	rows, e = scopeRows(ctx, q, "SELECT row_index,hard_conflicts FROM work_pair_blocks WHERE job_id=?", job)
	if e != nil {
		return g, e
	}
	for rows.Next() {
		var row int
		var bits []byte
		if e = rows.Scan(&row, &bits); e != nil {
			rows.Close()
			return g, e
		}
		g.Hard[row] = bits
	}
	e = rows.Err()
	rows.Close()
	return g, e
}
func frozenDiscoveryPool(ctx context.Context, q queryer, job string) (rec.Pool, error) {
	var raw string
	var p rec.Pool
	e := scopeRow(ctx, q, "SELECT pool_json FROM recommendation_batches WHERE job_id=?", job).Scan(&raw)
	if e == nil {
		e = json.Unmarshal([]byte(raw), &p)
	}
	return p, e
}

type preparedWorkProposals struct {
	job, library, token string
	selectionToken      string
	plans               []rec.Proposal
	decisions           map[string]string
}

func discoveryToken(ctx context.Context, q queryer, library string) (string, error) {
	var raw string
	e := scopeRow(ctx, q, `SELECT COALESCE(json_group_array(json_array(id,work_id,revision,organization_revision,trashed_at,content_revision,file_state,root_identity,disabled,library_trashed,fields_json,properties_json,material_digest,cover_revision,work_revision,name_override,preferred_edition_id,series_id,series_revision)), '[]') FROM (SELECT e.id,e.work_id,e.revision,e.organization_revision,e.trashed_at,f.content_revision,f.file_state,l.root_identity,l.disabled,l.trashed_at library_trashed,eb.fields_json,p.properties_json,a.digest material_digest,COALESCE(cs.revision,0) cover_revision,w.revision work_revision,w.name_override,w.preferred_edition_id,sm.series_id,s.revision series_revision FROM editions e JOIN files f ON f.edition_id=e.id JOIN libraries l ON l.id=e.library_id JOIN works w ON w.id=e.work_id LEFT JOIN cover_selections cs ON cs.edition_id=e.id LEFT JOIN effective_editions eb ON eb.edition_id=e.id LEFT JOIN effective_edition_properties p ON p.edition_id=e.id LEFT JOIN recommendation_materials rm ON rm.edition_id=e.id LEFT JOIN assets a ON a.id=rm.asset_id LEFT JOIN series_members sm ON sm.work_id=w.id LEFT JOIN series s ON s.id=sm.series_id WHERE e.library_id=? ORDER BY e.id)`, library).Scan(&raw)
	return rec.Fingerprint(raw), e
}
func (s *Store) prepareWorkProposals(ctx context.Context, job string) (preparedWorkProposals, error) {
	out := preparedWorkProposals{job: job, decisions: map[string]string{}}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	frozen, e := frozenDiscoveryPool(ctx, tx, job)
	if e != nil {
		return out, e
	}
	var next int
	if e = scopeRow(ctx, tx, "SELECT library_id,next_row FROM work_discovery_runs WHERE job_id=?", job).Scan(&out.library, &next); e != nil {
		return out, e
	}
	if next != len(frozen.Editions) {
		return out, model.ErrConflict
	}
	out.token, e = discoveryToken(ctx, tx, out.library)
	if e != nil {
		return out, e
	}
	current, e := recommendationPool(ctx, tx, []string{out.library})
	if e != nil {
		return out, e
	}
	old := map[string]string{}
	for _, v := range frozen.Editions {
		old[v.Book.ID] = rec.EvidenceFingerprint(v)
	}
	eligible := current.Editions[:0]
	for _, v := range current.Editions {
		if old[v.Book.ID] == rec.EvidenceFingerprint(v) {
			eligible = append(eligible, v)
		}
	}
	current.Editions = eligible
	context, e := discoveryContext(ctx, tx, out.library)
	if e != nil {
		return out, e
	}
	graph, e := discoveryGraph(ctx, tx, job, frozen)
	if e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, "SELECT proposal_key,decision FROM work_proposal_decisions")
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var key, value string
		if e = rows.Scan(&key, &value); e != nil {
			rows.Close()
			return out, e
		}
		out.decisions[key] = value
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	selected := []rec.Proposal{}
	rows, e = scopeRows(ctx, tx, "SELECT plan_json,state,revision,primary_plan FROM work_proposals WHERE library_id=? AND selection_plan=1 AND state='pending' ORDER BY id", out.library)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		selected = append(selected, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	out.selectionToken, e = selectionToken(ctx, tx, out.library)
	if e != nil {
		return out, e
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	// No database connection/transaction is held during clique search and plans.
	plans, e := rec.BuildProposalsContext(ctx, current, context, graph)
	if e != nil {
		return out, e
	}
	seen := map[string]bool{}
	for _, p := range plans {
		seen[p.Key] = true
	}
	inputEvidence := map[string]string{}
	for _, v := range current.Editions {
		inputEvidence[v.Book.ID] = rec.EvidenceFingerprint(v)
	}
	for _, p := range selected {
		if seen[p.Key] {
			continue
		}
		valid := true
		currentExpected := proposalContext(context, p).Expected
		for key, rev := range p.Expected {
			if currentExpected[key] != rev {
				valid = false
				break
			}
		}
		for id, fingerprint := range p.Evidence {
			if inputEvidence[id] != fingerprint {
				valid = false
				break
			}
		}
		if valid {
			plans = append(plans, p)
		}
	}
	taken := map[string]bool{}
	for _, p := range plans {
		p.JobID = job
		p.ID = rec.Fingerprint(p.Key)
		scoped := proposalContext(context, p)
		p.Expected = scoped.Expected
		result, err := planProposal(scoped, p)
		if err != nil {
			p.Blocked = true
			p.Risks = append(p.Risks, "organization_requires_manual_review")
		} else {
			p.Effects = result.Effects
		}

		if out.decisions[p.Key] == "dismissed" {
			p.State = "dismissed"
		}
		p.Primary = p.State == "pending" && !p.Blocked
		for _, id := range p.NewIDs {
			if taken[id] {
				p.Primary = false
			}
		}
		if p.Primary {
			for _, id := range p.NewIDs {
				taken[id] = true
			}
		}
		out.plans = append(out.plans, p)
	}
	return out, nil
}
func selectionToken(ctx context.Context, q queryer, library string) (string, error) {
	var value string
	e := scopeRow(ctx, q, "SELECT COALESCE(json_group_array(json_array(id,state,revision)),'[]') FROM(SELECT id,state,revision FROM work_proposals WHERE library_id=? AND selection_plan=1 ORDER BY id)", library).Scan(&value)
	return rec.Fingerprint(value), e
}
func publishWorkProposals(ctx context.Context, tx *sql.Tx, prepared preparedWorkProposals) error {
	token, e := discoveryToken(ctx, tx, prepared.library)
	if e != nil {
		return e
	}
	if token != prepared.token {
		return model.ErrConflict
	}
	selections, err := selectionToken(ctx, tx, prepared.library)
	if err != nil {
		return err
	}
	if selections != prepared.selectionToken {
		return model.ErrConflict
	}
	keys := []string{}
	for _, p := range prepared.plans {
		keys = append(keys, p.Key)
	}
	rawKeys, _ := json.Marshal(keys)
	batch, err := importJobScope(ctx, tx, prepared.job)
	if err != nil {
		return err
	}
	targets := map[string]bool{}
	if batch != "" {
		targets, err = importTargetIDs(ctx, tx, batch, "versions")
		if err != nil {
			return err
		}
	}
	if batch == "" {
		if _, e = tx.ExecContext(ctx, "UPDATE work_proposals SET state='superseded',revision=revision+1 WHERE library_id=? AND state='pending' AND proposal_key NOT IN(SELECT value FROM json_each(?))", prepared.library, string(rawKeys)); e != nil {
			return e
		}
	}
	for _, p := range prepared.plans {
		if batch != "" {
			if !involves(p.NewIDs, targets) {
				continue
			}
			p.ID = "import-work:" + rec.Fingerprint([]string{batch, p.Key})
			if e = linkImportProposal(ctx, tx, batch, "versions", p.ID); e != nil {
				return e
			}
		}
		var decision string
		err := scopeRow(ctx, tx, "SELECT decision FROM work_proposal_decisions WHERE proposal_key=?", p.Key).Scan(&decision)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if decision != prepared.decisions[p.Key] {
			return model.ErrConflict
		}
		old, err := scanProposal(scopeRow(ctx, tx, "SELECT plan_json,state,revision,primary_plan FROM work_proposals WHERE id=?", p.ID))
		exists := err == nil
		if err != nil && !errors.Is(err, model.ErrNotFound) {
			return err
		}
		p.Revision = 1
		if exists {
			p.Revision = old.Revision
			if old.State == "accepted" {
				p.State = old.State
				p.CanUndo = true
			}
			a, b := old, p
			a.JobID = ""
			b.JobID = ""
			a.Revision = 0
			b.Revision = 0
			a.Primary = false
			b.Primary = false
			a.CanUndo = false
			b.CanUndo = false
			if rec.Fingerprint(a) != rec.Fingerprint(b) {
				p.Revision++
			}
		}
		raw, _ := json.Marshal(p)
		if _, e = tx.ExecContext(ctx, `INSERT INTO work_proposals(id,job_id,library_id,proposal_key,kind,target_work_id,tier,score,primary_plan,state,plan_json,revision,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,unixepoch()) ON CONFLICT(id) DO UPDATE SET job_id=excluded.job_id,primary_plan=excluded.primary_plan,state=excluded.state,plan_json=excluded.plan_json,revision=excluded.revision`, p.ID, prepared.job, prepared.library, p.Key, p.Kind, p.Target, p.Tier, p.Score, p.Primary, p.State, string(raw), p.Revision); e != nil {
			return e
		}
		if !exists || !sameMembers(proposalIDs(old), proposalIDs(p)) {
			if _, e = tx.ExecContext(ctx, "DELETE FROM work_proposal_members WHERE proposal_id=?", p.ID); e != nil {
				return e
			}
			for i, m := range p.Members {
				if _, e = tx.ExecContext(ctx, "INSERT INTO work_proposal_members VALUES(?,?,?,?)", p.ID, m.ID, i, m.Role); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func (s *Store) PublishDiscoveryProposals(ctx context.Context, j model.Job) error {
	prepared, e := s.prepareWorkProposals(ctx, j.ID)
	if e != nil {
		return e
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	if e = publishWorkProposals(ctx, tx, prepared); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE work_discovery_runs SET phase='finished',refresh_required=0 WHERE job_id=?", j.ID); e != nil {
		return e
	}
	return tx.Commit()
}
func proposalCommand(p rec.Proposal, c model.WorkContext) (model.WorkCommand, error) {
	works := []string{}
	for _, m := range p.Members {
		if m.Role == "new" && m.WorkID != p.Target {
			works = append(works, m.WorkID)
		}
	}
	slices.Sort(works)
	works = slices.Compact(works)
	if len(works) == 0 {
		return model.WorkCommand{}, model.ErrInput
	}
	return model.WorkCommand{Action: "work_merge", ID: p.Target, WorkIDs: works, Expected: p.Expected}, nil
}
func scanProposal(row scanner) (rec.Proposal, error) {
	var p rec.Proposal
	var raw string
	var state string
	var rev int64
	var primary bool
	e := row.Scan(&raw, &state, &rev, &primary)
	if errors.Is(e, sql.ErrNoRows) {
		return p, model.ErrNotFound
	}
	if e != nil {
		return p, e
	}
	e = json.Unmarshal([]byte(raw), &p)
	p.Revision = rev
	p.State = state
	p.Primary = primary
	p.CanUndo = state == "accepted"
	return p, e
}
func (s *Store) WorkProposal(ctx context.Context, id string) (rec.Proposal, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return rec.Proposal{}, e
	}
	defer tx.Rollback()
	p, e := scanProposal(scopeRow(ctx, tx, "SELECT plan_json,state,revision,primary_plan FROM work_proposals WHERE id=?", id))
	if e != nil {
		return p, e
	}
	plans := []rec.Proposal{p}
	if e = refreshProposalLabelViews(ctx, tx, plans); e != nil {
		return p, e
	}
	return plans[0], tx.Commit()
}
func (s *Store) WorkProposals(ctx context.Context, job, library, kind, state string, alternatives bool, page, size int, target ...string) (rec.ProposalPage, error) {
	out := rec.ProposalPage{Items: []rec.Proposal{}, Page: page, PageSize: size}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	where := ` FROM work_proposals p WHERE (?='' OR p.job_id=?) AND (?='' OR p.library_id=?) AND (?='' OR p.kind=?) AND p.state=? AND (? OR p.primary_plan=1)`
	args := []any{job, job, library, library, kind, kind, state, alternatives}
	if len(target) > 2 && target[2] == "alternatives" {
		where += " AND p.primary_plan=0"
	}
	if len(target) > 0 && target[0] != "" {
		where += " AND p.target_work_id=?"
		args = append(args, target[0])
	}
	if len(target) > 1 && target[1] != "" {
		where += " AND EXISTS(SELECT 1 FROM import_proposals ip WHERE ip.proposal_id=p.id AND ip.batch_id=? AND ip.stage='versions')"
		args = append(args, target[1])
	}
	if e = scopeRow(ctx, tx, "SELECT count(*)"+where, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, "SELECT plan_json,state,revision,primary_plan"+where+" ORDER BY CASE tier WHEN 'strong' THEN 0 ELSE 1 END,score DESC,CASE kind WHEN 'add_to_work' THEN 0 ELSE 1 END,p.id LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
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

// Discovery reads the complete organization in three SQL queries, independent of
// public detail limits and without treating only visible Editions as singleton.
func discoveryContext(ctx context.Context, q queryer, library string) (model.WorkContext, error) {
	c := model.WorkContext{Works: []model.Work{}, Editions: []model.WorkEdition{}, Series: []model.Series{}, Expected: map[string]int64{}}
	positions := map[string]int{}
	rows, e := scopeRows(ctx, q, "SELECT "+workColumns+" FROM "+workSource+" WHERE w.library_id=? ORDER BY w.id", library)
	if e != nil {
		return c, e
	}
	for rows.Next() {
		w, e := scanWork(rows)
		if e != nil {
			rows.Close()
			return c, e
		}
		w.EditionIDs = []string{}
		positions[w.ID] = len(c.Works)
		c.Works = append(c.Works, w)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return c, e
	}
	rows, e = scopeRows(ctx, q, "SELECT "+workEditionColumns+" FROM "+workEditionSource+" WHERE e.library_id=? ORDER BY e.id", "", library)
	if e != nil {
		return c, e
	}
	for rows.Next() {
		v, e := scanWorkEdition(rows)
		if e != nil {
			rows.Close()
			return c, e
		}
		c.Editions = append(c.Editions, v)
		at := positions[v.WorkID]
		c.Works[at].EditionIDs = append(c.Works[at].EditionIDs, v.ID)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return c, e
	}
	rows, e = scopeRows(ctx, q, "SELECT s.id,s.library_id,s.name,s.revision,sm.work_id FROM series s LEFT JOIN series_members sm ON sm.series_id=s.id WHERE s.library_id=? ORDER BY s.id,sm.position", library)
	if e != nil {
		return c, e
	}
	for rows.Next() {
		var v model.Series
		var wid *string
		if e = rows.Scan(&v.ID, &v.LibraryID, &v.Name, &v.Revision, &wid); e != nil {
			rows.Close()
			return c, e
		}
		if len(c.Series) == 0 || c.Series[len(c.Series)-1].ID != v.ID {
			v.WorkIDs = []string{}
			c.Series = append(c.Series, v)
		}
		at := len(c.Series) - 1
		if wid != nil {
			c.Series[at].WorkIDs = append(c.Series[at].WorkIDs, *wid)
			c.Series[at].MemberCount++
		}
	}
	e = rows.Err()
	rows.Close()
	return c, e
}
func proposalContext(full model.WorkContext, p rec.Proposal) model.WorkContext {
	selected := map[string]bool{p.Target: true}
	for _, m := range p.Members {
		selected[m.WorkID] = true
	}
	series := map[string]bool{}
	for _, w := range full.Works {
		if selected[w.ID] && w.SeriesID != "" {
			series[w.SeriesID] = true
		}
	}
	c := model.WorkContext{Works: []model.Work{}, Editions: []model.WorkEdition{}, Series: []model.Series{}, Expected: map[string]int64{}}
	for _, s := range full.Series {
		if series[s.ID] {
			c.Series = append(c.Series, s)
			c.Expected["series:"+s.ID] = s.Revision
		}
	}
	for _, w := range full.Works {
		if selected[w.ID] {
			c.Expected["work:"+w.ID] = w.Revision
			c.Works = append(c.Works, w)
		} else if series[w.SeriesID] {
			w.EditionIDs = nil
			c.Works = append(c.Works, w)
		}
	}
	for _, ed := range full.Editions {
		if selected[ed.WorkID] {
			c.Editions = append(c.Editions, ed)
			c.Expected["edition:"+ed.ID] = ed.Revision
		}
	}
	return c
}

func planProposal(c model.WorkContext, p rec.Proposal) (model.WorkResult, error) {
	if p.Kind == "add_to_work" {
		return organization.PlanWorkAddition(c, p.Target, p.NewIDs)
	}
	command, e := proposalCommand(p, c)
	if e != nil {
		return model.WorkResult{}, e
	}
	return organization.PlanWork(c, command)
}
