package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func managementGuard(ctx context.Context, q queryer, id string) error {
	var disabled bool
	var bookTrash, libTrash *int64
	e := scopeRow(ctx, q, `SELECT l.disabled,b.trashed_at,l.trashed_at FROM `+editionRecordSQL+` b JOIN libraries l ON l.id=b.library_id WHERE b.id=?`, id).Scan(&disabled, &bookTrash, &libTrash)
	if errors.Is(e, sql.ErrNoRows) {
		return model.ErrNotFound
	}
	if e != nil {
		return e
	}
	if bookTrash != nil || libTrash != nil {
		return model.ErrTrashed
	}
	if disabled {
		return model.ErrDisabled
	}
	var busy int
	e = scopeRow(ctx, q, "SELECT count(*) FROM maintenance_blocks mb JOIN jobs j ON j.id=mb.job_id JOIN "+editionRecordSQL+" b ON b.library_id=mb.library_id WHERE b.id=? AND j.state IN('queued','running')", id).Scan(&busy)
	if e != nil {
		return e
	}
	if busy > 0 {
		return model.ErrBusy
	}
	return nil
}
func (s *Store) ManagementGuard(ctx context.Context, id string) error {
	return managementGuard(ctx, s.Read, id)
}
func validateMaintenance(ctx context.Context, q queryer, in model.MaintenanceInput) ([]string, error) {
	if in.LibraryID != "" && len(in.BookIDs) > 0 {
		return nil, model.ErrInput
	}
	if in.LibraryID == "" && len(in.BookIDs) == 0 && in.Operation != "cleanup" {
		return nil, model.ErrInput
	}
	if len(in.BookIDs) > 1000 {
		return nil, model.ErrInput
	}
	ids := append([]string{}, in.BookIDs...)
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			return nil, model.ErrInput
		}
		seen[id] = true
		var rev int64
		if e := scopeRow(ctx, q, "SELECT revision FROM "+editionRecordSQL+" editions WHERE id=?", id).Scan(&rev); e != nil {
			if e == sql.ErrNoRows {
				return nil, model.ErrNotFound
			}
			return nil, e
		}
		if expected, ok := in.Expected["book:"+id]; !ok || rev != expected {
			return nil, model.ErrConflict
		}
	}
	if in.LibraryID != "" {
		var rev int64
		if e := scopeRow(ctx, q, "SELECT revision FROM libraries WHERE id=?", in.LibraryID).Scan(&rev); e != nil {
			if e == sql.ErrNoRows {
				return nil, model.ErrNotFound
			}
			return nil, e
		}
		if expected, ok := in.Expected["library:"+in.LibraryID]; !ok || rev != expected {
			return nil, model.ErrConflict
		}
		rows, e := scopeRows(ctx, q, "SELECT id FROM "+editionRecordSQL+" editions WHERE library_id=? ORDER BY id", in.LibraryID)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return nil, e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	for key, want := range in.Expected {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			return nil, model.ErrInput
		}
		table := ""
		switch parts[0] {
		case "work":
			table = "works"
		case "series":
			table = "series"
		case "book":
			table = "editions"
		case "library":
			table = "libraries"
		default:
			return nil, model.ErrInput
		}
		var actual int64
		if e := scopeRow(ctx, q, "SELECT revision FROM "+table+" WHERE id=?", parts[1]).Scan(&actual); e != nil {
			if e == sql.ErrNoRows {
				return nil, model.ErrConflict
			}
			return nil, e
		}
		if actual != want {
			return nil, model.ErrConflict
		}
	}
	return ids, nil
}
func (s *Store) CreateMaintenancePlan(ctx context.Context, in model.MaintenanceInput) (model.MaintenancePlan, error) {
	out := model.MaintenancePlan{Input: in, Operation: in.Operation, Summary: map[string]any{}, Protected: []string{}}
	switch in.Operation {
	case "trash", "restore", "purge", "relink", "rebind", "cleanup", "reinstate":
	default:
		return out, model.ErrInput
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	ids, e := validateMaintenance(ctx, tx, in)
	if e != nil {
		return out, e
	}
	if in.Expected == nil {
		in.Expected = map[string]int64{}
	}
	for _, bid := range ids {
		var revision int64
		if e = scopeRow(ctx, tx, "SELECT revision FROM "+editionRecordSQL+" editions WHERE id=?", bid).Scan(&revision); e != nil {
			return out, e
		}
		in.Expected["book:"+bid] = revision
		rows, x := scopeRows(ctx, tx, `SELECT 'work:'||w.id,w.revision FROM editions e JOIN works w ON w.id=e.work_id WHERE e.id=? UNION ALL SELECT 'series:'||s.id,s.revision FROM editions e JOIN series_members sm ON sm.work_id=e.work_id JOIN series s ON s.id=sm.series_id WHERE e.id=?`, bid, bid)
		if x != nil {
			return out, x
		}
		for rows.Next() {
			var key string
			var rev int64
			if e = rows.Scan(&key, &rev); e != nil {
				rows.Close()
				return out, e
			}
			in.Expected[key] = rev
		}
		rows.Close()
	}
	if in.Operation == "rebind" {
		var options struct {
			Root string `json:"root_key"`
		}
		if json.Unmarshal(in.Options, &options) != nil || options.Root == "" {
			return out, model.ErrInput
		}
		var occupied int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM libraries WHERE root_key=? AND id!=?", options.Root, in.LibraryID).Scan(&occupied); e != nil {
			return out, e
		}
		if occupied > 0 {
			return out, model.ErrMembership
		}
		out.Summary["requires_rescan"] = true
		out.Summary["content_identity_verified"] = false
	}
	out.Input = in
	out.Affected = ids
	targets := []map[string]any{}
	bounded := ids
	if len(bounded) > 100 {
		bounded = bounded[:100]
	}
	if len(bounded) > 0 {
		rows, x := scopeRows(ctx, tx, "SELECT b.id,COALESCE(e.title,b.filename_title),b.relative_path,l.display_name FROM "+editionRecordSQL+" b JOIN libraries l ON l.id=b.library_id LEFT JOIN effective_editions e ON e.edition_id=b.id WHERE b.id IN("+placeholders(len(bounded))+") ORDER BY b.relative_path,b.id", stringArgs(bounded)...)
		if x != nil {
			return out, x
		}
		for rows.Next() {
			var id, title, path, name string
			if e = rows.Scan(&id, &title, &path, &name); e != nil {
				rows.Close()
				return out, e
			}
			targets = append(targets, map[string]any{"id": id, "title": title, "relative_path": path, "library_name": name})
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	out.Summary["targets"] = targets
	out.Summary["targets_truncated"] = len(ids) > len(targets)
	out.Summary["book_count"] = len(ids)
	out.Summary["source_files_deleted"] = false
	var options struct {
		Storage map[string]any `json:"_storage"`
	}
	_ = json.Unmarshal(in.Options, &options)
	if options.Storage != nil {
		out.Summary["storage"] = options.Storage
	}
	effects := []map[string]any{}
	for key, rev := range in.Expected {
		if strings.HasPrefix(key, "work:") || strings.HasPrefix(key, "series:") {
			effect := map[string]any{"resource": key, "revision": rev, "relationship_preserved": in.Operation != "purge"}
			parts := strings.SplitN(key, ":", 2)
			var title string
			if parts[0] == "series" {
				if e = scopeRow(ctx, tx, "SELECT name FROM series WHERE id=?", parts[1]).Scan(&title); e != nil {
					return out, e
				}
				effect["name"] = title
			} else {
				if e = scopeRow(ctx, tx, "SELECT COALESCE(w.name_override,eb.title,f.filename_title) FROM works w JOIN files f ON f.edition_id=w.preferred_edition_id LEFT JOIN effective_editions eb ON eb.edition_id=w.preferred_edition_id WHERE w.id=?", parts[1]).Scan(&title); e != nil {
					return out, e
				}
			}
			effect["title"] = title
			effects = append(effects, effect)
		}
	}
	if in.Operation == "purge" {
		if _, e = tx.ExecContext(ctx, "SAVEPOINT purge_preview"); e != nil {
			return out, e
		}
		for _, id := range ids {
			if e = purgeBook(ctx, tx, id); e != nil {
				return out, e
			}
		}
		if e = checkOrganization(ctx, tx); e != nil {
			return out, e
		}
		for _, effect := range effects {
			key := effect["resource"].(string)
			parts := strings.SplitN(key, ":", 2)
			if parts[0] == "work" {
				var count int
				var preferred string
				_ = scopeRow(ctx, tx, "SELECT (SELECT count(*) FROM editions WHERE work_id=?),COALESCE((SELECT preferred_edition_id FROM works WHERE id=?),'')", parts[1], parts[1]).Scan(&count, &preferred)
				effect["remaining_editions"] = count
				effect["preferred_edition_id"] = preferred
				effect["removed"] = count == 0
			} else {
				var count int
				if e = scopeRow(ctx, tx, "SELECT count(*) FROM series_members WHERE series_id=?", parts[1]).Scan(&count); e != nil {
					return out, e
				}
				effect["remaining_works"] = count
				rows, x := scopeRows(ctx, tx, `SELECT sm.work_id,COALESCE(w.name_override,eb.title,f.filename_title) FROM series_members sm JOIN works w ON w.id=sm.work_id JOIN files f ON f.edition_id=w.preferred_edition_id LEFT JOIN effective_editions eb ON eb.edition_id=w.preferred_edition_id WHERE sm.series_id=? ORDER BY sm.position LIMIT 100`, parts[1])
				if x != nil {
					return out, x
				}
				seq := []map[string]string{}
				for rows.Next() {
					var id, title string
					if e = rows.Scan(&id, &title); e != nil {
						rows.Close()
						return out, e
					}
					seq = append(seq, map[string]string{"id": id, "title": title})
				}
				e = rows.Err()
				rows.Close()
				if e != nil {
					return out, e
				}
				effect["remaining_order"] = seq
				effect["remaining_order_truncated"] = count > len(seq)
			}
		}
		if _, e = tx.ExecContext(ctx, "ROLLBACK TO purge_preview"); e != nil {
			return out, e
		}
		if _, e = tx.ExecContext(ctx, "RELEASE purge_preview"); e != nil {
			return out, e
		}
	}
	out.Summary["organization_effects"] = effects
	out.Protected = append(out.Protected, "source_archives")
	out.ID, e = model.NewID()
	if e != nil {
		return out, e
	}
	out.Expires = time.Now().Add(30 * time.Minute).Unix()
	raw, _ := json.Marshal(in)
	summary, _ := json.Marshal(out)
	_, e = tx.ExecContext(ctx, "INSERT INTO maintenance_plans VALUES(?,?,?,?,?,NULL,?)", out.ID, in.Operation, string(raw), string(summary), out.Expires, time.Now().Unix())
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func loadPlan(ctx context.Context, q queryer, id string) (model.MaintenancePlan, error) {
	var p model.MaintenancePlan
	var raw, summary string
	var job *string
	e := scopeRow(ctx, q, "SELECT input_json,summary_json,job_id FROM maintenance_plans WHERE id=?", id).Scan(&raw, &summary, &job)
	if e == sql.ErrNoRows {
		return p, model.ErrNotFound
	}
	if e != nil {
		return p, e
	}
	if e = json.Unmarshal([]byte(summary), &p); e != nil {
		return p, e
	}
	if e = json.Unmarshal([]byte(raw), &p.Input); e != nil {
		return p, e
	}
	if job != nil {
		p.JobID = *job
	}
	return p, nil
}
func (s *Store) MaintenancePlan(ctx context.Context, id string) (model.MaintenancePlan, error) {
	return loadPlan(ctx, s.Read, id)
}
func (s *Store) ExecuteMaintenance(ctx context.Context, id string) (*model.Job, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	p, e := loadPlan(ctx, tx, id)
	if e != nil {
		return nil, e
	}
	if p.JobID != "" {
		return scanJob(scopeRow(ctx, tx, "SELECT "+s.jobColumns()+" FROM jobs WHERE id=?", p.JobID))
	}
	if p.Expires < time.Now().Unix() {
		return nil, model.ErrPreviewExpired
	}
	if _, e = validateMaintenance(ctx, tx, p.Input); e != nil {
		return nil, e
	}
	job, e := model.NewID()
	if e != nil {
		return nil, e
	}
	now := time.Now().Unix()
	_, e = tx.ExecContext(ctx, `INSERT INTO jobs(id,type,state,stage,generation,total,created_at,updated_at) VALUES(?,'maintenance','queued','processing',1,1,?,?)`, job, now, now)
	if e != nil {
		return nil, e
	}
	raw, _ := json.Marshal(map[string]string{"plan_id": id})
	if _, e = tx.ExecContext(ctx, "INSERT INTO job_items(job_id,ordinal,state,input_json) VALUES(?,1,'pending',?)", job, string(raw)); e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE maintenance_plans SET job_id=? WHERE id=?", job, id); e != nil {
		return nil, e
	}
	libs := []string{}
	if p.Input.LibraryID != "" {
		libs = append(libs, p.Input.LibraryID)
	} else if p.Operation == "cleanup" {
		rows, x := scopeRows(ctx, tx, "SELECT id FROM libraries")
		if x != nil {
			return nil, x
		}
		for rows.Next() {
			var lib string
			_ = rows.Scan(&lib)
			libs = append(libs, lib)
		}
		rows.Close()
	} else {
		for _, bid := range p.Affected {
			var lib string
			if e = scopeRow(ctx, tx, "SELECT library_id FROM "+editionRecordSQL+" editions WHERE id=?", bid).Scan(&lib); e != nil {
				return nil, e
			}
			libs = append(libs, lib)
		}
	}
	for _, lib := range libs {
		var busy int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM maintenance_blocks mb JOIN jobs j ON j.id=mb.job_id WHERE mb.library_id=? AND j.id!=? AND j.state IN('queued','running')", lib, job).Scan(&busy); e != nil {
			return nil, e
		}
		if busy > 0 {
			return nil, model.ErrBusy
		}
		if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO maintenance_blocks VALUES(?,?)", lib, job); e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s.Job(ctx, job)
}
func pauseAffected(ctx context.Context, tx *sql.Tx, ids []string, library string) error {
	where := `(library_id=? AND ?!='')`
	args := []any{library, library}
	if len(ids) > 0 {
		where += ` OR (type='scan' AND library_id IN(SELECT library_id FROM ` + editionRecordSQL + ` editions WHERE id IN (` + placeholders(len(ids)) + `))) OR id IN (SELECT job_id FROM job_items WHERE edition_id IN (` + placeholders(len(ids)) + `))`
		args = append(args, stringArgs(ids)...)
		args = append(args, stringArgs(ids)...)
	}
	var recommendationTables int
	if e := scopeRow(ctx, tx, "SELECT count(*) FROM sqlite_master WHERE name='recommendation_batches' AND type='table'").Scan(&recommendationTables); e != nil {
		return e
	}
	if recommendationTables > 0 {
		where += ` OR (type='work_recommendation' AND id IN(SELECT rb.job_id FROM recommendation_batches rb,json_each(rb.pool_json,'$.editions') ed WHERE json_extract(ed.value,'$.book.library_id')=? AND ?!=''))`
		args = append(args, library, library)
		if len(ids) > 0 {
			where += ` OR (type='work_recommendation' AND id IN(SELECT rb.job_id FROM recommendation_batches rb,json_each(rb.pool_json,'$.editions') ed WHERE json_extract(ed.value,'$.book.id') IN(` + placeholders(len(ids)) + `)))`
			args = append(args, stringArgs(ids)...)
		}
	}
	// Series jobs have one item per Work, whose edition_id is only the preferred
	// Edition. Maintenance of any other version must invalidate the same worker.
	var seriesTables int
	if e := scopeRow(ctx, tx, "SELECT count(*) FROM sqlite_master WHERE name='series_discovery_runs' AND type='table'").Scan(&seriesTables); e != nil {
		return e
	}
	if seriesTables > 0 && len(ids) > 0 {
		where += ` OR (type='series_recommendation' AND id IN(SELECT rb.job_id FROM series_discovery_runs rb,json_each(rb.pool_json,'$.editions') ed WHERE json_extract(ed.value,'$.book.id') IN(` + placeholders(len(ids)) + `)))`
		args = append(args, stringArgs(ids)...)
	}
	_, e := tx.ExecContext(ctx, `UPDATE jobs SET state='paused',generation=generation+1,error_code='maintenance_required',updated_at=unixepoch() WHERE type IN('scan','metadata','metadata_preview','metadata_search','cover_rebuild','work_recommendation','series_recommendation') AND state IN('queued','running') AND (`+where+`)`, args...)
	return e
}
func (s *Store) PauseMaintenanceResources(ctx context.Context, p model.MaintenancePlan) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if p.Operation == "cleanup" {
		_, e = tx.ExecContext(ctx, "UPDATE jobs SET state='paused',generation=generation+1,error_code='maintenance_required' WHERE type IN('scan','metadata','metadata_preview','metadata_search','cover_rebuild','work_recommendation','series_recommendation') AND state IN('queued','running')")
	} else {
		e = pauseAffected(ctx, tx, p.Affected, p.Input.LibraryID)
	}
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) SetLibraryStatus(ctx context.Context, id string, revision int64, disabled bool) (*model.Library, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	r, e := tx.ExecContext(ctx, "UPDATE libraries SET disabled=?,revision=revision+1 WHERE id=? AND revision=?", disabled, id, revision)
	if e != nil {
		return nil, e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return nil, model.ErrConflict
	}
	if disabled {
		rows, e := scopeRows(ctx, tx, "SELECT id FROM "+editionRecordSQL+" editions WHERE library_id=?", id)
		if e != nil {
			return nil, e
		}
		ids := []string{}
		for rows.Next() {
			var bid string
			if e = rows.Scan(&bid); e != nil {
				rows.Close()
				return nil, e
			}
			ids = append(ids, bid)
		}
		rows.Close()
		if e = pauseAffected(ctx, tx, ids, id); e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s.Library(ctx, id)
}

// ApplyMaintenance commits data and the durable item checkpoint in one transaction.
func (s *Store) ApplyMaintenance(ctx context.Context, j model.Job, item model.Item, p model.MaintenancePlan, rootKey, identity, path string) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	if p.Operation != "cleanup" && p.Expires < time.Now().Unix() {
		return model.ErrPreviewExpired
	}
	if p.Operation == "cleanup" {
		var accepted int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM maintenance_plans WHERE id=? AND job_id=? AND operation='cleanup'", p.ID, j.ID).Scan(&accepted); e != nil {
			return e
		}
		if accepted != 1 {
			return model.ErrConflict
		}
	}
	ids, e := validateMaintenance(ctx, tx, p.Input)
	if e != nil {
		return e
	}
	if len(ids) != len(p.Affected) {
		return model.ErrConflict
	}
	for i, id := range ids {
		if id != p.Affected[i] {
			return model.ErrConflict
		}
	}
	exec := func(q string, args ...any) error { _, e := tx.ExecContext(ctx, q, args...); return e }
	now := time.Now().Unix()
	switch p.Operation {
	case "trash", "restore":
		var stamp any
		if p.Operation == "trash" {
			stamp = now
		}
		if p.Input.LibraryID != "" {
			e = exec("UPDATE libraries SET trashed_at=?,revision=revision+1 WHERE id=?", stamp, p.Input.LibraryID)
		} else {
			for _, id := range ids {
				if e = exec("UPDATE editions SET trashed_at=?,revision=revision+1 WHERE id=?", stamp, id); e != nil {
					return e
				}
				if p.Operation == "trash" {
					if e = exec("INSERT OR IGNORE INTO scan_exclusions SELECT library_id,relative_path,? FROM "+editionRecordSQL+" editions WHERE id=?", now, id); e != nil {
						return e
					}
				} else {
					if e = exec("DELETE FROM scan_exclusions WHERE (library_id,relative_path) IN (SELECT library_id,relative_path FROM "+editionRecordSQL+" editions WHERE id=?)", id); e != nil {
						return e
					}
				}
			}
		}
	case "reinstate":
		var options struct {
			Path string `json:"relative_path"`
		}
		_ = json.Unmarshal(p.Input.Options, &options)
		if p.Input.LibraryID == "" || options.Path == "" {
			return model.ErrInput
		}
		e = exec("DELETE FROM scan_exclusions WHERE library_id=? AND relative_path=?", p.Input.LibraryID, options.Path)
	case "purge":
		for _, id := range ids {
			if e = purgeBook(ctx, tx, id); e != nil {
				return e
			}
		}
		if p.Input.LibraryID != "" {
			if e = purgeLibraryGrants(ctx, tx, p.Input.LibraryID, now); e != nil {
				return e
			}
			for _, q := range []string{"DELETE FROM job_resources WHERE job_id IN(SELECT id FROM jobs WHERE library_id=?)", "UPDATE jobs SET library_id=NULL WHERE library_id=?", "DELETE FROM scan_exclusions WHERE library_id=?", "DELETE FROM maintenance_blocks WHERE library_id=?", "DELETE FROM series WHERE library_id=?", "DELETE FROM libraries WHERE id=?"} {
				if e = exec(q, p.Input.LibraryID); e != nil {
					return e
				}
			}
		}
	case "relink":
		if len(ids) != 1 {
			return model.ErrInput
		}
		var lib string
		if e = scopeRow(ctx, tx, "SELECT library_id FROM "+editionRecordSQL+" editions WHERE id=?", ids[0]).Scan(&lib); e != nil {
			return e
		}
		var actual string
		if e = scopeRow(ctx, tx, "SELECT root_key FROM libraries WHERE id=?", lib).Scan(&actual); e != nil {
			return e
		}
		if actual != rootKey {
			return model.ErrLibraryConflict
		}
		var options struct {
			Fingerprint string `json:"_fingerprint"`
		}
		_ = json.Unmarshal(p.Input.Options, &options)
		parts := strings.Split(options.Fingerprint, ":")
		if len(parts) != 3 {
			return model.ErrInput
		}
		size, x := strconv.ParseInt(parts[1], 10, 64)
		if x != nil {
			return model.ErrInput
		}
		mtime, x := strconv.ParseInt(parts[2], 10, 64)
		if x != nil {
			return model.ErrInput
		}
		var occupied int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM "+editionRecordSQL+" editions WHERE library_id=? AND relative_path=? AND id!=?", lib, path, ids[0]).Scan(&occupied); e != nil {
			return e
		}
		if occupied > 0 {
			return model.ErrMembership
		}
		filename := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		e = exec("UPDATE files SET relative_path=?,filename_title=?,size=?,mtime_ns=?,representative_sha1=NULL,legacy_cover_sha1=NULL,content_revision=content_revision+1,file_state='needs_review',error_code='file_relinked',page_count=0 WHERE edition_id=?", path, filename, size, mtime, ids[0])
		if e == nil {
			e = exec("UPDATE editions SET revision=revision+1 WHERE id=?", ids[0])
		}
		if e == nil {
			e = reproject(ctx, tx, ids[0])
		}
	case "rebind":
		if p.Input.LibraryID == "" {
			return model.ErrInput
		}
		var occupied int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM libraries WHERE root_key=? AND id!=?", rootKey, p.Input.LibraryID).Scan(&occupied); e != nil {
			return e
		}
		if occupied > 0 {
			return model.ErrMembership
		}
		e = exec("UPDATE libraries SET root_key=?,root_identity=?,revision=revision+1,last_error='root_rebound',last_complete_scan_id=NULL WHERE id=?", rootKey, identity, p.Input.LibraryID)
		if e == nil {
			e = exec("UPDATE files SET content_revision=content_revision+1,representative_sha1=NULL,legacy_cover_sha1=NULL,page_count=0,file_state='needs_review',error_code='root_rebound' WHERE library_id=?", p.Input.LibraryID)
		}
		if e == nil {
			e = exec("UPDATE editions SET revision=revision+1 WHERE library_id=?", p.Input.LibraryID)
		}
	case "cleanup":
	default:
		return model.ErrInput
	}
	if e != nil {
		return e
	}
	if e = finishItem(ctx, tx, j, item, "completed", "", nil); e != nil {
		return e
	}
	return tx.Commit()
}
func purgeBook(ctx context.Context, tx *sql.Tx, id string) error {
	var e error
	exec := func(q string, args ...any) error { _, e := tx.ExecContext(ctx, q, args...); return e }
	var stamp *int64
	var libTrash *int64
	if e := scopeRow(ctx, tx, "SELECT b.trashed_at,l.trashed_at FROM "+editionRecordSQL+" b JOIN libraries l ON l.id=b.library_id WHERE b.id=?", id).Scan(&stamp, &libTrash); e != nil {
		return e
	}
	if stamp == nil && libTrash == nil {
		return model.ErrInput
	}
	var work, preferred string
	var workRevision int64
	if e := scopeRow(ctx, tx, "SELECT e.work_id,w.preferred_edition_id,w.revision FROM editions e JOIN works w ON w.id=e.work_id WHERE e.id=?", id).Scan(&work, &preferred, &workRevision); e != nil {
		return e
	}
	if e := exec("INSERT OR IGNORE INTO scan_exclusions SELECT library_id,relative_path,unixepoch() FROM files WHERE edition_id=?", id); e != nil {
		return e
	}
	var remaining int
	if e := scopeRow(ctx, tx, "SELECT count(*) FROM editions WHERE work_id=? AND id!=?", work, id).Scan(&remaining); e != nil {
		return e
	}
	if remaining > 0 {
		if preferred == id {
			if e := exec("UPDATE works SET preferred_edition_id=(SELECT min(id) FROM editions WHERE work_id=? AND id!=?),revision=revision+1 WHERE id=?", work, id, work); e != nil {
				return e
			}
		} else {
			if e := exec("UPDATE works SET revision=revision+1 WHERE id=?", work); e != nil {
				return e
			}
		}
	} else {
		var series string
		_ = scopeRow(ctx, tx, "SELECT series_id FROM series_members WHERE work_id=?", work).Scan(&series)
		if series != "" {
			if e := exec("DELETE FROM series_members WHERE work_id=?", work); e != nil {
				return e
			}
			if e := compactSeries(ctx, tx, series); e != nil {
				return e
			}
		}
	}
	rows, x := scopeRows(ctx, tx, "SELECT job_id,ordinal FROM job_items WHERE edition_id=? AND state='pending'", id)
	if x != nil {
		return x
	}
	pending := []model.Item{}
	for rows.Next() {
		var item model.Item
		if e = rows.Scan(&item.JobID, &item.Ordinal); e != nil {
			rows.Close()
			return e
		}
		pending = append(pending, item)
	}
	rows.Close()
	for _, item := range pending {
		if e = finishItem(ctx, tx, model.Job{ID: item.JobID}, item, "deferred", "resource_removed", nil); e != nil {
			return e
		}
	}
	if e = exec("DELETE FROM job_resources WHERE resource_key=?", "metadata:book:"+id); e != nil {
		return e
	}
	for _, q := range []string{"UPDATE job_items SET edition_id=NULL WHERE edition_id=?", "DELETE FROM source_previews WHERE edition_id=?", "DELETE FROM cover_selections WHERE edition_id=?", "DELETE FROM effective_edition_tags WHERE edition_id=?", "DELETE FROM field_overrides WHERE edition_id=?", "DELETE FROM edition_sources WHERE edition_id=?", "DELETE FROM edition_metadata_state WHERE edition_id=?", "DELETE FROM effective_editions WHERE edition_id=?", "DELETE FROM edition_user_states WHERE edition_id=?", "DELETE FROM search_documents WHERE edition_id=?", "DELETE FROM edition_version_labels WHERE edition_id=?", "DELETE FROM files WHERE edition_id=?", "DELETE FROM editions WHERE id=?"} {
		if e = exec(q, id); e != nil {
			return e
		}
	}
	if remaining == 0 {
		if e := exec("DELETE FROM works WHERE id=?", work); e != nil {
			return e
		}
		if e := exec("INSERT INTO events(type,resource_id,revision,created_at) VALUES('work',?,?,unixepoch())", work, workRevision+1); e != nil {
			return e
		}
	}
	if remaining > 0 {
		return refreshWorkLabels(ctx, tx, work, true)
	}
	return nil
}
func (s *Store) AssetKeys(ctx context.Context) ([]string, error) {
	rows, e := scopeRows(ctx, s.Read, `SELECT a.storage_key FROM assets a WHERE EXISTS(SELECT 1 FROM cover_selections c WHERE c.asset_id=a.id) OR EXISTS(SELECT 1 FROM recommendation_materials r WHERE r.asset_id=a.id) OR EXISTS(SELECT 1 FROM tag_images t WHERE t.asset_id=a.id)`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) CleanHistory(ctx context.Context, j model.Job, categories []string) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	for _, kind := range categories {
		if kind == "terminal_jobs" {
			ids := []string{}
			query := terminalHistoryQuery(s.Version)
			rows, e := scopeRows(ctx, tx, query)
			if e != nil {
				return e
			}
			for rows.Next() {
				var id string
				if e = rows.Scan(&id); e != nil {
					rows.Close()
					return e
				}
				ids = append(ids, id)
			}
			rows.Close()
			for _, id := range ids {
				for _, q := range []string{"DELETE FROM maintenance_blocks WHERE job_id=?", "DELETE FROM job_resources WHERE job_id=?", "DELETE FROM job_items WHERE job_id=?", "DELETE FROM scan_entries WHERE job_id=?", "UPDATE libraries SET last_complete_scan_id=NULL WHERE last_complete_scan_id=?", "DELETE FROM jobs WHERE id=?"} {
					if _, e = tx.ExecContext(ctx, q, id); e != nil {
						return e
					}
				}
			}
		}
	}
	return tx.Commit()
}
func (s *Store) Trash(ctx context.Context, kind string, page, size int) (map[string]any, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	items := []any{}
	var total int
	if kind == "library" {
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM libraries WHERE trashed_at IS NOT NULL").Scan(&total); e != nil {
			return nil, e
		}
		rows, e := scopeRows(ctx, tx, "SELECT "+libraryColumns+" FROM libraries WHERE trashed_at IS NOT NULL ORDER BY trashed_at DESC,id LIMIT ? OFFSET ?", size, (page-1)*size)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			v, e := scanLibrary(rows)
			if e != nil {
				rows.Close()
				return nil, e
			}
			items = append(items, *v)
		}
		e = rows.Err()
		rows.Close()
	} else if kind == "book" || kind == "" {
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM "+editionRecordSQL+" editions WHERE trashed_at IS NOT NULL").Scan(&total); e != nil {
			return nil, e
		}
		rows, e := scopeRows(ctx, tx, "SELECT "+s.editionColumns()+" FROM "+editionRecordSQL+" editions WHERE trashed_at IS NOT NULL ORDER BY trashed_at DESC,id LIMIT ? OFFSET ?", size, (page-1)*size)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			v, e := scanEdition(rows)
			if e != nil {
				rows.Close()
				return nil, e
			}
			items = append(items, *v)
		}
		e = rows.Err()
		rows.Close()
	} else {
		return nil, model.ErrInput
	}
	if e != nil {
		return nil, e
	}
	return map[string]any{"items": items, "total": total, "page": page, "page_size": size}, tx.Commit()
}
func (s *Store) ScanExclusions(ctx context.Context, library string) (map[string]bool, error) {
	rows, e := scopeRows(ctx, s.Read, "SELECT relative_path FROM scan_exclusions WHERE library_id=?", library)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var p string
		if e = rows.Scan(&p); e != nil {
			return nil, e
		}
		out[p] = true
	}
	return out, rows.Err()
}

