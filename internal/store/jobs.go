package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"time"
)

func (s *Store) Jobs(ctx context.Context, limit int) ([]Job, error) {
	rows, e := scopeRows(ctx, s.Read, "SELECT "+s.jobColumns()+" FROM jobs ORDER BY created_at DESC,id DESC LIMIT ?", limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		v, e := scanJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}
func (s *Store) Job(ctx context.Context, id string) (*Job, error) {
	return scanJob(scopeRow(ctx, s.Read, "SELECT "+s.jobColumns()+" FROM jobs WHERE id=?", id))
}
func (s *Store) StartScan(ctx context.Context, id, library string) (*Job, error) {
	var contextErr error
	ctx, contextErr = s.scanContext(ctx, library)
	if contextErr != nil {
		return nil, contextErr
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	l, e := scanLibrary(scopeRow(ctx, tx, "SELECT "+libraryColumns+" FROM libraries WHERE id=?", library))
	if e != nil {
		return nil, e
	}
	var blocking int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM maintenance_blocks b JOIN jobs j ON j.id=b.job_id WHERE b.library_id=? AND j.state IN('queued','running')", library).Scan(&blocking); e != nil {
		return nil, e
	}
	if blocking > 0 {
		return nil, model.ErrBusy
	}
	if l.Disabled {
		return nil, model.ErrDisabled
	}
	if l.TrashedAt != nil {
		return nil, model.ErrTrashed
	}
	active, e := scanJob(scopeRow(ctx, tx, "SELECT "+s.jobColumns()+" FROM jobs WHERE type='scan' AND library_id=? AND state IN ('queued','running','paused')", library))
	if e == nil {
		return active, ErrScanActive
	}
	if !errors.Is(e, ErrNotFound) {
		return nil, e
	}
	now := time.Now().Unix()
	_, e = tx.ExecContext(ctx, "INSERT INTO jobs(id,library_id,state,stage,generation,created_at,updated_at) VALUES(?,?,'queued','enumerating',1,?,?)", id, library, now, now)
	if e != nil {
		return nil, e
	}
	if l.Interval != nil {
		_, e = tx.ExecContext(ctx, "UPDATE libraries SET next_scan_at=? WHERE id=?", now+int64(*l.Interval)*60, library)
		if e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s.Job(ctx, id)
}
func (s *Store) Claim(ctx context.Context, id string) (*Job, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = healthJobAllowed(ctx, tx, id); e != nil {
		return nil, e
	}
	j, e := scanJob(scopeRow(ctx, tx, "SELECT "+s.jobColumns()+" FROM jobs WHERE id=? AND state IN ('queued','running')", id))
	if e != nil {
		return nil, e
	}
	if e = authorizeJob(ctx, tx, *j); e != nil {
		return nil, e
	}
	if e = enhancementGate(ctx, tx, model.EnhancementForJob(j.Type)); e != nil {
		return nil, e
	}
	_, e = tx.ExecContext(ctx, "UPDATE jobs SET state='running',generation=generation+1,updated_at=? WHERE id=?", time.Now().Unix(), id)
	if e != nil {
		return nil, e
	}
	j.State = "running"
	j.Generation++
	if j.Stage == "enumerating" {
		if _, e = tx.ExecContext(ctx, "DELETE FROM scan_entries WHERE job_id=?", id); e != nil {
			return nil, e
		}
	}
	if _, e = tx.ExecContext(ctx, "UPDATE job_resources SET generation=? WHERE job_id=?", j.Generation, j.ID); e != nil {
		return nil, e
	}
	return j, tx.Commit()
}
func guard(ctx context.Context, tx *sql.Tx, j Job) error {
	if e := authorizeJob(ctx, tx, j); e != nil {
		return e
	}
	if e := healthJobAllowed(ctx, tx, j.ID); e != nil {
		return e
	}
	var n int
	e := scopeRow(ctx, tx, "SELECT count(*) FROM jobs WHERE id=? AND generation=? AND state='running'", j.ID, j.Generation).Scan(&n)
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrStaleJob
	}
	var kind string
	if e = scopeRow(ctx, tx, "SELECT type FROM jobs WHERE id=?", j.ID).Scan(&kind); e != nil {
		return e
	}
	return enhancementGate(ctx, tx, model.EnhancementForJob(kind))
}
func (s *Store) StageEntries(ctx context.Context, j Job, items []Item) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	for _, v := range items {
		_, e = tx.ExecContext(ctx, "INSERT INTO scan_entries SELECT ?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM scan_exclusions WHERE library_id=? AND relative_path=?)", j.ID, v.Ordinal, v.Path, v.Size, v.Mtime, j.LibraryID, v.Path)
		if e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) Freeze(ctx context.Context, j Job) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO job_items(job_id,ordinal,relative_path,size,mtime_ns,state) SELECT job_id,ordinal,relative_path,size,mtime_ns,'pending' FROM scan_entries WHERE job_id=?", j.ID)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, "UPDATE jobs SET stage='processing',total=(SELECT count(*) FROM job_items WHERE job_id=?),updated_at=? WHERE id=?", j.ID, time.Now().Unix(), j.ID)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Items(ctx context.Context, id string, page, size int, pending bool) ([]Item, error) {
	filter := ""
	if pending {
		filter = " AND state='pending'"
	}
	extra := ",''"
	if s.Version >= 27 {
		extra = ",scan_result_json"
	}
	rows, e := scopeRows(ctx, s.Read, "SELECT job_id,ordinal,relative_path,size,mtime_ns,state,error_code,edition_id,content_revision,selection_revision,input_json,candidates_json,attempt,preview_id"+extra+" FROM job_items WHERE job_id=?"+filter+" ORDER BY ordinal LIMIT ? OFFSET ?", id, size, (page-1)*size)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var v Item
		var candidates, scanResult string
		if e = rows.Scan(&v.JobID, &v.Ordinal, &v.Path, &v.Size, &v.Mtime, &v.State, &v.Error, &v.BookID, &v.ContentRevision, &v.SelectionRevision, &v.Input, &candidates, &v.Attempt, &v.PreviewID, &scanResult); e != nil {
			return nil, e
		}
		_ = json.Unmarshal([]byte(candidates), &v.Candidates)
		if scanResult != "" {
			_ = json.Unmarshal([]byte(scanResult), &v.ScanResult)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Publish(ctx context.Context, j Job, item Item, b Edition) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	var excluded int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM scan_exclusions WHERE library_id=? AND relative_path=?", j.LibraryID, item.Path).Scan(&excluded); e != nil {
		return e
	}
	if excluded > 0 {
		return finishExcluded(ctx, tx, j, item)
	}
	var state string
	if e = scopeRow(ctx, tx, "SELECT state FROM job_items WHERE job_id=? AND ordinal=?", j.ID, item.Ordinal).Scan(&state); e != nil {
		return e
	}
	if state != "pending" {
		return nil
	}
	if s.Version >= 27 && b.ScanResult != nil && b.ScanResult.InputParsingRevision > 0 {
		var current model.FileParsing
		e = scopeRow(ctx, tx, "SELECT encoding_override,parsing_revision,parser_version,directory_digest,effective_encoding FROM files WHERE edition_id=?", b.ID).Scan(&current.EncodingOverride, &current.ParsingRevision, &current.ParserVersion, &current.DirectoryDigest, &current.EffectiveEncoding)
		if e != nil {
			return e
		}
		if current.ParsingRevision != b.ScanResult.InputParsingRevision || current.Fingerprint() != b.ScanResult.InputParsingFingerprint {
			return model.ErrStaleJob
		}
	}
	b.IndexedAt = time.Now().Unix()
	if e = saveEditionIndex(ctx, tx, b); e != nil {
		return e
	}
	if s.Version >= 27 && b.ScanResult != nil {
		if _, e = tx.ExecContext(ctx, "UPDATE files SET parsing_revision=?,parser_version=?,directory_digest=?,effective_encoding=? WHERE edition_id=?", b.ParsingRevision, b.ParserVersion, b.DirectoryDigest, b.EffectiveEncoding, b.ID); e != nil {
			return e
		}
		raw, _ := json.Marshal(b.ScanResult)
		if _, e = tx.ExecContext(ctx, "UPDATE job_items SET scan_result_json=? WHERE job_id=? AND ordinal=?", string(raw), j.ID, item.Ordinal); e != nil {
			return e
		}
	}
	if s.Version >= 12 {
		if e = importScanMember(ctx, tx, j, b); e != nil {
			return e
		}
	}
	if e = reproject(ctx, tx, b.ID); e != nil {
		return e
	}
	itemState := "completed"
	failed := 0
	if b.Error != "" {
		itemState = "failed"
		failed = 1
	}
	_, e = tx.ExecContext(ctx, "UPDATE job_items SET state=?,error_code=?,edition_id=?,content_revision=? WHERE job_id=? AND ordinal=?", itemState, b.Error, b.ID, b.ContentRevision, j.ID, item.Ordinal)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, "UPDATE jobs SET done=done+1,failed=failed+?,updated_at=? WHERE id=?", failed, time.Now().Unix(), j.ID)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Finish(ctx context.Context, j Job, allowMissing bool, code string) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	if allowMissing {
		if _, e = tx.ExecContext(ctx, "UPDATE editions SET revision=revision+1 WHERE trashed_at IS NULL AND id IN(SELECT edition_id FROM files WHERE library_id=? AND file_state!='missing' AND NOT EXISTS(SELECT 1 FROM scan_exclusions ex WHERE ex.library_id=files.library_id AND ex.relative_path=files.relative_path) AND NOT EXISTS(SELECT 1 FROM job_items WHERE job_id=? AND relative_path=files.relative_path))", j.LibraryID, j.ID); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE files SET file_state='missing',error_code='file_missing' WHERE library_id=? AND edition_id IN(SELECT id FROM editions WHERE trashed_at IS NULL) AND file_state!='missing' AND NOT EXISTS(SELECT 1 FROM scan_exclusions ex WHERE ex.library_id=files.library_id AND ex.relative_path=files.relative_path) AND NOT EXISTS(SELECT 1 FROM job_items WHERE job_id=? AND relative_path=files.relative_path)", j.LibraryID, j.ID)
		if e != nil {
			return e
		}
	}
	state := "completed"
	if code != "" {
		state = "failed"
	}
	_, e = tx.ExecContext(ctx, "UPDATE jobs SET state=?,stage='finished',error_code=?,updated_at=? WHERE id=?", state, code, time.Now().Unix(), j.ID)
	if e != nil {
		return e
	}
	if allowMissing {
		_, e = tx.ExecContext(ctx, "UPDATE libraries SET last_complete_scan_id=?,last_error=? WHERE id=?", j.ID, code, j.LibraryID)
	} else {
		_, e = tx.ExecContext(ctx, "UPDATE libraries SET last_error=? WHERE id=?", code, j.LibraryID)
	}
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Cancel(ctx context.Context, id string) error {
	return s.ControlJob(ctx, id, "cancel", "")
}
func (s *Store) ActiveJobs(ctx context.Context) ([]Job, error) {
	rows, e := scopeRows(ctx, s.Read, "SELECT "+s.jobColumns()+" FROM jobs WHERE type='scan' AND state IN ('queued','running') ORDER BY created_at,id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		v, e := scanJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}
func (s *Store) DueLibraries(ctx context.Context, now int64) ([]string, error) {
	rows, e := scopeRows(ctx, s.Read, "SELECT id FROM libraries WHERE disabled=0 AND trashed_at IS NULL AND next_scan_at<=? AND scan_interval_minutes IS NOT NULL AND NOT EXISTS(SELECT 1 FROM jobs WHERE type='scan' AND library_id=libraries.id AND state IN ('queued','running','paused'))", now)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) DeferPeriodic(ctx context.Context, id, code string) error {
	_, e := s.Write.ExecContext(ctx, "UPDATE libraries SET next_scan_at=?+scan_interval_minutes*60,last_error=? WHERE id=? AND scan_interval_minutes IS NOT NULL", time.Now().Unix(), code, id)
	return e
}

func (s *Store) Finalizing(ctx context.Context, j Job) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	var pending int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM job_items WHERE job_id=? AND state='pending'", j.ID).Scan(&pending); e != nil {
		return e
	}
	if pending != 0 {
		return ErrConflict
	}
	_, e = tx.ExecContext(ctx, "UPDATE jobs SET stage='finalizing',updated_at=? WHERE id=? AND total IS NOT NULL", time.Now().Unix(), j.ID)
	if e != nil {
		return e
	}
	return tx.Commit()
}

func finishExcluded(ctx context.Context, tx *sql.Tx, j Job, item Item) error {
	if e := finishItem(ctx, tx, j, item, "deferred", "scan_excluded", nil); e != nil {
		return e
	}
	return tx.Commit()
}
