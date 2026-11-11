package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/migrations"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func schemaSixteen(t *testing.T) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	entries, e := migrations.Files.ReadDir(".")
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") && entry.Name() < "017" {
			b, _ := migrations.Files.ReadFile(entry.Name())
			out[entry.Name()] = &fstest.MapFile{Data: b}
		}
	}
	return out
}
func TestTagCatalogMigrationRollbackAndProjectionResume(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := schemaSixteen(t)
	old, e := Open(ctx, dir, base)
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, old)
	// The seed fixture plus 500 additional Editions crosses the exact batch boundary.
	tx, e := old.Write.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 501; n++ {
		id := fmt.Sprintf("e%04d", n)
		for _, query := range []string{`INSERT INTO works(id,library_id,preferred_edition_id,revision) VALUES('i:'||?,'lib',?,1)`, `INSERT INTO editions(id,library_id,work_id,revision,organization_revision) VALUES(?,'lib','i:'||?,1,1)`} {
			if _, e = tx.ExecContext(ctx, query, id, id); e != nil {
				t.Fatal(e)
			}
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO files(edition_id,library_id,relative_path,filename_title,size,mtime_ns,content_revision,representative_algorithm,page_count,file_state,indexed_at) VALUES(?,'lib',?,'Anonymous',1,1,1,'content-page8-v1',0,'missing',1)`, id, id+".cbz"); e != nil {
			t.Fatal(e)
		}
	}
	p, c := tagID("parody", "alpha"), tagID("character", "hero")
	if _, e = tx.ExecContext(ctx, "INSERT INTO tags VALUES(?,'parody','alpha'),(?,'character','hero')", p, c); e != nil {
		t.Fatal(e)
	}
	for _, tag := range []string{p, c} {
		if _, e = tx.ExecContext(ctx, `INSERT INTO effective_edition_tags SELECT id,? FROM editions WHERE id LIKE 'e%'`, tag); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}

	// Populate representative authentication, source, manual value and paused task
	// rows; migration/backfill must leave every original table byte-value intact.
	if e = old.Initialize(ctx, Account{ID: "anonymous-account", PasswordHash: "anonymous-hash", Revision: 1}, Session{Hash: make([]byte, 32), AccountID: "anonymous-account", Created: 1, Expires: 2}); e != nil {
		t.Fatal(e)
	}
	sourceTx, e := old.Write.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = publishSource(ctx, sourceTx, "book", model.RemoteMetadata{ExternalID: "anonymous", URL: "https://e-hentai.org/g/1/0123456789/", Method: "manual", Raw: json.RawMessage(`{}`), Fields: model.Metadata{Tags: []model.TagName{}}}); e != nil {
		t.Fatal(e)
	}
	if e = sourceTx.Commit(); e != nil {
		t.Fatal(e)
	}
	book, _ := old.Edition(ctx, "book")
	if e = old.ApplyOverrides(ctx, "book", book.Revision, map[string]model.Override{"title": {Op: "set", Value: json.RawMessage(`"Anonymous manual"`)}}); e != nil {
		t.Fatal(e)
	}
	if _, e = old.CreateMetadataJob(ctx, "preserved-task", "", []string{"book"}, nil); e != nil {
		t.Fatal(e)
	}
	if e = old.ControlJob(ctx, "preserved-task", "pause", "user_paused"); e != nil {
		t.Fatal(e)
	}
	original := map[string][][]any{}
	tableRows, e := old.Read.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name!='schema_migrations' ORDER BY name")
	if e != nil {
		t.Fatal(e)
	}
	names := []string{}
	for tableRows.Next() {
		var name string
		if e = tableRows.Scan(&name); e != nil {
			t.Fatal(e)
		}
		names = append(names, name)
	}
	tableRows.Close()
	for _, name := range names {
		original[name] = dumpRows(t, old, fmt.Sprintf("SELECT * FROM %q ORDER BY rowid", name))
	}
	old.Close()
	raw, _ := migrations.Files.ReadFile("017_tag_catalog.sql")
	bad := schemaSixteen(t)
	bad["017_tag_catalog.sql"] = &fstest.MapFile{Data: append(append([]byte{}, raw...), []byte("INSERT INTO missing_table VALUES(1);")...)}
	if db, e := Open(ctx, dir, bad); e == nil {
		db.Close()
		t.Fatal("failed migration accepted")
	}
	old, e = Open(ctx, dir, base)
	if e != nil || old.Version != 16 {
		t.Fatal("ledger advanced on rollback", e)
	}
	for _, name := range names {
		if !reflect.DeepEqual(original[name], dumpRows(t, old, fmt.Sprintf("SELECT * FROM %q ORDER BY rowid", name))) {
			t.Fatal("rollback changed original table", name)
		}
	}
	var n int
	if e = old.Read.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='tag_character_evidence'").Scan(&n); e != nil || n != 0 {
		t.Fatal("migration did not roll back", e)
	}
	old.Close()
	interrupted := schemaSixteen(t)
	interrupted["017_tag_catalog.sql"] = &fstest.MapFile{Data: append(append([]byte{}, raw...), []byte(`CREATE TRIGGER stop_evidence BEFORE INSERT ON tag_character_evidence WHEN NEW.edition_id='e0500' BEGIN SELECT RAISE(ABORT,'anonymous interruption');END;`)...)}
	if db, e := Open(ctx, dir, interrupted); e == nil {
		db.Close()
		t.Fatal("partial projection served")
	}
	db, e := sql.Open("sqlite", filepath.Join(dir, "app.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	var ready int
	var last string
	if e = db.QueryRow("SELECT ready,last_edition_id FROM tag_relation_projection").Scan(&ready, &last); e != nil || ready != 0 || last != "e0498" {
		t.Fatal("checkpoint mismatch", ready, last, e)
	}
	// 'book' sorts before e0000, so the first 500 batch has 499 evidence rows.
	if e = db.QueryRow("SELECT count(*) FROM tag_character_evidence").Scan(&n); e != nil || n != 499 {
		t.Fatal("partial batch committed", n, e)
	}
	if _, e = db.Exec("DROP TRIGGER stop_evidence"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	s, e := Open(ctx, dir, interrupted)
	if e != nil {
		t.Fatal("resume", e)
	}
	defer s.Close()
	if e = s.Read.QueryRow("SELECT count(*) FROM tag_character_evidence").Scan(&n); e != nil || n != 501 {
		t.Fatal(n, e)
	}
	if e = s.Read.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	for _, name := range names {
		if !reflect.DeepEqual(original[name], dumpRows(t, s, fmt.Sprintf("SELECT * FROM %q ORDER BY rowid", name))) {
			t.Fatal("upgrade/backfill changed original table", name)
		}
	}
	before := dumpRows(t, s, "SELECT * FROM tag_character_evidence ORDER BY edition_id")
	s.Close()
	s, e = Open(ctx, dir, interrupted)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	after := dumpRows(t, s, "SELECT * FROM tag_character_evidence ORDER BY edition_id")
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatal("repeat startup rewrote evidence")
	}
	snapshots, _ := filepath.Glob(filepath.Join(dir, "upgrade-snapshots", "*.sqlite"))
	if len(snapshots) == 0 {
		t.Fatal("missing upgrade snapshot")
	}
}
