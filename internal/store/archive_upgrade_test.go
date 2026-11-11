package store

import (
	"context"
	"nyamangaserver.local/server/migrations"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestArchiveUpgradePreservesSelectionAndRollsBack(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	files := schemaFour(t)
	fifth, _ := migrations.Files.ReadFile("005_properties_dictionary.sql")
	files["005_properties_dictionary.sql"] = &fstest.MapFile{Data: fifth}
	s, e := Open(ctx, dir, files)
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, s)
	if _, e = s.Write.Exec(`INSERT INTO book_pages VALUES('book',1,1,7,'a.png','image/png');INSERT INTO cover_selections(book_id,mode,page_index,content_revision,revision) VALUES('book','page',1,1,3);INSERT INTO version_groups VALUES('g','i:book','lib',1);INSERT INTO version_members VALUES('g','book',1,'中文,彩色',1)`); e != nil {
		t.Fatal(e)
	}
	s.Close()
	sixth, _ := migrations.Files.ReadFile("006_archive_manifest.sql")
	bad := fstest.MapFS{}
	for k, v := range files {
		bad[k] = v
	}
	bad["006_archive_manifest.sql"] = &fstest.MapFile{Data: append(append([]byte{}, sixth...), []byte(";INSERT INTO no_such_table VALUES(1);")...)}
	if db, e := Open(ctx, dir, bad); e == nil {
		db.Close()
		t.Fatal("bad migration accepted")
	}
	s, e = Open(ctx, dir, files)
	if e != nil {
		t.Fatal(e)
	}
	var n int
	if e = s.Read.QueryRow("SELECT count(*) FROM book_pages").Scan(&n); e != nil || n != 1 {
		t.Fatal("page table not rolled back", n, e)
	}
	s.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	selection, e := s.CoverSelection(ctx, "book")
	if e != nil || selection.EntryIndex == nil || *selection.EntryIndex != 7 || selection.EntryName == nil || *selection.EntryName != "a.png" || selection.Revision != 3 {
		t.Fatal(selection, e)
	}
	if e = s.Read.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='book_pages'").Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	var label string
	if e = s.Read.QueryRow("SELECT label FROM edition_version_labels WHERE edition_id='book'").Scan(&label); e != nil || label != "中文,彩色" {
		t.Fatal(label, e)
	}
	b, e := s.Edition(ctx, "book")
	if e != nil || b.RepresentativeAlgorithm != "first-page-v1" {
		t.Fatal(b, e)
	}
	snapshots, _ := filepath.Glob(filepath.Join(dir, "upgrade-snapshots", "*.sqlite"))
	if len(snapshots) == 0 {
		t.Fatal("missing snapshot")
	}
	s.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
}
