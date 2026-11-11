// Package tagimages owns global tag image IO and its independent derived cache.
package tagimages

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/imagecache"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Repository interface {
	RequireAdministrator(context.Context) error
	TagImages(context.Context, string) (model.TagImages, error)
	SetTagImage(context.Context, string, string, int64, model.TagImage) (model.TagImage, error)
}
type Service struct {
	repo    Repository
	dir     string
	pool    *comic.Pool
	cache   *imagecache.Cache
	mu      sync.Mutex
	persist func(string, []byte) error
}

func New(repo Repository, c config.Config, pool *comic.Pool) *Service {
	b, n := c.TagImageCache.Limits()
	return &Service{repo: repo, dir: c.DataDir, pool: pool, cache: imagecache.New(filepath.Join(c.DataDir, "cache", "tag-images", "v1"), b, n)}
}

// LockStorage serializes explicit cleanup with active reads, atomic publication
// and replacement. Callers must re-read asset references while holding it.
func (s *Service) LockStorage() func() { s.mu.Lock(); return s.mu.Unlock }
func (s *Service) State(ctx context.Context, id string) (model.TagImages, error) {
	return s.repo.TagImages(ctx, id)
}
func valid(role string, rev int64) error {
	if !model.TagImageRole(role) || rev < 0 || rev == math.MaxInt64 {
		return model.ErrInput
	}
	return nil
}
func choose(v model.TagImages, role string) model.TagImage {
	if role == "foreground" {
		return v.Foreground
	}
	return v.Background
}
func (s *Service) Upload(ctx context.Context, id, role string, expected int64, media string, raw []byte) (model.TagImage, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.TagImage{}, e
	}
	release, err := s.healthIO(ctx)
	if err != nil {
		return model.TagImage{}, err
	}
	defer release()
	if e := valid(role, expected); e != nil {
		return model.TagImage{}, e
	}
	// Check identity before decoder work, then CAS again in the publication transaction.
	if _, e := s.repo.TagImages(ctx, id); e != nil {
		return model.TagImage{}, e
	}
	_, w, h, format, e := s.pool.TagImage(ctx, raw, role)
	if e != nil {
		return model.TagImage{}, e
	}
	if media != "image/"+format {
		return model.TagImage{}, model.ErrTagImageType
	}
	unlock := s.LockStorage()
	defer unlock()
	state, e := s.repo.TagImages(ctx, id)
	if e != nil {
		return model.TagImage{}, e
	}
	if choose(state, role).Revision != expected {
		return model.TagImage{}, model.ErrConflict
	}
	aid, e := model.NewID()
	if e != nil {
		return model.TagImage{}, e
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	key := filepath.ToSlash(filepath.Join("assets", "tags", digest[:2], aid+"."+format))
	persist := s.write
	if s.persist != nil {
		persist = s.persist
	}
	if e = persist(key, raw); e != nil {
		return model.TagImage{}, model.ErrTagImageStorage
	}
	v := model.TagImage{Width: w, Height: h, Format: format, Asset: &model.Asset{ID: aid, Key: key, Digest: digest, Size: int64(len(raw))}}
	// A failed commit deliberately leaves an unreferenced file for explicit cleanup.
	out, e := s.repo.SetTagImage(ctx, id, role, expected, v)
	if e != nil && !errors.Is(e, model.ErrConflict) && !errors.Is(e, model.ErrInput) && !errors.Is(e, model.ErrNotFound) {
		e = model.ErrTagImageStorage
	}
	return out, e
}
func (s *Service) Remove(ctx context.Context, id, role string, expected int64) (model.TagImage, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.TagImage{}, e
	}
	release, err := s.healthIO(ctx)
	if err != nil {
		return model.TagImage{}, err
	}
	defer release()
	if e := valid(role, expected); e != nil {
		return model.TagImage{}, e
	}
	unlock := s.LockStorage()
	defer unlock()
	v, e := s.repo.SetTagImage(ctx, id, role, expected, model.TagImage{})
	if e != nil && !errors.Is(e, model.ErrConflict) && !errors.Is(e, model.ErrInput) && !errors.Is(e, model.ErrNotFound) {
		e = model.ErrTagImageStorage
	}
	return v, e
}
func (s *Service) write(key string, raw []byte) error {
	path := filepath.Join(s.dir, filepath.FromSlash(key))
	if e := appdata.NoSymlinks(path); e != nil {
		return e
	}
	root, e := os.OpenRoot(s.dir)
	if e != nil {
		return e
	}
	defer root.Close()
	dir := filepath.ToSlash(filepath.Dir(key))
	if e = root.MkdirAll(dir, 0700); e != nil {
		return e
	}
	token, e := model.NewID()
	if e != nil {
		return e
	}
	pending := dir + "/pending-" + token
	f, e := root.OpenFile(pending, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	defer root.Remove(pending)
	defer f.Close()
	if _, e = f.Write(raw); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = root.Rename(pending, key); e != nil {
		return e
	}
	d, e := root.Open(dir)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}

func cacheKey(v model.TagImage) string {
	return model.TagImageCacheName(v.Asset.Digest, v.Role)
}
func (s *Service) Content(ctx context.Context, id, role string, revision int64) ([]byte, string, error) {
	release, err := s.healthIO(ctx)
	if err != nil {
		if errors.Is(err, model.ErrHealthMaintenance) {
			if !model.TagImageRole(role) || revision < 1 {
				return nil, "", model.ErrInput
			}
			state, e := s.repo.TagImages(ctx, id)
			if e != nil {
				return nil, "", e
			}
			v := choose(state, role)
			if v.Revision != revision {
				return nil, "", model.ErrConflict
			}
			if v.Asset == nil {
				return nil, "", model.ErrNotFound
			}
			if data, e := s.cache.Peek(cacheKey(v)); e == nil {
				return data, fmt.Sprintf("\"%x\"", sha256.Sum256(data)), nil
			}
		}
		return nil, "", err
	}
	defer release()

	if !model.TagImageRole(role) || revision < 1 {
		return nil, "", model.ErrInput
	}
	unlock := s.LockStorage()
	defer unlock()
	state, e := s.repo.TagImages(ctx, id)
	if e != nil {
		return nil, "", e
	}
	v := choose(state, role)
	if v.Revision != revision {
		return nil, "", model.ErrConflict
	}
	if v.Asset == nil {
		return nil, "", model.ErrNotFound
	}
	key := cacheKey(v)
	data, e := s.cache.Get(key)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return nil, "", model.ErrTagImageStorage
	}
	if errors.Is(e, os.ErrNotExist) {
		a := v.Asset
		if !fs.ValidPath(a.Key) || !strings.HasPrefix(a.Key, "assets/tags/") || appdata.NoSymlinks(filepath.Join(s.dir, a.Key)) != nil {
			return nil, "", model.ErrTagImageStorage
		}
		root, e := os.OpenRoot(s.dir)
		if e != nil {
			return nil, "", model.ErrTagImageStorage
		}
		defer root.Close()
		f, e := root.Open(a.Key)
		if e != nil {
			return nil, "", model.ErrTagImageStorage
		}
		defer f.Close()
		raw, e := io.ReadAll(io.LimitReader(f, 10<<20+1))
		if e != nil || len(raw) > 10<<20 || int64(len(raw)) != a.Size || fmt.Sprintf("%x", sha256.Sum256(raw)) != a.Digest {
			return nil, "", model.ErrTagImageStorage
		}
		data, _, _, _, e = s.pool.TagImage(ctx, raw, role)
		if e != nil {
			return nil, "", model.ErrTagImageStorage
		}
		if e = s.cache.Put(key, data); e != nil {
			return nil, "", model.ErrTagImageStorage
		}
	}
	etag := fmt.Sprintf("\"%x\"", sha256.Sum256(data))
	return data, etag, nil
}

