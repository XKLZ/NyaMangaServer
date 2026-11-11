package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"strings"
	"time"
)

func (s *Store) CreateHealthPlan(ctx context.Context, in model.HealthPlanInput, required int64) (model.HealthPlan, error) {
	if len(in.FindingIDs) < 1 || len(in.FindingIDs) > 1000 {
		return model.HealthPlan{}, model.ErrCapacity
	}
	id, e := model.NewID()
	if e != nil {
		return model.HealthPlan{}, e
	}
	raw, _ := json.Marshal(in)
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return model.HealthPlan{}, e
	}
	defer tx.Rollback()
	var prior, old string
	e = scopeRow(ctx, tx, "SELECT id,input_json FROM health_repair_plans WHERE request_key=?", in.RequestKey).Scan(&prior, &old)
	if e == nil {
		if old != string(raw) {
			return model.HealthPlan{}, model.ErrConflict
		}
		tx.Rollback()
		return s.HealthPlan(ctx, prior)
	}
	if e != sql.ErrNoRows {
		return model.HealthPlan{}, e
	}
	var unsafe int
	if e = scopeRow(ctx, tx, `SELECT count(*) FROM health_findings WHERE check_id=? AND category='database' AND code IN('database_identity_invalid','migration_ledger_invalid','database_integrity_invalid','schema_definition_mismatch') AND status='confirmed'`, in.CheckID).Scan(&unsafe); e != nil {
		return model.HealthPlan{}, e
	}
	if unsafe != 0 {
		return model.HealthPlan{}, model.ErrHealthRecovery
	}
	out := model.HealthPlan{ID: id, CheckID: in.CheckID, Revision: 1, Actions: []model.HealthAction{}, RequiredBytes: required, Rollback: "one consistent snapshot per repair and preserved target files; compare each target post-state before scoped rollback", ExpiresAt: time.Now().Add(30 * time.Minute).Unix()}
	var pages, pageSize int64
	if e = scopeRow(ctx, tx, "PRAGMA page_count").Scan(&pages); e != nil {
		return out, e
	}
	if e = scopeRow(ctx, tx, "PRAGMA page_size").Scan(&pageSize); e != nil {
		return out, e
	}
	out.SnapshotBytes = pages * pageSize
	for _, fid := range in.FindingIDs {
		var kind, object, fingerprint, status, repair string
		e = scopeRow(ctx, tx, "SELECT object_kind,object_id,fingerprint,status,repair FROM health_findings WHERE id=? AND check_id=?", fid, in.CheckID).Scan(&kind, &object, &fingerprint, &status, &repair)
		if e == sql.ErrNoRows {
			return out, model.ErrNotFound
		}
		if e != nil {
			return out, e
		}
		actual, _, e := healthStamp(ctx, tx, kind, object)
		if e != nil {
			return out, e
		}
		if fingerprint != "" && actual != fingerprint {
			return out, model.ErrHealthStale
		}
		if status == "stale" {
			return out, model.ErrHealthStale
		}
		if len(in.FindingIDs) > 100 && repair != "series_positions" {
			return out, model.ErrCapacity
		}
		if repair == "" || status != "confirmed" {
			return out, model.ErrInput
		}
		a := model.HealthAction{FindingID: fid, Kind: repair, ObjectID: object, Fingerprint: fingerprint, AllowSources: in.AllowSources}
		a.AllowSources = false
		if repair == "cover_cache" {
			var mode string
			if e = scopeRow(ctx, tx, "SELECT COALESCE((SELECT mode FROM cover_selections WHERE edition_id=?),'auto')", object).Scan(&mode); e != nil {
				return out, e
			}
			a.RequiresSources = mode != "asset"
			if a.RequiresSources && !in.AllowSources {
				return out, model.ErrInput
			}
			a.AllowSources = a.RequiresSources && in.AllowSources
		}
		if repair == "preferred" {
			var count int
			var only string
			if e = scopeRow(ctx, tx, "SELECT count(*),COALESCE(min(id),'') FROM editions WHERE work_id=?", object).Scan(&count, &only); e != nil {
				return out, e
			}
			if count == 1 {
				a.PreferredEditionID = only
			} else {
				a.PreferredEditionID = in.PreferredEditions[object]
			}
			var n int
			if e = scopeRow(ctx, tx, "SELECT count(*) FROM editions WHERE work_id=? AND id=?", object, a.PreferredEditionID).Scan(&n); e != nil {
				return out, e
			}
			if n != 1 {
				return out, model.ErrInput
			}
		}
		out.Actions = append(out.Actions, a)
	}
	// Projection rebuilds already refresh derived character evidence. Merge
	// overlapping target actions so one frozen before-image suffices.
	projection := map[string]bool{}
	for _, a := range out.Actions {
		if a.Kind == "projection" {
			projection[a.ObjectID] = true
		}
	}
	merged := []model.HealthAction{}
	for _, a := range out.Actions {
		if a.Kind == "tag_evidence" && projection[a.ObjectID] {
			continue
		}
		merged = append(merged, a)
	}
	out.Actions = merged
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM jobs WHERE state IN('queued','running')").Scan(&out.PausedTasks); e != nil {
		return out, e
	}
	summary, _ := json.Marshal(out)
	_, e = tx.ExecContext(ctx, "INSERT INTO health_repair_plans(id,request_key,check_id,input_json,summary_json,created_at,expires_at) VALUES(?,?,?,?,?,unixepoch(),?)", id, in.RequestKey, in.CheckID, string(raw), string(summary), out.ExpiresAt)
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *Store) HealthPlan(ctx context.Context, id string) (model.HealthPlan, error) {
	var raw string
	var v model.HealthPlan
	e := scopeRow(ctx, s.Read, "SELECT summary_json FROM health_repair_plans WHERE id=?", id).Scan(&raw)
	if e == sql.ErrNoRows {
		return v, model.ErrNotFound
	}
	if e != nil {
		return v, e
	}
	e = json.Unmarshal([]byte(raw), &v)
	return v, e
}
func (s *Store) ExecuteHealthPlan(ctx context.Context, id string, in model.HealthExecuteInput) (model.HealthRepair, error) {
	prior, err := s.HealthRepairByRequest(ctx, in.RequestKey)
	if err == nil {
		if prior.PlanID != id {
			return model.HealthRepair{}, model.ErrConflict
		}
		return prior, nil
	}
	if err != model.ErrNotFound {
		return model.HealthRepair{}, err
	}

	plan, e := s.HealthPlan(ctx, id)
	if e != nil {
		return model.HealthRepair{}, e
	}
	if plan.Revision != in.ExpectedRevision || plan.ExpiresAt < time.Now().Unix() {
		return model.HealthRepair{}, model.ErrHealthStale
	}
	rid, e := model.NewID()
	if e != nil {
		return model.HealthRepair{}, e
	}
	e = s.healthWrite(ctx, func(c *sql.Conn) error {
		tx, e := c.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		var prior, priorPlan string
		e = scopeRow(ctx, tx, "SELECT id,plan_id FROM health_repairs WHERE request_key=?", in.RequestKey).Scan(&prior, &priorPlan)
		if e == nil {
			if priorPlan != id {
				return model.ErrConflict
			}
			rid = prior
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
		for _, a := range plan.Actions {
			var kind, status string
			if e = scopeRow(ctx, tx, "SELECT object_kind,status FROM health_findings WHERE id=?", a.FindingID).Scan(&kind, &status); e != nil {
				return e
			}
			if status != "confirmed" {
				return model.ErrHealthStale
			}
			actual, _, e := healthStamp(ctx, tx, kind, a.ObjectID)
			if e != nil {
				return e
			}
			if a.Fingerprint != "" && actual != a.Fingerprint {
				return model.ErrHealthStale
			}
		}
		var count int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM jobs WHERE state IN('queued','running')").Scan(&count); e != nil {
			return e
		}
		if count != plan.PausedTasks {
			return model.ErrHealthStale
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO jobs(id,type,state,stage,generation,created_at,updated_at) VALUES(?,'maintenance','queued','processing',1,unixepoch(),unixepoch())", rid)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO health_repairs(id,plan_id,job_id,request_key,state,created_at,updated_at) VALUES(?,?,?,?,'draining',unixepoch(),unixepoch())", rid, id, rid, in.RequestKey)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO health_paused_jobs SELECT ?,id,state,generation FROM jobs WHERE id<>? AND state IN('queued','running')", rid, rid)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE jobs SET state='paused',generation=generation+1,error_code='global_maintenance',updated_at=unixepoch() WHERE id<>? AND state IN('queued','running')", rid)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE enhancement_settings SET runtime_armed=0 WHERE feature IN('work_recommendations','series_recommendations')"); e != nil {
			return e
		}
		for i, a := range plan.Actions {
			raw, _ := json.Marshal(a)
			if _, e = tx.ExecContext(ctx, "INSERT INTO health_repair_steps(repair_id,ordinal,action_json) VALUES(?,?,?)", rid, i, string(raw)); e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(ctx, "UPDATE health_state SET phase='draining',repair_id=?,revision=revision+1,updated_at=unixepoch() WHERE singleton=1", rid)
		if e != nil {
			return e
		}
		return tx.Commit()
	})
	if e != nil {
		return model.HealthRepair{}, e
	}
	return s.HealthRepair(ctx, rid)
}
func (s *Store) HealthRepair(ctx context.Context, id string) (model.HealthRepair, error) {
	var v model.HealthRepair
	e := scopeRow(ctx, s.Read, "SELECT id,plan_id,job_id,operation,state,revision,checkpoint,error_code,created_at,updated_at FROM health_repairs WHERE id=?", id).Scan(&v.ID, &v.PlanID, &v.JobID, &v.Operation, &v.State, &v.Revision, &v.Checkpoint, &v.ErrorCode, &v.CreatedAt, &v.UpdatedAt)
	if e == sql.ErrNoRows {
		return v, model.ErrNotFound
	}
	if e != nil {
		return v, e
	}
	if s.Version >= 27 {
		if e = scopeRow(ctx, s.Read, "SELECT EXISTS(SELECT 1 FROM storage_material_receipts WHERE id=?)", "health:"+id).Scan(&v.MaterialsCleaned); e != nil {
			return v, e
		}
	}

	v.Steps = []model.HealthRepairStep{}
	rows, e := scopeRows(ctx, s.Read, "SELECT ordinal,action_json,state,error_code FROM health_repair_steps WHERE repair_id=? ORDER BY ordinal", id)
	if e != nil {
		return v, e
	}
	defer rows.Close()
	for rows.Next() {
		var x model.HealthRepairStep
		var raw string
		if e = rows.Scan(&x.Ordinal, &raw, &x.State, &x.ErrorCode); e != nil {
			return v, e
		}
		if e = json.Unmarshal([]byte(raw), &x.Action); e != nil {
			return v, e
		}
		v.Steps = append(v.Steps, x)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return v, e
	}
	v.PausedJobs = []model.HealthPausedJob{}
	paused, e := scopeRows(ctx, s.Read, "SELECT job_id,old_state,old_generation FROM health_paused_jobs WHERE repair_id=? ORDER BY job_id", id)
	if e != nil {
		return v, e
	}
	defer paused.Close()
	for paused.Next() {
		var p model.HealthPausedJob
		if e = paused.Scan(&p.ID, &p.PreviousState, &p.PreviousGeneration); e != nil {
			return v, e
		}
		v.PausedJobs = append(v.PausedJobs, p)
	}
	return v, paused.Err()
}
func (s *Store) SetHealthRepair(ctx context.Context, j model.Job, state, code string, ordinal int) error {
	return s.healthWrite(ctx, func(c *sql.Conn) error {
		tx, e := c.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if e = guard(ctx, tx, j); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE health_repairs SET state=?,error_code=?,revision=revision+1,updated_at=unixepoch() WHERE id=?", state, code, j.ID); e != nil {
			return e
		}
		if ordinal >= 0 && code != "" {
			if _, e = tx.ExecContext(ctx, "UPDATE health_repair_steps SET state=CASE WHEN state='rolled_back' THEN state ELSE 'failed' END,error_code=? WHERE repair_id=? AND ordinal=?", code, j.ID, ordinal); e != nil {
				return e
			}
		}
		if state == "paused" {
			if _, e = tx.ExecContext(ctx, "UPDATE jobs SET state='paused',generation=generation+1,error_code=?,updated_at=unixepoch() WHERE id=? AND state IN('queued','running')", code, j.ID); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, "UPDATE health_state SET phase=?,revision=revision+1,updated_at=unixepoch() WHERE singleton=1 AND repair_id=?", state, j.ID); e != nil {
			return e
		}
		return tx.Commit()
	})
}
func (s *Store) FinishHealthStep(ctx context.Context, j model.Job, ordinal int, state, key, after string) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, "UPDATE health_repair_steps SET state=?,before_key=?,after_fingerprint=? WHERE repair_id=? AND ordinal=?", state, key, after, j.ID, ordinal)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, "UPDATE health_repairs SET checkpoint=?,revision=revision+1,updated_at=unixepoch() WHERE id=?", ordinal+1, j.ID)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) HealthRepairObject(ctx context.Context, a model.HealthAction) (model.HealthObject, error) {
	var raw string
	e := scopeRow(ctx, s.Read, "SELECT private_json FROM health_findings WHERE id=?", a.FindingID).Scan(&raw)
	var o model.HealthObject
	if e != nil {
		return o, e
	}
	e = json.Unmarshal([]byte(raw), &o)
	return o, e
}
func (s *Store) PinHealthArtifact(ctx context.Context, id, key, digest, role string, size int64) error {
	_, e := s.Write.ExecContext(ctx, "INSERT INTO health_artifacts(key,repair_id,digest,size,role) VALUES(?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET digest=excluded.digest,size=excluded.size", key, id, digest, size, role)
	return e
}
func (s *Store) EndHealthMaintenance(ctx context.Context, j model.Job, rolled bool) error {
	id := j.ID
	e := s.healthWrite(ctx, func(c *sql.Conn) error {
		tx, e := c.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if e = guard(ctx, tx, j); e != nil {
			return e
		}
		var owner string
		if e = scopeRow(ctx, tx, "SELECT COALESCE(repair_id,'') FROM health_state WHERE singleton=1").Scan(&owner); e != nil {
			return e
		}
		if owner != id {
			return model.ErrConflict
		}
		state := "completed"
		if rolled {
			state = "rolled_back"
		}
		_, e = tx.ExecContext(ctx, "UPDATE health_repairs SET state=?,revision=revision+1,updated_at=unixepoch() WHERE id=?", state, id)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE health_state SET phase='finishing',revision=revision+1,updated_at=unixepoch() WHERE singleton=1")
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE jobs SET state='completed',stage='finished',updated_at=unixepoch() WHERE id=?", id)
		if e != nil {
			return e
		}
		return tx.Commit()
	})
	if e != nil {
		return e
	}
	// Reset/discard of the leased permission has completed while protection was on.
	if s.healthFinishCommitted != nil {
		s.healthFinishCommitted()
	}
	// The durable job is now completed. Its cancellation watcher may notice
	// that completion; finish only this protected state transition with a bounded
	// detached context rather than leaving a successfully verified repair locked.
	settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	ctx = settle
	c, e := s.Write.Conn(ctx)
	if e != nil {
		return e
	}
	defer c.Close()
	final, e := c.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer final.Rollback()
	var authorized int
	if e = scopeRow(ctx, final, "SELECT count(*) FROM jobs j JOIN health_repairs r ON r.job_id=j.id WHERE j.id=? AND j.generation=? AND j.state='completed' AND r.state=?", j.ID, j.Generation, map[bool]string{false: "completed", true: "rolled_back"}[rolled]).Scan(&authorized); e != nil {
		return e
	}
	if authorized != 1 {
		return model.ErrStaleJob
	}
	if e = s.clearHealthPoison(ctx, final, id); e != nil {
		return e
	}
	result, e := final.ExecContext(ctx, "UPDATE health_state SET phase='idle',repair_id=NULL,revision=revision+1,updated_at=unixepoch() WHERE singleton=1 AND repair_id=? AND phase IN('finishing','recovery_required') AND EXISTS(SELECT 1 FROM jobs WHERE id=? AND generation=? AND state='completed')", id, j.ID, j.Generation)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return model.ErrHealthRecovery
	}
	return final.Commit()
}

