package importflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"nyamangaserver.local/server/internal/imagecache"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"os"
	"path/filepath"
	"sync"
)

type ThumbnailFetcher interface {
	FetchThumbnail(context.Context, string) ([]byte, error)
}
type coverFlight struct {
	done chan struct{}
	data []byte
	err  error
}
type coverCache struct {
	remote  ThumbnailFetcher
	cache   *imagecache.Cache
	slots   chan struct{}
	mu      sync.Mutex
	flights map[string]*coverFlight
}

func (s *Service) WithCovers(remote ThumbnailFetcher, dataDir string) *Service {
	s.covers = &coverCache{remote: remote, cache: imagecache.New(filepath.Join(dataDir, "cache", "import-previews", "v1"), 128<<20, 1000), slots: make(chan struct{}, 2), flights: map[string]*coverFlight{}}
	return s
}
func (s *Service) CandidateCover(ctx context.Context, batch, id string, revision int64) ([]byte, error) {
	readOnly, release, leaseErr := s.coverReadLease(ctx)
	if leaseErr != nil {
		return nil, leaseErr
	}
	defer release()

	if revision < 1 {
		return nil, model.ErrInput
	}
	c, e := s.repo.ImportCandidateDetail(ctx, batch, id)
	if e != nil {
		return nil, e
	}
	if c.Revision != revision {
		return nil, model.ErrConflict
	}
	thumb := provider.CandidateThumbnail(c.Result)
	if thumb == "" || s.covers == nil {
		return nil, &model.RemoteError{Code: "thumbnail_unavailable"}
	}
	key := fmt.Sprintf("%x.png", sha256.Sum256([]byte(fmt.Sprintf("import-cover-v1\x00%s\x00%s\x00%d\x00%s", batch, id, revision, thumb))))
	var data []byte
	if readOnly {
		data, e = s.covers.cache.Peek(key)
		if e != nil {
			return nil, model.ErrHealthMaintenance
		}
	} else {
		data, e = s.covers.get(ctx, key, thumb, s.normalizeCover)
	}
	if e != nil {
		return nil, e
	}
	// An obsolete read must not publish against a newer candidate, including a
	// cache hit. It never mutates the source selection or the user's cover.
	current, e := s.repo.ImportCandidateDetail(ctx, batch, id)
	if e != nil {
		return nil, e
	}
	if current.Revision != revision || provider.CandidateThumbnail(current.Result) != thumb {
		return nil, model.ErrConflict
	}
	return data, nil
}
func (c *coverCache) get(ctx context.Context, key, url string, normalize func(context.Context, []byte) ([]byte, error)) ([]byte, error) {
	for {
		if data, e := c.cached(key); e == nil {
			return data, nil
		} else if !os.IsNotExist(e) {
			return nil, e
		}

		c.mu.Lock()
		if flight := c.flights[key]; flight != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-flight.done:
			}
			if flight.err == context.Canceled || flight.err == context.DeadlineExceeded {
				continue
			}
			return flight.data, flight.err
		}
		// A previous leader can finish between the first cache read and taking
		// this lock. Recheck before creating another download for the same key.
		if data, e := c.cached(key); e == nil {
			c.mu.Unlock()
			return data, nil
		} else if !os.IsNotExist(e) {
			c.mu.Unlock()
			return nil, e
		}
		flight := &coverFlight{done: make(chan struct{})}
		c.flights[key] = flight
		c.mu.Unlock()
		flight.data, flight.err = func() ([]byte, error) {
			select {
			case c.slots <- struct{}{}:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			defer func() { <-c.slots }()
			raw, e := c.remote.FetchThumbnail(ctx, url)
			if e != nil {
				return nil, e
			}
			data, e := normalize(ctx, raw)
			if e != nil {
				return nil, e
			}
			if e = ctx.Err(); e != nil {
				return nil, e
			}
			if e = c.cache.Put(key, data); e != nil {
				return nil, e
			}
			return data, nil
		}()
		c.mu.Lock()
		delete(c.flights, key)
		close(flight.done)
		c.mu.Unlock()
		return flight.data, flight.err
	}
}

func (c *coverCache) cached(key string) ([]byte, error) {
	data, e := c.cache.Get(key)
	if e != nil {
		return nil, e
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err == nil && format == "png" && len(data) <= 2<<20 && cfg.Width > 0 && cfg.Height > 0 && cfg.Width <= 320 && cfg.Height <= 480 {
		return data, nil
	}
	if err = c.cache.Remove(key); err != nil {
		return nil, err
	}
	return nil, os.ErrNotExist
}

func (s *Service) normalizeCover(ctx context.Context, raw []byte) ([]byte, error) {
	if len(raw) > 4<<20 {
		return nil, &model.RemoteError{Code: "thumbnail_too_large"}
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "jpeg" && format != "png" && format != "webp") || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > 8_000_000 {
		return nil, &model.RemoteError{Code: "thumbnail_invalid_image"}
	}
	image, err := s.catalog.Pool.Thumbnail(ctx, raw)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || len(image) > 2<<20 {
		return nil, &model.RemoteError{Code: "thumbnail_invalid_image"}
	}
	return image, nil
}

func (s *Service) SourceSearchCover(ctx context.Context, batch, id, search, result string, revision int64) ([]byte, error) {
	readOnly, release, leaseErr := s.coverReadLease(ctx)
	if leaseErr != nil {
		return nil, leaseErr
	}
	defer release()

	repo, ok := s.repo.(interface {
		SourceSearchResult(context.Context, string, string, string, string) (model.SourceSearchResult, error)
	})
	if !ok || s.covers == nil {
		return nil, &model.RemoteError{Code: "thumbnail_unavailable"}
	}
	r, e := repo.SourceSearchResult(ctx, batch, id, search, result)
	if e != nil {
		return nil, e
	}
	if revision < 1 || r.Revision != revision {
		return nil, model.ErrConflict
	}
	if r.State != "ready" || r.Result == nil {
		return nil, &model.RemoteError{Code: "thumbnail_unavailable"}
	}
	thumb := provider.CandidateThumbnail(*r.Result)
	if thumb == "" {
		return nil, &model.RemoteError{Code: "thumbnail_unavailable"}
	}
	key := fmt.Sprintf("%x.png", sha256.Sum256([]byte(fmt.Sprintf("source-search-cover-v1\x00%s\x00%s\x00%d\x00%s", search, result, revision, thumb))))
	var data []byte
	if readOnly {
		data, e = s.covers.cache.Peek(key)
		if e != nil {
			return nil, model.ErrHealthMaintenance
		}
	} else {
		data, e = s.covers.get(ctx, key, thumb, s.normalizeCover)
	}
	if e != nil {
		return nil, e
	}
	now, e := repo.SourceSearchResult(ctx, batch, id, search, result)
	if e != nil {
		return nil, e
	}
	if now.Revision != revision || now.Result == nil || provider.CandidateThumbnail(*now.Result) != thumb {
		return nil, model.ErrConflict
	}
	return data, nil
}

func (s *Service) coverReadLease(ctx context.Context) (bool, func(), error) {
	release := func() {}
	if r, ok := s.repo.(interface {
		BeginHealthIO(context.Context) (func(), error)
	}); ok {
		var e error
		release, e = r.BeginHealthIO(ctx)
		if errors.Is(e, model.ErrHealthMaintenance) {
			return true, func() {}, nil
		}
		if e != nil {
			return false, func() {}, e
		}
	}
	return false, release, nil
}
