// Package organization owns deterministic Work/Edition and Series transformations.
package organization

import (
	"context"
	"nyamangaserver.local/server/internal/model"
	"strings"
	"unicode/utf8"
)

type Service struct{ repo WorkRepository }

func New(r WorkRepository) *Service { return &Service{repo: r} }
func (s *Service) EditionDetails(ctx context.Context, account, id string) (*model.Edition, model.MetadataDetails, model.UserState, model.WorkContext, error) {
	return s.repo.EditionDetails(ctx, account, id)
}
func unique(v []string) bool {
	seen := map[string]bool{}
	for _, id := range v {
		if id == "" || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func validText(s string, max int, empty bool) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0) && utf8.RuneCountInString(s) <= max && len(s) <= max*4 && (empty || strings.TrimSpace(s) != "")
}
