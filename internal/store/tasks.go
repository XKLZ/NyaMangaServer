package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"strings"
	"time"
)

func (s *Store) ControlJob(ctx context.Context, id, action, reason string) error {
	state, e := s.HealthState(ctx)
	if e != nil {
		return e
	}
	if state.Phase != "idle" {
		if action == "resume" {
			if e = healthJobAllowed(ctx, s.Read, id); e != nil {
				return e
			}
		}
		return s.healthWrite(ctx, func(c *sql.Conn) error {
			if action == "resume" && state.RepairID != nil && *state.RepairID == id {
				if _, e := c.ExecContext(ctx, "UPDATE jobs SET state='paused' WHERE id=? AND state IN('cancelled','failed','completed')", id); e != nil {
					return e
				}
			}
			return s.controlJob(ctx, id, action, reason, c)
		})
	}
	return s.controlJob(ctx, id, action, reason, s.Write)
}
func (s *Store) controlJob(ctx context.Context, id, action, reason string, begin interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}) error {
	tx, e := begin.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	j, e := scanJob(scopeRow(ctx, tx, "SELECT "+s.jobColumns()+" FROM jobs WHERE id=?", id))
	if e != nil {
		return e
	}
	if j.Type == "metadata_search" && action == "resume" {
		var batch, id, search string
		if e = scopeRow(ctx, tx, "SELECT batch_id,edition_id,id FROM source_searches WHERE job_id=?", j.ID).Scan(&batch, &id, &search); e != nil {
			return model.ErrConflict
		}
		v, e := sourceSearchTx(ctx, tx, batch, id, search)
		if e != nil {
			return e
		}
		if e = searchGuard(ctx, tx, v); e != nil {
			return e
		}
	}
	target := ""
	switch action {
	case "pause":
		target = "paused"
		if j.State == target {
			return nil
		}
		if j.State != "queued" && j.State != "running" {
			return ErrConflict
		}
	case "resume":
		if e = authorizeJob(ctx, tx, *j); e != nil {
			return e
		}
		if e = armEnhancement(ctx, tx, model.EnhancementForJob(j.Type)); e != nil {
			return e
		}
		if e = enhancementGate(ctx, tx, model.EnhancementForJob(j.Type)); e != nil {
			return e
		}
		if s.Version >= 4 {
			if j.LibraryID != "" {
				l, e := scanLibrary(scopeRow(ctx, tx, "SELECT "+libraryColumns+" FROM libraries WHERE id=?", j.LibraryID))
				if e != nil {
					return e
				}
				if l.Disabled {
					return model.ErrDisabled
				}
				if l.TrashedAt != nil {
					return model.ErrTrashed
				}
			}
			rows, e := scopeRows(ctx, tx, "SELECT edition_id FROM job_items WHERE job_id=? AND edition_id IS NOT NULL", id)
			if e != nil {
				return e
			}
			ids := []string{}
			for rows.Next() {
				var bid string
				if e = rows.Scan(&bid); e != nil {
					rows.Close()
					return e
				}
				ids = append(ids, bid)
			}
			rows.Close()
			for _, bid := range ids {
				if e = managementGuard(ctx, tx, bid); e != nil {
					return e
				}
			}
		}
		target = "queued"
		if j.State == target || j.State == "running" {
			return nil
		}
		if j.State != "paused" {
			return ErrConflict
		}
	case "cancel":
		target = "cancelled"
		if j.State == target {
			return nil
		}
		if j.State != "queued" && j.State != "running" && j.State != "paused" {
			return ErrConflict
		}
	default:
		return model.ErrInput
	}
	if _, e = tx.ExecContext(ctx, "UPDATE jobs SET state=?,generation=generation+1,error_code=?,updated_at=? WHERE id=?", target, reason, time.Now().Unix(), id); e != nil {
		return e
	}
	if target == "queued" {
		if _, e = tx.ExecContext(ctx, "UPDATE job_items SET attempt=0 WHERE job_id=? AND state='pending'", id); e != nil {
			return e
		}
	}
	if target == "cancelled" {
		if _, e = tx.ExecContext(ctx, "DELETE FROM job_resources WHERE job_id=?", id); e != nil {
			return e
		}
	}
	if s.Version >= 23 {
		phase := "paused"
		errorCode := "user_" + action
		if target == "queued" {
			phase = "draining"
			errorCode = ""
		}
		if _, e = tx.ExecContext(ctx, "UPDATE health_repairs SET state=?,error_code=?,revision=revision+1,updated_at=unixepoch() WHERE job_id=?", phase, errorCode, id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE health_state SET phase=?,revision=revision+1,updated_at=unixepoch() WHERE repair_id=?", phase, id); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) BackgroundJobs(ctx context.Context) ([]model.Job, error) {
	dictionary, err := enhancementEnabled(ctx, s.Read, model.EnhancementDictionary)
	if err != nil {
		return nil, err
	}
	rows, e := scopeRows(ctx, s.Read, "SELECT "+s.jobColumns()+" FROM jobs WHERE type IN ('metadata','tag_translation','metadata_preview','metadata_search') AND state IN ('queued','running') AND (type!='tag_translation' OR ?) ORDER BY created_at,id", dictionary)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Job{}
	for rows.Next() {
		v, e := scanJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}
func (s *Store) CreateMetadataJob(ctx context.Context, id, parent string, ids []string, inputs map[string]model.MetadataInput) (*model.Job, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var parentID any
	if parent != "" {
		parentID = parent
	}
	now := time.Now().Unix()
	library, e := jobLibrary(ctx, tx, ids)
	if e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO jobs(id,library_id,type,state,stage,generation,total,parent_job_id,created_at,updated_at) VALUES(?,?,'metadata','queued','processing',1,?,?,?,?)`, id, library, len(ids), parentID, now, now); e != nil {
		return nil, e
	}
	for i, bookID := range ids {
		b, e := scanEdition(scopeRow(ctx, tx, "SELECT "+s.editionColumns()+" FROM "+editionRecordSQL+" editions WHERE id=?", bookID))
		if e != nil {
			return nil, e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO job_resources VALUES(?,?,1)", "metadata:book:"+bookID, id); e != nil {
			if strings.Contains(e.Error(), "UNIQUE") {
				return nil, model.ErrJobActive
			}
			return nil, e
		}
		if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO edition_metadata_state(edition_id) VALUES(?)", bookID); e != nil {
			return nil, e
		}
		if s.Version >= 4 {
			if e = managementGuard(ctx, tx, bookID); e != nil {
				return nil, e
			}
		}
		input := inputs[bookID]
		input.LookupSHA1 = b.SHA1
		input.LookupAlgorithm = b.RepresentativeAlgorithm

		var selection int64
		var manual string
		if e = scopeRow(ctx, tx, "SELECT selection_revision,manual_url FROM edition_metadata_state WHERE edition_id=?", bookID).Scan(&selection, &manual); e != nil {
			return nil, e
		}
		if input.URL == "" {
			input.URL = manual
		}
		raw, _ := json.Marshal(input)
		if _, e = tx.ExecContext(ctx, `INSERT INTO job_items(job_id,ordinal,relative_path,size,mtime_ns,state,edition_id,content_revision,selection_revision,input_json) VALUES(?,?,?,?,?,'pending',?,?,?,?)`, id, i+1, b.Path, b.Size, b.Mtime, bookID, b.ContentRevision, selection, string(raw)); e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s.Job(ctx, id)
}
func (s *Store) CreateTranslationJob(ctx context.Context, id, parent string) (*model.Job, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = enhancementGate(ctx, tx, model.EnhancementDictionary); e != nil {
		return nil, e
	}
	var parentID any
	if parent != "" {
		parentID = parent
	}
	now := time.Now().Unix()
	if _, e = tx.ExecContext(ctx, `INSERT INTO jobs(id,type,state,stage,generation,total,parent_job_id,created_at,updated_at) VALUES(?,'tag_translation','queued','processing',1,1,?,?,?)`, id, parentID, now, now); e != nil {
		if strings.Contains(e.Error(), "UNIQUE") {
			return nil, model.ErrJobActive
		}
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO job_items(job_id,ordinal,state) VALUES(?,1,'pending')", id); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s.Job(ctx, id)
}
func (s *Store) CreateScanRetry(ctx context.Context, id, parent, lib string, items []model.Item) (*model.Job, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var blocking int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM maintenance_blocks b JOIN jobs j ON j.id=b.job_id WHERE b.library_id=? AND j.state IN('queued','running')", lib).Scan(&blocking); e != nil {
		return nil, e
	}
	if blocking > 0 {
		return nil, model.ErrBusy
	}
	now := time.Now().Unix()
	if _, e = tx.ExecContext(ctx, `INSERT INTO jobs(id,library_id,type,state,stage,generation,total,parent_job_id,created_at,updated_at) VALUES(?,?,'scan','queued','processing',1,?,?,?,?)`, id, lib, len(items), parent, now, now); e != nil {
		if strings.Contains(e.Error(), "UNIQUE") {
			return nil, ErrScanActive
		}
		return nil, e
	}
	for i, v := range items {
		raw := "{}"
		if v.ScanResult != nil {
			encoded, _ := json.Marshal(v.ScanResult)
			raw = string(encoded)
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO job_items(job_id,ordinal,relative_path,size,mtime_ns,state,edition_id,content_revision,input_json) VALUES(?,?,?,?,?,'pending',?,?,?)", id, i+1, v.Path, v.Size, v.Mtime, v.BookID, v.ContentRevision, raw); e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s.Job(ctx, id)
}
func finishItem(ctx context.Context, tx *sql.Tx, j model.Job, item model.Item, state, code string, candidates []model.Candidate) error {
	raw, _ := json.Marshal(candidates)
	if candidates == nil {
		raw = []byte("[]")
	}
	r, e := tx.ExecContext(ctx, "UPDATE job_items SET state=?,error_code=?,candidates_json=? WHERE job_id=? AND ordinal=? AND state='pending'", state, code, string(raw), j.ID, item.Ordinal)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return nil
	}
	failed := 0
	if state != "completed" {
		failed = 1
	}
	_, e = tx.ExecContext(ctx, "UPDATE jobs SET done=done+1,failed=failed+?,updated_at=? WHERE id=?", failed, time.Now().Unix(), j.ID)
	return e
}
func (s *Store) PublishMetadata(ctx context.Context, j model.Job, item model.Item, result *model.RemoteMetadata, code string, candidates []model.Candidate) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	var state string
	if e = scopeRow(ctx, tx, "SELECT state FROM job_items WHERE job_id=? AND ordinal=?", j.ID, item.Ordinal).Scan(&state); e != nil {
		return e
	}
	if state != "pending" {
		return nil
	}
	if item.BookID == nil {
		return model.ErrInput
	}
	var resource int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM job_resources WHERE resource_key=? AND job_id=? AND generation=?", "metadata:book:"+*item.BookID, j.ID, j.Generation).Scan(&resource); e != nil {
		return e
	}
	if resource != 1 {
		return ErrStaleJob
	}
	var content, selection int64
	if e = scopeRow(ctx, tx, "SELECT b.content_revision,m.selection_revision FROM "+editionRecordSQL+" b JOIN edition_metadata_state m ON m.edition_id=b.id WHERE b.id=?", *item.BookID).Scan(&content, &selection); e != nil {
		return e
	}
	if s.Version >= 6 {
		ok, e := lookupMatches(ctx, tx, item)
		if e != nil {
			return e
		}
		if !ok {
			code = "lookup_input_changed"
			result = nil
			candidates = nil
		}
	}
	if content != item.ContentRevision {
		code = "content_changed"
		result = nil
	}
	if selection != item.SelectionRevision {
		code = "revision_conflict"
		result = nil
	}
	if s.Version >= 12 {
		handled, err := stageImportMetadata(ctx, tx, j, item, result, code, candidates)
		if err != nil {
			return err
		}
		if handled {
			return tx.Commit()
		}
	}
	state = "completed"
	if result != nil {
		if s.Version >= 4 {
			if e = managementGuard(ctx, tx, *item.BookID); e != nil {
				return e
			}
		}
		if e = publishSource(ctx, tx, *item.BookID, *result); e != nil {
			return e
		}
		var input model.MetadataInput
		_ = json.Unmarshal([]byte(item.Input), &input)
		if input.URL != "" && result.Method == "manual" {
			if _, e = tx.ExecContext(ctx, "UPDATE edition_metadata_state SET manual_url=? WHERE edition_id=?", input.URL, *item.BookID); e != nil {
				return e
			}
		}
	} else {
		state = "failed"
		if code == "no_match" || code == "multiple_matches" || code == "no_sha1" {
			state = "deferred"
		}
	}
	if e = finishItem(ctx, tx, j, item, state, code, candidates); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) FinishBackground(ctx context.Context, j model.Job, code string) error {
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
	if pending > 0 && code == "" {
		return ErrConflict
	}
	state := "completed"
	if code != "" {
		state = "failed"
	}
	if _, e = tx.ExecContext(ctx, "UPDATE jobs SET state=?,stage='finished',error_code=?,updated_at=? WHERE id=?", state, code, time.Now().Unix(), j.ID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM job_resources WHERE job_id=? AND generation=?", j.ID, j.Generation); e != nil {
		return e
	}
	if j.Type == "maintenance" {
		if e = finishCleanup(ctx, tx, j, code); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) NoteAttempt(ctx context.Context, j model.Job, item model.Item) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, "UPDATE job_items SET attempt=attempt+1 WHERE job_id=? AND ordinal=? AND state='pending'", j.ID, item.Ordinal)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) LookupHash(ctx context.Context, site, hash string) ([]model.Candidate, bool, error) {
	var raw string
	e := scopeRow(ctx, s.Read, "SELECT candidates_json FROM hash_lookup_cache WHERE site=? AND sha1=? AND expires_at>?", site, hash, time.Now().Unix()).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, false, nil
	}
	if e != nil {
		return nil, false, e
	}
	var out []model.Candidate
	e = json.Unmarshal([]byte(raw), &out)
	return out, true, e
}
func (s *Store) CacheHash(ctx context.Context, site, hash string, candidates []model.Candidate) error {
	ttl := 7 * 24 * time.Hour
	if len(candidates) == 0 {
		ttl = time.Hour
	}
	b, _ := json.Marshal(candidates)
	_, e := s.Write.ExecContext(ctx, `INSERT INTO hash_lookup_cache VALUES(?,?,?,?) ON CONFLICT(site,sha1) DO UPDATE SET candidates_json=excluded.candidates_json,expires_at=excluded.expires_at`, site, hash, string(b), time.Now().Add(ttl).Unix())
	return e
}

func (s *Store) Events(ctx context.Context, after int64, limit int) ([]model.Event, int64, bool, error) {
	return s.EventsForAccount(ctx, "", after, limit)
}
func (s *Store) EventsForAccount(ctx context.Context, account string, after int64, limit int) ([]model.Event, int64, bool, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, 0, false, e
	}
	defer tx.Rollback()
	var low, high int64
	if e = scopeRow(ctx, tx, "SELECT COALESCE(min(id),0),COALESCE(max(id),0) FROM events WHERE created_at>=?", time.Now().Add(-24*time.Hour).Unix()).Scan(&low, &high); e != nil {
		return nil, 0, false, e
	}
	gap := after > 0 && (after < low-1 || after > high || low == 0)
	if gap {
		return []model.Event{}, high, true, tx.Commit()
	}
	// Legacy migration fixtures have no private events. Filter before LIMIT so
	// another owner cannot starve later global events or leak identifiers.
	owner := ""
	var hasOwner int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM pragma_table_info('events') WHERE name='owner_account_id'").Scan(&hasOwner); e != nil {
		return nil, 0, false, e
	}
	args := []any{after, time.Now().Add(-24 * time.Hour).Unix()}
	if hasOwner > 0 {
		owner = " AND (owner_account_id IS NULL OR owner_account_id=?)"
		args = append(args, account)
	}
	args = append(args, limit)
	rows, e := scopeRows(ctx, tx, "SELECT id,type,resource_id,revision FROM events WHERE id>? AND created_at>=?"+owner+" ORDER BY id LIMIT ?", args...)
	if e != nil {
		return nil, 0, false, e
	}
	out := []model.Event{}
	for rows.Next() {
		var v model.Event
		if e = rows.Scan(&v.ID, &v.Type, &v.ResourceID, &v.Revision); e != nil {
			rows.Close()
			return nil, 0, false, e
		}
		out = append(out, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, 0, false, e
	}
	return out, high, false, tx.Commit()
}