func (s *Store) MaintenanceJobs(ctx context.Context) ([]model.Job, error) {
	rows, e := scopeRows(ctx, s.Read, "SELECT "+s.jobColumns()+" FROM jobs WHERE type IN('maintenance','cover_rebuild') AND state IN('queued','running')"+healthExclude(s.Version)+" ORDER BY created_at,id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Job{}
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}
func (s *Store) CheckExecution(ctx context.Context, j model.Job) error {
	var n int
	if e := scopeRow(ctx, s.Read, "SELECT count(*) FROM jobs WHERE id=? AND state='running' AND generation=?", j.ID, j.Generation).Scan(&n); e != nil {
		return e
	}
	if n != 1 {
		return model.ErrStaleJob
	}
	return nil
}
func (s *Store) PruneAssets(ctx context.Context, j model.Job, keys []string) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	for _, key := range keys {
		if _, e = tx.ExecContext(ctx, "DELETE FROM assets WHERE storage_key=? AND NOT EXISTS(SELECT 1 FROM cover_selections WHERE asset_id=assets.id) AND NOT EXISTS(SELECT 1 FROM recommendation_materials WHERE asset_id=assets.id) AND NOT EXISTS(SELECT 1 FROM tag_images WHERE asset_id=assets.id)", key); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func healthExclude(version int) string {
	if version < 23 {
		return ""
	}
	return " AND id NOT IN(SELECT job_id FROM health_checks) AND id NOT IN(SELECT job_id FROM health_repairs)"
}

func healthRetainedJobFilter(version int) string {
	if version < 23 {
		return ""
	}
	return ` AND NOT EXISTS(SELECT 1 FROM health_checks h WHERE h.job_id=jobs.id) AND NOT EXISTS(SELECT 1 FROM health_repairs h WHERE h.job_id=jobs.id) AND NOT EXISTS(SELECT 1 FROM health_paused_jobs h WHERE h.job_id=jobs.id)`
}
func terminalHistoryQuery(version int) string {
	query := `SELECT id FROM jobs WHERE state IN('completed','failed','cancelled') AND updated_at<unixepoch()-2592000 AND NOT EXISTS(SELECT 1 FROM jobs child WHERE child.parent_job_id=jobs.id) AND NOT EXISTS(SELECT 1 FROM maintenance_plans p WHERE p.job_id=jobs.id) AND NOT EXISTS(SELECT 1 FROM source_previews p WHERE p.job_id=jobs.id)`
	if version >= 16 {
		query += ` AND NOT EXISTS(SELECT 1 FROM source_search_requests r WHERE r.job_id=jobs.id)`
	}
	// Retain frozen evidence needed by pending/ignored proposals and Undo.
	if version >= 10 {
		query += ` AND NOT EXISTS(SELECT 1 FROM series_proposals sp WHERE sp.job_id=jobs.id)`
	}
	if version >= 11 {
		query += ` AND NOT EXISTS(SELECT 1 FROM work_discovery_runs d WHERE d.base_job_id=jobs.id)`
	}
	query += healthRetainedJobFilter(version)
	return query

}
func (s *Store) HealthCleanupPreview(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	query := strings.Replace(terminalHistoryQuery(s.Version), "SELECT id FROM jobs", "SELECT count(*) FROM jobs", 1)
	var eligible int
	if e := scopeRow(ctx, s.Read, query).Scan(&eligible); e != nil {
		return nil, e
	}
	out["eligible_terminal_jobs"] = eligible
	var protected int
	if s.Version >= 23 {
		if e := scopeRow(ctx, s.Read, `SELECT count(*) FROM jobs WHERE state IN('completed','failed','cancelled') AND updated_at<unixepoch()-2592000 AND (EXISTS(SELECT 1 FROM health_checks h WHERE h.job_id=jobs.id) OR EXISTS(SELECT 1 FROM health_repairs h WHERE h.job_id=jobs.id) OR EXISTS(SELECT 1 FROM health_paused_jobs h WHERE h.job_id=jobs.id))`).Scan(&protected); e != nil {
			return nil, e
		}
	}
	out["protected_health_jobs"] = protected
	return out, nil
}

// Removal and ACL invalidation share the permanent library deletion transaction.
// Trash/restore deliberately never call this helper.
func purgeLibraryGrants(ctx context.Context, tx *sql.Tx, library string, now int64) error {
	rows, err := scopeRows(ctx, tx, "SELECT account_id FROM account_library_grants WHERE library_id=? ORDER BY account_id", library)
	if err != nil {
		return err
	}
	var accounts []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		accounts = append(accounts, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	actor := ""
	if a, ok := model.Actor(ctx); ok {
		actor = a.ID
	} else if len(accounts) > 0 {
		if err = tx.QueryRowContext(ctx, "SELECT id FROM accounts WHERE singleton=1").Scan(&actor); err != nil {
			return err
		}
	}
	for _, id := range accounts {
		changes, _ := json.Marshal(map[string]string{"removed_library_id": library})
		for _, op := range []struct {
			q    string
			args []any
		}{
			{"UPDATE account_profiles SET revision=revision+1,acl_revision=acl_revision+1 WHERE account_id=?", []any{id}},
			{"INSERT INTO account_security_audit(actor_id,target_id,action,changes_json,created_at) VALUES(?,?,'library_purged',?,?)", []any{actor, id, string(changes), now}},
			{"INSERT INTO events(type,resource_id,revision,created_at,owner_account_id) SELECT 'account_permissions',account_id,acl_revision,?,account_id FROM account_profiles WHERE account_id=?", []any{now, id}},
		} {
			if _, err = tx.ExecContext(ctx, op.q, op.args...); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM account_library_grants WHERE library_id=?", library)
	return err
}
