package store

import (
	"context"
	"reflect"
	"testing"
	"testing/fstest"

	"nyamangaserver.local/server/migrations"
)

func TestClientMigration28Preserves27AndRollsBack(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	old := migrationsThrough(t, 27)
	s, e := Open(ctx, dir, old)
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, s)
	if _, e = s.Write.Exec(`INSERT INTO accounts VALUES(1,'sample-account','anonymous',1,1);
		INSERT INTO sessions VALUES(randomblob(32),'sample-account',1,9999999999);
		INSERT INTO edition_user_states VALUES('sample-account','book',1,0,2);
		INSERT INTO reading_states(account_id,edition_id,page_index,recorded_page_count,content_revision,completed,last_read_at,read_count,revision,parsing_revision)
		VALUES('sample-account','book',1,2,1,0,3,1,2,1)`); e != nil {
		t.Fatal(e)
	}
	tables := []string{"accounts", "account_profiles", "sessions", "libraries", "editions", "files", "edition_user_states", "reading_states", "schema_migrations"}
	query := func(table string) string {
		if table == "schema_migrations" {
			return "SELECT * FROM schema_migrations WHERE version<=27 ORDER BY version"
		}
		return "SELECT * FROM " + table
	}
	before := map[string][][]any{}
	for _, table := range tables {
		before[table] = dumpRows(t, s, query(table))
	}
	s.Close()

	broken := migrationsThrough(t, 28)
	const migration = "028_client_foundation.sql"
	raw, e := migrations.Files.ReadFile(migration)
	if e != nil {
		t.Fatal(e)
	}
	broken[migration] = &fstest.MapFile{Data: append(raw, []byte("\nINSERT INTO deliberately_missing_table VALUES(1);")...)}
	if failed, e := Open(ctx, dir, broken); e == nil {
		failed.Close()
		t.Fatal("invalid migration committed")
	}
	check := func(expected int, files fstest.MapFS) *Store {
		t.Helper()
		db, e := Open(ctx, dir, files)
		if e != nil {
			t.Fatal(e)
		}
		if db.SchemaVersion() != expected {
			t.Fatalf("schema %d, expected %d", db.SchemaVersion(), expected)
		}
		for _, table := range tables {
			if got := dumpRows(t, db, query(table)); !reflect.DeepEqual(before[table], got) {
				t.Fatalf("migration %d changed %s", expected, table)
			}
		}
		return db
	}
	s = check(27, old)
	var found int
	if e = s.Read.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('device_sessions','instance_client_identity')").Scan(&found); e != nil || found != 0 {
		t.Fatal("partial schema committed", found, e)
	}
	s.Close()
	current := migrationsThrough(t, 28)
	s = check(28, current)
	id, e := s.ClientIdentity(ctx)
	if e != nil || id.InstanceID == "" || id.CacheEpoch == "" {
		t.Fatal("missing client identity", id, e)
	}
	s.Close()
	s = check(28, current)
	defer s.Close()
	if reopened, e := s.ClientIdentity(ctx); e != nil || reopened != id {
		t.Fatal("client identity changed across reopen", reopened, e)
	}
	if e = s.Read.QueryRow("SELECT count(*) FROM device_sessions").Scan(&found); e != nil || found != 0 {
		t.Fatal("migration created device authorization", found, e)
	}
}