func healthTables(a model.HealthAction) []string {
	switch a.Kind {
	case "projection":
		return []string{"tags", "effective_editions", "effective_edition_tags", "effective_edition_properties", "effective_edition_languages", "search_documents", "tag_character_evidence"}
	case "tag_evidence":
		return []string{"tag_character_evidence"}
	case "preferred":
		return []string{"works"}
	case "series_positions":
		return []string{"series", "series_members"}
	case "dictionary":
		return []string{"dictionary_generations", "dictionary_entries", "dictionary_state", "tag_translations"}
	}
	return nil
}
func healthTableHash(ctx context.Context, q queryer, tables []string) (string, error) {
	hash := ""
	for _, table := range tables {
		query := fmt.Sprintf("SELECT * FROM %q ORDER BY rowid", table)
		if table == "tag_translations" {
			query = "SELECT * FROM tag_translations WHERE origin='remote' ORDER BY rowid"
		}
		if table == "dictionary_state" {
			query = "SELECT singleton,active_generation,previous_generation FROM dictionary_state ORDER BY singleton"
		}
		rows, e := healthOrderedRows(ctx, q, table, query)
		if e != nil {
			return "", e
		}
		cols, e := rows.Columns()
		if e != nil {
			rows.Close()
			return "", e
		}
		for rows.Next() {
			values := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range values {
				dest[i] = &values[i]
			}
			if e = rows.Scan(dest...); e != nil {
				rows.Close()
				return "", e
			}
			raw, _ := json.Marshal(values)
			hash = healthID(hash, table, string(raw))
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return "", e
		}
	}
	return hash, nil
}
func (s *Store) ApplyHealthRepair(ctx context.Context, j model.Job, id string, ordinal int, a model.HealthAction, snapshot string) (string, error) {
	after := ""
	e := s.healthWrite(ctx, func(c *sql.Conn) error {
		tx, e := c.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if e = guard(ctx, tx, j); e != nil {
			return e
		}
		switch a.Kind {
		case "projection":
			ctx = context.WithValue(ctx, healthProjectionKey{}, true)
			// Force rebuilding language/search rows even if the effective JSON matches.
			if _, _, e = healthExpectedProjection(ctx, tx, a.ObjectID); e != nil {
				return e
			}
			for _, table := range []string{"effective_edition_properties", "effective_edition_languages", "search_documents"} {
				if _, e = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE edition_id=?", a.ObjectID); e != nil {
					return e
				}
			}
			if e = reproject(ctx, tx, a.ObjectID); e != nil {
				return e
			}
			_, e = tx.ExecContext(ctx, `INSERT INTO search_documents SELECT edition_id,nya_normalize_v1(title),nya_normalize_v1(COALESCE(json_extract(fields_json,'$.title_original'),'')),nya_natural_v1(title) FROM effective_editions WHERE edition_id=? ON CONFLICT(edition_id) DO UPDATE SET title=excluded.title,original_title=excluded.original_title,sort_key=excluded.sort_key`, a.ObjectID)
		case "tag_evidence":
			e = refreshCharacterEvidence(ctx, tx, a.ObjectID)
		case "preferred":
			var n int
			if e = scopeRow(ctx, tx, "SELECT count(*) FROM editions WHERE work_id=? AND id=?", a.ObjectID, a.PreferredEditionID).Scan(&n); e != nil {
				return e
			}
			if n != 1 {
				return model.ErrInput
			}
			_, e = tx.ExecContext(ctx, "UPDATE works SET preferred_edition_id=?,revision=revision+1 WHERE id=?", a.PreferredEditionID, a.ObjectID)
		case "series_positions":
			rows, err := scopeRows(ctx, tx, "SELECT work_id FROM series_members WHERE series_id=? ORDER BY position,work_id", a.ObjectID)
			if err != nil {
				return err
			}
			ids := []string{}
			for rows.Next() {
				var work string
				rows.Scan(&work)
				ids = append(ids, work)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if _, e = tx.ExecContext(ctx, "DELETE FROM series_members WHERE series_id=?", a.ObjectID); e != nil {
				return e
			}
			for i, work := range ids {
				if _, e = tx.ExecContext(ctx, "INSERT INTO series_members VALUES(?,?,?)", a.ObjectID, work, i+1); e != nil {
					return e
				}
			}
			_, e = tx.ExecContext(ctx, "UPDATE series SET revision=revision+1 WHERE id=?", a.ObjectID)
		case "dictionary":
			e = healthRepairDictionary(ctx, tx, a.Dictionary)
		case "cover_cache", "tag_cache", "quarantine_recommendation": // File/dictionary stages are handled through dedicated methods.
		default:
			return model.ErrInput
		}
		if e != nil {
			return e
		}
		if e = healthInvalidate(ctx, tx, a); e != nil {
			return e
		}
		after, e = healthActionHash(ctx, tx, a)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE health_repair_steps SET state='applied',before_key=?,after_fingerprint=? WHERE repair_id=? AND ordinal=?", snapshot, after, id, ordinal)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE health_repairs SET revision=revision+1,updated_at=unixepoch() WHERE id=?", id); e != nil {
			return e
		}

		return tx.Commit()
	})
	return after, e
}
func (s *Store) RollbackHealthStep(ctx context.Context, j model.Job, ordinal int, a model.HealthAction, snapshot, after string) error {
	id := j.ID
	return s.healthWrite(ctx, func(c *sql.Conn) error {
		current, e := healthActionHash(ctx, c, a)
		if e != nil {
			return e
		}
		if current != after {
			return model.ErrHealthStale
		}
		if _, e = c.ExecContext(ctx, "ATTACH DATABASE ? AS health_before", snapshot); e != nil {
			return e
		}
		defer c.ExecContext(context.WithoutCancel(ctx), "DETACH DATABASE health_before")
		// Restoring a known invalid preferred reference is a scoped inverse of the
		// explicit repair, not a new valid organization decision. Foreign keys are
		// disabled only on this exclusively leased connection and re-enabled (or the
		// connection is discarded, with maintenance still protected) by healthWrite.
		if a.Kind == "preferred" {
			if _, e = c.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); e != nil {
				return e
			}
		}
		tx, e := c.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if e = guard(ctx, tx, j); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "PRAGMA defer_foreign_keys=ON"); e != nil {
			return e
		}
		var revision int64
		if a.Kind == "preferred" {
			scopeRow(ctx, tx, "SELECT revision FROM works WHERE id=?", a.ObjectID).Scan(&revision)
		}
		if a.Kind == "series_positions" {
			scopeRow(ctx, tx, "SELECT revision FROM series WHERE id=?", a.ObjectID).Scan(&revision)
		}

		if a.Kind == "preferred" {
			_, e = tx.ExecContext(ctx, "UPDATE works SET preferred_edition_id=(SELECT preferred_edition_id FROM health_before.works WHERE id=?) WHERE id=?", a.ObjectID, a.ObjectID)
		} else if a.Kind == "series_positions" {
			if _, e = tx.ExecContext(ctx, "DELETE FROM series_members WHERE series_id=?", a.ObjectID); e != nil {
				return e
			}
			_, e = tx.ExecContext(ctx, "INSERT INTO series_members SELECT * FROM health_before.series_members WHERE series_id=?", a.ObjectID)
		} else {
			for _, table := range healthTables(a) {
				if table == "tags" {
					_, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO main.tags SELECT * FROM health_before.tags WHERE id IN(SELECT tag_id FROM health_before.effective_edition_tags WHERE edition_id=?)", a.ObjectID)
				} else if a.Kind == "dictionary" {
					if table == "dictionary_state" {
						_, e = tx.ExecContext(ctx, "UPDATE main.dictionary_state SET active_generation=(SELECT active_generation FROM health_before.dictionary_state WHERE singleton=1),previous_generation=(SELECT previous_generation FROM health_before.dictionary_state WHERE singleton=1) WHERE singleton=1")
					} else if table == "tag_translations" {
						if _, e = tx.ExecContext(ctx, "DELETE FROM main.tag_translations WHERE origin='remote'"); e != nil {
							return e
						}
						_, e = tx.ExecContext(ctx, "INSERT INTO main.tag_translations SELECT * FROM health_before.tag_translations WHERE origin='remote'")
					} else {
						if _, e = tx.ExecContext(ctx, "DELETE FROM main."+table); e != nil {
							return e
						}
						_, e = tx.ExecContext(ctx, "INSERT INTO main."+table+" SELECT * FROM health_before."+table)
					}
				} else {
					where, key := healthRowPredicate(a, table)
					if _, e = tx.ExecContext(ctx, "DELETE FROM main."+table+" WHERE "+where, key); e != nil {
						return e
					}
					_, e = tx.ExecContext(ctx, "INSERT INTO main."+table+" SELECT * FROM health_before."+table+" WHERE "+where, key)
				}
				if e != nil {
					return e
				}

			}
		}
		if e != nil {
			return e
		}

		if a.Kind == "preferred" {
			_, e = tx.ExecContext(ctx, "UPDATE works SET revision=? WHERE id=?", revision+1, a.ObjectID)
		}
		if a.Kind == "series_positions" {
			_, e = tx.ExecContext(ctx, "UPDATE series SET revision=? WHERE id=?", revision+1, a.ObjectID)
		}
		if e != nil {
			return e
		}
		if e = healthInvalidate(ctx, tx, a); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE catalog_state SET revision=revision+1")
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "UPDATE health_repair_steps SET state='rolled_back' WHERE repair_id=? AND ordinal=?", id, ordinal)
		if e != nil {
			return e
		}
		return tx.Commit()
	})
}

