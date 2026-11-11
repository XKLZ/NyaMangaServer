package store

import (
	"context"
	"database/sql"
	"errors"
	"nyamangaserver.local/server/internal/model"
)

func (s *Store) ReconcileImports(ctx context.Context) error {
	rows, e := scopeRows(ctx, s.Read, "SELECT id FROM import_batches WHERE state='processing' ORDER BY created_at,id")
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		if e = s.reconcileImport(ctx, id); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) reconcileImport(ctx context.Context, id string) error {
	var contextErr error
	ctx, contextErr = s.importContext(ctx, id)
	if contextErr != nil {
		return contextErr
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	b, e := scanImport(scopeRow(ctx, tx, "SELECT "+importColumns+" FROM import_batches WHERE id=?", id))
	if e != nil {
		return e
	}
	if b.State != "processing" {
		return nil
	}
	if feature := importStageFeature(b.Stage); feature != "" {
		if err := enhancementGate(ctx, tx, feature); err != nil {
			if err != model.ErrEnhancementDisabled {
				return err
			}
			_, e = tx.ExecContext(ctx, "UPDATE import_batches SET state='paused',error_code='enhancement_disabled',revision=revision+1,updated_at=unixepoch() WHERE id=?", id)
			if e != nil {
				return e
			}
			return tx.Commit()
		}
	}
	if b.Stage == "versions" || b.Stage == "series" {
		_, state, _, err := importAttempt(ctx, tx, id, b.Stage)
		if err != nil {
			return err
		}
		if state == "failed" || state == "cancelled" {
			if _, e = tx.ExecContext(ctx, "UPDATE import_batches SET state='failed',error_code=?,revision=revision+1,updated_at=unixepoch() WHERE id=?", b.Stage+"_recommendation_failed", id); e != nil {
				return e
			}
			return tx.Commit()
		}
	}
	var pending, paused, failed int
	e = scopeRow(ctx, tx, `SELECT COALESCE(sum(CASE WHEN j.id IS NULL OR j.state IN('queued','running') THEN 1 ELSE 0 END),0),COALESCE(sum(j.state='paused'),0),COALESCE(sum(j.state IN('failed','cancelled')),0) FROM import_child_requests r LEFT JOIN jobs j ON j.id=r.job_id WHERE r.batch_id=? AND r.stage=? AND (r.stage='metadata' OR r.rowid=(SELECT max(rowid) FROM import_child_requests WHERE batch_id=r.batch_id AND stage=r.stage))`, id, b.Stage).Scan(&pending, &paused, &failed)
	if e != nil {
		return e
	}
	if paused > 0 {
		if _, e = tx.ExecContext(ctx, "UPDATE jobs SET state='paused',generation=generation+1,updated_at=unixepoch() WHERE id IN(SELECT job_id FROM import_children WHERE batch_id=?) AND state IN('queued','running')", id); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE import_batches SET state='paused',error_code='child_requires_attention',revision=revision+1,updated_at=unixepoch() WHERE id=?", id)
		if e != nil {
			return e
		}
		return tx.Commit()
	}
	if pending > 0 {
		return nil
	}
	if b.Stage == "scan" {
		if failed > 0 {
			_, e = tx.ExecContext(ctx, "UPDATE import_batches SET state='failed',error_code='scan_incomplete',revision=revision+1,updated_at=unixepoch() WHERE id=?", id)
			if e != nil {
				return e
			}
			return tx.Commit()
		}
		var count int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM import_members WHERE batch_id=?", id).Scan(&count); e != nil {
			return e
		}
		if count == 0 {
			_, e = tx.ExecContext(ctx, "UPDATE import_batches SET state='completed',stage='finished',revision=revision+1,updated_at=unixepoch() WHERE id=?", id)
			if e != nil {
				return e
			}
			return tx.Commit()
		}
		if _, e = tx.ExecContext(ctx, "UPDATE import_batches SET stage='metadata',revision=revision+1,updated_at=unixepoch() WHERE id=?", id); e != nil {
			return e
		}
		rows, err := scopeRows(ctx, tx, "SELECT edition_id FROM import_members WHERE batch_id=? AND scan_error='' AND active_job_id IS NULL ORDER BY edition_id", id)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var v string
			if err = rows.Scan(&v); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for start := 0; start < len(ids); start += 1000 {
			end := min(start+1000, len(ids))
			job, err := reserveImport(ctx, tx, id, "metadata", ids[start:end], "")
			if err != nil {
				return err
			}
			for _, ed := range ids[start:end] {
				if _, err = tx.ExecContext(ctx, "UPDATE import_members SET active_job_id=?,request_generation=request_generation+1 WHERE batch_id=? AND edition_id=?", job, id, ed); err != nil {
					return err
				}
			}
		}
		if len(ids) > 0 {
			return tx.Commit()
		}
	}
	_, e = tx.ExecContext(ctx, "UPDATE import_batches SET state='review',revision=revision+1,updated_at=unixepoch() WHERE id=?", id)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) AdvanceImport(ctx context.Context, id string, expected int64) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	b, e := importGuard(ctx, tx, id, expected)
	if e != nil {
		return e
	}
	if e = advanceImportTx(ctx, tx, b); e != nil {
		return e
	}
	return tx.Commit()
}

