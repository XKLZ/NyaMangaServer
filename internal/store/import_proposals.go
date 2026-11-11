package store

import (
	"context"
	"database/sql"
	"errors"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
)

func importJobScope(ctx context.Context, q queryer, job string) (string, error) {
	var exists int
	if e := scopeRow(ctx, q, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='import_children'").Scan(&exists); e != nil || exists == 0 {
		return "", e
	}
	var batch string
	e := scopeRow(ctx, q, "SELECT batch_id FROM import_children WHERE job_id=?", job).Scan(&batch)
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	return batch, e
}
func importTargetIDs(ctx context.Context, q queryer, batch, stage string) (map[string]bool, error) {
	table, column := "import_members", "edition_id"
	if stage == "series" {
		table, column = "import_works", "work_id"
	}
	rows, e := scopeRows(ctx, q, "SELECT "+column+" FROM "+table+" WHERE batch_id=?", batch)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out[id] = true
	}
	return out, rows.Err()
}
func involves(ids []string, targets map[string]bool) bool {
	for _, id := range ids {
		if targets[id] {
			return true
		}
	}
	return false
}
func linkImportProposal(ctx context.Context, tx *sql.Tx, batch, stage, id string) error {
	_, e := tx.ExecContext(ctx, "INSERT OR IGNORE INTO import_proposals VALUES(?,?,?)", batch, stage, id)
	return e
}
func (s *Store) CheckImportProposal(ctx context.Context, batch, stage, id string, actions ...string) error {
	var n int
	query := `SELECT count(*) FROM import_proposals p JOIN import_batches b ON b.id=p.batch_id WHERE p.batch_id=? AND p.stage=? AND p.proposal_id=?`
	args := []any{batch, stage, id}
	if len(actions) == 0 || (actions[0] != "read" && actions[0] != "undo") {
		query += ` AND b.stage=? AND b.state IN('processing','review')`
		args = append(args, stage)
	}
	e := scopeRow(ctx, s.Read, query, args...).Scan(&n)
	if e != nil {
		return e
	}
	if n != 1 {
		return model.ErrConflict
	}
	return nil
}

// Batch linkage is checked before invoking the ordinary organization service.
// Actual constraints, revision checks and decision snapshots remain its single
// transaction; no second organization model is maintained by importflow.
func (s *Store) ImportWorkProposals(ctx context.Context, batch, state string, page, size int, alternatives ...bool) (rec.ProposalPage, error) {
	return s.WorkProposals(ctx, "", "", "", state, importAlternatives(alternatives), page, size, "", batch, importOnlyAlternatives(alternatives))
}
func (s *Store) ImportSeriesProposals(ctx context.Context, batch, state string, page, size int, alternatives ...bool) (rec.SeriesProposalPage, error) {
	return s.SeriesProposals(ctx, "", "", "", state, page, size, rec.SeriesProposalFilter{Alternatives: importAlternatives(alternatives), OnlyAlternatives: importOnlyAlternatives(alternatives) != "", ImportBatchID: batch})
}

func guardImportPlan(ctx context.Context, tx *sql.Tx, proposal, stage, action string) error {
	var exists int
	if e := scopeRow(ctx, tx, "SELECT count(*) FROM sqlite_master WHERE name='import_proposals'").Scan(&exists); e != nil || exists == 0 {
		return e
	}
	var state, current string
	e := scopeRow(ctx, tx, "SELECT b.state,b.stage FROM import_proposals p JOIN import_batches b ON b.id=p.batch_id WHERE p.proposal_id=? AND p.stage=?", proposal, stage).Scan(&state, &current)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if action == "undo" {
		return nil
	}
	if current != stage || (state != "processing" && state != "review") {
		return model.ErrConflict
	}
	return nil
}
