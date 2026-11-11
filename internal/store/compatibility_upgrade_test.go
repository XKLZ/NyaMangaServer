package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/testfixture"
	"nyamangaserver.local/server/migrations"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

func migrationsThrough(t *testing.T, version int) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	names, e := fs.Glob(migrations.Files, "*.sql")
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range names {
		if name[:3] > fmt.Sprintf("%03d", version) {
			continue
		}
		raw, e := fs.ReadFile(migrations.Files, name)
		if e != nil {
			t.Fatal(e)
		}
		out[name] = &fstest.MapFile{Data: raw}
	}
	return out
}
func oldColumnRows(t *testing.T, s *Store, columns map[string][]string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for table, cols := range columns {
		quoted := make([]string, len(cols))
		for i, c := range cols {
			quoted[i] = fmt.Sprintf("%q", c)
		}
		rows, e := s.Read.Query("SELECT " + strings.Join(quoted, ",") + " FROM " + fmt.Sprintf("%q", table))
		if e != nil {
			t.Fatal(e)
		}
		records := []string{}
		for rows.Next() {
			values := make([]any, len(cols))
			pointers := make([]any, len(cols))
			for i := range values {
				pointers[i] = &values[i]
			}
			if e = rows.Scan(pointers...); e != nil {
				t.Fatal(e)
			}
			raw, _ := json.Marshal(values)
			records = append(records, string(raw))
		}
		if e = rows.Err(); e != nil {
			t.Fatal(e)
		}
		rows.Close()
		sort.Strings(records)
		raw, _ := json.Marshal(records)
		out[table] = string(raw)
	}
	return out
}
func schemaColumns(t *testing.T, s *Store) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	rows, e := s.Read.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name<>'schema_migrations'")
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
	for _, n := range names {
		rows, e = s.Read.Query("SELECT name FROM pragma_table_info(?) ORDER BY cid", n)
		if e != nil {
			t.Fatal(e)
		}
		for rows.Next() {
			var c string
			rows.Scan(&c)
			out[n] = append(out[n], c)
		}
		rows.Close()
	}
	return out
}
func TestCompatibilityUpgrade24To25To26PreservesOldRowsSessionsAndRollback(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := Open(ctx, dir, migrationsThrough(t, 24))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Initialize(ctx, Account{ID: "original", PasswordHash: "opaque", Revision: 7}, Session{Hash: make([]byte, 32), AccountID: "original", Created: 1, Expires: 9999999999}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("INSERT INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision) VALUES('lib','lib','anonymous','utf8','fixed',1)"); e != nil {
		t.Fatal(e)
	}
	if e = testfixture.SeedEdition(ctx, s.Write, "book", "lib", "anonymous.zip", "anonymous"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("INSERT INTO jobs(id,library_id,state,stage,generation,created_at,updated_at) VALUES('old-paused','lib','paused','processing',7,1,1);INSERT INTO import_batches(id,library_id,request_key,state) VALUES('old-batch','lib','old-request','paused')"); e != nil {
		t.Fatal(e)
	}

	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	title := "anonymous source"
	if e = publishSource(ctx, tx, "book", model.RemoteMetadata{ExternalID: "123", URL: "https://e-hentai.org/g/123/0123456789/", Method: "manual", Fields: model.Metadata{Title: &title, Tags: []model.TagName{{Namespace: "artist", Name: "anonymous"}}}, Raw: []byte(`{"gid":123}`)}); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	b, e := s.Edition(ctx, "book")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ApplyOverrides(ctx, "book", b.Revision, map[string]model.Override{"description": {Op: "set", Value: json.RawMessage(`"anonymous manual"`)}}); e != nil {
		t.Fatal(e)
	}
	yes := true
	if _, e = s.SetUserState(ctx, "original", "book", model.StatePatch{Favorite: &yes, Hidden: &yes}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec(`INSERT INTO saved_searches VALUES('old-search','original','anonymous search',1,'{"library_id":"lib"}',1)`); e != nil {
		t.Fatal(e)
	}
	var tag string
	if e = s.Read.QueryRow("SELECT id FROM tags WHERE name='anonymous'").Scan(&tag); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetTagFollow(ctx, "original", tag, 0, true); e != nil {
		t.Fatal(e)
	}
	if e = s.PrepareProperties(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.PrepareTagRelations(ctx); e != nil {
		t.Fatal(e)
	}
	columns := schemaColumns(t, s)
	columns["instance_state"] = []string{"singleton", "owner", "background_hold", "revision"}
	before := oldColumnRows(t, s, columns)
	s.Close()
	s, e = Open(ctx, dir, migrationsThrough(t, 25))
	if e != nil {
		t.Fatal(e)
	}
	after := oldColumnRows(t, s, columns)
	for table, rows := range before {
		if rows != after[table] {
			t.Fatalf("025 changed old columns: %s", table)
		}
	}
	var actor string
	if e = s.Read.QueryRow("SELECT actor_id FROM jobs WHERE id='old-paused'").Scan(&actor); e != nil || actor != "original" {
		t.Fatal(actor, e)
	}
	s.Close()
	bad := migrationsThrough(t, 25)
	bad["026_reading_state.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE should_rollback(id);SELECT * FROM missing_reading_table;")}
	if x, e := Open(ctx, dir, bad); e == nil {
		x.Close()
		t.Fatal("failed 026 accepted")
	}
	s, e = Open(ctx, dir, migrationsThrough(t, 25))
	if e != nil {
		t.Fatal("rollback lost25", e)
	}
	s.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	after = oldColumnRows(t, s, columns)
	for table, rows := range before {
		if rows != after[table] {
			t.Fatalf("026 changed old columns: %s", table)
		}
	}
	session, e := s.Session(ctx, make([]byte, 32), 2)
	if e != nil || session == nil || session.AccountID != "original" {
		t.Fatal("session lost", session, e)
	}
	rows, e := s.Read.Query("PRAGMA foreign_key_check")
	if e != nil {
		t.Fatal(e)
	}
	if rows.Next() {
		t.Fatal("foreign key issue")
	}
	rows.Close()
	s.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal("repeat", e)
	}
	s.Close()
}