func healthActionHash(ctx context.Context, q queryer, a model.HealthAction) (string, error) {
	if a.Kind == "dictionary" {
		return healthTableHash(ctx, q, healthTables(a))
	}
	raw := ""
	for _, table := range healthTables(a) {
		where, key := healthRowPredicate(a, table)
		rows, e := healthOrderedRows(ctx, q, table, "SELECT * FROM "+table+" WHERE "+where+" ORDER BY rowid", key)
		if e != nil {
			return "", e
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			v := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range v {
				dest[i] = &v[i]
			}
			if e = rows.Scan(dest...); e != nil {
				rows.Close()
				return "", e
			}
			b, _ := json.Marshal(v)
			raw += table + string(b)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return "", e
		}
	}
	if a.Kind == "projection" || a.Kind == "tag_evidence" {
		stamp, _, e := healthStamp(ctx, q, "edition", a.ObjectID)
		if e != nil {
			return "", e
		}
		raw += stamp
	}
	return healthID(raw), nil
}
func healthRowPredicate(a model.HealthAction, table string) (string, string) {
	if table == "tags" {
		return "id IN(SELECT tag_id FROM effective_edition_tags WHERE edition_id=?)", a.ObjectID
	}
	key := "edition_id"
	if table == "works" || table == "series" {
		key = "id"
	}
	if table == "series_members" {
		key = "series_id"
	}
	return key + "=?", a.ObjectID
}

