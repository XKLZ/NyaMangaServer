package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
)

func (s *Store) BeginSeriesEvidence(ctx context.Context, j model.Job, profiles map[string]string) ([]rec.SeriesEvidenceBlock, error) {
	out := []rec.SeriesEvidenceBlock{}
	if s.Version < 13 {
		return out, nil
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return nil, e
	}
	raw, _ := json.Marshal(profiles)
	var base sql.NullString
	e = scopeRow(ctx, tx, `SELECT g.job_id FROM series_evidence_generations g JOIN jobs j ON j.id=g.job_id WHERE g.library_id=? AND g.execution_version=? AND g.job_id!=? ORDER BY g.state='complete' DESC,g.created_at DESC,g.job_id DESC LIMIT 1`, j.LibraryID, rec.SeriesExecutionVersion, j.ID).Scan(&base)
	if e != nil && e != sql.ErrNoRows {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO series_evidence_generations(job_id,library_id,execution_version,profiles_json,base_job_id) VALUES(?,?,?,?,?) ON CONFLICT(job_id) DO UPDATE SET profiles_json=excluded.profiles_json`, j.ID, j.LibraryID, rec.SeriesExecutionVersion, string(raw), base); e != nil {
		return nil, e
	}
	// Include this run's committed partial blocks before the pinned baseline.
	rows, e := scopeRows(ctx, tx, `SELECT block_key,storage_key,sha256,size,records FROM series_evidence_blocks WHERE job_id=? OR job_id=(SELECT base_job_id FROM series_evidence_generations WHERE job_id=?) ORDER BY job_id=? DESC,block_key`, j.ID, j.ID, j.ID)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var v rec.SeriesEvidenceBlock
		if e = rows.Scan(&v.Key, &v.StorageKey, &v.SHA256, &v.Size, &v.Records); e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	return out, tx.Commit()
}
func (s *Store) SaveSeriesEvidence(ctx context.Context, j model.Job, b rec.SeriesEvidenceBlock, computed, reused int64) error {
	if s.Version < 13 {
		return nil
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	result, e := tx.ExecContext(ctx, "INSERT OR IGNORE INTO series_evidence_blocks VALUES(?,?,?,?,?,?)", j.ID, b.Key, b.StorageKey, b.SHA256, b.Size, b.Records)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if _, e = tx.ExecContext(ctx, "UPDATE series_evidence_generations SET bytes=bytes+?,computed_pairs=?,reused_pairs=? WHERE job_id=?", n*b.Size, computed, reused, j.ID); e != nil {
		return model.ErrCapacity
	}
	return tx.Commit()
}
func (s *Store) CompleteSeriesEvidence(ctx context.Context, j model.Job) (finishErr error) {
	defer func() { finishErr = seriesStorageError(finishErr) }()
	if s.Version < 13 {
		return nil
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE series_evidence_generations SET state='complete',base_job_id=NULL WHERE job_id=?", j.ID); e != nil {
		return e
	}
	// Only unpinned generations outside active/previous/building are retired.
	_, e = tx.ExecContext(ctx, `DELETE FROM series_evidence_generations WHERE library_id=? AND job_id!=? AND job_id NOT IN(SELECT job_id FROM series_evidence_generations WHERE library_id=? AND state='complete' ORDER BY created_at DESC,job_id DESC LIMIT 2) AND job_id NOT IN(SELECT base_job_id FROM series_evidence_generations WHERE base_job_id IS NOT NULL) AND job_id NOT IN(SELECT id FROM jobs WHERE state IN('queued','running','paused'))`, j.LibraryID, j.ID, j.LibraryID)
	if e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) SeriesEvidenceKeys(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	if s.Version < 13 {
		return out, nil
	}
	rows, e := scopeRows(ctx, s.Read, "SELECT DISTINCT storage_key FROM series_evidence_blocks")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if e = rows.Scan(&key); e != nil {
			return nil, e
		}
		out[key] = true
	}
	return out, rows.Err()
}
func (s *Store) SeriesEvidenceStats(ctx context.Context, id string) (map[string]any, error) {
	out := map[string]any{"execution_version": rec.SeriesExecutionVersion, "baseline_reason": "no_verified_baseline", "computed_pairs": 0, "reused_pairs": 0}
	if s.Version < 13 {
		return out, nil
	}
	var computed, reused, bytes int64
	var base sql.NullString
	var state, diagnostic string
	e := scopeRow(ctx, s.Read, `SELECT COALESCE(g.computed_pairs,0),COALESCE(g.reused_pairs,0),COALESCE(g.bytes,0),g.base_job_id,COALESCE(g.state,''),r.diagnostics_json FROM series_discovery_runs r LEFT JOIN series_evidence_generations g ON g.job_id=r.job_id WHERE r.job_id=?`, id).Scan(&computed, &reused, &bytes, &base, &state, &diagnostic)
	if e == sql.ErrNoRows {
		return out, nil
	}
	if e != nil {
		return nil, e
	}
	var details map[string]string
	if json.Unmarshal([]byte(diagnostic), &details) == nil && len(details) > 0 {
		out["diagnostics"] = details
	}
	out["computed_pairs"] = computed
	out["reused_pairs"] = reused
	out["compressed_bytes"] = bytes
	out["state"] = state
	if reused > 0 || base.Valid {
		out["baseline_reason"] = "verified_signals"
	}
	return out, nil
}

func (s *Store) SaveSeriesDiagnostic(ctx context.Context, j model.Job, phase, reason string) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	allowedPhase := map[string]bool{"prepare_features": true, "restore_checkpoint": true, "compare": true, "generate_proposals": true, "publish": true, "finalize_evidence": true}
	allowedReason := map[string]bool{"runtime_unavailable": true, "invalid_checkpoint": true, "resource_budget": true, "database_constraint": true, "storage_unavailable": true, "storage_capacity": true, "proposal_invalid": true}
	if !allowedPhase[phase] || !allowedReason[reason] {
		return model.ErrInput
	}
	raw, _ := json.Marshal(map[string]string{"phase": phase, "reason": reason})
	if _, e = tx.ExecContext(ctx, "UPDATE series_discovery_runs SET diagnostics_json=? WHERE job_id=?", string(raw), j.ID); e != nil {
		return e
	}
	return tx.Commit()
}
