// Package importflow orchestrates durable import batches. Waiting for a human
// never retains a transaction, file handle, or inference worker.
package importflow

import (
	"context"
	"errors"
	"log/slog"
	"nyamangaserver.local/server/internal/library"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	rec "nyamangaserver.local/server/internal/recommendation"
	"strings"
	"time"
)

type Repository interface {
	OrganizationReview(context.Context, string) (model.OrganizationReview, error)
	CompleteOrganizationReview(context.Context, string, model.CompleteOrganizationReview) error
	ImportSelections(context.Context, string, string, int, int) (model.ImportPage[model.ImportSelection], error)
	ImportEditor(context.Context, string, string) (model.ImportEditor, error)
	ReviewImportManual(context.Context, string, string, model.ImportManualReview) error

	ImportJobBatch(context.Context, string) (string, error)
	RetryImportFiles(context.Context, string, int64, []string) (string, error)
	ImportCandidateDetail(context.Context, string, string) (*model.ImportCandidate, error)
	CheckImportProposal(context.Context, string, string, string, ...string) error
	ImportWorkProposals(context.Context, string, string, int, int, ...bool) (rec.ProposalPage, error)
	ImportSeriesProposals(context.Context, string, string, int, int, ...bool) (rec.SeriesProposalPage, error)
	CreateImport(context.Context, string, string, string) (*model.ImportBatch, error)
	ImportBatch(context.Context, string) (*model.ImportBatch, error)
	ImportBatches(context.Context, int, int, ...model.ImportFilter) (model.ImportPage[model.ImportBatch], error)
	ImportMembers(context.Context, string, string, int, int, ...string) (model.ImportPage[model.ImportMember], error)
	ImportRequests(context.Context) ([]model.ImportRequest, error)
	MaterializeImport(context.Context, model.ImportRequest) error
	ReconcileImports(context.Context) error
	ImportRequestFailed(context.Context, model.ImportRequest, string) error
	ReviewImport(context.Context, string, []model.ImportReview) error
	RefetchImport(context.Context, string, string, string, int64) (string, error)
	AdvanceImport(context.Context, string, int64) error
	ControlImport(context.Context, string, string, int64) error
}
type Service struct {
	covers    *coverCache
	Interrupt func(context.Context, model.Job, string) error
	repo      Repository
	catalog   *library.Service
	logger    *slog.Logger
	wake      chan struct{}
	done      chan struct{}
	cancel    context.CancelFunc
	notify    func()
}

