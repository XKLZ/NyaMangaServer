package store

import (
	"context"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/migrations"
	"testing"
	"testing/fstest"
)

func seedBook(t *testing.T, s *Store) {
	t.Helper()
	if _, e := s.Write.Exec(`INSERT INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision) VALUES('lib','anonymous','Anonymous','utf8','test',1)`); e != nil {
		t.Fatal(e)
	}
	if s.Version < 7 {
		if _, e := s.Write.Exec(`INSERT INTO books(id,library_id,relative_path,filename_title,size,mtime_ns,content_revision,representative_sha1,page_count,file_state,error_code,indexed_at,revision) VALUES('book','lib','sample.cbz','Filename',1,1,1,'0123456789012345678901234567890123456789',2,'available','',1,1)`); e != nil {
			t.Fatal(e)
		}
		return
	}
	seedEdition(t, s, "book", "lib", "sample.cbz", "Filename")
}
func seedEdition(t *testing.T, s *Store, id, lib, path, title string) {
	t.Helper()
	hash := "0123456789012345678901234567890123456789"
	tx, e := s.Write.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	b := model.Edition{ID: id, LibraryID: lib, Revision: 1, File: model.File{Path: path, FilenameTitle: title, Size: 1, Mtime: 1, ContentRevision: 1, SHA1: &hash, PageCount: 2, State: "available", IndexedAt: 1}}
	if e = saveEditionIndex(context.Background(), tx, b); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}
func TestMetadataOwnershipAndProjection(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	seedBook(t, s)
	apply := func(title string) {
		t.Helper()
		tx, e := s.Write.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		tags := []model.TagName{{Namespace: "artist", Name: "anonymous"}}
		e = publishSource(ctx, tx, "book", model.RemoteMetadata{ExternalID: "123", URL: "https://e-hentai.org/g/123/0123456789/", Method: "manual", Fields: model.Metadata{Title: &title, Tags: tags}, Raw: json.RawMessage(`{"gid":123}`)})
		if e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	apply("Source title")
	b, _ := s.Edition(ctx, "book")
	if b.Title != "Source title" {
		t.Fatal(b)
	}
	if e := s.ApplyOverrides(ctx, "book", b.Revision, map[string]model.Override{"title": {Op: "set", Value: json.RawMessage(`"Manual title"`)}, "tags": {Op: "empty"}}); e != nil {
		t.Fatal(e)
	}
	apply("Refreshed source")
	d, e := s.MetadataDetails(ctx, "book")
	if e != nil || d.Fields.Title == nil || *d.Fields.Title != "Manual title" || len(d.Fields.Tags) != 0 || *d.Source.Fields.Title != "Refreshed source" {
		t.Fatal(d, e)
	}
	tags, e := s.Tags(ctx, "", "", "zh-CN")
	if e != nil || len(tags) != 1 {
		t.Fatal(tags, e)
	}
	if e = s.TranslateTag(ctx, tags[0].ID, "zh-CN", 0, ptr("人工译文")); e != nil {
		t.Fatal(e)
	}
	if e = s.TranslateTag(ctx, tags[0].ID, "zh-CN", 0, ptr("陈旧写入")); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if e = s.TranslateTag(ctx, tags[0].ID, "zh-CN", 1, nil); e != nil {
		t.Fatal(e)
	}
	tags, e = s.Tags(ctx, "", "", "zh-CN")
	if e != nil || tags[0].Origin != "original" || tags[0].Revision != 2 {
		t.Fatal(tags, e)
	}
	b, _ = s.Edition(ctx, "book")
	if e = s.ApplyOverrides(ctx, "book", b.Revision, map[string]model.Override{"title": {Op: "empty"}}); e != nil {
		t.Fatal(e)
	}
	b, _ = s.Edition(ctx, "book")
	if b.Title != "Filename" {
		t.Fatal("empty inherited source", b)
	}
	if e = s.ApplyOverrides(ctx, "book", b.Revision, map[string]model.Override{"title": {Op: "inherit"}, "tags": {Op: "inherit"}}); e != nil {
		t.Fatal(e)
	}
	d, e = s.MetadataDetails(ctx, "book")
	if e != nil || *d.Fields.Title != "Refreshed source" || len(d.Fields.Tags) != 1 {
		t.Fatal(d, e)
	}
	if _, e = s.Write.Exec("UPDATE files SET content_revision=2 WHERE edition_id='book'"); e != nil {
		t.Fatal(e)
	}
	d, e = s.MetadataDetails(ctx, "book")
	if e != nil || d.State != "needs_review" {
		t.Fatal(d, e)
	}
}
func ptr[T any](v T) *T { return &v }
func TestMetadataMigrationPreservesFoundation(t *testing.T) {
	ctx := context.Background()
	b, _ := migrations.Files.ReadFile("001_foundation.sql")
	base := fstest.MapFS{"001_foundation.sql": {Data: b}}
	dir := t.TempDir()
	s, e := Open(ctx, dir, base)
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, s)
	if e = s.Initialize(ctx, model.Account{ID: "admin", PasswordHash: "anonymous-hash", Revision: 1}, model.Session{Hash: make([]byte, 32), AccountID: "admin", Created: 1, Expires: 99}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec(`INSERT INTO jobs(id,library_id,state,stage,generation,total,created_at,updated_at) VALUES('scan','lib','running','processing',2,1,1,1); INSERT INTO job_items(job_id,ordinal,relative_path,size,mtime_ns,state) VALUES('scan',1,'sample.cbz',1,1,'pending')`); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	account, e := s.Account(ctx)
	if e != nil || account.ID != "admin" {
		t.Fatal(account, e)
	}
	j, e := s.Job(ctx, "scan")
	if e != nil || j.Type != "scan" || j.State != "running" || j.Generation != 2 || j.Total == nil || *j.Total != 1 {
		t.Fatal(j, e)
	}
	items, e := s.Items(ctx, j.ID, 1, 10, true)
	if e != nil || len(items) != 1 || items[0].Path != "sample.cbz" {
		t.Fatal(items, e)
	}
	var violations int
	rows, e := s.Read.Query("PRAGMA foreign_key_check")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	for rows.Next() {
		violations++
	}
	if violations != 0 {
		t.Fatal(violations)
	}
}

func TestOverridesProjectionFailureRollsBackBookAndEvents(t *testing.T) {
	s, _ := openTest(t)
	seedBook(t, s)
	ctx := context.Background()
	_, high, _, _ := s.Events(ctx, 0, 100)
	if _, e := s.Write.Exec(`CREATE TRIGGER fail_projection BEFORE INSERT ON effective_editions BEGIN SELECT RAISE(ABORT,'anonymous projection failure'); END`); e != nil {
		t.Fatal(e)
	}
	if e := s.ApplyOverrides(ctx, "book", 1, map[string]model.Override{"title": {Op: "set", Value: json.RawMessage(`"Should rollback"`)}}); e == nil {
		t.Fatal("projection failure ignored")
	}
	b, d, e := s.EditionWithMetadata(ctx, "book")
	if e != nil || b.Revision != 1 || b.Title != "Filename" || len(d.Overrides) != 0 {
		t.Fatal(b, d, e)
	}
	events, _, _, e := s.Events(ctx, high, 100)
	if e != nil || len(events) != 0 {
		t.Fatal("rolled back events leaked", events, e)
	}
}
func TestM3MigrationFailurePreservesPriorSchemaAndCheckpoint(t *testing.T) {
	ctx := context.Background()
	first, _ := migrations.Files.ReadFile("001_foundation.sql")
	second, _ := migrations.Files.ReadFile("002_metadata.sql")
	base := fstest.MapFS{"001_foundation.sql": {Data: first}}
	dir := t.TempDir()
	s, e := Open(ctx, dir, base)
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, s)
	if _, e = s.Write.Exec(`INSERT INTO jobs(id,library_id,state,stage,generation,total,created_at,updated_at) VALUES('scan','lib','running','processing',2,1,1,1);INSERT INTO job_items(job_id,ordinal,relative_path,size,mtime_ns,state) VALUES('scan',1,'sample.cbz',1,1,'pending')`); e != nil {
		t.Fatal(e)
	}
	s.Close()
	bad := fstest.MapFS{"001_foundation.sql": {Data: first}, "002_metadata.sql": {Data: append(second, []byte("\nINSERT INTO nonexistent VALUES(1);")...)}}
	if db, e := Open(ctx, dir, bad); e == nil {
		db.Close()
		t.Fatal("migration failure ignored")
	}
	s, e = Open(ctx, dir, base)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var state string
	var generation int
	var count int
	if e = s.Read.QueryRow("SELECT state,generation FROM jobs WHERE id='scan'").Scan(&state, &generation); e != nil || state != "running" || generation != 2 {
		t.Fatal(state, generation, e)
	}
	s.Read.QueryRow("SELECT count(*) FROM job_items WHERE job_id='scan'").Scan(&count)
	if count != 1 || s.Version != 1 {
		t.Fatal(count, s.Version)
	}
}

