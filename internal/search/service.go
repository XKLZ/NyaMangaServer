package search

import (
	"context"
	"nyamangaserver.local/server/internal/model"
	"strings"
	"unicode/utf8"
)

type Repository interface {
	Search(context.Context, string, model.SearchInput, *model.SearchNode) (model.SearchResult, error)
	SearchMatches(context.Context, string, model.SearchInput, *model.SearchNode, string, string) (model.MatchResult, error)
	SavedSearches(context.Context, string) ([]model.SavedSearch, error)
	SaveSearch(context.Context, string, string, string, model.SearchInput, int64) (model.SavedSearch, error)
	DeleteSearch(context.Context, string, string, int64) error
}
type Service struct{ repo Repository }

func New(r Repository) *Service { return &Service{r} }
func Validate(v model.SearchInput) (model.SearchInput, *model.SearchNode, error) {
	if e := model.ValidatePropertyFilter(v.Properties); e != nil {
		return v, nil, e
	}
	if v.Scope == "" {
		v.Scope = "all"
	}
	if v.Collapse == "versions" {
		v.Collapse = "works"
	}
	if v.Collapse == "" {
		v.Collapse = "series"
	}
	if v.Sort == "" {
		v.Sort = "indexed_at"
	}
	if v.Order == "" {
		v.Order = "desc"
	}
	if v.Page == 0 {
		v.Page = 1
	}
	if v.PageSize == 0 {
		v.PageSize = 24
	}
	if v.Page < 1 || v.Page > 10000000 || v.PageSize < 1 || v.PageSize > 100 || !one(v.Scope, "all", "favorites", "missing_metadata", "missing", "corrupt", "hidden") || !one(v.Collapse, "none", "works", "series") || !one(v.Sort, "title", "indexed_at") || !one(v.Order, "asc", "desc") || !one(v.FileState, "", "available", "missing", "corrupt", "needs_review") {
		return v, nil, model.ErrInput
	}
	n, e := Parse(v.Query)
	return v, n, e
}
func one(v string, all ...string) bool {
	for _, s := range all {
		if v == s {
			return true
		}
	}
	return false
}
func (s *Service) Search(ctx context.Context, a string, v model.SearchInput) (model.SearchResult, error) {
	if v.Visibility != "" || v.MetadataState != "" || v.IncludeDisabled {
		return model.SearchResult{}, model.ErrInput
	}
	v, n, e := Validate(v)
	if e != nil {
		return model.SearchResult{}, e
	}
	return s.repo.Search(ctx, a, v, n)
}
func (s *Service) Matches(ctx context.Context, a string, v model.SearchInput, kind, id string) (model.MatchResult, error) {
	if v.Visibility != "" || v.MetadataState != "" || v.IncludeDisabled {
		return model.MatchResult{}, model.ErrInput
	}
	v, n, e := Validate(v)
	if e != nil {
		return model.MatchResult{}, e
	}
	if kind == "version_group" {
		return model.MatchResult{}, model.ErrOrganizationModelChanged
	}
	if kind == "book" {
		kind = "edition"
	}
	if !one(kind, "edition", "work", "series") || id == "" || v.Snapshot == "" {
		return model.MatchResult{}, model.ErrInput
	}
	return s.repo.SearchMatches(ctx, a, v, n, kind, id)
}
func (s *Service) List(ctx context.Context, a string) ([]model.SavedSearch, error) {
	return s.repo.SavedSearches(ctx, a)
}
func (s *Service) Save(ctx context.Context, a, id, name string, v model.SearchInput, revision int64) (model.SavedSearch, error) {
	if !one(v.Visibility, "", "active", "trash", "all") || !one(v.MetadataState, "", "none", "matched", "needs_review") {
		return model.SavedSearch{}, model.ErrInput
	}
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 200 || strings.TrimSpace(name) == "" || strings.ContainsRune(name, 0) || revision < 0 {
		return model.SavedSearch{}, model.ErrInput
	}
	v, _, e := Validate(v)
	if e != nil {
		return model.SavedSearch{}, e
	}
	v.Page = 0
	v.Snapshot = ""
	return s.repo.SaveSearch(ctx, a, id, strings.TrimSpace(name), v, revision)
}
func (s *Service) Delete(ctx context.Context, a, id string, revision int64) error {
	if revision < 1 {
		return model.ErrInput
	}
	return s.repo.DeleteSearch(ctx, a, id, revision)
}

func (s *Service) Admin(ctx context.Context, a string, v model.SearchInput) (model.SearchResult, error) {
	if v.Visibility == "" {
		v.Visibility = "active"
	}
	if !one(v.Visibility, "active", "trash", "all") || !one(v.MetadataState, "", "none", "matched", "needs_review") {
		return model.SearchResult{}, model.ErrInput
	}
	if v.PageSize == 0 {
		v.PageSize = 50
	}
	v.Admin = true
	v.Collapse = "none"
	v, n, e := Validate(v)
	if e != nil {
		return model.SearchResult{}, e
	}
	return s.repo.Search(ctx, a, v, n)
}
