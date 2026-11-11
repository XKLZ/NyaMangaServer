package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestInstanceMigration24PreservesAppliedLedgerAndRows(t *testing.T) {
	ctx := context.Background()
	old := fstest.MapFS{}
	names, e := fs.Glob(migrations.Files, "*.sql")
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range names {
		if name >= "024_" {
			continue
		}
		raw, e := fs.ReadFile(migrations.Files, name)
		if e != nil {
			t.Fatal(e)
		}
		old[name] = &fstest.MapFile{Data: raw}
	}
	dir := t.TempDir()
	s, e := Open(ctx, dir, old)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.ExecContext(ctx, "INSERT INTO accounts VALUES(1,'fixed-account','fixed-password',9,3)"); e != nil {
		t.Fatal(e)
	}
	before := map[int]string{}
	rows, e := s.Read.QueryContext(ctx, "SELECT version,checksum FROM schema_migrations ORDER BY version")
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var version int
		var hash string
		if e = rows.Scan(&version, &hash); e != nil {
			t.Fatal(e)
		}
		before[version] = hash
	}
	rows.Close()
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	bad := fstest.MapFS{}
	for _, name := range names {
		raw, e := fs.ReadFile(migrations.Files, name)
		if e != nil {
			t.Fatal(e)
		}
		if name == "024_instance_recovery.sql" {
			raw = append(append([]byte{}, raw...), []byte("INSERT INTO nonexistent_fixture_table VALUES(1);")...)
		}
		bad[name] = &fstest.MapFile{Data: raw}
	}
	if failed, e := Open(ctx, dir, bad); e == nil {
		failed.Close()
		t.Fatal("failed024 accepted")
	}
	retained, e := Open(ctx, dir, old)
	if e != nil {
		t.Fatal(e)
	}
	if retained.Version != 23 {
		t.Fatalf("failed024 changed old schema: %d", retained.Version)
	}
	var retainedID string
	if e = retained.Read.QueryRowContext(ctx, "SELECT id FROM accounts").Scan(&retainedID); e != nil || retainedID != "fixed-account" {
		t.Fatalf("failed024 changed account %v", e)
	}
	retained.Close()
	s, e = Open(ctx, dir, migrationsThrough(t, 24))
	if e != nil {
		t.Fatal(e)
	}
	if s.Version != 24 {
		t.Fatalf("schema %d", s.Version)
	}
	var count int
	if e = s.Read.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&count); e != nil || count != 106 {
		t.Fatalf("table count %d %v", count, e)
	}
	var account, password string
	var revision int
	if e = s.Read.QueryRowContext(ctx, "SELECT id,password_hash,auth_revision FROM accounts WHERE singleton=1").Scan(&account, &password, &revision); e != nil || account != "fixed-account" || password != "fixed-password" || revision != 9 {
		t.Fatalf("account not retained: %s %s %d %v", account, password, revision, e)
	}
	for version, hash := range before {
		var actual string
		if e = s.Read.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE version=?", version).Scan(&actual); e != nil || actual != hash {
			t.Fatalf("ledger %d changed: %v", version, e)
		}
	}
	state, e := s.InstanceState(ctx)
	if e != nil || state.Owner != nil || state.BackgroundHold {
		t.Fatalf("state %+v %v", state, e)
	}
	if _, e = s.Write.ExecContext(ctx, "UPDATE accounts SET auth_revision=auth_revision+1"); e != nil {
		t.Fatal(e)
	}
	after, e := s.InstanceState(ctx)
	if e != nil || after.WriteEpoch <= state.WriteEpoch {
		t.Fatalf("write epoch %+v %v", after, e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(ctx, dir, migrationsThrough(t, 24))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, name := range names {
		raw, e := fs.ReadFile(migrations.Files, name)
		if e != nil {
			t.Fatal(e)
		}
		hash := sha256.Sum256(raw)
		if name < "024_" {
			version := 0
			for i := 0; i < 3; i++ {
				version = version*10 + int(name[i]-'0')
			}
			if before[version] != hex.EncodeToString(hash[:]) {
				t.Fatalf("applied bytes changed: %s", name)
			}
		}
	}
}

func TestInstanceSnapshotSchemaForgeryAndCorruptionRejected(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(t.TempDir(), "snapshot.sqlite")
	if e = s.HealthSnapshot(ctx, target); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if _, e = ValidateInstanceSchema(ctx, target, migrations.Files); e != nil {
		t.Fatal(e)
	}
	forged, e := Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = forged.Write.ExecContext(ctx, "CREATE TABLE extra_untrusted(value TEXT)"); e != nil {
		t.Fatal(e)
	}
	malicious := filepath.Join(t.TempDir(), "forged.sqlite")
	if e = forged.HealthSnapshot(ctx, malicious); e != nil {
		t.Fatal(e)
	}
	forged.Close()
	if _, e = ValidateInstanceSchema(ctx, malicious, migrations.Files); e == nil {
		t.Fatal("accepted forged schema with valid ledger")
	}
	bad := filepath.Join(t.TempDir(), "corrupt.sqlite")
	if e = os.WriteFile(bad, []byte("not a database"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = ValidateInstanceSchema(ctx, bad, migrations.Files); e == nil {
		t.Fatal("accepted corrupt package DB")
	}
}
