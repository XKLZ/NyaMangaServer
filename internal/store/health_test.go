package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHealthDatabaseAndReport(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	f, e := s.HealthDatabase(ctx, true, filepath.Join(dir, "inspection.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range f {
		if v.Status != "normal" {
			t.Fatalf("%+v", v)
		}
	}
	c, e := s.CreateHealthCheck(ctx, model.HealthCheckInput{RequestKey: "anonymous-one", Mode: "quick"})
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.Claim(ctx, c.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.HealthCheckpoint(ctx, *j, 1, "", f, 0, true, ""); e != nil {
		t.Fatal(e)
	}
	page, e := s.HealthFindings(ctx, c.ID, "normal", "database", 1, 2)
	if e != nil || page.Total != len(f) || len(page.Items) != 2 {
		t.Fatal(page, e)
	}
	rules, e := s.HealthTaskRules(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range rules {
		if v.Status != "normal" {
			t.Fatal(v)
		}
	}
	if _, e = s.HealthFiles(ctx, c.ID, ""); e != nil {
		t.Fatal(e)
	}
}
func TestHealthBarrierRejectsUnleasedWrites(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, e := s.Write.Exec("UPDATE health_state SET phase='draining'"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Write.Exec("UPDATE enhancement_settings SET runtime_armed=0"); e == nil {
		t.Fatal("unguarded direct store write")
	}
	if _, e := s.Write.Exec("DELETE FROM job_resources"); e == nil { // Empty table triggers do not fire; seed tested in final-writer cases.
		if _, e = s.Write.Exec("INSERT INTO jobs(id,type,state,stage,generation,created_at,updated_at) VALUES('blocked','maintenance','queued','processing',1,1,1)"); e == nil {
			t.Fatal("job insert bypass")
		}
	}
	if _, e := s.BeginHealthIO(ctx); e == nil {
		t.Fatal("IO accepted")
	}
	if e := s.healthWrite(ctx, func(c *sql.Conn) error {
		_, e := c.ExecContext(ctx, "UPDATE enhancement_settings SET runtime_armed=0")
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Write.Exec("UPDATE enhancement_settings SET runtime_armed=1"); e == nil {
		t.Fatal("permission leaked")
	}
}

func TestHealthOfflineWALAndCorruptPreservation(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	// Force a committed identity change into WAL; diagnostic reader must see it.
	if _, e := s.Write.Exec("PRAGMA wal_autocheckpoint=0;UPDATE application_identity SET application='anonymous-wal-probe'"); e != nil {
		t.Fatal(e)
	}
	before := map[string][]byte{}
	for _, name := range []string{"app.sqlite", "app.sqlite-wal", "app.sqlite-shm"} {
		raw, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil {
			t.Fatal(e)
		}
		before[name] = raw
	}
	out := filepath.Join(t.TempDir(), "report")
	report, e := DiagnoseOffline(ctx, dir, out, true, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	if !report.Readable || report.IdentityValid || !report.Preserved || !report.LedgerValid || report.Integrity != "ok" {
		t.Fatal(report)
	}
	for name, raw := range before {
		after, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil || !bytes.Equal(raw, after) {
			t.Fatal("source changed", name, e)
		}
	}
	lock, e := appdata.Acquire(dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = DiagnoseOffline(ctx, dir, filepath.Join(t.TempDir(), "locked"), false, migrations.Files); e == nil {
		t.Fatal("running instance accepted")
	}
	lock.Close()
	missing := filepath.Join(t.TempDir(), "missing")
	if _, e = DiagnoseOffline(ctx, missing, filepath.Join(t.TempDir(), "unused"), false, migrations.Files); e == nil {
		t.Fatal("missing instance accepted")
	}
	if _, e = os.Stat(missing); !os.IsNotExist(e) {
		t.Fatal("instance created", e)
	}
	bad := t.TempDir()
	raw := []byte("anonymous corrupt database")
	os.WriteFile(filepath.Join(bad, "app.sqlite"), raw, 0600)
	report, e = DiagnoseOffline(ctx, bad, filepath.Join(t.TempDir(), "corrupt"), false, migrations.Files)
	if e != nil || report.Readable || report.ErrorCode != "database_unreadable" {
		t.Fatal(report, e)
	}
	after, _ := os.ReadFile(filepath.Join(bad, "app.sqlite"))
	if !bytes.Equal(raw, after) {
		t.Fatal("corrupt original changed")
	}
}

func TestHealthMigrationPreservesTwentyTwoAndFailure(t *testing.T) {
	ctx := context.Background()
	base := fstest.MapFS{}
	entries, _ := migrations.Files.ReadDir(".")
	for _, n := range entries {
		if strings.HasSuffix(n.Name(), ".sql") && n.Name() < "023" {
			b, _ := migrations.Files.ReadFile(n.Name())
			base[n.Name()] = &fstest.MapFile{Data: b}
		}
	}
	dir := t.TempDir()
	old, e := Open(ctx, dir, base)
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, old)
	if e = old.Initialize(ctx, Account{ID: "anonymous", PasswordHash: "anonymous", Revision: 1}, Session{Hash: make([]byte, 32), AccountID: "anonymous", Created: 1, Expires: 2}); e != nil {
		t.Fatal(e)
	}
	if _, e = old.Write.Exec("INSERT INTO jobs(id,type,state,stage,generation,created_at,updated_at) VALUES('retained','maintenance','paused','processing',7,1,1)"); e != nil {
		t.Fatal(e)
	}
	rows, e := old.Read.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name!='schema_migrations' ORDER BY name")
	if e != nil {
		t.Fatal(e)
	}
	names := []string{}
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	rows.Close()
	before := map[string][][]any{}
	for _, n := range names {
		before[n] = dumpRows(t, old, fmt.Sprintf("SELECT * FROM %q ORDER BY rowid", n))
	}
	ledger := dumpRows(t, old, "SELECT * FROM schema_migrations ORDER BY version")
	old.Close()
	bad := fstest.MapFS{}
	for n, f := range base {
		bad[n] = f
	}
	raw, _ := migrations.Files.ReadFile("023_data_health.sql")
	bad["023_data_health.sql"] = &fstest.MapFile{Data: append(raw, []byte("INSERT INTO absent VALUES(1);")...)}
	if s, e := Open(ctx, dir, bad); e == nil {
		s.Close()
		t.Fatal("failed 023 accepted")
	}
	for i := 0; i < 2; i++ {
		s, e := Open(ctx, dir, migrationsThrough(t, 24))
		if e != nil {
			t.Fatal(e)
		}
		if s.Version != 24 {
			t.Fatal(s.Version)
		}
		for _, n := range names {
			if !reflect.DeepEqual(before[n], dumpRows(t, s, fmt.Sprintf("SELECT * FROM %q ORDER BY rowid", n))) {
				t.Fatal("old table changed", n)
			}
		}
		if !reflect.DeepEqual(ledger, dumpRows(t, s, "SELECT * FROM schema_migrations WHERE version<=22 ORDER BY version")) {
			t.Fatal("old ledger changed")
		}
		var n int
		s.Read.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&n)
		if n != 0 {
			t.Fatal("foreign keys", n)
		}
		s.Close()
	}
}

func TestHealthPermitPoisonRecoveryAndReopen(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	e := s.healthWrite(ctx, func(c *sql.Conn) error {
		_, e := c.ExecContext(ctx, "DELETE FROM temp.health_permit WHERE singleton=1")
		return e
	})
	if !errors.Is(e, model.ErrHealthRecovery) {
		t.Fatal(e)
	}
	state, e := s.HealthState(ctx)
	if e != nil || state.Phase != "recovery_required" {
		t.Fatal(state, e)
	}
	if _, e = s.Write.Exec("UPDATE enhancement_settings SET runtime_armed=0"); e == nil {
		t.Fatal("poison unlocked")
	}
	if e = s.RecoverHealthProtection(ctx, state.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(dir, "health-write-unsafe")); !os.IsNotExist(e) {
		t.Fatal("poison marker remains", e)
	}
	s.Close()
	again, e := Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer again.Close()
	state, e = again.HealthState(ctx)
	if e != nil || state.Phase != "idle" {
		t.Fatal(state, e)
	}
}
func TestHealthFrozenPaginationIncludesDeletedAndExcludesNewTargets(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	for i := 0; i < 55; i++ {
		id := fmt.Sprintf("b%03d", i)
		if _, e := s.Write.Exec("INSERT INTO assets VALUES(?,?,?,1,1)", id, "assets/"+id+".png", "anonymous"); e != nil {
			t.Fatal(e)
		}
	}
	c, e := s.CreateHealthCheck(ctx, model.HealthCheckInput{RequestKey: "frozen-pagination", Mode: "application"})
	if e != nil {
		t.Fatal(e)
	}
	first, e := s.HealthObjects(ctx, "asset", "", c.Scope)
	if e != nil || len(first) != 50 {
		t.Fatal(len(first), e)
	}
	if _, e = s.Write.Exec("INSERT INTO assets VALUES('a-new','assets/new.png','anonymous',1,1);DELETE FROM assets WHERE id='b052'"); e != nil {
		t.Fatal(e)
	}
	second, e := s.HealthObjects(ctx, "asset", first[49].ID, c.Scope)
	if e != nil || len(second) != 5 {
		t.Fatal(second, e)
	}
	deleted := false
	for _, o := range second {
		if o.ID == "a-new" {
			t.Fatal("new target leaked")
		}
		if o.ID == "b052" {
			deleted = true
			stamp, _, e := s.HealthObjectStamp(ctx, "asset", o.ID)
			if e != nil || stamp == o.Fingerprint {
				t.Fatal(stamp, o, e)
			}
		}
	}
	if !deleted {
		t.Fatal("deleted frozen target silently omitted")
	}
}
func TestHealthTerminalCleanupRetainsReportsRepairsAndPausedJobs(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	c, e := s.CreateHealthCheck(ctx, model.HealthCheckInput{RequestKey: "retained-health", Mode: "quick"})
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"ordinary-old", "retained-paused-owner", "cleanup-worker"} {
		if _, e = s.Write.Exec("INSERT INTO jobs(id,type,state,stage,generation,created_at,updated_at) VALUES(?,'maintenance','completed','finished',1,1,1)", id); e != nil {
			t.Fatal(e)
		}
	}
	_, e = s.Write.Exec("UPDATE jobs SET state='completed',updated_at=1 WHERE id=?;INSERT INTO health_repair_plans(id,request_key,check_id,input_json,summary_json,created_at,expires_at) VALUES('p','retain-plan',?,'{}','{}',1,9999999999);INSERT INTO health_repairs(id,plan_id,job_id,request_key,state,created_at,updated_at) VALUES('r','p','retained-paused-owner','retain-repair','completed',1,1);INSERT INTO health_paused_jobs VALUES('r','ordinary-old','running',1)", c.ID, c.ID)
	if e != nil {
		t.Fatal(e)
	}
	counts, e := s.HealthCleanupPreview(ctx)
	if e != nil || counts["protected_health_jobs"] != 3 {
		t.Fatal(counts, e)
	}
	if _, e = s.Write.Exec("UPDATE jobs SET state='running',stage='processing',generation=2 WHERE id='cleanup-worker'"); e != nil {
		t.Fatal(e)
	}
	if e = s.CleanHistory(ctx, model.Job{ID: "cleanup-worker", Generation: 2}, []string{"terminal_jobs"}); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{c.ID, "ordinary-old", "retained-paused-owner"} {
		if _, e = s.Job(ctx, id); e != nil {
			t.Fatal(id, e)
		}
	}
}

func TestHealthCompletionWatcherCancellationCannotLeaveFinishing(t *testing.T) {
	s, dir := openTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, e := s.CreateHealthCheck(ctx, model.HealthCheckInput{RequestKey: "completion-check", Mode: "quick"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Write.Exec("INSERT INTO health_repair_plans(id,request_key,check_id,input_json,summary_json,created_at,expires_at) VALUES('p','completion-plan',?,'{}','{}',1,9999999999);INSERT INTO jobs(id,type,state,stage,generation,created_at,updated_at) VALUES('r','maintenance','running','processing',1,1,1);INSERT INTO health_repairs(id,plan_id,job_id,request_key,state,created_at,updated_at) VALUES('r','p','r','completion-repair','executing',1,1);UPDATE health_state SET phase='executing',repair_id='r'", c.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "health-write-unsafe"), []byte("recovery_required"), 0600); e != nil {
		t.Fatal(e)
	}
	// Deterministically reproduce the worker watcher observing its own completed
	// receipt, before the second protected transition can clear the barrier.
	called := 0
	s.healthFinishCommitted = func() { called++; cancel() }
	if e = s.EndHealthMaintenance(ctx, model.Job{ID: "r", Generation: 1}, false); e != nil {
		t.Fatal(e)
	}
	state, e := s.HealthState(context.Background())
	if e != nil || state.Phase != "idle" || called != 1 {
		t.Fatal(state, e, called)
	}
	if _, e = os.Stat(filepath.Join(dir, "health-write-unsafe")); !os.IsNotExist(e) {
		t.Fatal("safe recovery marker retained", e)
	}
	if e = s.EndHealthMaintenance(context.Background(), model.Job{ID: "r", Generation: 1}, false); e == nil {
		t.Fatal("completed/old worker regained authority")
	}
}

func TestHealthOldCompletionCannotUnlockNewGeneration(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	c, e := s.CreateHealthCheck(ctx, model.HealthCheckInput{RequestKey: "generation-check", Mode: "quick"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Write.Exec("INSERT INTO health_repair_plans(id,request_key,check_id,input_json,summary_json,created_at,expires_at) VALUES('p','generation-plan',?,'{}','{}',1,9999999999);INSERT INTO jobs(id,type,state,stage,generation,created_at,updated_at) VALUES('r','maintenance','running','processing',1,1,1);INSERT INTO health_repairs(id,plan_id,job_id,request_key,state,created_at,updated_at) VALUES('r','p','r','generation-repair','executing',1,1);UPDATE health_state SET phase='executing',repair_id='r'", c.ID)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(dir, "health-write-unsafe"), []byte("recovery_required"), 0600)
	s.healthFinishCommitted = func() {
		if e := s.ControlJob(ctx, "r", "resume", ""); e != nil {
			t.Error(e)
		}
		if _, e := s.Write.Exec("UPDATE health_state SET phase='recovery_required'"); e != nil {
			t.Error(e)
		}
	}
	if e = s.EndHealthMaintenance(ctx, model.Job{ID: "r", Generation: 1}, false); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal(e)
	}
	state, _ := s.HealthState(ctx)
	next, _ := s.Job(ctx, "r")
	if state.Phase == "idle" || next.Generation <= 1 || next.State != "queued" {
		t.Fatal(state, next)
	}
	if _, e = os.Stat(filepath.Join(dir, "health-write-unsafe")); e != nil {
		t.Fatal("old generation cleared poison", e)
	}
}

func TestHealthRestartPausesQueuedRepairAndPreservesUserPause(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	c, e := s.CreateHealthCheck(ctx, model.HealthCheckInput{RequestKey: "restart-check", Mode: "quick"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Write.Exec("INSERT INTO health_repair_plans(id,request_key,check_id,input_json,summary_json,created_at,expires_at) VALUES('p','restart-plan',?,'{}','{}',1,9999999999);INSERT INTO jobs(id,type,state,stage,generation,created_at,updated_at) VALUES('r','maintenance','queued','processing',1,1,1),('paused','maintenance','paused','processing',7,1,1);INSERT INTO health_repairs(id,plan_id,job_id,request_key,state,created_at,updated_at) VALUES('r','p','r','restart-repair','draining',1,1);UPDATE health_state SET phase='draining',repair_id='r'", c.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.HealthRecover(ctx); e != nil {
		t.Fatal(e)
	}
	state, _ := s.HealthState(ctx)
	r, _ := s.Job(ctx, "r")
	paused, _ := s.Job(ctx, "paused")
	if state.Phase != "paused" || r.State != "paused" || r.Generation != 2 || paused.Generation != 7 || paused.State != "paused" {
		t.Fatal(state, r, paused)
	}
}

func TestHealthSnapshotPreservesIndexDamageForDiagnosis(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	if _, e := s.Write.Exec("CREATE TABLE anonymous_probe(v INTEGER);INSERT INTO anonymous_probe VALUES(1),(2);CREATE INDEX anonymous_index ON anonymous_probe(v);PRAGMA writable_schema=ON;UPDATE sqlite_master SET sql='CREATE INDEX anonymous_index ON anonymous_probe(v+1)' WHERE name='anonymous_index';PRAGMA writable_schema=OFF"); e != nil {
		t.Fatal(e)
	}
	findings, e := s.HealthDatabase(ctx, true, filepath.Join(dir, "damaged-inspection.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, f := range findings {
		if f.Code == "database_integrity_invalid" && f.Status == "confirmed" {
			found = true
		}
	}
	if !found {
		t.Fatal("snapshot rebuilt and concealed the damaged index", findings)
	}
}
