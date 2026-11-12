package store

import (
	"context"
	"database/sql"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"strings"
)

// writeDatabase binds the server principal to the same exclusive SQL transaction
// used by every domain repository. TEMP triggers recheck live grants at publication.
type writeDatabase struct {
	db      *sql.DB
	version int
}

func (w *writeDatabase) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	tx, e := w.db.BeginTx(ctx, opts)
	if e != nil {
		return nil, e
	}
	device, isDevice := model.DeviceActor(ctx)
	if isDevice {
		a, _ := model.Actor(ctx)
		if e = checkDeviceActor(ctx, tx, a.ID, device); e != nil {
			tx.Rollback()
			return nil, e
		}
	}
	if w.version >= 25 {
		a, ok := model.Actor(ctx)
		var id any
		if ok {
			id = a.ID
		}
		_, e = tx.ExecContext(ctx, "UPDATE temp.acl_actor SET account_id=?,password_change=?,source_edition=NULL,account_management=0,device=? WHERE singleton=1", id, model.PasswordChange(ctx), isDevice)
		if e != nil && strings.Contains(e.Error(), "no such table: temp.acl_actor") {
			if e = installAuthorizationOn(ctx, tx); e == nil {
				_, e = tx.ExecContext(ctx, "UPDATE temp.acl_actor SET account_id=?,password_change=?,source_edition=NULL,account_management=0,device=? WHERE singleton=1", id, model.PasswordChange(ctx), isDevice)
			}
		}
		if e != nil {
			tx.Rollback()
			return nil, e
		}
	}
	if w.version >= 25 {
		if _, e = tx.ExecContext(ctx, "DELETE FROM temp.acl_deleted"); e != nil {
			tx.Rollback()
			return nil, e
		}
	}
	return tx, nil
}
func (w *writeDatabase) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if w.version < 25 {
		return w.db.ExecContext(ctx, query, args...)
	}
	tx, e := w.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	r, e := tx.ExecContext(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return r, nil
}

// Direct fixture/maintenance SQL without a principal is trusted local code.
func (w *writeDatabase) Exec(query string, args ...any) (sql.Result, error) {
	return w.ExecContext(context.Background(), query, args...)
}