func (s *Store) RequestHealthRollback(ctx context.Context, id string, in model.HealthExecuteInput) (model.HealthRepair, error) {
	e := s.healthWrite(ctx, func(c *sql.Conn) error {
		tx, e := c.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if s.Version >= 27 {
			var n int
			if e = scopeRow(ctx, tx, "SELECT count(*) FROM storage_material_receipts WHERE id=?", "health:"+id).Scan(&n); e != nil {
				return e
			}
			if n > 0 {
				return model.ErrHealthStale
			}
		}

		var phase, owner, state string
		var revision int64
		if e = scopeRow(ctx, tx, "SELECT phase,COALESCE(repair_id,'') FROM health_state WHERE singleton=1").Scan(&phase, &owner); e != nil {
			return e
		}
		if phase != "idle" && owner != id {
			return model.ErrHealthMaintenance
		}
		if e = scopeRow(ctx, tx, "SELECT state,revision FROM health_repairs WHERE id=?", id).Scan(&state, &revision); e != nil {
			return e
		}
		var prior string
		if e = scopeRow(ctx, tx, "SELECT rollback_request_key FROM health_repairs WHERE id=?", id).Scan(&prior); e != nil {
			return e
		}
		if prior == in.RequestKey {
			return nil
		}
		if revision != in.ExpectedRevision || state == "rolled_back" || state == "rollback_requested" {
			return model.ErrHealthStale
		}
		if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO health_paused_jobs SELECT ?,id,state,generation FROM jobs WHERE id<>? AND state IN('queued','running')", id, id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE jobs SET state='paused',generation=generation+1,error_code='global_maintenance',updated_at=unixepoch() WHERE id<>? AND state IN('queued','running')", id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE jobs SET state='queued',stage='processing',generation=generation+1,error_code='',updated_at=unixepoch() WHERE id=?", id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE health_repairs SET operation='rollback',state='rollback_requested',rollback_request_key=?,revision=revision+1,updated_at=unixepoch() WHERE id=?", in.RequestKey, id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE health_state SET phase='draining',repair_id=?,revision=revision+1,updated_at=unixepoch() WHERE singleton=1", id); e != nil {
			return e
		}
		return tx.Commit()
	})
	if e != nil {
		return model.HealthRepair{}, e
	}
	return s.HealthRepair(ctx, id)
}

