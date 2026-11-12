package userstate

import (
	"context"
	"nyamangaserver.local/server/internal/model"
)

type Repository interface {
	ApplyOfflineOperation(context.Context, model.OfflineQueue, model.OfflineOperation, int64) (model.OfflineOperationResult, error)
	OfflineOperationStatus(context.Context, model.OfflineQueue, []string) ([]model.OfflineOperationResult, error)
	ReadingState(context.Context, string) (model.ReadingState, error)
	WriteReadingState(context.Context, string, model.ReadingWrite) (model.ReadingState, error)
	StartReadingSession(context.Context, string, model.ReadingSessionStart) (model.ReadingSession, error)
	SetUserState(context.Context, string, string, model.StatePatch) (model.UserState, error)
	UserState(context.Context, string, string) (model.UserState, error)
}
type Service struct{ repo Repository }

func New(r Repository) *Service { return &Service{r} }
func (s *Service) Set(ctx context.Context, account, id string, p model.StatePatch) (model.UserState, error) {
	if p.Expected < 0 || (p.Favorite == nil && p.Hidden == nil) {
		return model.UserState{}, model.ErrInput
	}
	return s.repo.SetUserState(ctx, account, id, p)
}
func (s *Service) Get(ctx context.Context, account, id string) (model.UserState, error) {
	return s.repo.UserState(ctx, account, id)
}