func advanceImportTx(ctx context.Context, tx *sql.Tx, b *model.ImportBatch) error {
	id := b.ID
	var e error
	if b.State != "review" || b.Stage == "scan" {
		return model.ErrConflict
	}
	if b.Stage == "versions" || b.Stage == "series" {
		failure, skipped, err := importRecommendationOutcome(ctx, tx, id, b.Stage)
		if err != nil {
			return err
		}
		if failure != nil && !skipped {
			return model.ErrConflict
		}
	}
	table, key, col, tokenCol := "import_members", "edition_id", "metadata_decision", "metadata_token"
	if b.Stage == "versions" {
		col, tokenCol = "version_decision", "version_token"
	}
	if b.Stage == "series" {
		table, key, col, tokenCol = "import_works", "work_id", "decision", "input_token"
	}
	rows, e := scopeRows(ctx, tx, "SELECT "+key+","+col+","+tokenCol+" FROM "+table+" WHERE batch_id=? ORDER BY "+key, id)
	if e != nil {
		return e
	}
	type reviewed struct{ id, decision, token string }
	items := []reviewed{}
	for rows.Next() {
		var v reviewed
		if e = rows.Scan(&v.id, &v.decision, &v.token); e != nil {
			rows.Close()
			return e
		}
		items = append(items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range items {
		if v.decision == "" {
			return model.ErrConflict
		}
		var token string
		if b.Stage == "metadata" {
			token, e = importMetadataToken(ctx, tx, v.id)
		} else {
			token, e = importOrganizationToken(ctx, tx, v.id, b.Stage == "series")
		}
		if e != nil {
			return e
		}
		if token != v.token {
			return model.ErrConflict
		}
	}

	// Every gate rechecks upstream inputs as well. Series decisions themselves do
	// not invalidate a version decision: its token describes Work membership only.
	if b.Stage != "metadata" {
		rows, err := scopeRows(ctx, tx, "SELECT edition_id,metadata_token,version_token FROM import_members WHERE batch_id=?", id)
		if err != nil {
			return err
		}
		type upstream struct{ id, meta, version string }
		all := []upstream{}
		for rows.Next() {
			var v upstream
			if err = rows.Scan(&v.id, &v.meta, &v.version); err != nil {
				rows.Close()
				return err
			}
			all = append(all, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, v := range all {
			now, err := importMetadataToken(ctx, tx, v.id)
			if err != nil {
				return err
			}
			if v.meta != now {
				return model.ErrConflict
			}
			frozen, err := importEnhancementFlags(ctx, tx, id)
			if err != nil {
				return err
			}
			if b.Stage == "series" && frozen["versions"] {
				now, err = importOrganizationToken(ctx, tx, v.id, false)
				if err != nil {
					return err
				}
				if v.version != now {
					return model.ErrConflict
				}
			}
		}
	}
	var outstanding int
	if e = scopeRow(ctx, tx, `SELECT count(*) FROM import_child_requests r LEFT JOIN jobs j ON j.id=r.job_id WHERE r.batch_id=? AND r.stage=? AND (r.stage='metadata' OR r.rowid=(SELECT max(rowid) FROM import_child_requests WHERE batch_id=r.batch_id AND stage=r.stage)) AND (j.id IS NULL OR j.state IN('queued','running','paused'))`, id, b.Stage).Scan(&outstanding); e != nil {
		return e
	}
	decisions, err := importDecisions(ctx, tx, id)
	if err != nil {
		return err
	}
	if outstanding > 0 && decisions[b.Stage].Decision != "skipped_disabled" {
		return model.ErrBusy
	}
	flags, err := importEnhancementFlags(ctx, tx, id)
	if err != nil {
		return err
	}
	stage, state := "finished", "completed"
	if b.Stage == "metadata" {
		stage, state = "versions", "processing"
	}
	if b.Stage == "versions" || b.Stage == "metadata" && !flags["versions"] {
		stage, state = "series", "processing"
		if flags["series"] {
			_, e = tx.ExecContext(ctx, `INSERT INTO import_works(batch_id,work_id,input_token) SELECT DISTINCT m.batch_id,e.work_id,'' FROM import_members m JOIN editions e ON e.id=m.edition_id WHERE m.batch_id=?`, id)
			if e != nil {
				return e
			}
		}
	}
	if stage == "series" && !flags["series"] {
		stage, state = "finished", "completed"
	}
	if _, e = tx.ExecContext(ctx, "UPDATE import_batches SET stage=?,state=?,error_code='',revision=revision+1,updated_at=unixepoch() WHERE id=?", stage, state, id); e != nil {
		return e
	}
	if state == "processing" {
		if err := enhancementGate(ctx, tx, importStageFeature(stage)); err != nil {
			if err != model.ErrEnhancementDisabled {
				return err
			}
			_, e = tx.ExecContext(ctx, "UPDATE import_batches SET state='paused',error_code='enhancement_disabled' WHERE id=?", id)
			return e
		}
	}
	if state == "processing" {
		pool, err := recommendationPool(ctx, tx, []string{b.LibraryID})
		if err != nil {
			return err
		}
		total := len(pool.Works)
		var eligible int
		if stage == "versions" {
			e = scopeRow(ctx, tx, `SELECT count(*) FROM import_members m JOIN editions ed ON ed.id=m.edition_id WHERE m.batch_id=? AND (SELECT count(*) FROM editions x WHERE x.work_id=ed.work_id)=1`, id).Scan(&eligible)
		} else {
			e = scopeRow(ctx, tx, `SELECT count(*) FROM import_works w LEFT JOIN series_members sm ON sm.work_id=w.work_id WHERE w.batch_id=? AND sm.series_id IS NULL`, id).Scan(&eligible)
		}
		if e != nil {
			return e
		}
		if eligible == 0 {
			total = 0
		}
		// With a single Work there is no possible target. Human disposition is still required.
		if total < 2 {
			_, e = tx.ExecContext(ctx, "UPDATE import_batches SET state='review' WHERE id=?", id)
		} else {
			_, e = reserveImport(ctx, tx, id, stage, nil, "")
		}
		if e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) ImportRequestFailed(ctx context.Context, r model.ImportRequest, code string) error {
	_, e := s.Write.ExecContext(ctx, `UPDATE import_batches SET state='failed',error_code=?,revision=revision+1,updated_at=unixepoch() WHERE id=? AND state='processing' AND stage=?`, code, r.BatchID, r.Stage)
	return e
}
func (s *Store) ControlImport(ctx context.Context, id, action string, expected int64) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	b, e := scanImport(scopeRow(ctx, tx, "SELECT "+importColumns+" FROM import_batches WHERE id=?", id))
	if e != nil {
		return e
	}
	if b.Revision != expected {
		return model.ErrConflict
	}
	if action == "resume" || action == "retry" {
		if e = armEnhancement(ctx, tx, importStageFeature(b.Stage)); e != nil {
			return e
		}
		if e = enhancementGate(ctx, tx, importStageFeature(b.Stage)); e != nil {
			return e
		}
		l, err := scanLibrary(scopeRow(ctx, tx, "SELECT "+libraryColumns+" FROM libraries WHERE id=?", b.LibraryID))
		if err != nil {
			return err
		}
		if l.Disabled {
			return model.ErrDisabled
		}
		if l.TrashedAt != nil {
			return model.ErrTrashed
		}
	}
	if action == "skip_recommendation" {
		if e = skipImportRecommendation(ctx, tx, b); e != nil {
			return e
		}
		return tx.Commit()
	}
	target := ""
	switch action {
	case "pause":
		if b.State != "processing" {
			return model.ErrConflict
		}
		target = "paused"
	case "resume":
		if b.State != "paused" {
			return model.ErrConflict
		}
		target = "processing"
	case "cancel":
		if b.State == "completed" || b.State == "cancelled" {
			return model.ErrConflict
		}
		target = "cancelled"
	case "recheck":
		if b.Stage == "scan" || b.Stage == "finished" || b.State == "cancelled" || b.State == "completed" {
			return model.ErrConflict
		}
		target = "review"
	case "retry":
		if b.State != "failed" {
			return model.ErrConflict
		}
		target = "processing"
	default:
		return model.ErrInput
	}
	if e = controlImportSearches(ctx, tx, id, action); e != nil {
		return e
	}
	if action == "recheck" {
		if _, e = tx.ExecContext(ctx, "UPDATE jobs SET state='cancelled',generation=generation+1 WHERE id IN(SELECT job_id FROM import_children WHERE batch_id=?) AND state IN('queued','running','paused')", id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM job_resources WHERE job_id IN(SELECT job_id FROM import_children WHERE batch_id=?)", id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE import_members SET metadata_decision='',metadata_token='',version_decision='',version_token='',active_job_id=NULL,revision=revision+1 WHERE batch_id=?", id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM import_works WHERE batch_id=?", id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE import_batches SET stage='metadata',state='review',error_code='',revision=revision+1,updated_at=unixepoch() WHERE id=?", id); e != nil {
			return e
		}
		return tx.Commit()
	}
	// The generation update invalidates pending worker commits in the same transaction.
	if action == "pause" || action == "cancel" {
		jobState := "paused"
		if action == "cancel" {
			jobState = "cancelled"
		}
		if _, e = tx.ExecContext(ctx, "UPDATE jobs SET state=?,generation=generation+1,updated_at=unixepoch() WHERE id IN(SELECT job_id FROM import_children WHERE batch_id=?) AND state IN('queued','running','paused')", jobState, id); e != nil {
			return e
		}
		if action == "cancel" {
			if _, e = tx.ExecContext(ctx, "DELETE FROM job_resources WHERE job_id IN(SELECT job_id FROM import_children WHERE batch_id=?)", id); e != nil {
				return e
			}
		}
	} else {
		if _, e = tx.ExecContext(ctx, "UPDATE jobs SET state='queued',generation=generation+1,updated_at=unixepoch() WHERE id IN(SELECT job_id FROM import_children WHERE batch_id=? AND stage=?) AND state='paused'", id, b.Stage); e != nil {
			return e
		}
		if action == "resume" && importStageFeature(b.Stage) != "" {
			var requests int
			if e = scopeRow(ctx, tx, "SELECT count(*) FROM import_child_requests WHERE batch_id=? AND stage=?", id, b.Stage).Scan(&requests); e != nil {
				return e
			}
			if requests == 0 {
				if _, e = reserveImport(ctx, tx, id, b.Stage, nil, ""); e != nil {
					return e
				}
			}
		}
		if action == "retry" {
			// Failed scan jobs cannot be resumed mid-finalization; reserve a fresh scan.
			if b.Stage == "scan" || b.Stage == "versions" || b.Stage == "series" {
				if _, e = reserveImport(ctx, tx, id, b.Stage, nil, ""); e != nil {
					return e
				}
			}
		}
	}
	if _, e = tx.ExecContext(ctx, "UPDATE import_batches SET state=?,error_code='',revision=revision+1,updated_at=unixepoch() WHERE id=?", target, id); e != nil {
		return e
	}
	return tx.Commit()
}

// Suppress SQL no-row leakage at service boundaries.
func importNotFound(e error) error {
	if errors.Is(e, sql.ErrNoRows) {
		return model.ErrNotFound
	}
	return e
}