func New(r Repository, c *library.Service, l *slog.Logger, notify func()) *Service {
	return &Service{repo: r, catalog: c, logger: l, wake: make(chan struct{}, 1), notify: notify}
}
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			if e := s.Tick(ctx); e != nil && ctx.Err() == nil {
				s.logger.Error("import orchestration interrupted", "reason", "checkpoint_unavailable")
			}
			select {
			case <-ctx.Done():
				return
			case <-s.wake:
			case <-t.C:
			}
		}
	}()
}
func (s *Service) Shutdown(ctx context.Context) error {
	if s.cancel == nil {
		return nil
	}
	s.cancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) Tick(ctx context.Context) error {
	if !model.AutomaticWorkAllowed(ctx, s.repo) {
		return nil
	}
	if r, ok := s.repo.(interface {
		BeginHealthIO(context.Context) (func(), error)
	}); ok {
		release, e := r.BeginHealthIO(ctx)
		if e != nil {
			if errors.Is(e, model.ErrHealthMaintenance) {
				return nil
			}
			return e
		}
		defer release()
	}

	if e := s.repo.ReconcileImports(ctx); e != nil {
		return e
	}
	requests, e := s.repo.ImportRequests(ctx)
	if e != nil {
		return e
	}
	for _, r := range requests {
		if r.Stage == "scan" {
			if e = s.catalog.ValidateScan(ctx, r.LibraryID); e != nil {
				if err := s.repo.ImportRequestFailed(ctx, r, "scan_root_unavailable"); err != nil {
					return err
				}
				continue
			}
		}
		if r.Stage == "scan" && r.Parent != "" {
			_, e = s.catalog.RetryScanWithID(ctx, r.Parent, r.Ordinals, r.JobID)
		} else {
			e = s.repo.MaterializeImport(ctx, r)
		}
		if errors.Is(e, model.ErrEnhancementDisabled) {
			if repo, ok := s.repo.(interface {
				PauseImportForEnhancement(context.Context, string, string) error
			}); ok {
				if err := repo.PauseImportForEnhancement(ctx, r.BatchID, r.Stage); err != nil {
					return err
				}
				continue
			}
			return e
		}
		if e != nil {
			if errors.Is(e, model.ErrBusy) || errors.Is(e, model.ErrJobActive) || errors.Is(e, model.ErrScanActive) {
				continue
			}
			if err := s.repo.ImportRequestFailed(ctx, r, "child_creation_failed"); err != nil {
				return err
			}
			continue
		}
		if s.notify != nil {
			s.notify()
		}
	}
	return nil
}
func (s *Service) Create(ctx context.Context, lib, key string) (*model.ImportBatch, error) {
	if lib == "" || len(lib) > 128 || len(key) < 8 || len(key) > 128 || strings.TrimSpace(key) != key {
		return nil, model.ErrInput
	}
	if e := s.catalog.ValidateScan(ctx, lib); e != nil {
		return nil, e
	}
	id, e := model.NewID()
	if e != nil {
		return nil, e
	}
	b, e := s.repo.CreateImport(ctx, id, lib, key)
	if e == nil {
		s.Wake()
	}
	return b, e
}
func (s *Service) Batch(ctx context.Context, id string) (*model.ImportBatch, error) {
	return s.repo.ImportBatch(ctx, id)
}
func (s *Service) List(ctx context.Context, p, n int, filters ...model.ImportFilter) (model.ImportPage[model.ImportBatch], error) {
	if len(filters) > 1 {
		return model.ImportPage[model.ImportBatch]{}, model.ErrInput
	}
	if len(filters) == 1 {
		f := filters[0]
		if len(f.LibraryID) > 128 {
			return model.ImportPage[model.ImportBatch]{}, model.ErrInput
		}
		switch f.State {
		case "", "processing", "review", "paused", "failed", "completed", "cancelled":
		default:
			return model.ImportPage[model.ImportBatch]{}, model.ErrInput
		}
	}
	return s.repo.ImportBatches(ctx, p, n, filters...)
}
func (s *Service) Members(ctx context.Context, id, filter string, p, n int, stages ...string) (model.ImportPage[model.ImportMember], error) {
	return s.repo.ImportMembers(ctx, id, filter, p, n, stages...)
}
func (s *Service) Review(ctx context.Context, id string, in []model.ImportReview) error {
	if len(in) == 0 || len(in) > 1000 {
		return model.ErrInput
	}
	seen := map[string]bool{}
	for _, r := range in {
		if r.ID == "" || r.ExpectedRevision < 1 || seen[r.ID] {
			return model.ErrInput
		}
		seen[r.ID] = true
	}
	return s.repo.ReviewImport(ctx, id, in)
}
func (s *Service) Refetch(ctx context.Context, batch, id, url string, revision int64) (string, error) {
	if url != "" {
		if _, _, _, e := provider.Gallery(url); e != nil {
			return "", model.ErrInput
		}
	}
	if revision < 1 {
		return "", model.ErrInput
	}
	job, e := s.repo.RefetchImport(ctx, batch, id, url, revision)
	if e == nil {
		s.Wake()
	}
	return job, e
}
func (s *Service) Control(ctx context.Context, id, action string, revision int64) error {
	var e error
	var before *model.ImportBatch
	if action == "pause" || action == "cancel" || action == "recheck" {
		before, e = s.repo.ImportBatch(ctx, id)
		if e != nil {
			return e
		}
	}
	if action == "continue" {
		e = s.repo.AdvanceImport(ctx, id, revision)
	} else {
		e = s.repo.ControlImport(ctx, id, action, revision)
	}
	if e == nil {
		if before != nil && s.Interrupt != nil {
			for _, j := range before.Jobs {
				if j.State == "queued" || j.State == "running" || j.State == "paused" {
					interrupt := action
					if interrupt == "recheck" {
						interrupt = "cancel"
					}
					if err := s.Interrupt(ctx, j, interrupt); err != nil {
						return err
					}
				}
			}
		}
		s.Wake()
		if s.notify != nil {
			s.notify()
		}
	}
	return e
}

