package importflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/png"
	"nyamangaserver.local/server/internal/imagecache"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type thumbnailStub struct {
	calls   atomic.Int32
	active  atomic.Int32
	peak    atomic.Int32
	release <-chan struct{}
}

func (s *thumbnailStub) FetchThumbnail(ctx context.Context, _ string) ([]byte, error) {
	s.calls.Add(1)
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		old := s.peak.Load()
		if active <= old || s.peak.CompareAndSwap(old, active) {
			break
		}
	}
	if s.release != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.release:
		}
	}
	var out bytes.Buffer
	png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 8, 8)))
	return out.Bytes(), nil
}
func coverKey(s string) string { return fmt.Sprintf("%x.png", sha256.Sum256([]byte(s))) }
func TestCoverCacheDedupeCancellationConcurrencyAndQuota(t *testing.T) {
	release := make(chan struct{})
	remote := &thumbnailStub{release: release}
	cache := &coverCache{remote: remote, cache: imagecache.New(filepath.Join(t.TempDir(), "independent"), 128<<20, 1000), slots: make(chan struct{}, 2), flights: map[string]*coverFlight{}}
	normalize := func(_ context.Context, b []byte) ([]byte, error) { return b, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	for _, key := range []string{"a", "a", "b", "c"} {
		wg.Go(func() { _, _ = cache.get(ctx, coverKey(key), key, normalize) })
	}
	deadline := time.Now().Add(time.Second)
	for remote.active.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("no concurrent reads")
		}
		time.Sleep(time.Millisecond)
	}
	if remote.peak.Load() > 2 {
		t.Fatal(remote.peak.Load())
	}
	close(release)
	wg.Wait()
	if remote.calls.Load() != 3 {
		t.Fatal("not deduplicated", remote.calls.Load())
	}
	if _, e := cache.get(ctx, coverKey("a"), "a", normalize); e != nil || remote.calls.Load() != 3 {
		t.Fatal("cache miss", e)
	}
	blocked := make(chan struct{})
	remote.release = blocked
	cancelled, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); cache.get(cancelled, coverKey("cancelled"), "cancelled", normalize) }()
	for remote.active.Load() < 1 {
		time.Sleep(time.Millisecond)
	}
	stop()
	<-done
	close(blocked)
	if _, e := cache.get(ctx, coverKey("cancelled"), "cancelled", normalize); e != nil {
		t.Fatal("cancelled flight poisoned next read", e)
	}
	quota := &coverCache{remote: remote, cache: imagecache.New(filepath.Join(t.TempDir(), "quota"), 1024, 2), slots: make(chan struct{}, 2), flights: map[string]*coverFlight{}}
	for _, key := range []string{"1", "2", "3"} {
		if _, e := quota.get(ctx, coverKey(key), key, normalize); e != nil {
			t.Fatal(e)
		}
	}
	before := remote.calls.Load()
	if _, e := quota.get(ctx, coverKey("1"), "1", normalize); e != nil || remote.calls.Load() != before+1 {
		t.Fatal("LRU quota not applied", e)
	}
}

type healthReadOnlyCoverRepo struct{ Repository }

func (healthReadOnlyCoverRepo) BeginHealthIO(context.Context) (func(), error) {
	return nil, model.ErrHealthMaintenance
}
func (healthReadOnlyCoverRepo) ImportCandidateDetail(context.Context, string, string) (*model.ImportCandidate, error) {
	return &model.ImportCandidate{Revision: 1, Result: model.RemoteMetadata{Thumbnail: "https://ehgt.org/anonymous-preview.png"}}, nil
}
func TestHealthMaintenanceImportCoverCannotDownload(t *testing.T) {
	remote := &thumbnailStub{}
	dir := t.TempDir()
	s := New(healthReadOnlyCoverRepo{}, nil, nil, nil).WithCovers(remote, dir)
	if _, e := s.CandidateCover(context.Background(), "batch", "book", 1); !errors.Is(e, model.ErrHealthMaintenance) {
		t.Fatal(e)
	}
	if remote.calls.Load() != 0 {
		t.Fatal("read-only miss downloaded")
	}
	if _, e := os.Stat(filepath.Join(dir, "cache")); !os.IsNotExist(e) {
		t.Fatal("read-only miss initialized storage", e)
	}
	key := fmt.Sprintf("%x.png", sha256.Sum256([]byte("import-cover-v1\x00batch\x00book\x001\x00https://ehgt.org/anonymous-preview.png")))
	var raw bytes.Buffer
	png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 8, 8)))
	if e := s.covers.cache.Put(key, raw.Bytes()); e != nil {
		t.Fatal(e)
	}
	warm, e := s.CandidateCover(context.Background(), "batch", "book", 1)
	if e != nil || !bytes.Equal(warm, raw.Bytes()) || remote.calls.Load() != 0 {
		t.Fatal(e, remote.calls.Load())
	}
}