// Cleanup is called under LockStorage, sharing the cache's own eviction lock.
// Called only by maintenance under its health IO lease and writer transaction.
// Do not acquire another health lease or query SQL from this callback.
func (s *Service) RemoveCacheIfUnchanged(name string, size, mtime int64) error {
	return s.cache.RemoveIfUnchanged(name, size, mtime)
}

func (s *Service) healthIO(ctx context.Context) (func(), error) {
	if r, ok := s.repo.(interface {
		BeginHealthIO(context.Context) (func(), error)
	}); ok {
		return r.BeginHealthIO(ctx)
	}
	return func() {}, nil
}

func (s *Service) HealthCacheBytes(ctx context.Context, id, role string) (string, []byte, error) {
	v, e := s.repo.TagImages(ctx, id)
	if e != nil {
		return "", nil, e
	}
	image := choose(v, role)
	if image.Asset == nil {
		return "", nil, model.ErrNotFound
	}
	path := filepath.Join(s.dir, image.Asset.Key)
	if e = appdata.NoSymlinks(path); e != nil {
		return "", nil, e
	}
	f, e := os.Open(path)
	if e != nil {
		return "", nil, e
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, 10<<20+1))
	if e != nil {
		return "", nil, e
	}
	if int64(len(raw)) != image.Asset.Size || fmt.Sprintf("%x", sha256.Sum256(raw)) != image.Asset.Digest {
		return "", nil, model.ErrConflict
	}
	data, _, _, _, e := s.pool.TagImage(ctx, raw, role)
	key := cacheKey(image)
	return "cache/tag-images/v1/" + key[:2] + "/" + key, data, e
}
