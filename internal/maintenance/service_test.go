package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/library"
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

type noRemote struct{}

func (noRemote) Drain(context.Context) error { return nil }
func fixture(t *testing.T) (*Service, *store.Store, *library.Service, model.Library, string) {
	t.Helper()
	root := t.TempDir()
	if e := testfixture.Write(root); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	db, e := store.Open(context.Background(), dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	c := config.Config{DataDir: dir, Roots: []config.Root{{Key: "test", Path: root, Name: "Anonymous"}}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cat := library.New(db, c, logger)
	l, e := cat.SaveLibrary(context.Background(), model.Library{RootKey: "test", Name: "Anonymous", Encoding: "utf8"}, 0)
	if e != nil {
		t.Fatal(e)
	}
	s := New(db, c, cat, noRemote{}, logger)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
		_ = cat.Shutdown(ctx)
		db.Close()
	})
	return s, db, cat, *l, root
}
func wait(t *testing.T, db *store.Store, id string) *model.Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, e := db.Job(context.Background(), id)
		if e != nil {
			t.Fatal(e)
		}
		if j.State != "queued" && j.State != "running" {
			return j
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("job timeout")
	return nil
}
func indexed(t *testing.T, db *store.Store, cat *library.Service, l model.Library) model.Edition {
	t.Helper()
	cat.Start(context.Background())
	j, e := cat.StartScan(context.Background(), l.ID)
	if e != nil {
		t.Fatal(e)
	}
	if after := wait(t, db, j.ID); after.State != "completed" {
		t.Fatal(after)
	}
	books, _, e := db.Editions(context.Background(), l.ID, "available", 1, 100)
	if e != nil || len(books) == 0 {
		t.Fatal(books, e)
	}
	return books[0]
}
func TestRootBoundariesAndFingerprint(t *testing.T) {
	s, _, _, _, root := fixture(t)
	ctx := context.Background()
	for _, path := range []string{"../outside", "/etc", "sub/../../outside", "a\\b"} {
		if _, e := s.Entries(ctx, "test", path, 1, 50); e == nil {
			t.Fatal("unsafe path accepted", path)
		}
	}
	outside := t.TempDir()
	if e := os.Symlink(outside, filepath.Join(root, "escape")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Entries(ctx, "test", "escape", 1, 50); e == nil {
		t.Fatal("symlink accepted")
	}
	one, e := s.fingerprint(ctx, "test", "合成1.cbz")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "合成1.cbz"), []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	two, e := s.fingerprint(ctx, "test", "合成1.cbz")
	if e != nil || one == two {
		t.Fatal("fingerprint ignored bytes", e)
	}
}
func TestDurableMaintenanceRestartAndRebindIdentityConflict(t *testing.T) {
	s, db, cat, l, root := fixture(t)
	ctx := context.Background()
	b := indexed(t, db, cat, l)
	p, e := s.Plan(ctx, model.MaintenanceInput{Operation: "trash", BookIDs: []string{b.ID}, Expected: map[string]int64{"book:" + b.ID: b.Revision}})
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.Execute(ctx, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := db.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	s.Start(ctx)
	done := wait(t, db, j.ID)
	if done.State != "completed" || done.Generation <= claimed.Generation {
		t.Fatal(done)
	}
	saved, _ := db.Edition(ctx, b.ID)
	b = *saved
	if b.TrashedAt == nil {
		t.Fatal("restart lost task")
	}
	lptr, _ := db.Library(ctx, l.ID)
	opts, _ := json.Marshal(map[string]any{"root_key": "test"})
	plan, e := s.Plan(ctx, model.MaintenanceInput{Operation: "rebind", LibraryID: l.ID, Expected: map[string]int64{"library:" + l.ID: lptr.Revision}, Options: opts})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(root, root+"-old"); e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(root + "-old")
	if e = os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	job, e := s.Execute(ctx, plan.ID)
	if e != nil {
		t.Fatal(e)
	}
	after := wait(t, db, job.ID)
	if after.State != "failed" || after.Error != "revision_conflict" {
		t.Fatal("changed root silently adopted", after)
	}
}
func TestCoverAssetProtectionCleanupAndInvalidUploads(t *testing.T) {
	s, db, cat, l, _ := fixture(t)
	ctx := context.Background()
	b := indexed(t, db, cat, l)
	data, _, e := cat.Image(ctx, b.ID, b.ContentRevision, 1, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = cat.UploadCover(ctx, b.ID, 0, []byte("bad image")); e == nil {
		t.Fatal("invalid upload accepted")
	}
	v, e := cat.UploadCover(ctx, b.ID, 0, data)
	if e != nil {
		t.Fatal(e)
	}
	asset, e := db.Asset(ctx, *v.AssetID)
	if e != nil {
		t.Fatal(e)
	}
	assetPath := filepath.Join(s.c.DataDir, asset.Key)
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(assetPath, old, old)
	orphan := filepath.Join(s.c.DataDir, "assets", "orphan.png")
	if e = os.WriteFile(orphan, []byte("orphan"), 0600); e != nil {
		t.Fatal(e)
	}
	_ = os.Chtimes(orphan, old, old)
	options := json.RawMessage(`{"categories":["orphan_assets","covers"]}`)
	plan, e := s.Plan(ctx, model.MaintenanceInput{Operation: "cleanup", Expected: map[string]int64{}, Options: options})
	if e != nil {
		t.Fatal(e)
	}
	job, e := s.Execute(ctx, plan.ID)
	if e != nil {
		t.Fatal(e)
	}
	s.Start(ctx)
	if j := wait(t, db, job.ID); j.State != "completed" {
		t.Fatal(j)
	}
	if _, e = os.Stat(assetPath); e != nil {
		t.Fatal("live asset removed", e)
	}
	if _, e = os.Stat(orphan); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("orphan not removed", e)
	}
	if _, _, e = cat.Image(ctx, b.ID, b.ContentRevision, 1, true); e != nil {
		t.Fatal("cover not rebuildable", e)
	}
}
func TestCancelThenNewMaintenanceWaitsForOldIO(t *testing.T) {
	s, db, cat, l, _ := fixture(t)
	ctx := context.Background()
	b := indexed(t, db, cat, l)
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	s.BeforeExecute = func(context.Context, model.Job) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
	}
	makePlan := func() model.MaintenancePlan {
		p, e := s.Plan(ctx, model.MaintenanceInput{Operation: "trash", BookIDs: []string{b.ID}, Expected: map[string]int64{"book:" + b.ID: b.Revision}})
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	first, e := s.Execute(ctx, makePlan().ID)
	if e != nil {
		t.Fatal(e)
	}
	s.Start(ctx)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter")
	}
	if e = s.Control(ctx, first.ID, "cancel"); e != nil {
		t.Fatal(e)
	}
	next, e := s.Execute(ctx, makePlan().ID)
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("new worker overlapped old IO")
	}
	before, _ := db.Edition(ctx, b.ID)
	if before.TrashedAt != nil {
		t.Fatal("uncommitted preview changed data")
	}
	close(release)
	after := wait(t, db, next.ID)
	if after.State != "completed" {
		t.Fatal(after)
	}
	old, _ := db.Job(ctx, first.ID)
	if old.State != "cancelled" || old.Done != 0 {
		t.Fatal("old worker published", old)
	}
	book, _ := db.Edition(ctx, b.ID)
	if book.Revision != b.Revision+1 || book.TrashedAt == nil {
		t.Fatal("duplicate publication", book)
	}
}
func TestCleanupDoesNotRemoveFilesChangedAfterPreview(t *testing.T) {
	s, db, _, _, _ := fixture(t)
	ctx := context.Background()
	dir := filepath.Join(s.c.DataDir, "tmp")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "old")
	if e := os.WriteFile(path, []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(path, old, old)
	p, e := s.Plan(ctx, model.MaintenanceInput{Operation: "cleanup", Expected: map[string]int64{}, Options: json.RawMessage(`{"categories":["tmp"]}`)})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("new live material"), 0600); e != nil {
		t.Fatal(e)
	}
	job, e := s.Execute(ctx, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	s.Start(ctx)
	if j := wait(t, db, job.ID); j.State != "completed" {
		t.Fatal(j)
	}
	raw, e := os.ReadFile(path)
	if e != nil || string(raw) != "new live material" {
		t.Fatal("new material removed", string(raw), e)
	}
}
