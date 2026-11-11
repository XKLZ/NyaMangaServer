package library

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
)

func (s *Service) WebInfo(ctx context.Context, b *model.Edition) (string, error) {
	if e := s.store.ManagementGuard(ctx, b.ID); e != nil {
		return "", e
	}
	l, e := s.store.Library(ctx, b.LibraryID)
	if e != nil {
		return "", e
	}
	r, e := s.checkedRoot(*l)
	if e != nil {
		return "", e
	}
	return s.Pool.WebInfo(ctx, r.Path, b.Path, effectiveEncoding(*b, *l), b.Size, b.Mtime)
}

func (s *Service) cachePath(b model.Edition, index int) string {
	name := model.CoverCacheName(b.ID, b.ContentRevision, b.ParsingRevision, index)
	return filepath.Join(s.cacheDir, name[:2], name)
}
func (s *Service) writeCache(b model.Edition, index int, data []byte) error {
	return s.cache.Put(filepath.Base(s.cachePath(b, index)), data)
}
func (s *Service) Image(ctx context.Context, id string, revision int64, index int, cover bool) ([]byte, string, error) {
	if cover {
		release, err := s.healthIO(ctx)
		if err != nil {
			if errors.Is(err, model.ErrHealthMaintenance) {
				b, e := s.store.Edition(ctx, id)
				if e != nil {
					return nil, "", e
				}
				if b.ContentRevision != revision {
					return nil, "", comic.ErrChanged
				}
				selection, e := s.store.CoverSelection(ctx, id)
				if e != nil {
					return nil, "", e
				}
				if selection.State == "content_changed" || selection.State == "parsing_changed" {
					return nil, "", comic.ErrChanged
				}
				if raw, e := s.cache.Peek(filepath.Base(s.selectedCachePath(*b, selection))); e == nil {
					return raw, "image/png", nil
				}
			}
			return nil, "", err
		}
		defer release()
	}

	b, e := s.store.Edition(ctx, id)
	if e != nil {
		return nil, "", e
	}
	if b.ContentRevision != revision {
		return nil, "", comic.ErrChanged
	}
	if b.TrashedAt != nil {
		return nil, "", model.ErrTrashed
	}
	managementErr := s.store.ManagementGuard(ctx, id)
	if managementErr != nil && !errors.Is(managementErr, model.ErrDisabled) && !errors.Is(managementErr, model.ErrBusy) {
		return nil, "", managementErr
	}
	selection := model.CoverSelection{Mode: "auto"}
	if cover {
		selection, e = s.store.CoverSelection(ctx, id)
		if e != nil {
			return nil, "", e
		}
		if selection.State == "content_changed" || selection.State == "parsing_changed" {
			return nil, "", comic.ErrChanged
		}
		if selection.Mode == "page" && selection.PageIndex != nil {
			index = *selection.PageIndex
		}
		if selection.Mode == "asset" && selection.AssetID != nil {
			asset, e := s.store.Asset(ctx, *selection.AssetID)
			if e != nil {
				return nil, "", e
			}
			path := filepath.Join(s.Config.DataDir, asset.Key)
			if appdata.NoSymlinks(path) != nil {
				return nil, "", model.ErrInput
			}
			if data, e := s.cache.Get(filepath.Base(s.selectedCachePath(*b, selection))); e == nil {
				return data, "image/png", nil
			}
			f, e := os.Open(path)
			if e != nil {
				return nil, "", e
			}
			defer f.Close()
			raw, e := io.ReadAll(io.LimitReader(f, int64(comic.MaxPage)+1))
			if e != nil {
				return nil, "", e
			}
			data, e := s.Pool.Thumbnail(ctx, raw)
			if e != nil {
				return nil, "", e
			}
			_ = s.cache.Put(filepath.Base(s.selectedCachePath(*b, selection)), data)
			return data, "image/png", nil
		}
	}
	key := filepath.Base(s.selectedCachePath(*b, selection))
	cachedCover := func(cause error) ([]byte, string, error) {
		if cover {
			if data, e := s.coverBytes(*b, selection, key); e == nil {
				return data, "image/png", nil
			}
		}
		return nil, "", cause
	}
	if managementErr != nil {
		return cachedCover(managementErr)
	}
	if b.LibraryDisabled {
		return cachedCover(model.ErrDisabled)
	}
	if b.State == "missing" {
		return cachedCover(ErrUnavailable)
	}
	if b.State != "available" {
		return nil, "", ErrUnavailable
	}
	l, e := s.store.Library(ctx, b.LibraryID)
	if e != nil {
		return nil, "", e
	}
	r, e := s.checkedRoot(*l)
	if e != nil {
		if e == ErrRoot {
			return cachedCover(e)
		}
		return nil, "", e
	}
	root, e := os.OpenRoot(r.Path)
	if e != nil {
		return cachedCover(ErrRoot)
	}
	info, e := root.Stat(b.Path)
	root.Close()
	if e != nil {
		return cachedCover(ErrUnavailable)
	}
	if info.Size() != b.Size || info.ModTime().UnixNano() != b.Mtime {
		return nil, "", comic.ErrChanged
	}
	entries, e := s.Pool.Inspect(ctx, r.Path, b.Path, effectiveEncoding(*b, *l), b.Size, b.Mtime)
	if e != nil {
		return nil, "", e
	}
	if e = s.ensurePageOrder(ctx, *b, *l, r.Path, entries); e != nil {
		return nil, "", e
	}

	if cover && selection.Mode == "page" {
		selected, err := selectedCoverEntry(entries, selection, false)
		if err != nil {
			return nil, "", err
		}
		index = selected.Index
	}

	if index < 1 || index > len(entries) {
		return nil, "", model.ErrNotFound
	}
	p := entries[index-1]
	if cover {
		if cached, e := s.coverBytes(*b, selection, key); e == nil {
			return cached, "image/png", nil
		}
	}
	data, kind, _, e := s.Pool.Image(ctx, r.Path, b.Path, b.Size, b.Mtime, p.Entry, cover)
	if e != nil {
		return nil, "", e
	}
	if cover {
		if e = s.cache.Put(key, data); e != nil {
			s.Logger.Warn("cover cache unavailable", "book_id", id)
		}
	}
	return data, kind, nil
}

