package auth

import (
	"context"

	"nyamangaserver.local/server/internal/model"
)

func (s *Service) ClientIdentity(ctx context.Context) (model.ClientIdentity, error) {
	return s.store.ClientIdentity(ctx)
}

func (s *Service) ClientConfiguration(ctx context.Context) (model.ClientConfiguration, error) {
	return s.store.ClientConfiguration(ctx)
}
