package store

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/testfixture"
	"nyamangaserver.local/server/migrations"
	"reflect"
	"testing"
	"testing/fstest"
)

func TestParsingChangeProtectsReadingAndPageCover(t *testing.T) {
	s, ctx := aclFixture(t)
	viewer := ctx["viewer"]
	page := 1
	state, e := s.WriteReadingState(viewer, "a", model.ReadingWrite{ContentRevision: 1, IdempotencyKey: "old-page", Action: "progress", PageIndex: &page})
	if e != nil {
		t.Fatal(e)
	}
	entry := 0
	name := "old.png"
	cover, e := s.SetCover(ctx["editor"], "a", 0, model.CoverSelection{Mode: "page", PageIndex: &page, EntryIndex: &entry, EntryName: &name, DirectoryVersion: "natural-bmp-v2", ContentRevision: 1}, nil)
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Edition(ctx["editor"], "a")
	if e != nil {
		t.Fatal(e)
	}
	encoding := "shift_jis"
	changed, e := s.SetFileEncoding(ctx["editor"], "a", model.EncodingUpdate{Expected: b.Revision, ContentRevision: 1, ParsingRevision: 1, Encoding: &encoding, DirectoryDigest: "new-order"}, model.FileParsing{ParsingRevision: 2, ParserVersion: "natural-bmp-macos-v3", EffectiveEncoding: encoding, DirectoryDigest: "new-order"}, 2)
	if e != nil {
		t.Fatal(e)
	}
	if changed.ContentRevision != 1 || changed.ParsingRevision != 2 {
		t.Fatal(changed)
	}
	stale, e := s.ReadingState(viewer, "a")
	if e != nil || !stale.ParsingStale || stale.PageIndex == nil || *stale.PageIndex != page || stale.Revision != state.Revision {
		t.Fatal(stale, e)
	}
	selection, e := s.CoverSelection(ctx["editor"], "a")
	if e != nil || selection.State != "parsing_changed" || selection.Revision != cover.Revision || selection.PageIndex == nil {
		t.Fatal(selection, e)
	}
	if _, e = s.WriteReadingState(viewer, "a", model.ReadingWrite{Expected: state.Revision, ContentRevision: 1, ParsingRevision: 2, IdempotencyKey: "unconfirmed", Action: "heartbeat"}); !errors.Is(e, model.ErrReadingConfirmation) {
		t.Fatal(e)
	}
	confirmed, e := s.WriteReadingState(viewer, "a", model.ReadingWrite{Expected: state.Revision, ContentRevision: 1, ParsingRevision: 2, AcceptParsingChange: true, IdempotencyKey: "confirmed", Action: "heartbeat"})
	if e != nil || confirmed.PageIndex != nil || confirmed.ParsingStale || confirmed.CurrentContentRevision != 1 {
		t.Fatal(confirmed, e)
	}
	// An old device cannot acknowledge a new parsing identity using only content CAS.
	if _, e = s.WriteReadingState(viewer, "a", model.ReadingWrite{Expected: confirmed.Revision, ContentRevision: 1, IdempotencyKey: "old-device", Action: "progress", PageIndex: &page}); !errors.Is(e, model.ErrReadingContent) {
		t.Fatal(e)
	}
}
func TestScanStorageMigrationPreserves26RollbackAndReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := Open(ctx, dir, migrationsThrough(t, 26))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec(`INSERT INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision) VALUES('lib','lib','anonymous','utf8','anonymous',1)`); e != nil {
		t.Fatal(e)
	}
	if e = testfixture.SeedEdition(ctx, s.Write, "book", "lib", "book.cbz", "anonymous"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec(`INSERT INTO jobs(id,library_id,state,stage,generation,created_at,updated_at) VALUES('old','lib','failed','finished',1,1,1);INSERT INTO job_items(job_id,ordinal,relative_path,size,mtime_ns,state,error_code) VALUES('old',1,'book.cbz',1,1,'failed','lookup_resource_limit')`); e != nil {
		t.Fatal(e)
	}
	cols := schemaColumns(t, s)
	cols["instance_state"] = []string{"singleton", "owner", "background_hold", "revision"}
	before := oldColumnRows(t, s, cols)
	s.Close()
	broken := migrationsThrough(t, 27)
	for name, file := range broken {
		if name[:3] == "027" {
			broken[name] = &fstest.MapFile{Data: append(append([]byte{}, file.Data...), []byte("\nSELECT missing_migration_function();")...)}
		}
	}
	if bad, e := Open(ctx, dir, broken); e == nil {
		bad.Close()
		t.Fatal("broken migration committed")
	}
	s, e = Open(ctx, dir, migrationsThrough(t, 26))
	if e != nil {
		t.Fatal(e)
	}
	if after := oldColumnRows(t, s, cols); !reflect.DeepEqual(before, after) {
		t.Fatal("rollback changed old rows")
	}
	s.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	if after := oldColumnRows(t, s, cols); !reflect.DeepEqual(before, after) {
		for k, v := range before {
			if after[k] != v {
				t.Errorf("changed %s: %s => %s", k, v, after[k])
			}
		}
	}
	s.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.Version != 30 {
		t.Fatal(s.Version)
	}
	var foreign int
	if e = s.Read.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&foreign); e != nil || foreign != 0 {
		t.Fatal(foreign, e)
	}
	items, e := s.Items(ctx, "old", 1, 10, false)
	if e != nil || len(items) != 1 || items[0].Error != "lookup_resource_limit" || items[0].ScanResult != nil {
		t.Fatal(items, e)
	}
}