// coverBytes migrates old automatic thumbnails lazily, including when the source is offline.
func (s *Service) coverBytes(b model.Edition, v model.CoverSelection, key string) ([]byte, error) {
	data, e := s.cache.Get(key)
	if e == nil || v.Mode != "auto" || b.LegacyCoverSHA1 == nil {
		return data, e
	}
	old := fmt.Sprintf("%s:%d:page=0:sha1=%s:320x480:nearest:png:v1", b.ID, b.ContentRevision, *b.LegacyCoverSHA1)
	sum := sha256.Sum256([]byte(old))
	data, e = s.cache.Get(fmt.Sprintf("%x.png", sum))
	if e == nil {
		if err := s.cache.Put(key, data); err != nil {
			s.Logger.Warn("cover cache migration unavailable", "book_id", b.ID)
		}
	}
	return data, e
}
func (s *Service) directory(ctx context.Context, id string, revision int64) ([]comic.Entry, error) {
	if e := s.store.ManagementGuard(ctx, id); e != nil {
		return nil, e
	}
	b, e := s.store.Edition(ctx, id)
	if e != nil {
		return nil, e
	}
	if b.ContentRevision != revision {
		return nil, comic.ErrChanged
	}
	if b.TrashedAt != nil {
		return nil, model.ErrTrashed
	}
	if b.State != "available" {
		return nil, ErrUnavailable
	}
	l, e := s.store.Library(ctx, b.LibraryID)
	if e != nil {
		return nil, e
	}
	r, e := s.checkedRoot(*l)
	if e != nil {
		return nil, e
	}
	entries, e := s.Pool.Inspect(ctx, r.Path, b.Path, effectiveEncoding(*b, *l), b.Size, b.Mtime)
	if e != nil {
		return nil, e
	}
	if e = s.ensurePageOrder(ctx, *b, *l, r.Path, entries); e != nil {
		return nil, e
	}
	return entries, nil
}