func aclAdminSQL(actor string) string {
	return `EXISTS(SELECT 1 FROM main.account_profiles ap WHERE ap.account_id=` + actor + ` AND ap.enabled=1 AND ap.must_change_password=0 AND ap.role='admin')`
}
func aclLibrarySQL(actor, library string, write bool) string {
	role := ""
	if write {
		role = " AND ag.role='editor'"
	}
	return `EXISTS(SELECT 1 FROM main.account_profiles ap JOIN main.account_library_grants ag ON ag.account_id=ap.account_id WHERE ap.account_id=` + actor + ` AND ap.enabled=1 AND ap.must_change_password=0 AND ag.library_id=` + library + role + `)`
}
func aclTableScope(table, row, actor string, write bool) string {
	col := func(name string) string { return row + name }
	lib := func(expr string) string { return aclLibrarySQL(actor, expr, write) }
	switch table {
	case "health_state", "instance_state", "gallery_highlight_settings":
		if !write {
			return "1"
		}
		return "0"
	case "libraries":
		if write {
			return "0"
		}
		return lib(col("id"))
	case "editions", "files", "works", "series", "import_batches", "jobs", "series_match_feedback", "work_proposals", "series_proposals":
		return lib(col("library_id"))
	case "edition_user_states", "reading_states", "reading_sessions", "reading_state_requests":
		return col("account_id") + "=" + actor + " AND " + aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+col("edition_id")+")", false)
	case "gallery_highlight_connections":
		if !write {
			return col("account_id") + "=" + actor
		}
		return "0"
	case "saved_searches", "tag_follow_epochs":
		return col("account_id") + "=" + actor
	case "tag_follows":
		return col("account_id") + "=" + actor
	case "edition_sources", "edition_metadata_state", "field_overrides", "effective_editions", "effective_edition_tags", "edition_properties", "edition_languages", "cover_selections", "edition_version_labels", "edition_label_ownership", "recommendation_features", "recommendation_input_assets", "source_previews", "edition_character_suppressions", "file_import_state", "file_pages":
		return lib(aclParentLibrary("editions", col("edition_id"), write))
	case "series_members":
		return lib(aclParentLibrary("series", col("series_id"), write))
	case "external_metadata":
		if !write {
			return `EXISTS(SELECT 1 FROM main.edition_sources es JOIN main.editions ed ON ed.id=es.edition_id WHERE es.metadata_id=` + col("id") + ` AND (` + lib("ed.library_id") + `))`
		}
		return `EXISTS(SELECT 1 FROM main.edition_sources es WHERE es.metadata_id=` + col("id") + `) AND NOT EXISTS(SELECT 1 FROM main.edition_sources es JOIN main.editions ed ON ed.id=es.edition_id WHERE es.metadata_id=` + col("id") + ` AND NOT (` + lib("ed.library_id") + `))`
	case "tags":
		if write {
			return "0"
		}
		return `EXISTS(SELECT 1 FROM main.effective_edition_tags et JOIN main.editions ed ON ed.id=et.edition_id WHERE et.tag_id=` + col("id") + ` AND (` + lib("ed.library_id") + `)) OR EXISTS(SELECT 1 FROM main.tag_follows tf WHERE tf.account_id=` + actor + ` AND tf.tag_id=` + col("id") + `)`
	case "tag_images", "tag_translations":
		if write {
			return "0"
		}
		return `EXISTS(SELECT 1 FROM tags t WHERE t.id=` + col("tag_id") + `)`
	}
	if scope := aclChildScope(table, row, actor, write); scope != "0" {
		return scope
	}
	return aclSchemaScope(table, row, actor, write)
}
func (s *Store) installAuthorization(ctx context.Context) error {
	if s.Version < 25 {
		return nil
	}
	c, e := s.Write.Conn(ctx)
	if e != nil {
		return e
	}
	defer c.Close()
	return installAuthorizationOn(ctx, c)
}
func installAuthorizationOn(ctx context.Context, c interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) error {
	var e error
	if _, e = c.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS acl_actor(singleton INTEGER PRIMARY KEY,account_id TEXT,password_change INTEGER NOT NULL,source_edition TEXT,account_management INTEGER NOT NULL,device INTEGER NOT NULL);INSERT OR IGNORE INTO acl_actor VALUES(1,NULL,0,NULL,0,0);CREATE TEMP TABLE IF NOT EXISTS acl_deleted(kind TEXT,id TEXT,library_id TEXT,PRIMARY KEY(kind,id))`); e != nil {
		return e
	}
	rows, e := c.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if e != nil {
		return e
	}
	tables := []string{}
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			rows.Close()
			return e
		}
		tables = append(tables, name)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	actor := "(SELECT account_id FROM temp.acl_actor WHERE singleton=1)"
	for _, table := range tables {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			row := "NEW."
			if op == "DELETE" {
				row = "OLD."
			}
			allowed := aclTableScope(table, row, actor, true)
			switch table {
			case "events", "catalog_state", "tag_relation_projection", "dictionary_state":
				allowed = "1"
			case "libraries":
				if op == "UPDATE" {
					allowed = aclLibrarySQL(actor, "OLD.id", true) + " AND NEW.id=OLD.id AND NEW.root_key=OLD.root_key AND NEW.display_name=OLD.display_name AND NEW.legacy_zip_encoding=OLD.legacy_zip_encoding AND NEW.scan_interval_minutes IS OLD.scan_interval_minutes AND NEW.root_identity=OLD.root_identity AND NEW.revision=OLD.revision AND NEW.disabled=OLD.disabled AND NEW.trashed_at IS OLD.trashed_at AND NEW.scan_actor_id IS OLD.scan_actor_id"
				}
			case "enhancement_settings":
				if op == "UPDATE" {
					allowed = "OLD.feature IN('work_recommendations','series_recommendations') AND NEW.feature=OLD.feature AND NEW.enabled=OLD.enabled AND NEW.state=OLD.state AND NEW.transition_id IS OLD.transition_id AND NEW.attempt=OLD.attempt AND NEW.runtime_armed=1 AND EXISTS(SELECT 1 FROM main.account_library_grants g WHERE g.account_id=" + actor + " AND g.role='editor')"
				}
			case "instance_state":
				if op == "UPDATE" {
					allowed = "NEW.write_epoch=OLD.write_epoch+1 AND NEW.owner IS OLD.owner AND NEW.background_hold=OLD.background_hold AND NEW.revision=OLD.revision"
				}
			case "external_metadata":
				if op == "INSERT" {
					allowed = aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id=(SELECT source_edition FROM temp.acl_actor WHERE singleton=1))", true)
				}
			case "tags":
				if op == "INSERT" {
					allowed = "1"
				}
			}
			// Secondary rows have no library column; derive it through their parent.
			if allowed == "0" {
				allowed = aclChildScope(table, row, actor, true)
			}
			if op == "UPDATE" && table != "instance_state" && table != "libraries" && table != "enhancement_settings" {
				old := strings.ReplaceAll(allowed, "NEW.", "OLD.")
				allowed = "(" + allowed + ") AND (" + old + ")"
			}
			principalAllowed := "(" + aclActiveSQL(actor) + ") AND (" + allowed + ")"
			if table == "sessions" && op == "DELETE" {
				principalAllowed = "OLD.account_id=" + actor
			}
			credentialPermit := "(SELECT password_change FROM temp.acl_actor WHERE singleton=1)=1"
			if table == "instance_state" && op == "UPDATE" {
				principalAllowed += " OR ((" + credentialPermit + " OR (SELECT device FROM temp.acl_actor WHERE singleton=1)=1) AND (" + allowed + "))"
			}
			if table == "accounts" && op == "UPDATE" {
				principalAllowed += " OR (" + credentialPermit + " AND OLD.id=" + actor + " AND NEW.id=OLD.id AND NEW.singleton=OLD.singleton)"
			}
			if table == "account_profiles" && op == "UPDATE" {
				principalAllowed += " OR (" + credentialPermit + " AND OLD.account_id=" + actor + " AND NEW.account_id=OLD.account_id AND NEW.role=OLD.role AND NEW.enabled=OLD.enabled AND NEW.username=OLD.username AND NEW.acl_revision=OLD.acl_revision AND NEW.must_change_password=0)"
			}
			if table == "gallery_highlight_connections" && op == "UPDATE" {
				principalAllowed += " OR (" + credentialPermit + " AND OLD.account_id=" + actor + " AND NEW.account_id=OLD.account_id AND NEW.token_hash=OLD.token_hash AND NEW.revoked_at IS NOT NULL)"
			}
			if table == "device_sessions" && op == "UPDATE" {
				principalAllowed = "OLD.account_id=" + actor + " AND NEW.account_id=OLD.account_id"
			}
			if table == "account_security_audit" && op == "INSERT" {
				principalAllowed += " OR (" + credentialPermit + " AND NEW.actor_id=" + actor + " AND NEW.target_id=" + actor + " AND NEW.action='change_password')"
			}
			switch table {
			case "accounts", "account_profiles", "account_library_grants", "account_security_audit", "sessions", "gallery_highlight_connections", "device_sessions", "jobs", "import_batches", "job_resources", "events", "instance_state", "enhancement_transitions", "enhancement_settings":
				principalAllowed += " OR (SELECT account_management FROM temp.acl_actor WHERE singleton=1)=1"
			}
			statement := fmt.Sprintf(`CREATE TEMP TRIGGER IF NOT EXISTS %q BEFORE %s ON main.%q WHEN %s IS NOT NULL AND NOT (%s OR (%s)) BEGIN SELECT RAISE(ABORT,'forbidden:%s:%s');END`, "acl_"+table+"_"+op, op, table, actor, aclAdminSQL(actor), principalAllowed, table, op)
			if _, e = c.ExecContext(ctx, statement); e != nil {
				return fmt.Errorf("install authorization %s: %w", table, e)
			}
			deviceAllowed := deviceTableWrite(table, op, row, actor, credentialPermit)
			statement = fmt.Sprintf(`CREATE TEMP TRIGGER IF NOT EXISTS %q BEFORE %s ON main.%q WHEN (SELECT device FROM temp.acl_actor WHERE singleton=1)=1 AND NOT (%s) BEGIN SELECT RAISE(ABORT,'forbidden:device');END`,
				"acl_device_"+table+"_"+op, op, table, deviceAllowed)
			if _, e = c.ExecContext(ctx, statement); e != nil {
				return fmt.Errorf("install device authorization %s: %w", table, e)
			}

		}
	}
	for _, parent := range []string{"editions", "works", "series", "jobs", "import_batches"} {
		statement := fmt.Sprintf(`CREATE TEMP TRIGGER IF NOT EXISTS %q BEFORE DELETE ON main.%q BEGIN INSERT OR REPLACE INTO acl_deleted VALUES('%s',OLD.id,OLD.library_id);END`, "acl_deleted_"+parent, parent, parent)
		if _, e = c.ExecContext(ctx, statement); e != nil {
			return e
		}
	}
	for _, table := range []string{"jobs", "import_batches", "enhancement_transitions", "instance_operations"} {
		parent := ""
		if table == "jobs" {
			parent = "(SELECT actor_id FROM main.jobs WHERE id=NEW.parent_job_id),(SELECT b.actor_id FROM main.import_child_requests r JOIN main.import_batches b ON b.id=r.batch_id WHERE r.job_id=NEW.id),"
		}
		statement := fmt.Sprintf(`CREATE TEMP TRIGGER IF NOT EXISTS %q AFTER INSERT ON main.%q BEGIN UPDATE %q SET actor_id=COALESCE(NEW.actor_id,%s%s,(SELECT id FROM main.accounts WHERE singleton=1)),actor_acl_revision=COALESCE((SELECT acl_revision FROM main.account_profiles WHERE account_id=COALESCE(NEW.actor_id,%s%s,(SELECT id FROM main.accounts WHERE singleton=1))),1) WHERE id=NEW.id;END`, "acl_initiator_"+table, table, table, parent, actor, parent, actor)
		if _, e = c.ExecContext(ctx, statement); e != nil {
			return e
		}
	}
	return nil
}
func aclChildScope(table, row, actor string, write bool) string {
	parent, key := "", ""
	switch table {
	case "job_items", "scan_entries", "job_resources", "recommendation_batches", "recommendation_misses", "work_discovery_runs", "work_pair_blocks", "work_pair_evidence", "series_discovery_runs", "series_evidence_blocks", "series_evidence_generations", "series_pair_rows", "series_pending_works", "discovery_preprocessing", "discovery_run_features":
		parent = "jobs"
		key = "job_id"
	case "import_members", "import_children", "import_child_requests", "import_candidates", "import_works", "import_proposals", "import_batch_features":
		parent = "import_batches"
		key = "batch_id"
	case "source_searches":
		parent = "import_batches"
		key = "batch_id"
	case "source_search_results", "source_search_requests":
		return aclLibrarySQL(actor, "(SELECT b.library_id FROM main.source_searches ss JOIN main.import_batches b ON b.id=ss.batch_id WHERE ss.id="+row+"search_id)", write)
	case "work_recommendations":
		parent = "jobs"
		key = "job_id"
	case "work_proposal_members", "work_proposal_decisions":
		parent = "work_proposals"
		key = "proposal_id"
	case "series_proposal_members", "series_proposal_decisions":
		parent = "series_proposals"
		key = "proposal_id"
	}
	if parent == "" {
		return "0"
	}
	return aclLibrarySQL(actor, aclParentLibrary(parent, row+key, write), write)
}

// Every authenticated read gets the same fixed relational scopes. No table-name
// matching or user-supplied SQL decides visibility. Scopes precede aggregation.
func scopedStatement(ctx context.Context, query string, args []any) (string, []any) {
	a, ok := model.Actor(ctx)
	if !ok {
		return query, args
	}
	trim := strings.TrimSpace(query)
	upper := strings.ToUpper(trim)
	if !strings.HasPrefix(upper, "SELECT ") && !strings.HasPrefix(upper, "WITH ") {
		return query, args
	}
	// Application reads must use scoped logical tables, never bypass them with a
	// main-qualified name. System identity/pragma inspection has no business data.
	if strings.Contains(strings.ToLower(query), "main.") {
		return "SELECT * FROM _authorization_bypass_rejected", nil
	}
	actor := ":acl_actor"
	actorValid := "1"
	device, isDevice := model.DeviceActor(ctx)
	if isDevice {
		actorValid = `EXISTS(SELECT 1 FROM main.device_sessions d JOIN main.accounts a ON a.id=d.account_id
			JOIN main.account_profiles p ON p.account_id=a.id WHERE d.id=:acl_device AND d.account_id=:acl_actor AND d.revoked_at IS NULL
			AND d.expires_at>:acl_device_now AND d.auth_revision=a.auth_revision AND p.enabled=1 AND p.must_change_password=0)`
		args = append([]any{sql.Named("acl_device", device.ID), sql.Named("acl_device_now", device.Now().Unix())}, args...)
	}
	defs := make([]string, 0, len(scopedReadTables))
	for _, table := range scopedReadTables {
		scope := aclTableScope(table, "r.", actor, false)
		if scope == "0" {
			scope = aclChildScope(table, "r.", actor, false)
		}
		projection := "r.*"
		switch table {
		case "import_child_requests", "work_discovery_runs", "source_searches", "series_discovery_runs":
			projection = "r.rowid AS rowid,r.*"
		}
		gate := actorValid
		if isDevice {
			switch table {
			case "edition_user_states", "reading_states", "reading_sessions", "reading_state_requests", "saved_searches", "tag_follows", "tag_follow_epochs":
				gate += " AND r.account_id=" + actor
			case "events", "gallery_highlight_connections", "account_security_audit":
				gate = "0"
			}
		}
		defs = append(defs, table+" AS (SELECT "+projection+" FROM main."+table+" r WHERE ("+gate+") AND ("+aclAdminSQL(actor)+" OR (("+aclActiveSQL(actor)+") AND ("+scope+"))))")
	}
	prefix := "WITH "
	tail := trim
	if strings.HasPrefix(upper, "WITH RECURSIVE ") {
		prefix = "WITH RECURSIVE "
		tail = strings.TrimSpace(trim[len("WITH RECURSIVE "):])
	} else if strings.HasPrefix(upper, "WITH ") {
		tail = strings.TrimSpace(trim[5:])
	} else {
		tail = "_acl_marker AS (SELECT 1) " + trim
	}
	return prefix + strings.Join(defs, ",") + "," + tail, append([]any{sql.Named("acl_actor", a.ID)}, args...)
}
func scopeRow(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, query string, args ...any) *sql.Row {
	query, args = scopedStatement(ctx, query, args)
	return q.QueryRowContext(ctx, query, args...)
}
func scopeRows(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string, args ...any) (*sql.Rows, error) {
	query, args = scopedStatement(ctx, query, args)
	return q.QueryContext(ctx, query, args...)
}

var scopedReadTables = []string{"storage_material_receipts", "reading_states", "reading_sessions", "reading_state_requests", "account_security_audit", "assets", "cover_selections", "discovery_feature_inputs", "discovery_preprocessing", "discovery_run_features", "edition_automatic_labels", "edition_character_suppressions", "edition_label_policy", "edition_metadata_state", "edition_sources", "edition_user_states", "edition_version_labels", "editions", "effective_edition_languages", "effective_edition_properties", "effective_edition_tags", "effective_editions", "enhancement_transitions", "events", "external_metadata", "field_overrides", "file_import_state", "files", "gallery_highlight_connections", "gallery_highlight_settings", "hash_lookup_cache", "health_artifacts", "health_check_objects", "health_checks", "health_findings", "health_paused_jobs", "health_repair_plans", "health_repair_steps", "health_repairs", "health_state", "import_batches", "import_candidates", "import_child_requests", "import_children", "import_enhancements", "import_members", "import_proposals", "import_works", "instance_operations", "instance_root_links", "instance_state", "job_items", "job_resources", "jobs", "libraries", "maintenance_blocks", "maintenance_plans", "parody_character_decisions", "recommendation_batches", "recommendation_features", "recommendation_feedback", "recommendation_materials", "recommendation_misses", "saved_searches", "scan_entries", "scan_exclusions", "search_documents", "search_tags", "series", "series_discovery_runs", "series_evidence_blocks", "series_evidence_generations", "series_match_feedback", "series_members", "series_pair_rows", "series_pending_works", "series_proposal_decisions", "series_proposal_members", "series_proposals", "source_previews", "source_search_requests", "source_search_results", "source_searches", "tag_character_evidence", "tag_follow_epochs", "tag_follows", "tag_images", "tag_translations", "tags", "work_discovery_runs", "work_pair_blocks", "work_pair_evidence", "work_proposal_decisions", "work_proposal_members", "work_proposals", "work_recommendations", "works"}

func aclSchemaScope(table, row, actor string, write bool) string {
	switch table {
	case "account_library_grants":
		return aclLibrarySQL(actor, row+"library_id", write)
	case "cover_selections":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "discovery_feature_inputs":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "discovery_preprocessing":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.jobs WHERE id="+row+"job_id)", write)
	case "discovery_run_features":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "edition_automatic_labels":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "edition_character_suppressions":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "edition_label_policy":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "edition_metadata_state":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "edition_sources":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "edition_user_states":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "edition_version_labels":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "editions":
		return aclLibrarySQL(actor, row+"library_id", write)
	case "effective_edition_languages":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "effective_edition_properties":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "effective_edition_tags":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "effective_editions":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "field_overrides":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "file_import_state":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "files":
		return aclLibrarySQL(actor, row+"library_id", write)
	case "health_checks":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.jobs WHERE id="+row+"job_id)", write)
	case "health_paused_jobs":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.jobs WHERE id="+row+"job_id)", write)
	case "health_repairs":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.jobs WHERE id="+row+"job_id)", write)
	case "import_batches":
		return aclLibrarySQL(actor, row+"library_id", write)
	case "import_candidates":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "import_child_requests":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.import_batches WHERE id="+row+"batch_id)", write)
	case "import_children":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.import_batches WHERE id="+row+"batch_id)", write)
	case "import_enhancements":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.import_batches WHERE id="+row+"batch_id)", write)
	case "import_members":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "import_proposals":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.import_batches WHERE id="+row+"batch_id)", write)
	case "import_works":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.import_batches WHERE id="+row+"batch_id)", write)
	case "job_items":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "job_resources":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.jobs WHERE id="+row+"job_id)", write)
	case "jobs":
		return aclLibrarySQL(actor, row+"library_id", write)
	case "maintenance_blocks":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.jobs WHERE id="+row+"job_id)", write)
	case "maintenance_plans":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.jobs WHERE id="+row+"job_id)", write)
	case "recommendation_batches":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.jobs WHERE id="+row+"job_id)", write)
	case "recommendation_features":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "recommendation_materials":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "recommendation_misses":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.jobs WHERE id="+row+"job_id)", write)
	case "scan_entries":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.jobs WHERE id="+row+"job_id)", write)
	case "scan_exclusions":
		return aclLibrarySQL(actor, row+"library_id", write)
	case "search_documents":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "series":
		return aclLibrarySQL(actor, row+"library_id", write)
	case "series_discovery_runs":
		return aclLibrarySQL(actor, row+"library_id", write)
	case "series_evidence_blocks":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.jobs WHERE id="+row+"job_id)", write)
	case "series_evidence_generations":
		return aclLibrarySQL(actor, row+"library_id", write)
	case "series_match_feedback":
		return aclLibrarySQL(actor, row+"library_id", write)
	case "series_pair_rows":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.jobs WHERE id="+row+"job_id)", write)
	case "series_pending_works":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.jobs WHERE id="+row+"job_id)", write)
	case "series_proposals":
		return aclLibrarySQL(actor, row+"library_id", write)
	case "source_previews":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "source_search_requests":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.jobs WHERE id="+row+"job_id)", write)
	case "source_searches":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "tag_character_evidence":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "work_discovery_runs":
		return aclLibrarySQL(actor, row+"library_id", write)
	case "work_pair_blocks":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.jobs WHERE id="+row+"job_id)", write)
	case "work_pair_evidence":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.jobs WHERE id="+row+"job_id)", write)
	case "work_proposal_members":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.editions WHERE id="+row+"edition_id)", write)
	case "work_proposals":
		return aclLibrarySQL(actor, row+"library_id", write)
	case "work_recommendations":
		return aclLibrarySQL(actor, "(SELECT library_id FROM main.jobs WHERE id="+row+"job_id)", write)
	case "works":
		return aclLibrarySQL(actor, row+"library_id", write)
	case "recommendation_feedback":
		return aclLibrarySQL(actor, "(SELECT j.library_id FROM main.work_recommendations r JOIN main.jobs j ON j.id=r.job_id WHERE r.id="+row+"recommendation_id)", write)
	case "search_tags":
		if write {
			return "1"
		}
		return "EXISTS(SELECT 1 FROM tags t WHERE t.id=" + row + "tag_id)"
	case "parody_character_decisions":
		if write {
			return "0"
		}
		return "EXISTS(SELECT 1 FROM tags p JOIN tags c WHERE p.id=" + row + "parody_id AND c.id=" + row + "character_id)"
	case "assets":
		if write {
			return "1"
		}
		return "EXISTS(SELECT 1 FROM cover_selections cs JOIN main.editions e ON e.id=cs.edition_id WHERE cs.asset_id=" + row + "id AND (" + aclLibrarySQL(actor, "e.library_id", false) + ")) OR EXISTS(SELECT 1 FROM tag_images ti WHERE ti.asset_id=" + row + "id)"
	case "hash_lookup_cache":
		return "1"
	case "events":
		return "(" + row + "owner_account_id=" + actor + ") OR (" + row + "owner_account_id IS NULL AND " + aclLibrarySQL(actor, row+"acl_library_id", false) + ")"
	}
	return "0"
}

func aclActiveSQL(actor string) string {
	return `EXISTS(SELECT 1 FROM main.account_profiles WHERE account_id=` + actor + ` AND enabled=1 AND must_change_password=0)`
}

func (w *writeDatabase) Close() error            { return w.db.Close() }
func (w *writeDatabase) Begin() (*sql.Tx, error) { return w.BeginTx(context.Background(), nil) }
func (w *writeDatabase) QueryRow(query string, args ...any) *sql.Row {
	return w.db.QueryRow(query, args...)
}
func (w *writeDatabase) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return w.db.QueryRowContext(ctx, query, args...)
}
func (w *writeDatabase) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return w.db.QueryContext(ctx, query, args...)
}
func (w *writeDatabase) Conn(ctx context.Context) (*sql.Conn, error) {
	if _, device := model.DeviceActor(ctx); device {
		return nil, model.ErrForbidden
	}
	c, e := w.db.Conn(ctx)
	if e != nil {
		return nil, e
	}
	if w.version >= 25 {
		a, ok := model.Actor(ctx)
		var id any
		if ok {
			id = a.ID
		}
		_, e = c.ExecContext(ctx, "UPDATE temp.acl_actor SET account_id=?,password_change=?,source_edition=NULL,account_management=0,device=0 WHERE singleton=1", id, model.PasswordChange(ctx))
		if e != nil && strings.Contains(e.Error(), "no such table: temp.acl_actor") {
			if e = installAuthorizationOn(ctx, c); e == nil {
				_, e = c.ExecContext(ctx, "UPDATE temp.acl_actor SET account_id=?,password_change=?,source_edition=NULL,account_management=0,device=0 WHERE singleton=1", id, model.PasswordChange(ctx))
			}
		}
		if e != nil {
			c.Close()
			return nil, e
		}
	}
	return c, nil
}

func requireLibrary(ctx context.Context, q queryer, library string, write bool) error {
	a, ok := model.Actor(ctx)
	if !ok {
		return nil
	}
	if _, device := model.DeviceActor(ctx); device && write {
		return model.ErrForbidden
	}
	var visible, allowed bool
	e := q.QueryRowContext(ctx, "SELECT "+aclAdminSQL("?")+" OR "+aclLibrarySQL("?", "?", false), a.ID, a.ID, library).Scan(&visible)
	if e != nil {
		return e
	}
	if !visible {
		return model.ErrNotFound
	}
	e = q.QueryRowContext(ctx, "SELECT "+aclAdminSQL("?")+" OR "+aclLibrarySQL("?", "?", write), a.ID, a.ID, library).Scan(&allowed)
	if e != nil {
		return e
	}
	if !allowed {
		return model.ErrForbidden
	}
	return nil
}
func jobLibrary(ctx context.Context, tx *sql.Tx, ids []string) (*string, error) {
	libs := map[string]bool{}
	for _, id := range ids {
		var lib string
		if e := scopeRow(ctx, tx, "SELECT library_id FROM editions WHERE id=?", id).Scan(&lib); e == sql.ErrNoRows {
			return nil, model.ErrNotFound
		} else if e != nil {
			return nil, e
		}
		if e := requireLibrary(ctx, tx, lib, true); e != nil {
			return nil, e
		}
		libs[lib] = true
	}
	if len(libs) == 1 {
		for lib := range libs {
			return &lib, nil
		}
	}
	if _, ok := model.Actor(ctx); ok {
		if _, e := administrator(ctx, tx); e != nil {
			return nil, model.ErrForbidden
		}
	}
	return nil, nil
}
func authorizeSourcePublication(ctx context.Context, tx *sql.Tx, id, external string) error {
	if _, ok := model.Actor(ctx); !ok {
		return nil
	}
	var lib string
	if e := scopeRow(ctx, tx, "SELECT library_id FROM editions WHERE id=?", id).Scan(&lib); e != nil {
		return e
	}
	if e := requireLibrary(ctx, tx, lib, true); e != nil {
		return e
	}
	// Use the complete unfiltered affected set, in the same transaction as update.
	rows, e := tx.QueryContext(ctx, `SELECT DISTINCT e.library_id FROM external_metadata m JOIN edition_sources es ON es.metadata_id=m.id JOIN editions e ON e.id=es.edition_id WHERE m.provider='ehentai' AND m.external_id=?`, external)
	if e != nil {
		return e
	}
	libs := []string{}
	for rows.Next() {
		var l string
		if e = rows.Scan(&l); e != nil {
			rows.Close()
			return e
		}
		libs = append(libs, l)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, l := range libs {
		if e = requireLibrary(ctx, tx, l, true); e != nil {
			return model.ErrSharedSourcePermission
		}
	}
	return nil
}

// JobContext restores durable initiator identity before any worker IO. It never
// treats a missing/disabled persisted account as a trusted system actor.
func (s *Store) JobContext(ctx context.Context, id string) (context.Context, error) {
	if s.Version < 25 {
		return ctx, nil
	}
	var actor sql.NullString
	var lib sql.NullString
	if e := s.Read.QueryRowContext(ctx, "SELECT actor_id,library_id FROM jobs WHERE id=?", id).Scan(&actor, &lib); e != nil {
		return ctx, e
	}
	if !actor.Valid {
		var n int
		if e := s.Read.QueryRowContext(ctx, "SELECT count(*) FROM accounts").Scan(&n); e != nil {
			return ctx, e
		}
		if n == 0 {
			return ctx, nil
		}
		return ctx, model.ErrForbidden
	}
	a, e := s.AccountByID(context.WithoutCancel(ctx), actor.String)
	if e != nil {
		return ctx, e
	}
	if a == nil || !a.Enabled || a.MustChangePassword {
		return ctx, model.ErrForbidden
	}
	next := model.WithActor(ctx, *a)
	if lib.Valid {
		if e = requireLibrary(next, s.Read, lib.String, true); e != nil {
			return ctx, e
		}
	} else if a.Role != "admin" {
		return ctx, model.ErrForbidden
	}
	return next, nil
}
func (s *Store) importContext(ctx context.Context, id string) (context.Context, error) {
	if s.Version < 25 {
		return ctx, nil
	}
	var actor sql.NullString
	var lib string
	if e := s.Read.QueryRowContext(ctx, "SELECT actor_id,library_id FROM import_batches WHERE id=?", id).Scan(&actor, &lib); e != nil {
		return ctx, e
	}
	if !actor.Valid {
		var n int
		if e := s.Read.QueryRowContext(ctx, "SELECT count(*) FROM accounts").Scan(&n); e != nil {
			return ctx, e
		}
		if n == 0 {
			return ctx, nil
		}
		return ctx, model.ErrForbidden
	}
	a, e := s.AccountByID(ctx, actor.String)
	if e != nil {
		return ctx, e
	}
	if a == nil || !a.Enabled || a.MustChangePassword {
		return ctx, model.ErrForbidden
	}
	next := model.WithActor(ctx, *a)
	if e = requireLibrary(next, s.Read, lib, true); e != nil {
		return ctx, e
	}
	return next, nil
}
func authorizeJob(ctx context.Context, tx *sql.Tx, j model.Job) error {
	if j.ActorID == "" {
		return nil
	} // only pre-025 anonymous/no-account fixtures
	var role string
	var enabled, forced bool
	e := tx.QueryRowContext(ctx, "SELECT role,enabled,must_change_password FROM account_profiles WHERE account_id=?", j.ActorID).Scan(&role, &enabled, &forced)
	if e != nil {
		return e
	}
	if !enabled || forced {
		return model.ErrForbidden
	}
	if role == "admin" {
		return nil
	}
	if j.LibraryID == "" {
		return model.ErrForbidden
	}
	var yes bool
	e = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM account_library_grants WHERE account_id=? AND library_id=? AND role='editor')", j.ActorID, j.LibraryID).Scan(&yes)
	if e != nil {
		return e
	}
	if !yes {
		return model.ErrForbidden
	}
	return nil
}

func aclParentLibrary(parent, id string, write bool) string {
	expression := "(SELECT library_id FROM main." + parent + " WHERE id=" + id + ")"
	if write {
		return "COALESCE(" + expression + ",(SELECT library_id FROM temp.acl_deleted WHERE kind='" + parent + "' AND id=" + id + "))"
	}
	return expression
}
func (s *Store) scanContext(ctx context.Context, library string) (context.Context, error) {
	if _, ok := model.Actor(ctx); ok || s.Version < 25 {
		return ctx, nil
	}
	var actor sql.NullString
	if e := s.Read.QueryRowContext(ctx, "SELECT scan_actor_id FROM libraries WHERE id=?", library).Scan(&actor); e != nil {
		return ctx, e
	}
	if !actor.Valid {
		a, e := s.Account(ctx)
		if e != nil {
			return ctx, e
		}
		if a == nil {
			return ctx, nil
		}
		actor.String = a.ID
	}
	a, e := s.AccountByID(ctx, actor.String)
	if e != nil {
		return ctx, e
	}
	if a == nil || !a.Enabled || a.MustChangePassword || a.PasswordHash == "" {
		return ctx, model.ErrForbidden
	}
	next := model.WithActor(ctx, *a)
	if e = requireLibrary(next, s.Read, library, true); e != nil {
		return ctx, e
	}
	return next, nil
}
func (s *Store) EnhancementContext(ctx context.Context, id string) (context.Context, error) {
	if s.Version < 25 {
		return ctx, nil
	}
	var actor sql.NullString
	if e := s.Read.QueryRowContext(ctx, "SELECT actor_id FROM enhancement_transitions WHERE id=?", id).Scan(&actor); e != nil {
		return ctx, e
	}
	if !actor.Valid {
		var n int
		if e := s.Read.QueryRowContext(ctx, "SELECT count(*) FROM accounts").Scan(&n); e != nil {
			return ctx, e
		}
		if n == 0 {
			return ctx, nil
		}
		return ctx, model.ErrForbidden
	}
	a, e := s.AccountByID(ctx, actor.String)
	if e != nil {
		return ctx, e
	}
	if a == nil {
		return ctx, model.ErrForbidden
	}
	next := model.WithActor(ctx, *a)
	if e = s.RequireAdministrator(next); e != nil {
		return ctx, e
	}
	return next, nil
}
