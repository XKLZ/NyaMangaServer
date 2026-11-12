package store

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"nyamangaserver.local/server/migrations"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, e := Open(context.Background(), dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	// Existing feature tests explicitly opt in; new-default tests open directly.
	if _, e = s.Write.Exec("UPDATE enhancement_settings SET enabled=1,runtime_armed=1"); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}
func TestMigrationAndConnectionPolicy(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	var mode string
	if e := s.Write.QueryRow("PRAGMA journal_mode").Scan(&mode); e != nil || mode != "wal" {
		t.Fatal(mode, e)
	}
	// Hold all read connections to prove pragmas apply to each new connection.
	var conns []*sql.Conn
	for i := 0; i < 4; i++ {
		c, e := s.Read.Conn(ctx)
		if e != nil {
			t.Fatal(e)
		}
		conns = append(conns, c)
		for pragma, want := range map[string]int{"foreign_keys": 1, "busy_timeout": 5000, "query_only": 1} {
			var n int
			if e = c.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&n); e != nil || n != want {
				t.Fatal(pragma, n, e)
			}
		}
	}
	for _, c := range conns {
		c.Close()
	}
	var n int
	if e := s.Read.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&n); e != nil || n != 115 {
		t.Fatal(n, e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	again, e := Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer again.Close()
	if again.Version != 29 {
		t.Fatal(again.Version)
	}
	b, e := migrations.Files.ReadFile("001_foundation.sql")
	if e != nil {
		t.Fatal(e)
	}
	bad := fstest.MapFS{"001_foundation.sql": {Data: append(b, []byte("\n-- changed")...)}}
	if db, e := Open(ctx, dir, bad); e == nil {
		db.Close()
		t.Fatal("checksum mismatch accepted")
	}
}
func TestUpgradeSnapshotAndRollback(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first, _ := migrations.Files.ReadFile("001_foundation.sql")
	secondBase, _ := migrations.Files.ReadFile("002_metadata.sql")
	thirdBase, _ := migrations.Files.ReadFile("003_organization.sql")
	s, e := Open(ctx, dir, fstest.MapFS{"001_foundation.sql": {Data: first}, "002_metadata.sql": {Data: secondBase}, "003_organization.sql": {Data: thirdBase}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a := Account{ID: "anonymous", PasswordHash: "test-hash", Revision: 1}
	v := Session{Hash: make([]byte, 32), AccountID: a.ID, Created: 1, Expires: 2}
	if e := s.Initialize(ctx, a, v); e != nil {
		t.Fatal(e)
	}
	// Leave the original connection open so committed rows remain in live WAL.
	wal, e := os.Stat(filepath.Join(dir, "app.sqlite-wal"))
	if e != nil || wal.Size() == 0 {
		t.Fatal("expected active WAL before snapshot", e)
	}
	b, _ := migrations.Files.ReadFile("001_foundation.sql")
	second, _ := migrations.Files.ReadFile("002_metadata.sql")
	third, _ := migrations.Files.ReadFile("003_organization.sql")
	files := fstest.MapFS{"001_foundation.sql": {Data: b}, "002_metadata.sql": {Data: second}, "003_organization.sql": {Data: third}, "004_bad.sql": {Data: []byte("CREATE TABLE should_rollback(id INTEGER); INSERT INTO nonexistent VALUES(1);")}}
	if db, e := Open(ctx, dir, files); e == nil {
		db.Close()
		t.Fatal("bad migration accepted")
	}
	db, e := Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var n int
	if e = db.Read.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='should_rollback'").Scan(&n); e != nil || n != 0 {
		t.Fatal("DDL not rolled back", n, e)
	}
	paths, e := filepath.Glob(filepath.Join(dir, "upgrade-snapshots", "*.sqlite"))
	if e != nil || len(paths) < 1 {
		t.Fatal(paths, e)
	}
	info, e := os.Stat(paths[0])
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("snapshot permissions", e)
	}
	snap, e := sql.Open("sqlite", paths[0])
	if e != nil {
		t.Fatal(e)
	}
	defer snap.Close()
	var integrity string
	if e = snap.QueryRow("PRAGMA integrity_check").Scan(&integrity); e != nil || integrity != "ok" {
		t.Fatal(integrity, e)
	}
	if e = snap.QueryRow("SELECT count(*) FROM accounts").Scan(&n); e != nil || n != 1 {
		t.Fatal("snapshot missing account", n, e)
	}
}
func TestAccountAndSessionAtomicity(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	a := Account{ID: "a", PasswordHash: "hash", Revision: 1}
	v := Session{Hash: make([]byte, 32), AccountID: "a", Created: 100, Expires: 200}
	if e := s.Initialize(ctx, a, v); e != nil {
		t.Fatal(e)
	}
	if e := s.Initialize(ctx, a, v); e != ErrInitialized {
		t.Fatal(e)
	}
	// Failed session insertion rolls the administrator insert back in a fresh database.
	other, _ := openTest(t)
	bad := v
	bad.AccountID = "missing"
	if e := other.Initialize(ctx, a, bad); e == nil {
		t.Fatal("foreign key accepted")
	}
	x, e := other.Account(ctx)
	if e != nil || x != nil {
		t.Fatal("partial initialization", x, e)
	}
	if x, e := s.Session(ctx, v.Hash, 200); e != nil || x != nil {
		t.Fatal("expired session accepted")
	}
	if e := s.ChangePassword(ctx, a, "new-hash", 150); e != nil {
		t.Fatal(e)
	}
	if x, e := s.Session(ctx, v.Hash, 151); e != nil || x != nil {
		t.Fatal("session survived password change")
	}
	v.Hash[0] = 1
	if e := s.CreateSession(ctx, a, v); e != ErrConflict {
		t.Fatal("stale login inserted session", e)
	}
	if e := s.ChangePassword(ctx, a, "stale-hash", 155); e != ErrConflict {
		t.Fatal("stale password change accepted", e)
	}
}

func TestDatabaseIdentityRejection(t *testing.T) {
	ctx := context.Background()
	for _, scenario := range []string{"legacy", "wrong application", "wrong baseline", "newer version", "checksum", "gap"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			filename := filepath.Join(dir, "app.sqlite")
			if scenario == "legacy" {
				db, e := sql.Open("sqlite", filename)
				if e != nil {
					t.Fatal(e)
				}
				_, e = db.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT); INSERT INTO schema_migrations VALUES(99,'old'); CREATE TABLE editions(id TEXT)")
				if e != nil {
					t.Fatal(e)
				}
				db.Close()
			} else {
				s, e := Open(ctx, dir, migrations.Files)
				if e != nil {
					t.Fatal(e)
				}
				statement := map[string]string{"wrong application": "UPDATE application_identity SET application='other'", "wrong baseline": "UPDATE application_identity SET baseline='other'", "newer version": "UPDATE schema_migrations SET version=999 WHERE version=2", "checksum": "UPDATE schema_migrations SET checksum='bad'", "gap": "DELETE FROM schema_migrations"}[scenario]
				if _, e = s.Write.Exec(statement); e != nil {
					t.Fatal(e)
				}
				s.Close()
			}
			before, e := os.ReadFile(filename)
			if e != nil {
				t.Fatal(e)
			}
			if s, e := Open(ctx, dir, migrations.Files); e == nil {
				s.Close()
				t.Fatal("invalid identity or history accepted")
			}
			after, e := os.ReadFile(filename)
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("rejected database modified")
			}
		})
	}
}

func TestInitialMigrationFailureRollsBackIdentity(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	files := fstest.MapFS{"001_bad.sql": {Data: []byte("CREATE TABLE partial(id INTEGER); INSERT INTO absent VALUES(1)")}}
	if s, e := Open(ctx, dir, files); e == nil {
		s.Close()
		t.Fatal("bad initial migration accepted")
	}
	db, e := sql.Open("sqlite", filepath.Join(dir, "app.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	var count int
	e = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&count)
	db.Close()
	if e != nil || count != 0 {
		t.Fatal("initial migration was partial", count, e)
	}
	s, e := Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal("retry fresh initialization", e)
	}
	s.Close()
}
