package library

import (
	"context"
	"nyamangaserver.local/server/internal/model"
	"path/filepath"
)

func (s *Service) Rehash(ctx context.Context, b model.Edition) (string, int, error) {
	_, e := s.directory(ctx, b.ID, b.ContentRevision)
	if e != nil {
		return "", 0, e
	}
	l, e := s.store.Library(ctx, b.LibraryID)
	if e != nil {
		return "", 0, e
	}
	r, e := s.checkedRoot(*l)
	if e != nil {
		return "", 0, e
	}
	// Migrate an old cached cover without regenerating it or changing its selection.
	_, _ = s.coverBytes(b, model.CoverSelection{Mode: "auto"}, filepath.Base(s.cachePath(b, 0)))
	hash, pages, e := s.Pool.LookupSHA1(ctx, r.Path, b.Path, effectiveEncoding(b, *l), b.Size, b.Mtime)
	if e != nil {
		return "", 0, e
	}
	if _, e = s.directory(ctx, b.ID, b.ContentRevision); e != nil {
		return "", 0, e
	}
	return hash, pages, nil
}
