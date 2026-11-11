package store

import (
	"context"
	"nyamangaserver.local/server/internal/testfixture"
	"nyamangaserver.local/server/migrations"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
)

func TestSourceSearchMigrationPreservesAllOldRowsAndRollback(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := Open(ctx, dir, schemaUntil(t, 15))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("INSERT INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision) VALUES('lib','lib','anonymous','utf8','identity',1);INSERT INTO accounts VALUES(1,'account','anonymous',1,1)"); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"e0000", "e0001"} {
		if e = testfixture.SeedEdition(ctx, s.Write, id, "lib", id+".cbz", id); e != nil {
			t.Fatal(e)
		}
	}
	j, _, _ := prepareScopedSeries(t, s, "old-task", "old-batch", [][]int{{0, 1}})
	if _, e = s.Write.Exec("UPDATE jobs SET state='paused' WHERE id=?", j.ID); e != nil {
		t.Fatal(e)
	}
	before := map[string][][]any{}
	rows, e := s.Read.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name!='schema_migrations'")
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
	// Compare every old table, including events affected by recreated triggers.
	for _, n := range names {
		before[n] = dumpRows(t, s, preservedTableQuery(n))
	}
	s.Close()
	broken := schemaUntil(t, 16)
	raw, _ := migrations.Files.ReadFile("016_source_searches.sql")
	broken["016_source_searches.sql"] = &fstest.MapFile{Data: append(raw, []byte("\nINSERT INTO no_table VALUES(1);")...)}
	if bad, e := Open(ctx, dir, broken); e == nil {
		bad.Close()
		t.Fatal("broken migration committed")
	}
	s, e = Open(ctx, dir, schemaUntil(t, 15))
	if e != nil {
		t.Fatal("rollback", e)
	}
	for _, n := range names {
		if !reflect.DeepEqual(before[n], dumpRows(t, s, preservedTableQuery(n))) {
			t.Fatal("rollback changed", n)
		}
	}
	s.Close()
	s, e = Open(ctx, dir, migrationsThrough(t, 24))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, n := range names {
		if !reflect.DeepEqual(before[n], dumpRows(t, s, preservedTableQuery(n))) {
			t.Fatal("upgrade changed", n)
		}
	}
	var n int
	if e = s.Read.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	snapshots, _ := filepath.Glob(filepath.Join(dir, "upgrade-snapshots", "*"))
	if len(snapshots) == 0 {
		t.Fatal("missing snapshot")
	}
	s.Close()
	s, e = Open(ctx, dir, migrationsThrough(t, 24))
	if e != nil || s.Version != 24 {
		t.Fatal(e)
	}
}
