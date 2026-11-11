package tagimages

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
)

func imageFixture(t *testing.T) (*Service, *store.Store, config.Config, []byte) {
	t.Helper()
	c := config.Config{DataDir: t.TempDir()}
	db, e := store.Open(context.Background(), c.DataDir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if _, e = db.Write.Exec("INSERT INTO tags VALUES('tag','parody','Anonymous'),('artist','artist','Anonymous')"); e != nil {
		t.Fatal(e)
	}
	pool := comic.NewPool()
	t.Cleanup(pool.Close)
	s := New(db, c, pool)
	src := image.NewNRGBA(image.Rect(0, 0, 4, 8))
	src.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 128})
	var b bytes.Buffer
	png.Encode(&b, src)
	return s, db, c, b.Bytes()
}
func TestTagImagesPrivateOriginalVersionCacheAndRestart(t *testing.T) {
	s, db, c, raw := imageFixture(t)
	ctx := context.Background()
	fg, e := s.Upload(ctx, "tag", "foreground", 0, "image/png", raw)
	if e != nil {
		t.Fatal(e)
	}
	original, e := os.ReadFile(filepath.Join(c.DataDir, fg.Asset.Key))
	if e != nil || !bytes.Equal(original, raw) {
		t.Fatal("original not preserved", e)
	}
	before, e := s.State(ctx, "tag")
	if e != nil || before.Background.Revision != 0 {
		t.Fatal(before, e)
	}
	data, etag, e := s.Content(ctx, "tag", "foreground", 1)
	if e != nil || etag == "" {
		t.Fatal(e)
	}
	decoded, e := png.Decode(bytes.NewReader(data))
	if e != nil {
		t.Fatal(e)
	}
	_, _, _, a := decoded.At(0, 0).RGBA()
	if a != 128*257 {
		t.Fatal(a)
	}
	restarted := New(db, c, s.pool)
	same, sameTag, e := restarted.Content(ctx, "tag", "foreground", 1)
	if e != nil || !bytes.Equal(same, data) || sameTag != etag {
		t.Fatal("restart", e)
	}
	bg, e := s.Upload(ctx, "tag", "background", 0, "image/png", raw)
	if e != nil || bg.Revision != 1 {
		t.Fatal(bg, e)
	}
	if _, e = s.Upload(ctx, "tag", "foreground", 0, "image/png", raw); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	removed, e := s.Remove(ctx, "tag", "foreground", 1)
	if e != nil || removed.Revision != 2 {
		t.Fatal(removed, e)
	}
	if _, _, e = s.Content(ctx, "tag", "foreground", 1); !errors.Is(e, model.ErrConflict) {
		t.Fatal("old URL substituted", e)
	}
	if _, _, e = s.Content(ctx, "tag", "foreground", 2); !errors.Is(e, model.ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(c.DataDir, fg.Asset.Key)); e != nil {
		t.Fatal("remove deleted asset", e)
	}
	if _, e = s.Upload(ctx, "tag", "foreground", 2, "image/jpeg", raw); !errors.Is(e, model.ErrTagImageType) {
		t.Fatal("MIME disguise", e)
	}
	if _, e = s.Upload(ctx, "artist", "foreground", 0, "image/png", raw); !errors.Is(e, model.ErrInput) {
		t.Fatal(e)
	}
	// Rebuild only the derived cache; private originals and role generations survive.
	if e = os.RemoveAll(filepath.Join(c.DataDir, "cache", "tag-images")); e != nil {
		t.Fatal(e)
	}
	rebuilt := New(db, c, s.pool)
	if _, _, e = rebuilt.Content(ctx, "tag", "background", 1); e != nil {
		t.Fatal(e)
	}
}

type failRepository struct {
	Repository
	cause error
}