func TestSharedSourceRefreshAndSelectionRevisionGuard(t *testing.T) {
	s, _ := openTest(t)
	seedBook(t, s)
	ctx := context.Background()
	seedEdition(t, s, "second", "lib", "second.cbz", "Filename")
	publish := func(book, title string) {
		t.Helper()
		tx, e := s.Write.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		result := model.RemoteMetadata{ExternalID: "1", URL: "https://e-hentai.org/g/1/0123456789/", Method: "manual", Fields: model.Metadata{Title: &title, Tags: []model.TagName{}}, Raw: json.RawMessage(`{"gid":1}`)}
		if e = publishSource(ctx, tx, book, result); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	publish("book", "Source one")
	publish("second", "Source two")
	first, _ := s.Edition(ctx, "book")
	if first.Title != "Source two" {
		t.Fatal("shared projection stale", first)
	}
	if e := s.ApplyOverrides(ctx, "book", first.Revision, map[string]model.Override{"title": {Op: "set", Value: json.RawMessage(`"Manual"`)}}); e != nil {
		t.Fatal(e)
	}
	publish("second", "Source three")
	first, _ = s.Edition(ctx, "book")
	if first.Title != "Manual" {
		t.Fatal(first)
	}
	j, e := s.CreateMetadataJob(ctx, "stale-selection", "", []string{"book"}, map[string]model.MetadataInput{"book": {URL: "https://e-hentai.org/g/1/0123456789/"}})
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	items, _ := s.Items(ctx, j.ID, 1, 10, true)
	if _, e = s.Write.Exec("UPDATE edition_metadata_state SET selection_revision=selection_revision+1 WHERE edition_id='book'"); e != nil {
		t.Fatal(e)
	}
	title := "Stale source"
	if e = s.PublishMetadata(ctx, *j, items[0], &model.RemoteMetadata{ExternalID: "2", URL: "https://e-hentai.org/g/2/0123456789/", Method: "manual", Fields: model.Metadata{Title: &title, Tags: []model.TagName{}}, Raw: json.RawMessage(`{"gid":2}`)}, "", nil); e != nil {
		t.Fatal(e)
	}
	items, _ = s.Items(ctx, j.ID, 1, 10, false)
	if items[0].Error != "revision_conflict" {
		t.Fatal(items)
	}
	d, _ := s.MetadataDetails(ctx, "book")
	if d.Source.ExternalID != "1" {
		t.Fatal("stale source published")
	}
}
