package library

import (
	"context"
	"nyamangaserver.local/server/internal/model"
)

// Repository describes the persistence operations required by this service.
type Repository interface {
	ClientReadingContext(context.Context, string, string, string) (model.ClientReadingContext, error)
	SetFileEncoding(context.Context, string, model.EncodingUpdate, model.FileParsing, int) (*model.Edition, error)
	JobContext(context.Context, string) (context.Context, error)
	CoverSelection(context.Context, string) (model.CoverSelection, error)
	SetCover(context.Context, string, int64, model.CoverSelection, *model.Asset) (model.CoverSelection, error)
	Asset(context.Context, string) (model.Asset, error)
	CreateCoverJob(context.Context, string, int64) (*model.Job, error)
	ManagementGuard(context.Context, string) error
	ScanExclusions(context.Context, string) (map[string]bool, error)
	ControlJob(context.Context, string, string, string) error
	CreateScanRetry(context.Context, string, string, string, []model.Item) (*model.Job, error)
	Job(ctx context.Context, id string) (*model.Job, error)
	ActiveJobs(ctx context.Context) ([]model.Job, error)
	Edition(ctx context.Context, id string) (*model.Edition, error)
	EditionAt(ctx context.Context, library, path string) (*model.Edition, error)
	Editions(ctx context.Context, library, state string, page, size int) ([]model.Edition, int, error)
	Cancel(ctx context.Context, id string) error
	Claim(ctx context.Context, id string) (*model.Job, error)
	DeferPeriodic(ctx context.Context, id, code string) error
	DueLibraries(ctx context.Context, now int64) ([]string, error)
	Finalizing(ctx context.Context, j model.Job) error
	Finish(ctx context.Context, j model.Job, allowMissing bool, code string) error
	Freeze(ctx context.Context, j model.Job) error
	Items(ctx context.Context, id string, page, size int, pending bool) ([]model.Item, error)
	Library(ctx context.Context, id string) (*model.Library, error)
	Publish(ctx context.Context, j model.Job, item model.Item, b model.Edition) error
	SaveLibrary(ctx context.Context, v model.Library, expected int64) error
	StageEntries(ctx context.Context, j model.Job, items []model.Item) error
	StartScan(ctx context.Context, id, library string) (*model.Job, error)
	Libraries(ctx context.Context) ([]model.Library, error)
	JobPage(context.Context, string, string, string, int, int) ([]model.Job, int, error)
	Jobs(ctx context.Context, limit int) ([]model.Job, error)
}

func (s *Service) Libraries(ctx context.Context) ([]model.Library, error) {
	return s.store.Libraries(ctx)
}
func (s *Service) Library(ctx context.Context, id string) (*model.Library, error) {
	return s.store.Library(ctx, id)
}
func (s *Service) Jobs(ctx context.Context, limit int) ([]model.Job, error) {
	return s.store.Jobs(ctx, limit)
}
func (s *Service) Job(ctx context.Context, id string) (*model.Job, error) {
	return s.store.Job(ctx, id)
}
func (s *Service) Items(ctx context.Context, id string, page, size int, pending bool) ([]model.Item, error) {
	return s.store.Items(ctx, id, page, size, pending)
}
func (s *Service) Editions(ctx context.Context, library, state string, page, size int) ([]model.Edition, int, error) {
	return s.store.Editions(ctx, library, state, page, size)
}
func (s *Service) Edition(ctx context.Context, id string) (*model.Edition, error) {
	return s.store.Edition(ctx, id)
}
func (s *Service) Pages(ctx context.Context, id string, revision int64, page, size int) ([]model.Page, error) {
	if page < 1 || size < 1 || size > 100 {
		return nil, model.ErrInput
	}
	entries, e := s.directory(ctx, id, revision)
	if e != nil {
		return nil, e
	}
	start := min(len(entries), (page-1)*size)
	end := min(len(entries), start+size)
	out := make([]model.Page, 0, end-start)
	for _, p := range entries[start:end] {
		out = append(out, model.Page{Index: p.Index, Entry: p.Entry, Name: p.Name, MediaType: p.MediaType})
	}
	return out, nil
}

func (s *Service) JobPage(ctx context.Context, kind, state, lib string, page, size int) ([]model.Job, int, error) {
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		return nil, 0, model.ErrInput
	}
	valid := func(value string, allowed []string) bool {
		for _, v := range allowed {
			if v == value {
				return true
			}
		}
		return false
	}
	if !valid(kind, []string{"", "scan", "metadata", "tag_translation", "metadata_preview", "metadata_search", "cover_rebuild", "maintenance", "work_recommendation", "series_recommendation"}) || !valid(state, []string{"", "queued", "running", "paused", "completed", "failed", "cancelled"}) {
		return nil, 0, model.ErrInput
	}
	return s.store.JobPage(ctx, kind, state, lib, page, size)
}

func (s *Service) SourceGuard(ctx context.Context, id string) error {
	return s.store.ManagementGuard(ctx, id)
}
