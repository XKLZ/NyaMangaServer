package store

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/migrations"
	"reflect"
	"testing"
	"testing/fstest"
)

func schemaSeventeen(t *testing.T) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	names, e := fs.ReadDir(migrations.Files, ".")
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range names {
		if n.Name() >= "018" {
			continue
		}
		raw, e := fs.ReadFile(migrations.Files, n.Name())
		if e != nil {
			t.Fatal(e)
		}
		out[n.Name()] = &fstest.MapFile{Data: raw}
	}
	return out
}
func TestTagImagesMigrationPreservesSeventeenAndRollback(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	old, e := Open(ctx, dir, schemaSeventeen(t))
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, old)
	setTagNames(t, old, "book", "parody", "anonymous", "character", "hero")
	if e = old.Initialize(ctx, Account{ID: "anonymous-account", PasswordHash: "anonymous-hash", Revision: 1}, Session{Hash: make([]byte, 32), AccountID: "anonymous-account", Created: 1, Expires: 2}); e != nil {
		t.Fatal(e)
	}
	tx, e := old.Write.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	title := "Anonymous source"
	if e = publishSource(ctx, tx, "book", model.RemoteMetadata{ExternalID: "anonymous", URL: "https://e-hentai.org/g/1/0123456789/", Method: "manual", Raw: json.RawMessage(`{}`), Fields: model.Metadata{Title: &title, Tags: []model.TagName{{Namespace: "parody", Name: "anonymous"}, {Namespace: "character", Name: "hero"}}}}); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	b, _ := old.Edition(ctx, "book")
	if e = old.ApplyOverrides(ctx, "book", b.Revision, map[string]model.Override{"title": {Op: "set", Value: json.RawMessage(`"Anonymous manual"`)}}); e != nil {
		t.Fatal(e)
	}
	translation := "Anonymous translation"
	if e = old.TranslateTag(ctx, tagID("parody", "anonymous"), "zh-CN", 0, &translation); e != nil {
		t.Fatal(e)
	}
	if _, e = old.DecideCharacterRelation(ctx, tagID("parody", "anonymous"), tagID("character", "hero"), 0, "confirmed"); e != nil {
		t.Fatal(e)
	}
	if _, e = old.SetCover(ctx, "book", 0, model.CoverSelection{Mode: "asset"}, &model.Asset{ID: "preserved-asset", Key: "assets/covers/ab/anonymous.png", Digest: "anonymous", Size: 4}); e != nil {
		t.Fatal(e)
	}
	if _, e = old.CreateMetadataJob(ctx, "preserved-task", "", []string{"book"}, nil); e != nil {
		t.Fatal(e)
	}
	if e = old.ControlJob(ctx, "preserved-task", "pause", "user_paused"); e != nil {
		t.Fatal(e)
	}
	ledger := dumpRows(t, old, "SELECT * FROM schema_migrations ORDER BY version")

	tables, e := old.Read.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name!='schema_migrations' ORDER BY name")
	if e != nil {
		t.Fatal(e)
	}
	names := []string{}
	for tables.Next() {
		var n string
		tables.Scan(&n)
		names = append(names, n)
	}
	tables.Close()
	before := map[string][][]any{}
	for _, n := range names {
		before[n] = dumpRows(t, old, preservedTableQuery(n))
	}
	old.Close()
	raw, _ := migrations.Files.ReadFile("018_tag_images.sql")
	bad := schemaSeventeen(t)
	bad["018_tag_images.sql"] = &fstest.MapFile{Data: append(append([]byte{}, raw...), []byte("INSERT INTO absent_table VALUES(1);")...)}
	if s, e := Open(ctx, dir, bad); e == nil {
		s.Close()
		t.Fatal("failed migration accepted")
	}
	s, e := Open(ctx, dir, schemaSeventeen(t))
	if e != nil || s.Version != 17 {
		t.Fatal(e)
	}
	s.Close()
	for repeat := 0; repeat < 2; repeat++ {
		s, e = Open(ctx, dir, migrationsThrough(t, 24))
		if e != nil || s.Version != 24 {
			t.Fatal(e)
		}
		for _, n := range names {
			if !reflect.DeepEqual(before[n], dumpRows(t, s, preservedTableQuery(n))) {
				t.Fatal("old data changed", n)
			}
		}
		if !reflect.DeepEqual(ledger, dumpRows(t, s, "SELECT * FROM schema_migrations WHERE version<=17 ORDER BY version")) {
			t.Fatal("old migration ledger changed")
		}
		var fk int
		s.Read.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&fk)
		if fk != 0 {
			t.Fatal(fk)
		}
		s.Close()
	}
}
func TestTagImagesIndependentRolesCASAndAssetProtection(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	tagLibraries(t, s)
	tagEdition(t, s, "a", "a", "parody", "shared")
	tagEdition(t, s, "b", "b", "parody", "shared")
	id := tagID("parody", "shared")
	initial, e := s.TagImages(ctx, id)
	if e != nil || initial.Foreground.Revision != 0 || initial.Background.AssetID != nil {
		t.Fatal(initial, e)
	}
	asset := func(id string) model.TagImage {
		return model.TagImage{Width: 2, Height: 3, Format: "png", Asset: &model.Asset{ID: id, Key: "assets/tags/ab/" + id + ".png", Digest: "anonymous", Size: 20}}
	}
	fg, e := s.SetTagImage(ctx, id, "foreground", 0, asset("fg"))
	if e != nil {
		t.Fatal(e)
	}
	bg, e := s.SetTagImage(ctx, id, "background", 0, asset("bg"))
	if e != nil || bg.Revision != 1 {
		t.Fatal(bg, e)
	}
	if _, e = s.SetTagImage(ctx, id, "foreground", 0, asset("stale")); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	keys, e := s.AssetKeys(ctx)
	if e != nil || len(keys) != 2 {
		t.Fatal(keys, e)
	}
	cleared, e := s.SetTagImage(ctx, id, "foreground", fg.Revision, model.TagImage{})
	if e != nil || cleared.Revision != 2 || cleared.AssetID != nil {
		t.Fatal(cleared, e)
	}
	if _, e = s.SetTagImage(ctx, id, "foreground", 0, asset("aba")); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	// A failed transaction must not add the asset or alter the old background.
	if _, e = s.Write.Exec(`CREATE TRIGGER fail_tag_image BEFORE UPDATE ON tag_images WHEN NEW.role='background' BEGIN SELECT RAISE(ABORT,'anonymous failure');END;`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetTagImage(ctx, id, "background", 1, asset("failed")); e == nil {
		t.Fatal("commit failure accepted")
	}
	after, e := s.TagImages(ctx, id)
	if e != nil || after.Background.Revision != 1 || *after.Background.AssetID != "bg" {
		t.Fatal(after, e)
	}
	var count int
	s.Read.QueryRow("SELECT count(*) FROM assets WHERE id='failed'").Scan(&count)
	if count != 0 {
		t.Fatal("orphan db row")
	}
	// Disabling a library never changes the global tag image read identity.
	s.Write.Exec("UPDATE libraries SET disabled=1")
	shared, e := s.TagImages(ctx, id)
	if e != nil || !reflect.DeepEqual(after, shared) {
		t.Fatal(shared, e)
	}
	tagEdition(t, s, "other", "a", "artist", "author")
	if _, e = s.TagImages(ctx, tagID("artist", "author")); !errors.Is(e, model.ErrInput) {
		t.Fatal(e)
	}
}

func TestTagImagesSnapshotExactAtGenerationBoundary(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	tagLibraries(t, s)
	tagEdition(t, s, "one", "a", "parody", "alpha")
	id := tagID("parody", "alpha")
	if _, e := s.SetTagImage(ctx, id, "foreground", 0, model.TagImage{}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SetTagImage(ctx, id, "background", 0, model.TagImage{}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Write.Exec("UPDATE tag_images SET revision=? WHERE role='foreground'", int64(math.MaxInt64-1)); e != nil {
		t.Fatal(e)
	}
	before, e := s.TagCatalog(ctx, tagInput("a"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e := s.Write.Exec("UPDATE tag_images SET revision=? WHERE role='foreground'", int64(math.MaxInt64)); e != nil {
		t.Fatal(e)
	}
	after, e := s.TagCatalog(ctx, tagInput("a"))
	if e != nil || before.Snapshot == after.Snapshot {
		t.Fatal("image generation fingerprint overflow", after, e)
	}
}
