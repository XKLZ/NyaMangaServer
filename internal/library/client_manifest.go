package library

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/model"
)

func (s *Service) CreateOfflineManifest(ctx context.Context, id string, in model.OfflineManifestRequest) (model.OfflineManifest, error) {
	var out model.OfflineManifest
	if in.ContentRevision < 1 || in.ParsingRevision < 1 {
		return out, model.ErrInput
	}
	b, e := s.store.Edition(ctx, id)
	if e != nil {
		return out, e
	}
	if b.ContentRevision != in.ContentRevision {
		return out, comic.ErrChanged
	}
	if b.ParsingRevision != in.ParsingRevision {
		return out, model.ErrParsingChanged
	}
	identity, e := s.store.ClientIdentity(ctx)
	if e != nil {
		return out, e
	}
	key, entries, e := s.offlineDirectory(ctx, *b)
	if e != nil {
		return out, e
	}
	if key.Epoch != identity.CacheEpoch {
		return out, model.ErrClientEpoch
	}
	if len(entries) == 0 {
		return out, model.ErrNotFound
	}
	newID, e := model.NewID()
	if e != nil {
		return out, e
	}
	out, e = s.store.CreateOfflineManifest(ctx, model.OfflineManifest{ID: newID, ClientIdentity: identity, EditionID: id,
		ContentRevision: b.ContentRevision, ParsingRevision: b.ParsingRevision, PageCount: len(entries),
		DirectoryVersion: comic.DirectoryVersion, RepresentationVersion: comic.PageRepresentationVersion, StructureDigest: structureDigest(*b, entries)})
	if e != nil {
		return out, e
	}
	if e = s.CheckPage(ctx, key); e != nil {
		return out, e
	}
	return s.withManifestMetadata(ctx, out, *b, "unchanged")
}

func (s *Service) offlineDirectory(ctx context.Context, b model.Edition) (comic.PageKey, []comic.Entry, error) {
	key, l, e := s.pageSource(ctx, b)
	if e != nil {
		return key, nil, e
	}
	entries, e := s.Pool.Inspect(ctx, key.Root, key.Path, effectiveEncoding(b, l), b.Size, b.Mtime)
	if e == nil {
		e = s.ensurePageOrder(ctx, b, l, key.Root, entries)
	}
	return key, entries, e
}

func structureDigest(b model.Edition, entries []comic.Entry) string {
	return clientDigest([]any{b.ID, b.ContentRevision, b.ParsingRevision, comic.DirectoryVersion, comic.PageRepresentationVersion, entries})
}

