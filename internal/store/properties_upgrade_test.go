package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/migrations"
)

func schemaFour(t *testing.T) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	for _, name := range []string{"001_foundation.sql", "002_metadata.sql", "003_organization.sql", "004_administration.sql"} {
		b, e := migrations.Files.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		out[name] = &fstest.MapFile{Data: b}
	}
	return out
}
func TestPropertyUpgradeCheckpointAndPreservation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := schemaFour(t)
	s, e := Open(ctx, dir, base)
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, s)
	if _, e = s.Write.Exec(`INSERT INTO field_overrides VALUES('book','language','value','"旧人工语言"');WITH RECURSIVE n(x) AS(SELECT 0 UNION ALL SELECT x+1 FROM n WHERE x<500) INSERT INTO books(id,library_id,relative_path,filename_title,size,mtime_ns,content_revision,page_count,file_state,indexed_at,revision) SELECT printf('p%04d',x),'lib',printf('%d.cbz',x),'[彩色] Anonymous',1,1,1,0,'missing',1,1 FROM n`); e != nil {
		t.Fatal(e)
	}
	s.Close()
	sql5, _ := migrations.Files.ReadFile("005_properties_dictionary.sql")
	bad := schemaFour(t)
	bad["005_properties_dictionary.sql"] = &fstest.MapFile{Data: append(append([]byte{}, sql5...), []byte(";INSERT INTO nonexistent VALUES(1);")...)}
	if db, e := Open(ctx, dir, bad); e == nil {
		db.Close()
		t.Fatal("broken migration succeeded")
	}
	s, e = Open(ctx, dir, base)
	if e != nil || s.Version != 4 {
		t.Fatal(e)
	}
	s.Close()
	interrupted := fstest.MapFS{}
	entries, _ := migrations.Files.ReadDir(".")
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") {
			raw, _ := migrations.Files.ReadFile(entry.Name())
			interrupted[entry.Name()] = &fstest.MapFile{Data: raw}
		}
	}
	sql7 := interrupted["007_work_edition_file.sql"].Data
	interrupted["007_work_edition_file.sql"] = &fstest.MapFile{Data: append(append([]byte{}, sql7...), []byte(`;CREATE TRIGGER interrupt_projection BEFORE INSERT ON effective_edition_properties WHEN NEW.edition_id='p0500' BEGIN SELECT RAISE(ABORT,'anonymous interruption');END;`)...)}
	if db, e := Open(ctx, dir, interrupted); e == nil {
		db.Close()
		t.Fatal("partial projection served")
	}
	db, e := sql.Open("sqlite", filepath.Join(dir, "app.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	var ready, count, version int
	var last string
	if e = db.QueryRow("SELECT ready,last_book_id FROM projection_state").Scan(&ready, &last); e != nil {
		t.Fatal(e)
	}
	_ = db.QueryRow("SELECT count(*) FROM effective_edition_properties").Scan(&count)
	_ = db.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&version)
	if ready != 0 || count != 500 || version != 27 || last == "" {
		t.Fatal(ready, count, version, last)
	}
	if _, e = db.Exec("DROP TRIGGER interrupt_projection"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	s, e = Open(ctx, dir, interrupted)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	d, e := s.MetadataDetails(ctx, "book")
	if e != nil || string(d.Properties.Languages.Value) != `["旧人工语言"]` {
		t.Fatal(d, e)
	}
	if e = s.Read.QueryRow("SELECT count(*) FROM effective_edition_properties").Scan(&count); e != nil || count != 502 {
		t.Fatal(count, e)
	}
	var raw string
	var revision int
	if e = s.Read.QueryRow("SELECT value_json FROM field_overrides WHERE edition_id='book' AND field='language'").Scan(&raw); e != nil || raw != `"旧人工语言"` {
		t.Fatal(raw, e)
	}
	if e = s.Read.QueryRow("SELECT revision FROM editions WHERE id='book'").Scan(&revision); e != nil || revision != 1 {
		t.Fatal(revision, e)
	}
	if e = s.PrepareProperties(ctx); e != nil {
		t.Fatal(e)
	}
}
func TestPropertySearchFilteringFoldingAndSavedConditions(t *testing.T) {
	s, o := orgFixture(t)
	ctx := context.Background()
	if e := s.ApplyOverrides(ctx, "book", 1, map[string]model.Override{"languages": {Op: "set", Value: json.RawMessage(`["english","japanese"]`)}, "translated": {Op: "set", Value: json.RawMessage(`false`)}}); e != nil {
		t.Fatal(e)
	}
	input := model.SearchInput{Scope: "all", Collapse: "none", Sort: "title", Order: "asc", Page: 1, PageSize: 24, Properties: &model.PropertyFilter{Languages: &model.LanguageFilter{AnyOf: []string{"english"}}, Translated: []string{"no"}}}
	out, e := s.Search(ctx, "a", input, nil)
	if e != nil || out.Total != 1 || out.Items[0].ID != "book" {
		t.Fatal(out, e)
	}
	group := changeWork(t, o, model.WorkCommand{Action: "work_merge", ID: "i:b2", WorkIDs: []string{"i:book"}})
	folded := input
	folded.Collapse = "versions"
	result, e := s.Search(ctx, "a", folded, nil)
	if e != nil || result.Total != 1 || result.Items[0].Kind != "work" || result.Items[0].ID != group.ID || result.Items[0].MatchCount != 1 {
		t.Fatal(result, e)
	}
	input.Admin = true
	input.Visibility = "active"
	admin, e := s.Search(ctx, "a", input, nil)
	if e != nil || admin.Total != 1 {
		t.Fatal(admin, e)
	}
	saved, e := s.SaveSearch(ctx, "a", "", "Properties", input, 0)
	if e != nil || saved.Conditions.Properties == nil {
		t.Fatal(saved, e)
	}
	input.Snapshot = out.Snapshot
	if e = s.ApplyOverrides(ctx, "book", 2, map[string]model.Override{"translated": {Op: "empty"}}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Search(ctx, "a", input, nil); !errors.Is(e, model.ErrSnapshot) {
		t.Fatal(e)
	}
	input.Snapshot = ""
	input.Properties.Translated = []string{"unknown"}
	out, e = s.Search(ctx, "a", input, nil)
	if e != nil || out.Total != 1 {
		t.Fatal(out, e)
	}
}

func TestPropertySharedSourceUnlinkAndPurge(t *testing.T) {
	s, _ := orgFixture(t)
	ctx := context.Background()
	title := "[彩色] Source"
	remote := model.RemoteMetadata{ExternalID: "shared", URL: "https://e-hentai.org/g/1/0123456789/", Method: "manual", Fields: model.Metadata{Title: &title, Tags: []model.TagName{{Namespace: "language", Name: "japanese"}}}, Raw: json.RawMessage(`{"anonymous":true}`)}
	publish := func(id string) {
		t.Helper()
		tx, e := s.Write.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		if e = publishSource(ctx, tx, id, remote); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	publish("book")
	publish("b2")
	b, _ := s.Edition(ctx, "b2")
	if e := s.ApplyOverrides(ctx, "b2", b.Revision, map[string]model.Override{"languages": {Op: "set", Value: json.RawMessage(`["人工语言"]`)}}); e != nil {
		t.Fatal(e)
	}
	remote.Fields.Tags = []model.TagName{{Namespace: "language", Name: "english"}}
	publish("book")
	for _, id := range []string{"book", "b2"} {
		d, e := s.MetadataDetails(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		expected := `["english"]`
		if id == "b2" {
			expected = `["人工语言"]`
		}
		if string(d.Properties.Languages.Value) != expected || string(d.Properties.ColorMode.Value) != `"color"` {
			t.Fatal(id, d)
		}
	}
	b, _ = s.Edition(ctx, "book")
	d, _ := s.MetadataDetails(ctx, "book")
	if e := s.RemoveSource(ctx, "book", b.Revision, d.SelectionRevision, false); e != nil {
		t.Fatal(e)
	}
	d, _ = s.MetadataDetails(ctx, "book")
	if d.Properties.Languages.State != "unknown" {
		t.Fatal(d)
	}
	for _, operation := range []string{"trash", "purge"} {
		b, _ = s.Edition(ctx, "book")
		p, e := s.CreateMaintenancePlan(ctx, model.MaintenanceInput{Operation: operation, BookIDs: []string{"book"}, Expected: map[string]int64{"book:book": b.Revision}})
		if e != nil {
			t.Fatal(e)
		}
		applyPlan(t, s, p)
	}
	var count int
	if e := s.Read.QueryRow("SELECT count(*) FROM effective_edition_properties WHERE edition_id='book'").Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
	d, e := s.MetadataDetails(ctx, "b2")
	if e != nil || d.Source == nil || string(d.Properties.Languages.Value) != `["人工语言"]` {
		t.Fatal(d, e)
	}
}
