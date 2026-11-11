package library

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/internal/testfixture"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func setup(t *testing.T) (*Service, model.Library, string) {
	t.Helper()
	root := t.TempDir()
	if e := testfixture.Write(root); e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(context.Background(), t.TempDir(), migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	s := New(db, config.Config{DataDir: t.TempDir(), Roots: []config.Root{{Key: "fixture", Name: "匿名", Path: root}}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	l, e := s.SaveLibrary(context.Background(), model.Library{RootKey: "fixture", Name: "匿名", Encoding: "utf8"}, 0)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close(); db.Close() })
	return s, *l, root
}
func wait(t *testing.T, s *Service, id string) *model.Job {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		j, e := s.store.Job(context.Background(), id)
		if e != nil {
			t.Fatal(e)
		}
		if j.State != "queued" && j.State != "running" {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job timeout")
	return nil
}
func scan(t *testing.T, s *Service, l model.Library) *model.Job {
	t.Helper()
	j, e := s.StartScan(context.Background(), l.ID)
	if e != nil {
		t.Fatal(e)
	}
	return wait(t, s, j.ID)
}
func TestScanRescanImagesAndMissingSafety(t *testing.T) {
	s, l, root := setup(t)
	ctx := context.Background()
	outside := t.TempDir()
	if e := testfixture.Write(outside); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(filepath.Join(outside, "合成1.cbz"), filepath.Join(root, "escape.cbz")); e != nil {
		t.Fatal(e)
	}
	s.Start(ctx)
	j := scan(t, s, l)
	if j.State != "completed" || j.Done != 3 || j.Failed != 1 || j.Total == nil || *j.Total != 3 {
		t.Fatal(j)
	}
	books, total, e := s.store.Editions(ctx, l.ID, "", 1, 100)
	if e != nil || total != 3 {
		t.Fatal(total, e)
	}
	identities := map[string]model.Edition{}
	var representative *model.Edition
	var firstHash string
	for _, b := range books {
		identities[b.Path] = b
		if b.State == "available" {
			if b.PageCount != 3 || b.SHA1 == nil {
				t.Fatal(b)
			}
			if firstHash == "" {
				firstHash = *b.SHA1
			} else if firstHash != *b.SHA1 {
				t.Fatal("fixture hashes differ")
			}
			copy := b
			representative = &copy
		}
	}
	if representative == nil {
		t.Fatal("no books")
	}
	data, kind, e := s.Image(ctx, representative.ID, representative.ContentRevision, 1, true)
	if e != nil || kind != "image/png" || len(data) == 0 {
		t.Fatal(e)
	}
	if _, _, e = s.Image(ctx, representative.ID, representative.ContentRevision+1, 1, false); e == nil {
		t.Fatal("old version accepted")
	}
	scan(t, s, l)
	again, _, _ := s.store.Editions(ctx, l.ID, "", 1, 100)
	for _, b := range again {
		old := identities[b.Path]
		if old.ID != b.ID || old.ContentRevision != b.ContentRevision {
			t.Fatal("rescan changed stable identity")
		}
	}
	// Metadata change invalidates content revision without replacing book identity.
	changed := filepath.Join(root, representative.Path)
	now := time.Now().Add(time.Second)
	if e = os.Chtimes(changed, now, now); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Image(ctx, representative.ID, representative.ContentRevision, 1, true); e == nil {
		t.Fatal("changed file served cached cover")
	}
	scan(t, s, l)
	updated, e := s.store.Edition(ctx, representative.ID)
	if e != nil || updated.ContentRevision != representative.ContentRevision+1 {
		t.Fatal(updated, e)
	}
	if e = os.Remove(changed); e != nil {
		t.Fatal(e)
	}
	scan(t, s, l)
	missing, _ := s.store.Edition(ctx, representative.ID)
	if missing.State != "missing" {
		t.Fatal("missing file not tracked")
	}
	// Empty and replaced roots must never authorize a mass missing update.
	for _, b := range books {
		_ = os.Remove(filepath.Join(root, b.Path))
	}
	empty := scan(t, s, l)
	if empty.Error != "suspicious_empty_root" || empty.State != "failed" {
		t.Fatal(empty)
	}
	unchanged, _ := s.store.Edition(ctx, identities["损坏.zip"].ID)
	if unchanged.State != "corrupt" {
		t.Fatal("empty root marked all missing")
	}
	moved := root + "-moved"
	if e = os.Rename(root, moved); e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(moved)
	if e = os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = s.StartScan(ctx, l.ID); !errors.Is(e, ErrIdentity) {
		t.Fatal("replaced root accepted", e)
	}
}
func TestCancellationImmediateRestartAndStalePublish(t *testing.T) {
	s, l, _ := setup(t)
	ctx := context.Background()
	var enabled atomic.Bool
	blocked := make(chan model.Job, 1)
	release := make(chan struct{})
	s.BeforePublish = func(ctx context.Context, j model.Job, item model.Item) {
		if enabled.CompareAndSwap(true, false) {
			blocked <- j
			<-release
		}
	}
	s.Start(ctx)
	scan(t, s, l)
	enabled.Store(true)
	old, e := s.StartScan(ctx, l.ID)
	if e != nil {
		t.Fatal(e)
	}
	claimed := <-blocked
	if _, e = s.StartScan(ctx, l.ID); !errors.Is(e, model.ErrScanActive) {
		t.Fatal("concurrent scan accepted")
	}
	if e = s.Cancel(ctx, old.ID); e != nil {
		t.Fatal(e)
	}
	next, e := s.StartScan(ctx, l.ID)
	if e != nil {
		t.Fatal(e)
	}
	queued, _ := s.store.Job(ctx, next.ID)
	if queued.State != "queued" {
		t.Fatal("new worker ran before old IO ended")
	}
	if e = s.store.Publish(ctx, claimed, model.Item{}, model.Edition{}); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal("stale publish accepted", e)
	}
	close(release)
	if j := wait(t, s, next.ID); j.State != "completed" || j.Done != 3 {
		t.Fatal(j)
	}
	cancelled, _ := s.store.Job(ctx, old.ID)
	if cancelled.State != "cancelled" {
		t.Fatal(cancelled)
	}
}
func TestInterruptedEnumerationAndProcessingRecovery(t *testing.T) {
	for _, stage := range []string{"enumerating", "processing"} {
		t.Run(stage, func(t *testing.T) {
			s, l, root := setup(t)
			ctx := context.Background()
			j, e := s.store.StartScan(ctx, "interrupted", l.ID)
			if e != nil {
				t.Fatal(e)
			}
			j, e = s.store.Claim(ctx, j.ID)
			if e != nil {
				t.Fatal(e)
			}
			var entries []model.Item
			for i, path := range []string{"合成1.cbz", "子目录/合成2.zip", "损坏.zip"} {
				info, e := os.Stat(filepath.Join(root, path))
				if e != nil {
					t.Fatal(e)
				}
				entries = append(entries, model.Item{Ordinal: i + 1, Path: path, Size: info.Size(), Mtime: info.ModTime().UnixNano()})
			}
			if stage == "enumerating" {
				entries = entries[:1]
			}
			if e = s.store.StageEntries(ctx, *j, entries); e != nil {
				t.Fatal(e)
			}
			if stage == "processing" {
				if e = s.store.Freeze(ctx, *j); e != nil {
					t.Fatal(e)
				}
				r, _ := s.Root(l.RootKey)
				if e = s.process(ctx, *j, l, r, entries[0]); e != nil {
					t.Fatal(e)
				}
			}
			s.Start(ctx)
			done := wait(t, s, j.ID)
			if done.State != "completed" || done.Done != 3 || done.Failed != 1 || done.Generation <= j.Generation {
				t.Fatal(done)
			}
		})
	}
}
func TestLibraryValidationConflictAndPeriodicScan(t *testing.T) {
	s, l, _ := setup(t)
	ctx := context.Background()
	interval := 15
	l.Interval = &interval
	if _, e := s.SaveLibrary(ctx, l, l.Revision+1); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	saved, e := s.SaveLibrary(ctx, l, l.Revision)
	if e != nil {
		t.Fatal(e)
	}
	bad := 2
	saved.Interval = &bad
	if _, e = s.SaveLibrary(ctx, *saved, saved.Revision); !errors.Is(e, ErrInput) {
		t.Fatal(e)
	}
	if _, e = s.store.(*store.Store).Write.Exec("UPDATE libraries SET next_scan_at=0 WHERE id=?", l.ID); e != nil {
		t.Fatal(e)
	}
	s.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		jobs, _ := s.store.Jobs(ctx, 10)
		if len(jobs) > 0 {
			done := wait(t, s, jobs[0].ID)
			if done.State != "completed" {
				t.Fatal(done)
			}
			var due int64
			s.store.(*store.Store).Read.QueryRow("SELECT next_scan_at FROM libraries WHERE id=?", l.ID).Scan(&due)
			if due <= time.Now().Unix() {
				t.Fatal("periodic scan not rescheduled")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("periodic scan did not run")
}

func TestEncodingSettingsInvalidatePageVersions(t *testing.T) {
	s, l, _ := setup(t)
	ctx := context.Background()
	s.Start(ctx)
	scan(t, s, l)
	books, _, e := s.store.Editions(ctx, l.ID, "available", 1, 50)
	if e != nil {
		t.Fatal(e)
	}
	old := books[0]
	l.Encoding = "gb18030"
	saved, e := s.SaveLibrary(ctx, l, l.Revision)
	if e != nil {
		t.Fatal(e)
	}
	changed, e := s.store.Edition(ctx, old.ID)
	if e != nil || changed.ContentRevision != old.ContentRevision+1 || changed.State != "needs_review" || changed.PageCount != 0 {
		t.Fatal(changed, e)
	}
	if _, _, e = s.Image(ctx, old.ID, old.ContentRevision, 1, true); e == nil {
		t.Fatal("old page version survived encoding change")
	}
	scan(t, s, *saved)
	fresh, _ := s.store.Edition(ctx, old.ID)
	if fresh.ID != old.ID || fresh.ContentRevision != old.ContentRevision+1 || fresh.State != "available" || fresh.PageCount != 3 {
		t.Fatal(fresh)
	}
	if _, _, e = s.Image(ctx, fresh.ID, fresh.ContentRevision, 1, true); e != nil {
		t.Fatal(e)
	}
}

func TestCoverOfflineRecoveryAndCacheFailure(t *testing.T) {
	s, l, root := setup(t)
	ctx := context.Background()
	s.Start(ctx)
	scan(t, s, l)
	books, _, e := s.Editions(ctx, l.ID, "available", 1, 100)
	if e != nil || len(books) != 2 {
		t.Fatal(books, e)
	}
	b := books[0]
	original, _, e := s.Image(ctx, b.ID, b.ContentRevision, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	moved := root + "-offline"
	if e = os.Rename(root, moved); e != nil {
		t.Fatal(e)
	}
	defer os.Rename(moved, root)
	offline, _, e := s.Image(ctx, b.ID, b.ContentRevision, 1, true)
	if e != nil || !bytes.Equal(original, offline) {
		t.Fatal("offline cached cover", e)
	}
	if _, _, e = s.Image(ctx, b.ID, b.ContentRevision, 1, false); e == nil {
		t.Fatal("offline source page served")
	}
	if _, _, e = s.Image(ctx, b.ID, b.ContentRevision+1, 1, true); e == nil {
		t.Fatal("offline stale revision served")
	}
	if e = os.Rename(moved, root); e != nil {
		t.Fatal(e)
	}
	// A new service recovers the on-disk sharded cache without scanning the source.
	fresh := New(s.store, s.Config, s.Logger)
	defer fresh.Close()
	if e = os.Rename(root, moved); e != nil {
		t.Fatal(e)
	}
	recovered, _, e := fresh.Image(ctx, b.ID, b.ContentRevision, 1, true)
	if e != nil || !bytes.Equal(original, recovered) {
		t.Fatal("restart cache recovery", e)
	}
	if e = os.Rename(moved, root); e != nil {
		t.Fatal(e)
	}
	// Make the cache path unusable, without depending on root/chmod behavior.
	bad := config.Config{DataDir: t.TempDir(), Roots: s.Config.Roots}
	if e = os.WriteFile(filepath.Join(bad.DataDir, "cache"), []byte("blocked"), 0600); e != nil {
		t.Fatal(e)
	}
	s.Close()
	degraded := New(s.store, bad, s.Logger)
	defer degraded.Close()
	degraded.Start(ctx)
	job := scan(t, degraded, l)
	if job.State != "completed" {
		t.Fatal(job)
	}
	books, _, e = degraded.Editions(ctx, l.ID, "available", 1, 100)
	if e != nil || len(books) != 2 {
		t.Fatal("index lost on cache failure", e)
	}
	for _, b := range books {
		if b.Error != "cover_cache_failed" || b.PageCount != 3 {
			t.Fatal("missing cache diagnostic or page index", b)
		}
		if data, kind, e := degraded.Image(ctx, b.ID, b.ContentRevision, 1, true); e != nil || len(data) == 0 || kind != "image/png" {
			t.Fatal("on-demand image must survive cache write failure", e)
		}
	}
}
