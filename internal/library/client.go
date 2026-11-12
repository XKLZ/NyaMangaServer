package library

import (
	"context"
	"errors"

	"nyamangaserver.local/server/internal/model"
)

func (s *Service) ClientReadingContext(ctx context.Context, id, edition, intent string) (model.ClientReadingContext, error) {
	v, e := s.store.ClientReadingContext(ctx, id, edition, intent)
	if e != nil {
		return v, e
	}
	rootReason := s.ClientRootReason(v.SourceLibrary)
	v.Edition.Readability = ClientReadability(v.Edition, rootReason)
	for i := range v.Editions {
		candidate := &v.Editions[i]
		candidate.Readability = ClientReadability(*candidate, rootReason)
		if !v.Edition.Readability.Readable && v.ReadableAlternativeID == nil && candidate.Readability.Readable {
			v.ReadableAlternativeID = &candidate.ID
		}
	}
	return v, nil
}

func (s *Service) ClientRootReason(l model.Library) string {
	_, e := s.checkedRoot(l)
	switch {
	case e == nil:
		return ""
	case errors.Is(e, model.ErrDisabled):
		return "library_disabled"
	case errors.Is(e, ErrIdentity):
		return "root_changed"
	default:
		return "root_unavailable"
	}
}

func ClientReadability(v model.ClientEdition, rootReason string) model.ClientReadability {
	if rootReason != "" {
		return model.ClientReadability{Reason: rootReason}
	}
	switch v.FileState {
	case "missing":
		return model.ClientReadability{Reason: "file_missing"}
	case "corrupt":
		return model.ClientReadability{Reason: "file_corrupt"}
	case "needs_review":
		return model.ClientReadability{Reason: "file_needs_review"}
	case "available":
		if v.PageCount != nil {
			return model.ClientReadability{Readable: true, Reason: "indexed_available"}
		}
	}
	return model.ClientReadability{Reason: "page_count_unknown"}
}
