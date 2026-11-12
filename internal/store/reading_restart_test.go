package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/fstest"

	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/migrations"
)

func TestReadingRestartPreservesHistoryAndChecksRevisions(t *testing.T) {
	s, actors := aclFixture(t)
	ctx := actors["viewer"]
	start := model.ReadingSessionStart{Expected: 0, ContentRevision: 1, IdempotencyKey: "initial-session"}
	first, e := s.StartReadingSession(ctx, "a", start)
	if e != nil {
		t.Fatal(e)
	}
	complete := model.ReadingWrite{Expected: first.State.Revision, ContentRevision: 1, IdempotencyKey: "mark-completed", Action: "complete"}
	completed, e := s.WriteReadingState(ctx, "a", complete)
	if e != nil || !completed.Completed {
		t.Fatal(completed, e)
	}
	start.Expected, start.IdempotencyKey = completed.Revision, "open-completed"
	opened, e := s.StartReadingSession(ctx, "a", start)
	if e != nil || !opened.State.Completed || opened.State.ReadCount != 2 {
		t.Fatal(opened, e)
	}
	page := 1
	progress := model.ReadingWrite{Expected: opened.State.Revision, ContentRevision: 1, IdempotencyKey: "flip-completed", Action: "progress", PageIndex: &page}
	flipped, e := s.WriteReadingState(ctx, "a", progress)
	if e != nil || !flipped.Completed || flipped.PageIndex == nil || *flipped.PageIndex != 1 {
		t.Fatal(flipped, e)
	}
	start.Intent, start.Expected, start.IdempotencyKey = model.ReadingRestart, opened.State.Revision, "restart-conflict"
	if _, e = s.StartReadingSession(ctx, "a", start); !errors.Is(e, model.ErrReadingRevision) {
		t.Fatal("restart bypassed CAS", e)
	}
	start.Expected, start.IdempotencyKey = flipped.Revision, "restart-explicit"
	restarted, e := s.StartReadingSession(ctx, "a", start)
	if e != nil || restarted.State.Completed || restarted.State.CompletedAt != nil || restarted.State.ReadCount != 3 ||
		restarted.State.PageIndex == nil || *restarted.State.PageIndex != 0 {
		t.Fatal(restarted, e)
	}
	repeated, e := s.StartReadingSession(ctx, "a", start)
	if e != nil || !reflect.DeepEqual(repeated, restarted) {
		t.Fatal(repeated, e)
	}
	start.Intent = model.ReadingStart
	if _, e = s.StartReadingSession(ctx, "a", start); !errors.Is(e, model.ErrReadingIdempotency) {
		t.Fatal("idempotency allowed intent change", e)
	}
	if _, e = s.Write.Exec("UPDATE files SET parsing_revision=2 WHERE edition_id='a'"); e != nil {
		t.Fatal(e)
	}
	start.Intent, start.Expected, start.ParsingRevision, start.IdempotencyKey = model.ReadingRestart, restarted.State.Revision, 2, "restart-parsing"
	if _, e = s.StartReadingSession(ctx, "a", start); !errors.Is(e, model.ErrReadingConfirmation) {
		t.Fatal("restart bypassed parsing confirmation", e)
	}
	if _, e = s.Write.Exec("UPDATE files SET content_revision=2,page_count=0 WHERE edition_id='a'"); e != nil {
		t.Fatal(e)
	}
	start.AcceptParsingChange, start.ContentRevision = true, 2
	if _, e = s.StartReadingSession(ctx, "a", start); !errors.Is(e, model.ErrReadingConfirmation) {
		t.Fatal("restart bypassed content confirmation", e)
	}
	start.AcceptContentChange = true
	if _, e = s.StartReadingSession(ctx, "a", start); !errors.Is(e, model.ErrReadingUnknownPages) {
		t.Fatal("restart accepted unknown local page count", e)
	}
	v, e := s.ReadingState(ctx, "a")
	if e != nil || v.ReadCount != restarted.State.ReadCount || v.Revision != restarted.State.Revision || !v.Stale {
		t.Fatal(v, e)
	}
}

func TestReadingMigration29PreservesSessionReplayAndRollsBack(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	old := migrationsThrough(t, 28)
	s, e := Open(ctx, dir, old)
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, s)
	if _, e = s.Write.Exec("INSERT INTO accounts VALUES(1,'fixture-reader','anonymous',1,1)"); e != nil {
		t.Fatal(e)
	}
	a, e := s.Account(ctx)
	if e != nil || a == nil {
		t.Fatal(a, e)
	}
	reader := model.WithActor(ctx, *a)
	req := model.ReadingSessionStart{Expected: 0, ContentRevision: 1, IdempotencyKey: "fixture-old-key"}
	before, e := s.StartReadingSession(reader, "book", req)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()

	bad := migrationsThrough(t, 29)
	const file = "029_reading_session_intent.sql"
	raw, e := migrations.Files.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	bad[file] = &fstest.MapFile{Data: append(raw, []byte("\nINSERT INTO deliberately_missing_table VALUES(1);")...)}
	if broken, e := Open(ctx, dir, bad); e == nil {
		broken.Close()
		t.Fatal("broken migration committed")
	}
	check := func(files fstest.MapFS, version int) {
		t.Helper()
		s, e := Open(ctx, dir, files)
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		var n int
		e = s.Read.QueryRow("SELECT count(*) FROM pragma_table_info('reading_sessions') WHERE name='intent'").Scan(&n)
		if e != nil || s.Version != version || n != version-28 {
			t.Fatal(s.Version, n, e)
		}
		replay, e := s.StartReadingSession(reader, "book", req)
		if e != nil || !reflect.DeepEqual(replay, before) {
			t.Fatal("old idempotent session changed", replay, before, e)
		}
	}
	check(old, 28)
	check(migrationsThrough(t, 29), 29)
}
