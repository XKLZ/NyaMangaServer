package metadata

import (
	"context"
	"encoding/json"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
)

func (s *Service) Preview(ctx context.Context, id, url string, expected, selection, content int64) (*model.Job, error) {
	url, _, _, e := provider.Gallery(url)
	if e != nil || expected < 1 || selection < 1 || content < 1 {
		return nil, model.ErrInput
	}
	j, e := s.repo.CreateSourcePreview(ctx, id, url, expected, selection, content)
	if e == nil {
		s.Wake()
	}
	return j, e
}
func (s *Service) SourcePreview(ctx context.Context, book, id string) (model.SourcePreview, error) {
	return s.repo.SourcePreview(ctx, book, id)
}
func (s *Service) ConfirmPreview(ctx context.Context, book, id string, expected, selection, content int64) error {
	return s.repo.ConfirmSourcePreview(ctx, book, id, expected, selection, content)
}
func (s *Service) RemoveSource(ctx context.Context, id string, expected, selection int64, hintOnly bool) error {
	return s.repo.RemoveSource(ctx, id, expected, selection, hintOnly)
}
func (s *Service) processPreview(ctx context.Context, j model.Job, item model.Item) error {
	var in model.MetadataInput
	if json.Unmarshal([]byte(item.Input), &in) != nil {
		return model.ErrInput
	}
	r, e := s.remote.Fetch(ctx, in.URL)
	if e != nil {
		return e
	}
	return s.repo.PublishSourcePreview(ctx, j, item, r)
}
func (s *Service) retryPreview(ctx context.Context, parent string, items []model.Item) (*model.Job, error) {
	if len(items) != 1 || items[0].BookID == nil {
		return nil, model.ErrInput
	}
	var in model.MetadataInput
	if json.Unmarshal([]byte(items[0].Input), &in) != nil {
		return nil, model.ErrInput
	}
	b, d, e := s.repo.EditionWithMetadata(ctx, *items[0].BookID)
	if e != nil {
		return nil, e
	}
	j, e := s.Preview(ctx, b.ID, in.URL, b.Revision, d.SelectionRevision, b.ContentRevision)
	if e != nil {
		return nil, e
	}
	return j, nil
}