func (s *Store) HealthStepReceipt(ctx context.Context, id string, ordinal int) (string, string, string, error) {
	var state, key, after string
	e := scopeRow(ctx, s.Read, "SELECT state,before_key,after_fingerprint FROM health_repair_steps WHERE repair_id=? AND ordinal=?", id, ordinal).Scan(&state, &key, &after)
	return state, key, after, e
}
func (s *Store) HealthArtifact(ctx context.Context, key string) (string, int64, error) {
	var digest string
	var size int64
	e := scopeRow(ctx, s.Read, "SELECT digest,size FROM health_artifacts WHERE key=? AND pinned=1", key).Scan(&digest, &size)
	return digest, size, e
}

func (s *Store) HealthRepairByRequest(ctx context.Context, key string) (model.HealthRepair, error) {
	var id string
	e := scopeRow(ctx, s.Read, "SELECT id FROM health_repairs WHERE request_key=?", key).Scan(&id)
	if e == sql.ErrNoRows {
		return model.HealthRepair{}, model.ErrNotFound
	}
	if e != nil {
		return model.HealthRepair{}, e
	}
	return s.HealthRepair(ctx, id)
}
func (s *Store) VerifyHealthPostState(ctx context.Context, a model.HealthAction, after string) error {
	current, e := healthActionHash(ctx, s.Read, a)
	if e != nil {
		return e
	}
	if current != after {
		return model.ErrHealthStale
	}
	return nil
}
func (s *Store) HealthDictionaryValid(ctx context.Context) error {
	var valid bool
	e := scopeRow(ctx, s.Read, `SELECT EXISTS(SELECT 1 FROM translation_state t JOIN translation_versions v ON v.id=t.active_version JOIN dictionary_state d ON d.singleton=1 JOIN dictionary_generations g ON g.id=d.active_generation WHERE g.version=v.id AND g.digest=v.digest AND g.state='complete' AND g.expected_count=g.checkpoint AND g.expected_count=(SELECT count(*) FROM dictionary_entries e WHERE e.generation_id=g.id))`).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return model.ErrHealthRecovery
	}
	return nil
}

