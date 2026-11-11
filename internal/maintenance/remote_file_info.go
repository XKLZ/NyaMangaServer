package maintenance

import (
	"context"
	"nyamangaserver.local/server/internal/model"
)

func (s *Service) BackfillRemoteFileInfo(ctx context.Context, in model.RemoteFileInfoBackfill) (model.RemoteFileInfoReport, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.RemoteFileInfoReport{}, e
	}
	return s.repo.BackfillRemoteFileInfo(ctx, in)
}
