package library

import (
	"context"

	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/model"
)

func (s *Service) PreparePage(ctx context.Context, b model.Edition, index int) (comic.PageKey, error) {
	key, l, err := s.pageSource(ctx, b)
	if err != nil {
		return key, err
	}
	entries, err := s.Pool.Inspect(ctx, key.Root, key.Path, effectiveEncoding(b, l), b.Size, b.Mtime)
	if err != nil {
		return key, err
	}
	if err = s.ensurePageOrder(ctx, b, l, key.Root, entries); err != nil {
		return key, err
	}
	if index < 1 || index > len(entries) {
		return key, model.ErrNotFound
	}
	key.Entry = entries[index-1].Entry
	return key, nil
}

func (s *Service) CheckPage(ctx context.Context, key comic.PageKey) error {
	b, err := s.store.Edition(ctx, key.EditionID)
	if err != nil {
		return err
	}
	if b.ContentRevision != key.ContentRevision {
		return comic.ErrChanged
	}
	current, _, err := s.pageSource(ctx, *b)
	if err != nil {
		return err
	}
	if current.ParsingFingerprint != key.ParsingFingerprint {
		return model.ErrParsingChanged
	}
	current.Entry = key.Entry
	if current != key {
		return comic.ErrChanged
	}
	return nil
}

func (s *Service) pageSource(ctx context.Context, b model.Edition) (comic.PageKey, model.Library, error) {
	var key comic.PageKey
	if err := s.store.ManagementGuard(ctx, b.ID); err != nil {
		return key, model.Library{}, err
	}
	if b.State != "available" {
		return key, model.Library{}, ErrUnavailable
	}
	l, err := s.store.Library(ctx, b.LibraryID)
	if err != nil {
		return key, model.Library{}, err
	}
	root, err := s.checkedRoot(*l)
	if err != nil {
		return key, *l, err
	}
	file, err := comic.IdentifySource(root.Path, b.Path)
	if err != nil {
		return key, *l, ErrUnavailable
	}
	if file.Size != b.Size || file.Mtime != b.Mtime {
		return key, *l, comic.ErrChanged
	}
	instance, err := s.store.ClientIdentity(ctx)
	if err != nil {
		return key, *l, err
	}
	return comic.PageKey{Epoch: instance.CacheEpoch, EditionID: b.ID, Root: root.Path, Path: b.Path, ContentRevision: b.ContentRevision,
		ParsingFingerprint: b.FileParsing.Fingerprint() + "\x00" + effectiveEncoding(b, *l) + "\x00" + comic.DirectoryVersion, Source: file}, *l, nil
}
