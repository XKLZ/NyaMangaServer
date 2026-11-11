package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"sort"
	"strings"
)

func healthID(parts ...string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(parts, "\x00"))))
}
func healthProjecting(ctx context.Context) bool {
	v, _ := ctx.Value(healthProjectionKey{}).(bool)
	return v
}

type healthProjectionKey struct{}

func (s *Store) CreateHealthCheck(ctx context.Context, in model.HealthCheckInput) (model.HealthCheck, error) {
	raw, _ := json.Marshal(in)
	id, e := model.NewID()
	if e != nil {
		return model.HealthCheck{}, e
	}
	e = s.healthWrite(ctx, func(c *sql.Conn) error {
		tx, e := c.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		var oldID, oldRaw string
		e = scopeRow(ctx, tx, "SELECT id,scope_json FROM health_checks WHERE request_key=?", in.RequestKey).Scan(&oldID, &oldRaw)
		if e == nil {
			if oldRaw != string(raw) {
				return model.ErrConflict
			}
			id = oldID
			return nil
		}
		if e != sql.ErrNoRows {
			return e
		}
		var phase string
		if e = scopeRow(ctx, tx, "SELECT phase FROM health_state WHERE singleton=1").Scan(&phase); e != nil {
			return e
		}
		if phase != "idle" {
			return model.ErrHealthMaintenance
		}
		for _, lib := range in.LibraryIDs {
			var n int
			if e = scopeRow(ctx, tx, "SELECT count(*) FROM libraries WHERE id=? AND trashed_at IS NULL", lib).Scan(&n); e != nil {
				return e
			}
			if n != 1 {
				return model.ErrNotFound
			}
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO jobs(id,type,state,stage,generation,created_at,updated_at) VALUES(?,'maintenance','queued','processing',1,unixepoch(),unixepoch())`, id)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO health_checks(id,job_id,request_key,mode,scope_json,created_at) VALUES(?,?,?,?,?,unixepoch())`, id, id, in.RequestKey, in.Mode, string(raw))
		if e != nil {
			return e
		}
		for _, kind := range []string{"edition", "work", "series", "asset"} {
			table := map[string]string{"edition": "editions", "work": "works", "series": "series", "asset": "assets"}[kind]
			if _, e = tx.ExecContext(ctx, "INSERT INTO health_check_objects SELECT ?,?,t.id,"+healthStampExpr(kind, "t.id")+","+map[string]string{"edition": "t.revision", "work": "t.revision", "series": "t.revision", "asset": "0"}[kind]+" FROM "+table+" t", id, kind); e != nil {
				return e
			}
		}
		if in.Mode == "sources" {
			libs, _ := json.Marshal(in.LibraryIDs)
			if _, e = tx.ExecContext(ctx, "INSERT INTO health_check_objects SELECT ?,'source',t.id,"+healthStampExpr("source", "t.id")+",t.revision FROM editions t WHERE library_id IN(SELECT value FROM json_each(?))", id, string(libs)); e != nil {
				return e
			}
		}
		for _, target := range []struct{ kind, query string }{
			{"cover_cache", "SELECT edition_id id FROM files"},
			{"tag_cache", "SELECT tag_id||':'||role id FROM tag_images WHERE asset_id IS NOT NULL"},
			{"recommendation_index", "SELECT edition_id||':'||algorithm id FROM recommendation_features"},
			{"series_block", "SELECT b.job_id||':'||b.block_key id FROM series_evidence_blocks b JOIN series_evidence_generations g ON g.job_id=b.job_id JOIN jobs j ON j.id=b.job_id WHERE j.state IN('queued','running','paused') OR g.state='complete' OR EXISTS(SELECT 1 FROM series_evidence_generations child WHERE child.base_job_id=g.job_id)"},
			{"dictionary", "SELECT CASE WHEN active_version IS NULL THEN 'not_installed' ELSE 'active' END id FROM translation_state"},
		} {
			if _, e = tx.ExecContext(ctx, "INSERT INTO health_check_objects SELECT ?,?,t.id,"+healthStampExpr(target.kind, "t.id", s.Version >= 27)+",0 FROM ("+target.query+")t", id, target.kind); e != nil {
				return e
			}
		}
		return tx.Commit()
	})
	if e != nil {
		return model.HealthCheck{}, e
	}
	return s.HealthCheck(ctx, id)
}
func (s *Store) HealthCheck(ctx context.Context, id string) (model.HealthCheck, error) {
	var v model.HealthCheck
	var raw string
	e := scopeRow(ctx, s.Read, `SELECT h.id,h.job_id,h.mode,h.scope_json,h.rules_version,h.phase,h.cursor,h.checked,h.bytes_read,h.source_bytes_read,h.created_at,h.finished_at,h.revision,h.error_code,j.state FROM health_checks h JOIN jobs j ON j.id=h.job_id WHERE h.id=?`, id).Scan(&v.ID, &v.JobID, &v.Mode, &raw, &v.RulesVersion, &v.Phase, &v.Cursor, &v.Checked, &v.BytesRead, &v.SourceBytesRead, &v.CreatedAt, &v.FinishedAt, &v.Revision, &v.ErrorCode, &v.State)
	if e == sql.ErrNoRows {
		return v, model.ErrNotFound
	}
	if e != nil {
		return v, e
	}
	if e = json.Unmarshal([]byte(raw), &v.Scope); e != nil {
		return v, e
	}
	v.Scope.CheckID = v.ID
	v.Counts = map[string]int{}
	v.Categories = map[string]map[string]int{}
	rows, e := scopeRows(ctx, s.Read, `SELECT category,actual_status,count(*) FROM (`+healthCurrentSQL(s.Version >= 27)+`) GROUP BY category,actual_status`, id)
	if e != nil {
		return v, e
	}
	for rows.Next() {
		var category, status string
		var n int
		if e = rows.Scan(&category, &status, &n); e != nil {
			rows.Close()
			return v, e
		}
		v.Counts[status] += n
		if v.Categories[category] == nil {
			v.Categories[category] = map[string]int{}
		}
		v.Categories[category][status] = n
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return v, e
	}
	v.TargetCounts = map[string]int{}
	targets, e := scopeRows(ctx, s.Read, "SELECT kind,count(*) FROM health_check_objects WHERE check_id=? GROUP BY kind", id)
	if e != nil {
		return v, e
	}
	for targets.Next() {
		var kind string
		var count int
		if e = targets.Scan(&kind, &count); e != nil {
			targets.Close()
			return v, e
		}
		v.TargetCounts[kind] = count
	}
	e = targets.Err()
	targets.Close()
	if e != nil {
		return v, e
	}
	v.Completion = "incomplete"
	if v.State == "completed" {
		v.Completion = "checked_scope"
		if v.Counts["not_checked"]+v.Counts["stale"]+v.Counts["inaccessible"] > 0 {
			v.Completion = "limited"
		}
	}
	return v, nil
}
func (s *Store) HealthChecks(ctx context.Context, page, size int) (model.HealthPage[model.HealthCheck], error) {
	out := model.HealthPage[model.HealthCheck]{Items: []model.HealthCheck{}, Page: page, PageSize: size}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM health_checks").Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, "SELECT id FROM health_checks ORDER BY created_at DESC,id LIMIT ? OFFSET ?", size, (page-1)*size)
	if e != nil {
		return out, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	for _, id := range ids {
		v, e := s.HealthCheck(ctx, id)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	return out, nil
}
func (s *Store) HealthJobs(ctx context.Context) ([]model.Job, error) {
	rows, e := scopeRows(ctx, s.Read, "SELECT j.id FROM jobs j WHERE j.state IN('queued','running') AND (EXISTS(SELECT 1 FROM health_checks h WHERE h.job_id=j.id) OR EXISTS(SELECT 1 FROM health_repairs r WHERE r.job_id=j.id)) ORDER BY j.created_at,j.id")
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []model.Job{}
	for _, id := range ids {
		j, e := s.Job(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, *j)
	}
	return out, nil
}
func (s *Store) HealthRecover(ctx context.Context) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `UPDATE jobs SET state='paused',generation=generation+1,error_code='health_interrupted',updated_at=unixepoch() WHERE (state='running' AND id IN(SELECT job_id FROM health_checks)) OR (state IN('queued','running') AND id IN(SELECT job_id FROM health_repairs))`); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE health_repairs SET state='paused',error_code='health_interrupted',revision=revision+1,updated_at=unixepoch() WHERE state NOT IN('completed','rolled_back') AND job_id IN(SELECT id FROM jobs WHERE state='paused' AND error_code='health_interrupted')`); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE health_state SET phase='paused',revision=revision+1,updated_at=unixepoch() WHERE repair_id IN(SELECT id FROM health_repairs WHERE state='paused' AND error_code='health_interrupted')`); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) HealthCheckpoint(ctx context.Context, j model.Job, phase int, cursor string, findings []model.HealthFinding, bytes int64, finished bool, code string) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	for _, f := range findings {
		stamp, _, err := healthStamp(ctx, tx, f.ObjectKind, f.ObjectID)
		if err != nil {
			return err
		}
		if f.Fingerprint != "" && stamp != f.Fingerprint {
			f.Status = "stale"
		}
		if f.Status == "" {
			f.Status = "normal"
		}
		if len(f.Details) == 0 {
			f.Details = json.RawMessage(`{}`)
		}
		id := healthID(j.ID, f.Category, f.ObjectKind, f.ObjectID, f.Code)
		_, e = tx.ExecContext(ctx, `INSERT INTO health_findings(id,check_id,category,object_kind,object_id,object_revision,fingerprint,status,code,repair,private_json,details_json,checked_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,unixepoch()) ON CONFLICT(id) DO UPDATE SET status=excluded.status,checked_at=excluded.checked_at`, id, j.ID, f.Category, f.ObjectKind, f.ObjectID, f.ObjectRevision, f.Fingerprint, f.Status, f.Code, f.Repair, healthPrivate(f.Private), string(f.Details))
		if e != nil {
			return e
		}
	}
	sourceBytes := int64(0)
	for _, f := range findings {
		if f.ObjectKind == "source" {
			sourceBytes = bytes
			break
		}
	}
	_, e = tx.ExecContext(ctx, "UPDATE health_checks SET phase=?,cursor=?,checked=checked+?,bytes_read=bytes_read+?,source_bytes_read=source_bytes_read+?,revision=revision+1,error_code=?,finished_at=CASE WHEN ? THEN unixepoch() ELSE finished_at END WHERE id=?", phase, cursor, len(findings), bytes, sourceBytes, code, finished, j.ID)
	if e != nil {
		return e
	}
	if finished {
		state := "completed"
		if code != "" {
			state = "failed"
		}
		_, e = tx.ExecContext(ctx, "UPDATE jobs SET state=?,stage='finished',error_code=?,updated_at=unixepoch() WHERE id=? AND generation=?", state, code, j.ID, j.Generation)
		if e != nil {
			return e
		}
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO events(type,resource_id,revision,created_at) SELECT 'health_check',id,revision,unixepoch() FROM health_checks WHERE id=?", j.ID)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func healthStamp(ctx context.Context, q queryer, kind, id string) (string, int64, error) {
	var stamp string
	if e := scopeRow(ctx, q, "SELECT "+healthStampExpr(kind, ":health_object", kind == "cover_cache" && hasParsing(ctx, q)), sql.Named("health_object", id)).Scan(&stamp); e != nil {
		return "", 0, e
	}
	var rev int64
	table := ""
	switch kind {
	case "edition", "source":
		table = "editions"
	case "work":
		table = "works"
	case "series":
		table = "series"
	}
	if table != "" {
		e := scopeRow(ctx, q, "SELECT revision FROM "+table+" WHERE id=?", id).Scan(&rev)
		if e != nil && e != sql.ErrNoRows {
			return "", 0, e
		}
	}
	return stamp, rev, nil
}