func healthInvalidate(ctx context.Context, tx *sql.Tx, a model.HealthAction) error {
	if _, e := tx.ExecContext(ctx, "UPDATE health_findings SET status='stale' WHERE object_kind=(SELECT object_kind FROM health_findings WHERE id=?) AND object_id=?", a.FindingID, a.ObjectID); e != nil {
		return e
	}

	if _, e := tx.ExecContext(ctx, "UPDATE catalog_state SET revision=revision+1"); e != nil {
		return e
	}
	switch a.Kind {
	case "series_positions":
		_, e := tx.ExecContext(ctx, "INSERT INTO events(type,resource_id,revision,created_at) SELECT 'series',id,revision,unixepoch() FROM series WHERE id=?", a.ObjectID)
		return e
	case "projection", "tag_evidence":
		if _, e := tx.ExecContext(ctx, "UPDATE tag_relation_projection SET revision=revision+1 WHERE singleton=1"); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO events(type,resource_id,revision,created_at) SELECT 'tag_relations','global',revision,unixepoch() FROM tag_relation_projection WHERE singleton=1")
		return e
	case "dictionary":
		if _, e := tx.ExecContext(ctx, "UPDATE dictionary_state SET manual_revision=manual_revision+1 WHERE singleton=1"); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO events(type,resource_id,revision,created_at) SELECT 'translations','dictionary',manual_revision,unixepoch() FROM dictionary_state WHERE singleton=1")
		return e
	}
	return nil
}

