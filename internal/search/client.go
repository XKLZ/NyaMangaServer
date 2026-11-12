package search

import (
	"context"
	"strings"

	"nyamangaserver.local/server/internal/model"
)

func (s *Service) ClientWorkSearch(ctx context.Context, v model.ClientSearchInput) (model.ClientSearchResult, error) {
	if v.LibraryID == "" || len(v.LibraryID) > 128 || strings.ContainsRune(v.LibraryID, 0) ||
		!one(v.Scope, "", "all", "favorites", "hidden", "continue") {
		return model.ClientSearchResult{}, model.ErrInput
	}
	in, node, e := validate(model.SearchInput{Query: v.Query, Scope: v.Scope, FileState: v.FileState, Properties: v.Properties,
		Sort: v.Sort, Order: v.Order, Page: v.Page, PageSize: v.PageSize, Collapse: "works"}, true)
	if e != nil {
		return model.ClientSearchResult{}, e
	}
	v.Scope, v.Sort, v.Order, v.Page, v.PageSize = in.Scope, in.Sort, in.Order, in.Page, in.PageSize
	return s.repo.ClientWorkSearch(ctx, v, node)
}