func (r failRepository) SetTagImage(context.Context, string, string, int64, model.TagImage) (model.TagImage, error) {
	return model.TagImage{}, r.cause
}
func TestTagImagesIOAndCommitFailuresKeepOldImage(t *testing.T) {
	s, db, c, raw := imageFixture(t)
	ctx := context.Background()
	old, e := s.Upload(ctx, "tag", "foreground", 0, "image/png", raw)
	if e != nil {
		t.Fatal(e)
	}
	oldBytes, oldETag, e := s.Content(ctx, "tag", "foreground", 1)
	if e != nil {
		t.Fatal(e)
	}
	for _, cause := range []error{os.ErrPermission, syscall.ENOSPC} {
		s.persist = func(string, []byte) error { return cause }
		if _, e = s.Upload(ctx, "tag", "foreground", 1, "image/png", raw); !errors.Is(e, model.ErrTagImageStorage) {
			t.Fatal(e)
		}
		state, _ := s.State(ctx, "tag")
		if state.Foreground.Revision != 1 || *state.Foreground.AssetID != *old.AssetID {
			t.Fatal(state)
		}
	}
	s.persist = nil
	s.repo = failRepository{Repository: db, cause: errors.New("anonymous commit failure")}
	if _, e = s.Upload(ctx, "tag", "foreground", 1, "image/png", raw); !errors.Is(e, model.ErrTagImageStorage) {
		t.Fatal(e)
	}
	s.repo = db
	after, etag, e := s.Content(ctx, "tag", "foreground", 1)
	if e != nil || etag != oldETag || !bytes.Equal(after, oldBytes) {
		t.Fatal(e)
	}
	// Failed publication leaves a private, unreferenced file, never a new DB reference.
	count := 0
	filepath.WalkDir(filepath.Join(c.DataDir, "assets", "tags"), func(_ string, d os.DirEntry, e error) error {
		if e == nil && !d.IsDir() {
			count++
		}
		return e
	})
	if count != 2 {
		t.Fatal("orphan missing", count)
	}
	refs, e := db.AssetKeys(ctx)
	if e != nil || len(refs) != 1 {
		t.Fatal(refs, e)
	}
	// Root path restrictions also fail safely before opening an outside target.
	moved := filepath.Join(c.DataDir, "held-tags")
	os.Rename(filepath.Join(c.DataDir, "assets", "tags"), moved)
	os.Symlink(t.TempDir(), filepath.Join(c.DataDir, "assets", "tags"))
	if _, e = s.Upload(ctx, "tag", "foreground", 1, "image/png", raw); !errors.Is(e, model.ErrTagImageStorage) {
		t.Fatal(e)
	}
	os.Remove(filepath.Join(c.DataDir, "assets", "tags"))
	os.Rename(moved, filepath.Join(c.DataDir, "assets", "tags"))
}
func TestTagImagesIndependentQuotaAndActiveStorageGuard(t *testing.T) {
	s, db, c, raw := imageFixture(t)
	ctx := context.Background()
	s.Upload(ctx, "tag", "foreground", 0, "image/png", raw)
	s.Upload(ctx, "tag", "background", 0, "image/png", raw)
	c.TagImageCache = config.TagImageCache{MaxBytes: 256 << 20, MaxFiles: 1}
	bounded := New(db, c, s.pool)
	if _, _, e := bounded.Content(ctx, "tag", "foreground", 1); e != nil {
		t.Fatal(e)
	}
	if _, _, e := bounded.Content(ctx, "tag", "background", 1); e != nil {
		t.Fatal(e)
	}
	count := 0
	filepath.WalkDir(filepath.Join(c.DataDir, "cache", "tag-images"), func(_ string, d os.DirEntry, e error) error {
		if e == nil && !d.IsDir() {
			count++
		}
		return e
	})
	if count != 1 {
		t.Fatal(count)
	}
	restarted := New(db, c, s.pool)
	if _, _, e := restarted.Content(ctx, "tag", "foreground", 1); e != nil {
		t.Fatal(e)
	}
	c.TagImageCache.MaxBytes = 1
	tiny := New(db, c, s.pool)
	if _, _, e := tiny.Content(ctx, "tag", "background", 1); !errors.Is(e, model.ErrTagImageStorage) {
		t.Fatal(e)
	}
	before, _ := s.State(ctx, "tag")
	if before.Background.Revision != 1 {
		t.Fatal(before)
	}
	// Explicit cleanup takes this same lock; no removal can pass an active read/upload.
	unlock := s.LockStorage()
	entered := make(chan struct{})
	finished := make(chan error, 1)
	go func() { close(entered); _, _, e := s.Content(ctx, "tag", "foreground", 1); finished <- e }()
	<-entered
	select {
	case <-finished:
		t.Fatal("active guard bypassed")
	default:
	}
	unlock()
	if e := <-finished; e != nil {
		t.Fatal(e)
	}
	// Concurrent independent roles retain both writes, while same-role old generations conflict.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, role := range []string{"foreground", "background"} {
		wg.Add(1)
		go func(role string) { defer wg.Done(); _, e := s.Upload(ctx, "tag", role, 1, "image/png", raw); errs <- e }(role)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	state, _ := s.State(ctx, "tag")
	if state.Foreground.Revision != 2 || state.Background.Revision != 2 {
		t.Fatal(state)
	}
}

func TestTagImagesExhaustedWriteGenerationStillReadable(t *testing.T) {
	s, db, _, raw := imageFixture(t)
	ctx := context.Background()
	if _, e := s.Upload(ctx, "tag", "foreground", 0, "image/png", raw); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Write.Exec("UPDATE tag_images SET revision=? WHERE tag_id='tag'", int64(math.MaxInt64)); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.Content(ctx, "tag", "foreground", math.MaxInt64); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Remove(ctx, "tag", "foreground", math.MaxInt64); !errors.Is(e, model.ErrInput) {
		t.Fatal("generation wrap accepted", e)
	}
}

func TestHealthMaintenanceTagContentKeepsWarmCacheAndRejectsMiss(t *testing.T) {
	s, db, c, raw := imageFixture(t)
	ctx := context.Background()
	if _, e := s.Upload(ctx, "tag", "foreground", 0, "image/png", raw); e != nil {
		t.Fatal(e)
	}
	before, _, e := s.Content(ctx, "tag", "foreground", 1)
	if e != nil {
		t.Fatal(e)
	}
	key, _, e := s.HealthCacheBytes(ctx, "tag", "foreground")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Write.Exec("UPDATE health_state SET phase='paused'"); e != nil {
		t.Fatal(e)
	}
	warm, _, e := s.Content(ctx, "tag", "foreground", 1)
	if e != nil || !bytes.Equal(warm, before) {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(c.DataDir, key)); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Content(ctx, "tag", "foreground", 1); !errors.Is(e, model.ErrHealthMaintenance) {
		t.Fatal("maintenance cache miss published", e)
	}
}