func clientDigest(v any) string {
	data, _ := json.Marshal(v)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (s *Service) withManifestMetadata(ctx context.Context, m model.OfflineManifest, b model.Edition, state string) (model.OfflineManifest, error) {
	v, e := s.store.ClientReadingContext(ctx, b.WorkID, b.ID, "open")
	if e != nil {
		return m, e
	}
	m.LibraryID, m.State = v.LibraryID, state
	meta := model.OfflineMetadata{WorkID: v.WorkID, Title: v.Edition.Title, CoverRevision: v.Edition.CoverRevision, Series: v.Series}
	meta.Digest = clientDigest(meta)
	m.Metadata = &meta
	return m, nil
}

func (s *Service) manifestIdentity(ctx context.Context, id string) (model.OfflineManifest, *model.Edition, error) {
	m, e := s.store.OfflineManifest(ctx, id)
	if e != nil {
		return m, nil, e
	}
	b, e := s.store.Edition(ctx, m.EditionID)
	if e != nil {
		return m, nil, e
	}
	identity, e := s.store.ClientIdentity(ctx)
	if e != nil {
		return m, nil, e
	}
	m.InstanceID, m.LibraryID = identity.InstanceID, b.LibraryID
	if m.CacheEpoch != identity.CacheEpoch {
		return m, b, model.ErrClientEpoch
	}
	return m, b, nil
}

func (s *Service) currentManifest(ctx context.Context, m model.OfflineManifest, b model.Edition) (comic.PageKey, []comic.Entry, error) {
	if m.ContentRevision != b.ContentRevision {
		return comic.PageKey{}, nil, comic.ErrChanged
	}
	if m.ParsingRevision != b.ParsingRevision || m.DirectoryVersion != comic.DirectoryVersion || m.RepresentationVersion != comic.PageRepresentationVersion {
		return comic.PageKey{}, nil, model.ErrParsingChanged
	}
	key, entries, e := s.offlineDirectory(ctx, b)
	if e != nil {
		return key, nil, e
	}
	if key.Epoch != m.CacheEpoch {
		return key, nil, model.ErrClientEpoch
	}
	if len(entries) != m.PageCount || structureDigest(b, entries) != m.StructureDigest {
		return key, nil, model.ErrParsingChanged
	}
	return key, entries, nil
}

func offlineState(e error) string {
	switch {
	case e == nil:
		return "unchanged"
	case errors.Is(e, model.ErrClientEpoch):
		return "cache_epoch_changed"
	case errors.Is(e, comic.ErrChanged):
		return "content_changed"
	case errors.Is(e, model.ErrParsingChanged):
		return "parsing_or_representation_changed"
	case errors.Is(e, model.ErrNotFound), errors.Is(e, model.ErrForbidden), errors.Is(e, model.ErrTrashed):
		return "inaccessible"
	case errors.Is(e, ErrRoot), errors.Is(e, ErrIdentity), errors.Is(e, ErrUnavailable), errors.Is(e, model.ErrDisabled), errors.Is(e, model.ErrBusy),
		errors.Is(e, comic.ErrArchive), errors.Is(e, comic.ErrLimit), errors.Is(e, comic.ErrName), errors.Is(e, comic.ErrEncoding):
		return "source_unavailable"
	default:
		return ""
	}
}

func (s *Service) OfflineManifest(ctx context.Context, id string) (model.OfflineManifest, error) {
	m, b, e := s.manifestIdentity(ctx, id)
	if e != nil {
		return m, e
	}
	_, _, e = s.currentManifest(ctx, m, *b)
	state := offlineState(e)
	if state == "" || state == "inaccessible" || state == "cache_epoch_changed" {
		return m, e
	}
	return s.withManifestMetadata(ctx, m, *b, state)
}

func (s *Service) OfflinePages(ctx context.Context, id string, page, size int) (model.OfflinePages, error) {
	var out model.OfflinePages
	if page < 1 || page > 1_000_000 || size < 1 || size > 100 {
		return out, model.ErrInput
	}
	m, b, e := s.manifestIdentity(ctx, id)
	if e != nil {
		return out, e
	}
	key, entries, e := s.currentManifest(ctx, m, *b)
	if e != nil {
		return out, e
	}
	if e = s.CheckPage(ctx, key); e != nil {
		return out, e
	}
	out = model.OfflinePages{ManifestID: id, EditionID: m.EditionID, ContentRevision: m.ContentRevision, ParsingRevision: m.ParsingRevision, StructureDigest: m.StructureDigest, Page: page, PageSize: size, Total: len(entries), Items: []model.OfflinePage{}}
	start := min(len(entries), (page-1)*size)
	for _, entry := range entries[start:min(len(entries), start+size)] {
		out.Items = append(out.Items, model.OfflinePage{Page: model.Page{Index: entry.Index - 1, Name: entry.Name, MediaType: entry.MediaType}})
	}
	return out, nil
}

func (s *Service) PrepareOfflinePage(ctx context.Context, id string, b model.Edition, index int) (comic.PageKey, error) {
	m, current, e := s.manifestIdentity(ctx, id)
	if e != nil {
		return comic.PageKey{}, e
	}
	if current.ID != b.ID {
		return comic.PageKey{}, model.ErrNotFound
	}
	if current.ContentRevision != b.ContentRevision {
		return comic.PageKey{}, comic.ErrChanged
	}
	if current.ParsingRevision != b.ParsingRevision {
		return comic.PageKey{}, model.ErrParsingChanged
	}
	key, entries, e := s.currentManifest(ctx, m, *current)
	if e != nil {
		return key, e
	}
	if index < 1 || index > len(entries) {
		return key, model.ErrNotFound
	}
	key.Entry = entries[index-1].Entry
	return key, nil
}

func (s *Service) CheckOffline(ctx context.Context, in model.OfflineCheckRequest) ([]model.OfflineCheckResult, error) {
	if len(in.Objects) < 1 || len(in.Objects) > 100 || in.InstanceID == "" || in.CacheEpoch == "" {
		return nil, model.ErrInput
	}
	identity, e := s.store.ClientIdentity(ctx)
	if e != nil {
		return nil, e
	}
	results := make([]model.OfflineCheckResult, 0, len(in.Objects))
	for _, object := range in.Objects {
		if object.ID == "" || len(object.ID) > 128 || len(object.MetadataDigest) > 64 {
			return nil, model.ErrInput
		}
		m, b, err := s.manifestIdentity(ctx, object.ID)
		out := model.OfflineCheckResult{ID: object.ID}
		if err == nil {
			if in.ClientIdentity != identity {
				err = model.ErrClientEpoch
			} else {
				_, _, err = s.currentManifest(ctx, m, *b)
			}
		}
		out.Status = offlineState(err)
		if out.Status == "" {
			return nil, err
		}
		if out.Status == "unchanged" || out.Status == "source_unavailable" {
			m, err = s.withManifestMetadata(ctx, m, *b, out.Status)
			if err != nil {
				if errors.Is(err, model.ErrNotFound) {
					out.Status = "inaccessible"
				} else {
					return nil, err
				}
			} else if m.Metadata.Digest != object.MetadataDigest {
				if out.Status == "unchanged" {
					out.Status = "metadata_changed"
				}
				out.Metadata = m.Metadata
			}
		}
		results = append(results, out)
	}
	return results, nil
}
