package library

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
)

func (s *Service) HealthCoverBytes(ctx context.Context, id string, allowSources bool) (string, []byte, error) {
	b, e := s.store.Edition(ctx, id)
	if e != nil {
		return "", nil, e
	}
	v, e := s.store.CoverSelection(ctx, id)
	if e != nil {
		return "", nil, e
	}
	if v.State == "content_changed" || v.State == "parsing_changed" {
		return "", nil, comic.ErrChanged
	}
	key, e := filepath.Rel(s.Config.DataDir, s.selectedCachePath(*b, v))
	if e != nil {
		return "", nil, e
	}
	if v.Mode == "asset" && v.AssetID != nil {
		a, e := s.store.Asset(ctx, *v.AssetID)
		if e != nil {
			return "", nil, e
		}
		path := filepath.Join(s.Config.DataDir, a.Key)
		if e = appdata.NoSymlinks(path); e != nil {
			return "", nil, e
		}
		f, e := os.Open(path)
		if e != nil {
			return "", nil, e
		}
		defer f.Close()
		raw, e := io.ReadAll(io.LimitReader(f, comic.MaxPage+1))
		if e != nil {
			return "", nil, e
		}
		if int64(len(raw)) != a.Size || fmt.Sprintf("%x", sha256.Sum256(raw)) != a.Digest {
			return "", nil, model.ErrConflict
		}
		data, e := s.Pool.Thumbnail(ctx, raw)
		return filepath.ToSlash(key), data, e
	}
	if !allowSources {
		return "", nil, model.ErrInput
	}
	l, e := s.store.Library(ctx, b.LibraryID)
	if e != nil {
		return "", nil, e
	}
	if l.Disabled {
		return "", nil, model.ErrDisabled
	}
	r, e := s.checkedRoot(*l)
	if e != nil {
		return "", nil, e
	}
	entries, e := s.Pool.Inspect(ctx, r.Path, b.Path, effectiveEncoding(*b, *l), b.Size, b.Mtime)
	if e != nil {
		return "", nil, e
	}
	selected, e := selectedCoverEntry(entries, v, true)
	if e != nil {
		return "", nil, e
	}

	data, _, _, e := s.Pool.Image(ctx, r.Path, b.Path, b.Size, b.Mtime, selected.Entry, true)
	return filepath.ToSlash(key), data, e
}

// Entry ordinal and original name are the persisted identity. A display page
// number may move when directory sorting changes and cannot identify a repair.
func selectedCoverEntry(entries []comic.Entry, v model.CoverSelection, strict bool) (comic.Entry, error) {
	if v.State == "content_changed" || v.State == "parsing_changed" {
		return comic.Entry{}, comic.ErrChanged
	}
	if v.Mode != "page" {
		if len(entries) == 0 {
			return comic.Entry{}, model.ErrNotFound
		}
		return entries[0], nil
	}
	if strict && (v.EntryIndex == nil || v.EntryName == nil || (v.DirectoryVersion != comic.DirectoryVersion && v.DirectoryVersion != "natural-v1" && v.DirectoryVersion != "natural-bmp-v2")) {
		return comic.Entry{}, comic.ErrChanged
	}
	if v.EntryIndex != nil {
		for _, entry := range entries {
			if entry.Entry == *v.EntryIndex && v.EntryName != nil && (entry.Name == *v.EntryName || v.OrderVerified) {
				return entry, nil
			}
		}
		return comic.Entry{}, comic.ErrChanged
	}
	if v.PageIndex == nil || *v.PageIndex < 1 || *v.PageIndex > len(entries) {
		return comic.Entry{}, model.ErrNotFound
	}
	return entries[*v.PageIndex-1], nil
}
