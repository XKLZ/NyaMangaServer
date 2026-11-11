// Package tagcatalog owns explicit-library tag browsing and human relation rules.
package tagcatalog

import (
	"context"
	"math"
	"nyamangaserver.local/server/internal/model"
	"strings"
	"unicode/utf8"
)

type Repository interface {
	RequireAdministrator(context.Context) error
	SetTagFollow(context.Context, string, string, int64, bool) (model.TagFollow, error)
	TagCatalogWorks(context.Context, string, model.TagCatalogInput) (model.TagWorkPage, error)
	TagCatalog(context.Context, model.TagCatalogInput) (model.TagCatalogPage, error)
	TagCatalogDetail(context.Context, string, model.TagCatalogInput) (model.TagCatalogDetail, error)
	TagCatalogEditions(context.Context, string, model.TagCatalogInput) (model.TagEditionPage, error)
	TagCatalogRelations(context.Context, string, model.TagCatalogInput) (model.TagRelationPage, error)
	DecideCharacterRelation(context.Context, string, string, int64, string) (model.TagRelationDecision, error)
	SetCharacterSuppression(context.Context, string, string, string, int64, bool) (model.CharacterSuppression, error)
}
type Service struct{ repo Repository }

func New(repo Repository) *Service { return &Service{repo} }
func Validate(in model.TagCatalogInput) (model.TagCatalogInput, error) {
	if in.Page == 0 {
		in.Page = 1
	}
	if in.PageSize == 0 {
		in.PageSize = 50
	}
	if in.Sort == "" {
		in.Sort = "name"
	}
	if in.Order == "" {
		in.Order = "asc"
	}
	if in.Partition == "" {
		in.Partition = "all"
	}
	if strings.TrimSpace(in.LibraryID) == "" || in.LibraryID != strings.TrimSpace(in.LibraryID) || len(in.LibraryID) > 128 || len(in.Query) > 512 || !utf8.ValidString(in.Query) || strings.ContainsRune(in.Query, 0) || !utf8.ValidString(in.Namespace) || strings.ContainsRune(in.Namespace, 0) || strings.ContainsRune(in.LibraryID, 0) || len(in.Namespace) > 64 || len(in.Snapshot) > 128 || len(in.RelatedTagID) > 128 || in.Page < 1 || in.Page > 1000000 || in.PageSize < 1 || in.PageSize > 100 || in.Page > 1 && in.Snapshot == "" {
		return in, model.ErrInput
	}
	switch in.View {
	case "", "all", "display":
	default:
		return in, model.ErrInput
	}
	switch in.Category {
	case "", "parody", "character", "artist", "group", "content":
	default:
		return in, model.ErrInput
	}
	switch in.Sort {
	case "name", "usage":
	default:
		return in, model.ErrInput
	}
	if in.Order != "asc" && in.Order != "desc" {
		return in, model.ErrInput
	}
	switch in.Partition {
	case "all", "related", "unclassified", "evidence", "suppressed", "inferred", "competing":
	default:
		return in, model.ErrInput
	}
	if (in.Partition == "related" || in.Partition == "evidence" || in.Partition == "suppressed" || in.Partition == "inferred" || in.Partition == "competing") && in.RelatedTagID == "" {
		return in, model.ErrInput
	}
	return in, nil
}
func (s *Service) Catalog(ctx context.Context, in model.TagCatalogInput) (model.TagCatalogPage, error) {
	v, e := Validate(in)
	if v.View != "" {
		e = model.ErrInput
	}
	if e != nil {
		return model.TagCatalogPage{}, e
	}
	return s.repo.TagCatalog(ctx, v)
}
func (s *Service) Detail(ctx context.Context, id string, in model.TagCatalogInput) (model.TagCatalogDetail, error) {
	v, e := Validate(in)
	if v.View != "" || v.FollowedOnly {
		e = model.ErrInput
	}
	if e != nil {
		return model.TagCatalogDetail{}, e
	}
	return s.repo.TagCatalogDetail(ctx, id, v)
}
func (s *Service) Editions(ctx context.Context, id string, in model.TagCatalogInput) (model.TagEditionPage, error) {
	v, e := Validate(in)
	if v.View != "" || v.FollowedOnly {
		e = model.ErrInput
	}
	if e == nil && v.Sort != "name" {
		e = model.ErrInput
	}
	if e != nil {
		return model.TagEditionPage{}, e
	}
	return s.repo.TagCatalogEditions(ctx, id, v)
}
func (s *Service) Relations(ctx context.Context, id string, in model.TagCatalogInput) (model.TagRelationPage, error) {
	v, e := Validate(in)
	if v.FollowedOnly {
		e = model.ErrInput
	}
	if e != nil {
		return model.TagRelationPage{}, e
	}
	return s.repo.TagCatalogRelations(ctx, id, v)
}
func (s *Service) Decide(ctx context.Context, p, c string, revision int64, decision string) (model.TagRelationDecision, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.TagRelationDecision{}, e
	}
	if revision < 0 || revision == math.MaxInt64 || (decision != "auto" && decision != "confirmed" && decision != "excluded") {
		return model.TagRelationDecision{}, model.ErrInput
	}
	return s.repo.DecideCharacterRelation(ctx, p, c, revision, decision)
}
func (s *Service) Suppress(ctx context.Context, id, p, c string, revision int64, active bool) (model.CharacterSuppression, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.CharacterSuppression{}, e
	}
	if revision < 0 || revision == math.MaxInt64 {
		return model.CharacterSuppression{}, model.ErrInput
	}
	return s.repo.SetCharacterSuppression(ctx, id, p, c, revision, active)
}

// ValidateWorks keeps the new read contract separate from Edition diagnostics.
func ValidateWorks(in model.TagCatalogInput) (model.TagCatalogInput, error) {
	v, e := Validate(in)
	if e == nil && (v.View != "" || v.Sort != "name" || v.Partition != "all" || v.RelatedTagID != "" || v.Category != "" || v.Namespace != "" || v.IncludeUnused || v.FollowedOnly) {
		e = model.ErrInput
	}
	return v, e
}
func (s *Service) Works(ctx context.Context, id string, in model.TagCatalogInput) (model.TagWorkPage, error) {
	v, e := ValidateWorks(in)
	if e != nil {
		return model.TagWorkPage{}, e
	}
	return s.repo.TagCatalogWorks(ctx, id, v)
}

func (s *Service) Follow(ctx context.Context, account, id string, expected int64, followed bool) (model.TagFollow, error) {
	if expected < 0 || expected == math.MaxInt64 || account == "" || id == "" || len(id) > 128 || strings.ContainsRune(id, 0) {
		return model.TagFollow{}, model.ErrInput
	}
	return s.repo.SetTagFollow(ctx, account, id, expected, followed)
}