func (s *Service) WorkProposals(ctx context.Context, id, state string, p, n int, alternatives ...bool) (rec.ProposalPage, error) {
	if state == "" {
		state = "pending"
	}
	switch state {
	case "pending", "accepted", "dismissed", "undone":
	default:
		return rec.ProposalPage{}, model.ErrInput
	}
	if _, e := s.repo.ImportBatch(ctx, id); e != nil {
		return rec.ProposalPage{}, e
	}
	return s.repo.ImportWorkProposals(ctx, id, state, p, n, alternatives...)
}
func (s *Service) SeriesProposals(ctx context.Context, id, state string, p, n int, alternatives ...bool) (rec.SeriesProposalPage, error) {
	if state == "" {
		state = "pending"
	}
	switch state {
	case "pending", "accepted", "dismissed", "undone":
	default:
		return rec.SeriesProposalPage{}, model.ErrInput
	}
	if _, e := s.repo.ImportBatch(ctx, id); e != nil {
		return rec.SeriesProposalPage{}, e
	}
	return s.repo.ImportSeriesProposals(ctx, id, state, p, n, alternatives...)
}
func (s *Service) CheckProposal(ctx context.Context, batch, stage, id string, actions ...string) error {
	return s.repo.CheckImportProposal(ctx, batch, stage, id, actions...)
}

func (s *Service) JobBatch(ctx context.Context, id string) (string, error) {
	return s.repo.ImportJobBatch(ctx, id)
}

func (s *Service) RetryFiles(ctx context.Context, id string, revision int64, ids []string) (string, error) {
	if len(ids) == 0 || len(ids) > 1000 {
		return "", model.ErrInput
	}
	job, e := s.repo.RetryImportFiles(ctx, id, revision, ids)
	if e == nil {
		s.Wake()
	}
	return job, e
}
func (s *Service) Candidate(ctx context.Context, batch, id string) (*model.ImportCandidate, error) {
	return s.repo.ImportCandidateDetail(ctx, batch, id)
}

func (s *Service) Selections(ctx context.Context, batch, filter string, page, size int) (model.ImportPage[model.ImportSelection], error) {
	return s.repo.ImportSelections(ctx, batch, filter, page, size)
}
func (s *Service) Editor(ctx context.Context, batch, id string) (model.ImportEditor, error) {
	return s.repo.ImportEditor(ctx, batch, id)
}
func (s *Service) ReviewManual(ctx context.Context, batch, id string, in model.ImportManualReview) error {
	if in.ExpectedRevision < 1 || in.MemberRevision < 1 || in.CandidateRevision < 0 || in.Fields == nil {
		return model.ErrInput
	}
	if len(in.Fields) > 0 {
		if e := model.ValidateOverrides(in.Fields); e != nil {
			return e
		}
	}
	return s.repo.ReviewImportManual(ctx, batch, id, in)
}

func (s *Service) OrganizationReview(ctx context.Context, id string) (model.OrganizationReview, error) {
	return s.repo.OrganizationReview(ctx, id)
}
func (s *Service) CompleteOrganizationReview(ctx context.Context, id string, in model.CompleteOrganizationReview) error {
	if in.Revision < 1 || len(in.Token) != 64 || (in.Stage != "versions" && in.Stage != "series") {
		return model.ErrInput
	}
	if e := s.repo.CompleteOrganizationReview(ctx, id, in); e != nil {
		return e
	}
	s.Wake()
	if s.notify != nil {
		s.notify()
	}
	return nil
}
