package store

import (
	"context"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/migrations"
	"sync"
	"testing"
)

func TestReadingSessionsIdempotencyCASBackwardsCompletionAndReset(t *testing.T) {
	s, ctx := aclFixture(t)
	viewer := ctx["viewer"]
	initial, e := s.ReadingState(viewer, "a")
	if e != nil || initial.Revision != 0 || initial.ReadCount != 0 {
		t.Fatal(initial, e)
	}
	start := model.ReadingSessionStart{Expected: 0, ContentRevision: 1, IdempotencyKey: "session-first"}
	first, e := s.StartReadingSession(viewer, "a", start)
	if e != nil || first.State.ReadCount != 1 || first.State.Revision != 1 {
		t.Fatal(first, e)
	}
	again, e := s.StartReadingSession(viewer, "a", start)
	if e != nil || again.ID != first.ID || again.State.ReadCount != 1 {
		t.Fatal("duplicate start", again, e)
	}
	page := 1
	in := model.ReadingWrite{Expected: 1, ContentRevision: 1, IdempotencyKey: "progress-first", Action: "progress", PageIndex: &page, SessionID: first.ID}
	progress, e := s.WriteReadingState(viewer, "a", in)
	if e != nil || progress.Completed || progress.ReadCount != 1 {
		t.Fatal(progress, e)
	}
	repeat, e := s.WriteReadingState(viewer, "a", in)
	if e != nil || repeat.Revision != progress.Revision {
		t.Fatal("retry", repeat, e)
	}
	page = 0
	in.IdempotencyKey = "progress-back"
	in.Expected = progress.Revision
	progress, e = s.WriteReadingState(viewer, "a", in)
	if e != nil || *progress.PageIndex != 0 {
		t.Fatal("backwards", progress, e)
	}
	if _, e = s.WriteReadingState(viewer, "a", model.ReadingWrite{Expected: 1, ContentRevision: 1, IdempotencyKey: "stale-device", Action: "complete"}); !errors.Is(e, model.ErrReadingRevision) {
		t.Fatal("device CAS", e)
	}
	completed, e := s.WriteReadingState(viewer, "a", model.ReadingWrite{Expected: progress.Revision, ContentRevision: 1, IdempotencyKey: "complete-first", Action: "complete"})
	if e != nil || !completed.Completed || completed.CompletedAt == nil {
		t.Fatal(completed, e)
	}
	reread, e := s.StartReadingSession(viewer, "a", model.ReadingSessionStart{Expected: completed.Revision, ContentRevision: 1, IdempotencyKey: "session-second"})
	if e != nil || !reread.State.Completed || reread.State.ReadCount != 2 {
		t.Fatal("reread changed completion", reread, e)
	}
	reset, e := s.WriteReadingState(viewer, "a", model.ReadingWrite{Expected: reread.State.Revision, ContentRevision: 1, IdempotencyKey: "reset-explicit", Action: "reset"})
	if e != nil || reset.Completed || reset.PageIndex != nil || reset.CompletedAt != nil || reset.ReadCount != 2 || *reset.LastReadAt != *reread.State.LastReadAt {
		t.Fatal(reset, e)
	}
	other, e := s.ReadingState(ctx["editor"], "a")
	if e != nil || other.Revision != 0 || other.ReadCount != 0 {
		t.Fatal("owner leak", other, e)
	}
	if _, e = s.ReadingState(viewer, "b"); !errors.Is(e, model.ErrNotFound) {
		t.Fatal("ACL", e)
	}
}
func TestReadingUnknownPagesAndContentConfirmation(t *testing.T) {
	s, ctx := aclFixture(t)
	viewer := ctx["viewer"]
	if _, e := s.Write.Exec("UPDATE files SET page_count=0 WHERE edition_id='a'"); e != nil {
		t.Fatal(e)
	}
	page := 0
	if _, e := s.WriteReadingState(viewer, "a", model.ReadingWrite{Expected: 0, ContentRevision: 1, IdempotencyKey: "unknown-page", Action: "progress", PageIndex: &page}); !errors.Is(e, model.ErrReadingUnknownPages) {
		t.Fatal(e)
	}
	complete, e := s.WriteReadingState(viewer, "a", model.ReadingWrite{Expected: 0, ContentRevision: 1, IdempotencyKey: "unknown-complete", Action: "complete"})
	if e != nil || !complete.Completed {
		t.Fatal(complete, e)
	}
	if _, e = s.Write.Exec("UPDATE files SET page_count=3,content_revision=2 WHERE edition_id='a'"); e != nil {
		t.Fatal(e)
	}
	stale, e := s.ReadingState(viewer, "a")
	if e != nil || !stale.Stale || !stale.Completed || stale.RecordedPageCount != nil || stale.ContentRevision != 1 {
		t.Fatal(stale, e)
	}
	in := model.ReadingWrite{Expected: complete.Revision, ContentRevision: 2, IdempotencyKey: "confirm-new-content", Action: "reset"}
	if _, e = s.WriteReadingState(viewer, "a", in); !errors.Is(e, model.ErrReadingConfirmation) {
		t.Fatal(e)
	}
	in.AcceptContentChange = true
	reset, e := s.WriteReadingState(viewer, "a", in)
	if e != nil || reset.Stale || reset.Completed || reset.ContentRevision != 2 || *reset.RecordedPageCount != 3 {
		t.Fatal(reset, e)
	}
	in.Expected = reset.Revision
	in.Action = "progress"
	in.IdempotencyKey = "wrong-old-content"
	in.ContentRevision = 1
	in.PageIndex = &page
	if _, e = s.WriteReadingState(viewer, "a", in); !errors.Is(e, model.ErrReadingContent) {
		t.Fatal(e)
	}
}
func TestReadingConcurrentSessionRetryAndWriteEpoch(t *testing.T) {
	s, ctx := aclFixture(t)
	viewer := ctx["viewer"]
	before, e := s.InstanceState(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	errorsCh := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.StartReadingSession(viewer, "a", model.ReadingSessionStart{ContentRevision: 1, IdempotencyKey: "concurrent-session"})
			errorsCh <- e
		}()
	}
	wg.Wait()
	close(errorsCh)
	for e := range errorsCh {
		if e != nil {
			t.Fatal(e)
		}
	}
	state, e := s.ReadingState(viewer, "a")
	if e != nil || state.ReadCount != 1 || state.Revision != 1 {
		t.Fatal(state, e)
	}
	after, e := s.InstanceState(context.Background())
	if e != nil || after.WriteEpoch <= before.WriteEpoch {
		t.Fatal("reading skipped write epoch", before, after, e)
	}
	if _, e = s.Write.Exec("UPDATE health_state SET phase='recovery_required' WHERE singleton=1"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.StartReadingSession(viewer, "a", model.ReadingSessionStart{Expected: 1, ContentRevision: 1, IdempotencyKey: "barrier-session"}); e == nil {
		t.Fatal("reading bypassed maintenance")
	}
}
func TestReadingUncompleteAndPermanentEditionPurge(t *testing.T) {
	s, ctx := aclFixture(t)
	viewer := ctx["viewer"]
	page := 1
	progress, e := s.WriteReadingState(viewer, "a", model.ReadingWrite{ContentRevision: 1, IdempotencyKey: "purge-progress", Action: "complete", PageIndex: &page})
	if e != nil {
		t.Fatal(e)
	}
	active, e := s.StartReadingSession(viewer, "a", model.ReadingSessionStart{Expected: progress.Revision, ContentRevision: 1, IdempotencyKey: "purge-session"})
	if e != nil {
		t.Fatal(e)
	}
	uncomplete, e := s.WriteReadingState(viewer, "a", model.ReadingWrite{Expected: active.State.Revision, ContentRevision: 1, IdempotencyKey: "explicit-uncomplete", Action: "uncomplete"})
	if e != nil || uncomplete.Completed || uncomplete.PageIndex == nil || *uncomplete.PageIndex != 1 || uncomplete.ReadCount != 1 || *uncomplete.LastReadAt != *active.State.LastReadAt {
		t.Fatal(uncomplete, e)
	}
	if _, e = s.Write.Exec("UPDATE editions SET trashed_at=unixepoch() WHERE id='a'"); e != nil {
		t.Fatal(e)
	}
	tx, e := s.Write.BeginTx(ctx["admin"], nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = purgeBook(ctx["admin"], tx, "a"); e != nil {
		t.Fatal("production purge", e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	for _, table := range []string{"reading_states", "reading_sessions", "reading_state_requests"} {
		var n int
		if e = s.Read.QueryRow("SELECT count(*) FROM " + table + " WHERE edition_id='a'").Scan(&n); e != nil || n != 0 {
			t.Fatal("reading purge failed", table, n, e)
		}
	}
}
func TestReadingCancelledWriteThenImmediateRetryAndDeviceRace(t *testing.T) {
	s, ctx := aclFixture(t)
	cancelled, cancel := context.WithCancel(ctx["viewer"])
	cancel()
	start := model.ReadingSessionStart{ContentRevision: 1, IdempotencyKey: "cancel-retry-session"}
	if _, e := s.StartReadingSession(cancelled, "a", start); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := s.StartReadingSession(ctx["viewer"], "a", start); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for page := 0; page < 2; page++ {
		wg.Add(1)
		go func(page int) {
			defer wg.Done()
			_, e := s.WriteReadingState(ctx["viewer"], "a", model.ReadingWrite{Expected: 1, ContentRevision: 1, IdempotencyKey: fmt.Sprintf("device-race-%d", page), Action: "progress", PageIndex: &page})
			results <- e
		}(page)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, model.ErrReadingRevision) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
	current, e := s.ReadingState(ctx["viewer"], "a")
	if e != nil || current.Revision != 2 || current.ReadCount != 1 {
		t.Fatal(current, e)
	}
}
func TestReadingSessionIdempotencySurvivesReopen(t *testing.T) {
	s, ctx := aclFixture(t)
	start := model.ReadingSessionStart{ContentRevision: 1, IdempotencyKey: "durable-session"}
	first, e := s.StartReadingSession(ctx["viewer"], "a", start)
	if e != nil {
		t.Fatal(e)
	}
	dir := s.healthDir
	s.Close()
	again, e := Open(context.Background(), dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer again.Close()
	repeat, e := again.StartReadingSession(ctx["viewer"], "a", start)
	if e != nil || repeat.ID != first.ID || repeat.State.ReadCount != 1 {
		t.Fatal(repeat, e)
	}
}
func TestReadingPersistenceFailureRollsBackStateAndEpoch(t *testing.T) {
	s, ctx := aclFixture(t)
	before, e := s.InstanceState(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("CREATE TEMP TRIGGER reading_fault BEFORE INSERT ON reading_state_requests BEGIN SELECT RAISE(ABORT,'anonymous reading persistence fault');END"); e != nil {
		t.Fatal(e)
	}
	in := model.ReadingWrite{ContentRevision: 1, IdempotencyKey: "atomic-reading", Action: "complete"}
	if _, e = s.WriteReadingState(ctx["viewer"], "a", in); e == nil {
		t.Fatal("fault ignored")
	}
	current, e := s.ReadingState(ctx["viewer"], "a")
	if e != nil || current.Revision != 0 || current.Completed {
		t.Fatal("partial state persisted", current, e)
	}
	after, e := s.InstanceState(context.Background())
	if e != nil || after.WriteEpoch != before.WriteEpoch {
		t.Fatal("rolled back write changed epoch", before, after, e)
	}
	if _, e = s.Write.Exec("DROP TRIGGER reading_fault"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.WriteReadingState(ctx["viewer"], "a", in); e != nil {
		t.Fatal("immediate retry", e)
	}
}