func healthStampColumns(kind string) string {
	switch kind {
	case "edition":
		return "id,library_id,work_id,revision,organization_revision,trashed_at"
	case "source":
		return "id,library_id,trashed_at"
	case "work":
		return "id,library_id,preferred_edition_id,revision"
	case "series":
		return "id,library_id,revision"
	case "asset":
		return "id,storage_key,digest,size"
	case "tag":
		return "id,namespace,name"
	case "dictionary":
		return "singleton,active_version"
	}
	return "NULL"
}
func (s *Store) HealthFindings(ctx context.Context, id, status, category string, page, size int) (model.HealthPage[model.HealthFinding], error) {
	out := model.HealthPage[model.HealthFinding]{Items: []model.HealthFinding{}, Page: page, PageSize: size}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()

	var exists bool
	if e = scopeRow(ctx, tx, "SELECT EXISTS(SELECT 1 FROM health_checks WHERE id=?)", id).Scan(&exists); e != nil {
		return out, e
	}
	if !exists {
		return out, model.ErrNotFound
	}
	args := []any{id, status, status, category, category}
	cte := "WITH current AS(" + healthCurrentSQL(s.Version >= 27) + ") "

	where := ` WHERE (?='' OR actual_status=?) AND (?='' OR category=?)`
	if e = scopeRow(ctx, tx, cte+"SELECT count(*) FROM current"+where, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, cte+"SELECT id,category,object_kind,object_id,object_revision,fingerprint,actual_status,code,repair,details_json,checked_at FROM current"+where+" ORDER BY id LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var f model.HealthFinding
		var details string
		f.CheckID = id
		if e = rows.Scan(&f.ID, &f.Category, &f.ObjectKind, &f.ObjectID, &f.ObjectRevision, &f.Fingerprint, &f.Status, &f.Code, &f.Repair, &details, &f.CheckedAt); e != nil {
			rows.Close()
			return out, e
		}
		f.Details = json.RawMessage(details)
		out.Items = append(out.Items, f)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *Store) HealthObjects(ctx context.Context, kind, cursor string, scope model.HealthCheckInput) ([]model.HealthObject, error) {
	out := []model.HealthObject{}
	rows, e := scopeRows(ctx, s.Read, "SELECT object_id, fingerprint,object_revision FROM health_check_objects WHERE check_id=? AND kind=? AND object_id>? ORDER BY object_id LIMIT 50", scope.CheckID, kind, cursor)
	if e != nil {
		return out, e
	}
	ids := []string{}
	stamps := map[string]string{}
	revisions := map[string]int64{}
	for rows.Next() {
		var id string
		var rev string
		var revision int64
		if e = rows.Scan(&id, &rev, &revision); e != nil {
			rows.Close()
			return out, e
		}
		ids = append(ids, id)
		stamps[id] = rev
		revisions[id] = revision
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	for _, id := range ids {
		o := model.HealthObject{Kind: kind, ID: id, Fingerprint: stamps[id], Revision: revisions[id]}
		switch kind {
		case "source":
			e = scopeRow(ctx, s.Read, `SELECT l.root_key,l.root_identity,l.legacy_zip_encoding,l.disabled,f.relative_path,f.size,f.mtime_ns,f.content_revision FROM editions e JOIN files f ON f.edition_id=e.id JOIN libraries l ON l.id=e.library_id WHERE e.id=?`, id).Scan(&o.RootKey, &o.RootIdentity, &o.Encoding, &o.Disabled, &o.Relative, &o.Size, &o.Mtime, &o.ContentRevision)
		case "asset":
			var refs string
			e = scopeRow(ctx, s.Read, "SELECT a.storage_key,a.digest,a.size,COALESCE((SELECT max(width) FROM tag_images WHERE asset_id=a.id),0),COALESCE((SELECT max(height) FROM tag_images WHERE asset_id=a.id),0),(SELECT json_group_array(json_object('width',width,'height',height,'format',format)) FROM tag_images WHERE asset_id=a.id) FROM assets a WHERE a.id=?", id).Scan(&o.Key, &o.Digest, &o.Size, &o.Width, &o.Height, &refs)
			o.Extra = json.RawMessage(refs)
		}
		if e != nil && e != sql.ErrNoRows {
			return out, e
		}
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (s *Store) HealthObjectStamp(ctx context.Context, kind, id string) (string, int64, error) {
	return healthStamp(ctx, s.Read, kind, id)
}

func healthPrivate(v json.RawMessage) string {
	if len(v) == 0 {
		return "{}"
	}
	return string(v)
}

func healthCurrentSQL(parsing bool) string {
	expr := "CASE f.object_kind"
	for _, kind := range []string{"edition", "source", "work", "series", "asset", "tag", "dictionary", "tag_cache", "cover_cache", "recommendation_index", "series_block"} {
		expr += " WHEN '" + kind + "' THEN " + healthStampExpr(kind, "f.object_id", parsing)
	}
	expr += " ELSE '' END"
	return `SELECT f.*,CASE WHEN fingerprint<>'' AND fingerprint<>(` + expr + `) THEN 'stale' ELSE status END actual_status FROM health_findings f WHERE check_id=?`
}