func (s *Store) HealthFileWrite(ctx context.Context, j model.Job, publish func() error) error {
	return s.healthWrite(ctx, func(c *sql.Conn) error {
		tx, e := c.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if e = guard(ctx, tx, j); e != nil {
			return e
		}
		if e = publish(); e != nil {
			return e
		}
		return tx.Commit()
	})
}

// Preserve the historical physical row ordering and hash columns while exposing
// rowid only inside the authenticated scope. Never bypass row visibility.
func healthOrderedRows(ctx context.Context, q queryer, table, query string, args ...any) (*sql.Rows, error) {
	if _, ok := model.Actor(ctx); !ok {
		return q.QueryContext(ctx, query, args...)
	}
	rows, e := q.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", table))
	if e != nil {
		return nil, e
	}
	columns := []string{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var defaultValue any
		if e = rows.Scan(&cid, &name, &kind, &notnull, &defaultValue, &pk); e != nil {
			rows.Close()
			return nil, e
		}
		columns = append(columns, fmt.Sprintf("%q", name))
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	query = strings.Replace(query, "SELECT * FROM", "SELECT "+strings.Join(columns, ",")+" FROM", 1)
	statement, values := scopedStatement(ctx, query, args)
	statement = strings.Replace(statement, table+" AS (SELECT r.* FROM main."+table+" r", table+" AS (SELECT r.rowid AS rowid,r.* FROM main."+table+" r", 1)
	return q.QueryContext(ctx, statement, values...)
}
