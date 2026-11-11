package userstate

import (
	"context"
	"nyamangaserver.local/server/internal/model"
	"regexp"
)

var readingKey = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

func (s *Service) Reading(ctx context.Context, id string) (model.ReadingState, error) {
	return s.repo.ReadingState(ctx, id)
}
func (s *Service) WriteReading(ctx context.Context, id string, in model.ReadingWrite) (model.ReadingState, error) {
	if in.Expected < 0 || in.ContentRevision < 1 || !readingKey.MatchString(in.IdempotencyKey) {
		return model.ReadingState{}, model.ErrInput
	}
	return s.repo.WriteReadingState(ctx, id, in)
}
func (s *Service) StartReading(ctx context.Context, id string, in model.ReadingSessionStart) (model.ReadingSession, error) {
	if in.Expected < 0 || in.ContentRevision < 1 || !readingKey.MatchString(in.IdempotencyKey) {
		return model.ReadingSession{}, model.ErrInput
	}
	return s.repo.StartReadingSession(ctx, id, in)
}