func TestEquivalentEncodingKeepsReadingButRejectsOldScanPublisher(t *testing.T) {
	s, ctx := aclFixture(t)
	page := 1
	state, e := s.WriteReadingState(ctx["viewer"], "a", model.ReadingWrite{ContentRevision: 1, IdempotencyKey: "equivalent-page", Action: "progress", PageIndex: &page})
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Edition(ctx["editor"], "a")
	if e != nil {
		t.Fatal(e)
	}
	frozen := b.FileParsing.Fingerprint()
	encoding := "gb18030"
	changed, e := s.SetFileEncoding(ctx["editor"], "a", model.EncodingUpdate{Expected: b.Revision, ContentRevision: 1, ParsingRevision: 1, Encoding: &encoding, DirectoryDigest: "same-order"}, model.FileParsing{ParsingRevision: 1, ParserVersion: "natural-bmp-macos-v3", DirectoryDigest: "same-order", EffectiveEncoding: encoding}, 2)
	if e != nil {
		t.Fatal(e)
	}
	current, e := s.ReadingState(ctx["viewer"], "a")
	if e != nil || current.Stale || current.ParsingStale || current.PageIndex == nil || *current.PageIndex != page || current.Revision != state.Revision {
		t.Fatal(current, e)
	}
	duplicate, e := s.SetFileEncoding(ctx["editor"], "a", model.EncodingUpdate{Expected: changed.Revision, ContentRevision: 1, ParsingRevision: 1, Encoding: &encoding, DirectoryDigest: "same-order"}, changed.FileParsing, 2)
	if e != nil || duplicate.Revision != changed.Revision {
		t.Fatal(duplicate, e)
	}
	if _, e = s.Write.Exec(`INSERT INTO jobs(id,library_id,type,state,stage,generation,created_at,updated_at) VALUES('stale-scan','a','scan','running','processing',1,1,1);INSERT INTO job_items(job_id,ordinal,relative_path,size,mtime_ns,state,error_code) VALUES('stale-scan',1,'a.zip',1,1,'pending','')`); e != nil {
		t.Fatal(e)
	}
	b.ScanResult = &model.ScanResult{InputParsingRevision: 1, InputParsingFingerprint: frozen}
	if e = s.Publish(context.Background(), model.Job{ID: "stale-scan", LibraryID: "a", Generation: 1}, model.Item{JobID: "stale-scan", Ordinal: 1, Path: "a.zip"}, *b); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal("old equivalent-encoding worker published", e)
	}
	after, _ := s.Edition(ctx["editor"], "a")
	if after.EffectiveEncoding != encoding {
		t.Fatal(after)
	}
}
