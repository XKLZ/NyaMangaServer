package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"modernc.org/sqlite"
	"net/url"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Store) HealthSnapshot(ctx context.Context, path string) error {
	u := url.URL{Scheme: "file", Path: filepath.Join(s.healthDir, "app.sqlite")}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(3000)")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return e
	}
	defer db.Close()
	f, e := os.CreateTemp(filepath.Dir(path), "snapshot-pending-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	source, e := db.Conn(ctx)
	if e != nil {
		return e
	}
	defer source.Close()
	destination := url.URL{Scheme: "file", Path: tmp}
	e = source.Raw(func(raw any) error {
		driver, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return model.ErrHealthRecovery
		}
		backup, err := driver.NewBackup(destination.String())
		if err != nil {
			return err
		}
		finished := false
		defer func() {
			if !finished {
				_ = backup.Finish()
			}
		}()
		for {
			if err = ctx.Err(); err != nil {
				return err
			}
			more, err := backup.Step(256)
			if err != nil {
				var sqliteError *sqlite.Error
				if errors.As(err, &sqliteError) && (sqliteError.Code()&255 == 5 || sqliteError.Code()&255 == 6) {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(10 * time.Millisecond):
						continue
					}
				}
				return err
			}
			if !more {
				break
			}
		}
		err = backup.Finish()
		finished = true
		return err
	})
	if e != nil {
		return e
	}
	f, e = os.OpenFile(tmp, os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	e = f.Sync()
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	// Callers use fresh check or per-repair names. A rollback point can never be replaced.
	if _, e = os.Lstat(path); e == nil {
		return model.ErrConflict
	} else if !os.IsNotExist(e) {
		return e
	}
	if e = os.Rename(tmp, path); e != nil {
		return e
	}
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
func (s *Store) HealthDatabase(ctx context.Context, full bool, snapshot string) ([]model.HealthFinding, error) {
	db := s.Read
	var e error
	if full {
		if e = os.Remove(snapshot); e != nil && !os.IsNotExist(e) {
			return nil, e
		}
		if e = s.HealthSnapshot(ctx, snapshot); e != nil {
			return nil, e
		}
		u := url.URL{Scheme: "file", Path: snapshot}
		q := u.Query()
		q.Set("mode", "ro")
		u.RawQuery = q.Encode()
		db, e = sql.Open("sqlite", u.String())
		if e != nil {
			return nil, e
		}
		defer db.Close()
	}
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var read queryer = tx
	out := []model.HealthFinding{}
	add := func(status, code string) {
		out = append(out, model.HealthFinding{Category: "database", ObjectKind: "database", ObjectID: "main", Status: status, Code: code})
	}
	var app, baseline string
	if e = read.QueryRowContext(ctx, "SELECT application,baseline FROM application_identity WHERE singleton=1").Scan(&app, &baseline); e != nil {
		return nil, e
	}
	if app != ApplicationID || baseline != BaselineID {
		add("confirmed", "database_identity_invalid")
	} else {
		add("normal", "database_identity_valid")
	}
	if s.Version >= 25 {
		if e = validateAccountIntegrity(ctx, read); e != nil {
			add("manual", "account_identity_inconsistent")
		} else {
			add("normal", "account_identity_valid")
		}
	}
	rows, e := read.QueryContext(ctx, "SELECT version,checksum FROM schema_migrations ORDER BY version")
	if e != nil {
		return nil, e
	}
	bad := false
	last := 0
	for rows.Next() {
		var version int
		var sum string
		if e = rows.Scan(&version, &sum); e != nil {
			rows.Close()
			return nil, e
		}
		if version != last+1 {
			bad = true
		}
		last = version
		entries, _ := fs.ReadDir(s.healthMigrations, ".")
		matched := false
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), fmt.Sprintf("%03d_", version)) && strings.HasSuffix(entry.Name(), ".sql") {
				b, err := fs.ReadFile(s.healthMigrations, entry.Name())
				if err != nil {
					rows.Close()
					return nil, err
				}
				matched = fmt.Sprintf("%x", sha256.Sum256(b)) == sum
				break
			}
		}
		if !matched {
			bad = true
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if last != s.Version {
		bad = true
	}
	if bad {
		add("confirmed", "migration_ledger_invalid")
	} else {
		add("normal", "migration_ledger_valid")
	}
	pragma := "PRAGMA quick_check"
	if full {
		pragma = "PRAGMA integrity_check"
	}
	rows, e = read.QueryContext(ctx, pragma)
	if e != nil {
		return nil, e
	}
	good := true
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			rows.Close()
			return nil, e
		}
		if v != "ok" {
			good = false
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if good {
		add("normal", "database_integrity_valid")
	} else {
		add("confirmed", "database_integrity_invalid")
	}
	var foreign int
	if e = read.QueryRowContext(ctx, "SELECT count(*) FROM pragma_foreign_key_check").Scan(&foreign); e != nil {
		return nil, e
	}
	if foreign > 0 {
		add("confirmed", "foreign_key_invalid")
	} else {
		add("normal", "foreign_key_valid")
	}
	// Compare schema DDL against a fresh anonymous construction from the same
	// immutable migrations. The scratch database contains no private business data.
	root, e := os.MkdirTemp(filepath.Dir(snapshot), "canonical-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(root)
	canonical, e := Open(model.WithoutActor(ctx), root, s.healthMigrations)
	if e != nil {
		return nil, e
	}
	defer canonical.Close()
	schema := func(db queryer) (map[string]string, error) {
		r, e := db.QueryContext(ctx, "SELECT name,COALESCE(sql,'') FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY name")
		if e != nil {
			return nil, e
		}
		defer r.Close()
		m := map[string]string{}
		for r.Next() {
			var name, ddl string
			if e = r.Scan(&name, &ddl); e != nil {
				return nil, e
			}
			m[name] = strings.Join(strings.Fields(ddl), " ")
		}
		return m, r.Err()
	}
	want, e := schema(canonical.Read)
	if e != nil {
		return nil, e
	}
	got, e := schema(read)
	if e != nil {
		return nil, e
	}
	mismatch := false
	for name, ddl := range want {
		if got[name] != ddl {
			mismatch = true
		}
	}
	if mismatch {
		add("confirmed", "schema_definition_mismatch")
	} else {
		add("normal", "schema_definition_valid")
	}
	if len(got) != len(want) {
		add("manual", "schema_unrecognized_objects")
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return out, nil
}
func (s *Store) HealthTaskRules(ctx context.Context) ([]model.HealthFinding, error) {
	out := []model.HealthFinding{}
	queries := []struct{ code, sql string }{
		{"running_resource_generation", `SELECT count(*) FROM job_resources r JOIN jobs j ON j.id=r.job_id WHERE j.state='running' AND r.generation<>j.generation`},
		{"active_import_stage", `SELECT count(*) FROM import_children c JOIN jobs j ON j.id=c.job_id JOIN import_batches b ON b.id=c.batch_id WHERE j.state='running' AND (b.state<>'processing' OR c.stage<>b.stage)`},
		{"import_candidate_json", `SELECT count(*) FROM import_candidates WHERE NOT json_valid(input_json) OR NOT json_valid(result_json)`},
		{"proposal_member_reference", `SELECT count(*) FROM work_proposal_members m LEFT JOIN editions e ON e.id=m.edition_id WHERE e.id IS NULL`},
		{"series_proposal_member_reference", `SELECT count(*) FROM series_proposal_members m JOIN series_proposals p ON p.id=m.proposal_id LEFT JOIN works w ON w.id=m.work_id WHERE p.state='pending' AND w.id IS NULL`},
		{"active_job_input_json", `SELECT count(*) FROM job_items i JOIN jobs j ON j.id=i.job_id WHERE j.state IN('queued','running','paused') AND (NOT json_valid(i.input_json) OR NOT json_valid(i.candidates_json))`},
		{"series_checkpoint_json", `SELECT count(*) FROM series_discovery_runs r JOIN jobs j ON j.id=r.job_id WHERE j.state IN('queued','running','paused') AND (NOT json_valid(r.pool_json) OR NOT json_valid(r.context_json) OR NOT json_valid(r.diagnostics_json))`},
		{"series_generation_profiles", `SELECT count(*) FROM series_evidence_generations g WHERE NOT json_valid(g.profiles_json) OR g.bytes<>(SELECT COALESCE(sum(b.size),0) FROM series_evidence_blocks b WHERE b.job_id=g.job_id)`},
		{"work_pair_block_shape", `SELECT count(*) FROM work_pair_blocks b JOIN work_discovery_runs r ON r.job_id=b.job_id JOIN recommendation_batches p ON p.job_id=r.job_id WHERE r.evidence_version='work-discovery-pairs-v1' AND CASE WHEN json_valid(p.pool_json) THEN b.row_index<0 OR b.row_index>=COALESCE(json_array_length(p.pool_json,'$.editions'),0) OR CASE WHEN length(b.hard_conflicts)>1048576 THEN 1 ELSE NOT nya_health_bits_v1(b.hard_conflicts,COALESCE(json_array_length(p.pool_json,'$.editions'),0)-b.row_index-1) END ELSE 1 END`},
		{"work_checkpoint_version_unsupported", `SELECT count(*) FROM work_discovery_runs r WHERE r.evidence_version<>'work-discovery-pairs-v1' AND EXISTS(SELECT 1 FROM work_pair_blocks b WHERE b.job_id=r.job_id)`},
		{"discovery_checkpoint_bounds", `SELECT count(*) FROM work_discovery_runs r JOIN recommendation_batches p ON p.job_id=r.job_id JOIN jobs j ON j.id=r.job_id WHERE j.state IN('queued','running','paused') AND CASE WHEN json_valid(p.pool_json) THEN r.next_row>COALESCE(json_array_length(p.pool_json,'$.editions'),0) ELSE 1 END`},
		{"series_checkpoint_bounds", `SELECT count(*) FROM series_discovery_runs r JOIN jobs j ON j.id=r.job_id WHERE j.state IN('queued','running','paused') AND CASE WHEN json_valid(r.pool_json) THEN r.next_row<0 OR r.features_done<0 OR r.next_row>COALESCE(json_array_length(r.pool_json,'$.works'),0) OR r.features_done>COALESCE(json_array_length(r.pool_json,'$.works'),0) ELSE 1 END`},
		{"pending_proposal_json", `SELECT (SELECT count(*) FROM work_proposals WHERE state='pending' AND NOT json_valid(plan_json))+(SELECT count(*) FROM series_proposals WHERE state='pending' AND NOT json_valid(plan_json))`},
		{"source_reference", `SELECT count(*) FROM edition_sources s LEFT JOIN external_metadata m ON m.id=s.metadata_id WHERE m.id IS NULL`},
	}
	for _, q := range queries {
		var n int
		if e := s.Read.QueryRowContext(ctx, q.sql).Scan(&n); e != nil {
			return nil, e
		}
		status := "normal"
		if n > 0 {
			status = "confirmed"
			if strings.HasSuffix(q.code, "_unsupported") {
				status = "not_checked"
			}
		}
		out = append(out, model.HealthFinding{Category: "tasks", ObjectKind: "tasks", ObjectID: q.code, Status: status, Code: q.code, Details: json.RawMessage(fmt.Sprintf(`{"violations":%d}`, n))})
	}
	return out, nil
}

func (s *Store) HealthDatabaseBytes(ctx context.Context) (int64, error) {
	var pages, size int64
	if e := s.Read.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); e != nil {
		return 0, e
	}
	if e := s.Read.QueryRowContext(ctx, "PRAGMA page_size").Scan(&size); e != nil {
		return 0, e
	}
	return pages * size, nil
}
