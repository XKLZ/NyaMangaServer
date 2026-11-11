package metadata

import (
	"context"
	"encoding/json"
	"nyamangaserver.local/server/internal/model"
)

type SourceSearchRepository interface {
	CreateSourceSearch(context.Context, string, string, string, model.SourceSearchInput) (model.SourceSearch, error)
	SourceSearch(context.Context, string, string, string) (model.SourceSearch, error)
	SourceSearchResults(context.Context, string, string, string, int) (model.SourceSearchPage, error)
	SourceSearchResult(context.Context, string, string, string, string) (model.SourceSearchResult, error)
	LoadSourceSearch(context.Context, string, string, string, model.SearchLoad) (model.SourceSearch, error)
	ValidateSearchJob(context.Context, model.Job, model.SearchOperation) (model.SourceSearch, error)
	PublishSearchPage(context.Context, model.Job, model.SearchOperation, model.SearchPage) error
	PublishSearchResults(context.Context, model.Job, model.SearchOperation, []model.FetchResult) error
	SelectSourceSearch(context.Context, string, string, string, model.SearchSelect) error
}
type SearchRemote interface {
	SearchPage(context.Context, string, string, string, string) (model.SearchPage, error)
	FetchMany(context.Context, []string) ([]model.FetchResult, error)
}

func (s *Service) CreateSourceSearch(ctx context.Context, batch, id string, in model.SourceSearchInput) (model.SourceSearch, error) {
	p, e := s.remote.Settings()
	if e != nil {
		return model.SourceSearch{}, e
	}
	v, e := s.repo.(SourceSearchRepository).CreateSourceSearch(ctx, batch, id, p.Site, in)
	if e == nil {
		_ = s.Drain(ctx)
		s.Wake()
	}
	return v, e
}
func (s *Service) SourceSearch(ctx context.Context, batch, id, search string) (model.SourceSearch, error) {
	return s.repo.(SourceSearchRepository).SourceSearch(ctx, batch, id, search)
}
func (s *Service) SearchResults(ctx context.Context, batch, id, search string, page int) (model.SourceSearchPage, error) {
	return s.repo.(SourceSearchRepository).SourceSearchResults(ctx, batch, id, search, page)
}
func (s *Service) SearchResult(ctx context.Context, batch, id, search, result string) (model.SourceSearchResult, error) {
	return s.repo.(SourceSearchRepository).SourceSearchResult(ctx, batch, id, search, result)
}
func (s *Service) LoadSourceSearch(ctx context.Context, batch, id, search string, in model.SearchLoad) (model.SourceSearch, error) {
	v, e := s.repo.(SourceSearchRepository).LoadSourceSearch(ctx, batch, id, search, in)
	if e == nil {
		_ = s.Drain(ctx)
		s.Wake()
	}
	return v, e
}
func (s *Service) SelectSourceSearch(ctx context.Context, batch, id, search string, in model.SearchSelect) error {
	return s.repo.(SourceSearchRepository).SelectSourceSearch(ctx, batch, id, search, in)
}
func (s *Service) ControlSourceSearch(ctx context.Context, batch, id, search, action string, expected int64) error {
	v, e := s.SourceSearch(ctx, batch, id, search)
	if e != nil {
		return e
	}
	if expected < 1 || v.Revision != expected {
		return model.ErrConflict
	}
	if action == "retry" {
		_, e = s.LoadSourceSearch(ctx, batch, id, search, model.SearchLoad{Page: v.ActivePage, ExpectedRevision: v.Revision, RequestKey: "retry-" + v.JobID})
		return e
	}
	if action != "resume" && action != "cancel" && action != "pause" {
		return model.ErrInput
	}
	return s.Control(ctx, v.JobID, action)
}
func (s *Service) processSourceSearch(ctx context.Context, j model.Job, item model.Item) error {
	var op model.SearchOperation
	if json.Unmarshal([]byte(item.Input), &op) != nil || op.Page < 1 || op.Page > 20 {
		return model.ErrInput
	}
	repo := s.repo.(SourceSearchRepository)
	remote, ok := s.remote.(SearchRemote)
	if !ok {
		return &model.RemoteError{Code: "provider_search_unavailable"}
	}
	v, e := repo.ValidateSearchJob(ctx, j, op)
	if e != nil {
		return e
	}
	// A saved list is sufficient for its known pages. Older lists explicitly keep
	// unknown continuation until the user asks for the next page.
	pagesFetched := 0
	for v.Loaded < op.Page*10 && (v.Continuation == "more" || v.Continuation == "unknown") {
		if op.SeedOnly && v.Loaded > 0 && op.Page == 1 && v.Continuation == "unknown" {
			break
		}
		pagesFetched++
		if pagesFetched > 20 {
			return &model.RemoteError{Code: "search_limit_reached"}
		}
		before := v.Loaded
		cursor := v.Next
		page, e := remote.SearchPage(ctx, v.Mode, v.Query, cursor, v.Site)
		if e != nil {
			return e
		}
		if e = repo.PublishSearchPage(ctx, j, op, page); e != nil {
			return e
		}
		v, e = repo.ValidateSearchJob(ctx, j, op)
		if e != nil {
			return e
		}
		if v.Next == cursor && v.Continuation == "more" {
			return &model.RemoteError{Code: "invalid_response"}
		}
		if v.Loaded == before && v.Continuation != "more" {
			break
		}
	}
	page, e := repo.SourceSearchResults(ctx, v.BatchID, v.EditionID, v.ID, op.Page)
	if e != nil {
		return e
	}
	urls := []string{}
	for _, r := range page.Items {
		if r.State != "ready" {
			urls = append(urls, r.URL)
		}
	}
	if len(urls) > 0 {
		settings, e := s.remote.Settings()
		if e != nil {
			return e
		}
		if settings.Site != v.Site {
			return &model.RemoteError{Code: "provider_settings_changed"}
		}
		result, e := remote.FetchMany(ctx, urls)
		if e != nil {
			return e
		}
		if e = repo.PublishSearchResults(ctx, j, op, result); e != nil {
			return e
		}
	}
	return s.repo.FailBackgroundItem(ctx, j, item, "")
}
