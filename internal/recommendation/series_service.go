package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"slices"
	"strings"
	"unicode/utf8"
)

func (s *Service) StartSeries(ctx context.Context, library, parent string) (*model.Job, error) {
	if e := s.requireFeature(ctx, model.EnhancementSeries); e != nil {
		return nil, e
	}
	d, ok := s.repo.(SeriesRepository)
	if !ok || s.runner == nil || !s.runner.Enabled() {
		return nil, ErrUnavailable
	}
	if library == "" || len(library) > 128 {
		return nil, model.ErrInput
	}
	if e := s.requireFeature(ctx, model.EnhancementSeries); e != nil {
		return nil, e
	}
	if e := s.probeFeature(ctx, model.EnhancementSeries); e != nil {
		return nil, e
	}
	id, e := model.NewID()
	if e != nil {
		return nil, e
	}
	j, e := d.CreateSeriesDiscovery(ctx, id, library, parent)
	if e == nil {
		s.Wake()
	}
	return j, e
}
func (s *Service) prepareSeries(ctx context.Context, r SeriesRun, generation int64, checkpoint ...bool) (string, error) {
	books := []BookInput{}
	works := []WorkInput{}
	for _, ed := range r.Pool.Editions {
		books = append(books, ed.Book)
	}
	for _, w := range r.Pool.Works {
		works = append(works, w.WorkInput)
	}
	key := Fingerprint([]any{r.JobID, generation, r.Token, SeriesAlgorithm})
	var out struct {
		Key      string            `json:"pool_key"`
		Works    int               `json:"works"`
		Profiles map[string]string `json:"profiles"`
	}
	_, e := s.callFeature(ctx, model.EnhancementSeries, "series_prepare", map[string]any{"books": books, "works": works, "series": r.Context.Series, "features": r.Features, "pool_key": key}, &out)
	if e == nil && (out.Key != key || out.Works != len(works)) {
		e = ErrWorker
	}
	if e == nil && len(checkpoint) > 0 && checkpoint[0] {
		e = s.restoreSeriesEvidence(ctx, model.Job{ID: r.JobID, LibraryID: r.LibraryID, Generation: generation}, key, out.Profiles)
	}
	return key, e
}
func (s *Service) executeSeries(ctx context.Context, j model.Job, d SeriesRepository) {
	phase := "prepare_features"
	fail := func(e error) {
		if ctx.Err() != nil {
			return
		}
		code, reason := seriesFailureCategory(phase, e)
		if errors.Is(e, ErrSeriesCheckpoint) {
			code, reason = "series_checkpoint_invalid", "invalid_checkpoint"
		}
		if errors.Is(e, ErrStale) || errors.Is(e, model.ErrConflict) {
			_ = s.repo.ControlJob(ctx, j.ID, "pause", "series_inputs_changed_start_new_discovery")
			return
		}
		if errors.Is(e, model.ErrCapacity) || errors.Is(e, ErrWorkerBudget) {
			code, reason = "series_resource_budget_exceeded", "resource_budget"
		}
		if diagnostic, ok := s.repo.(interface {
			SaveSeriesDiagnostic(context.Context, model.Job, string, string) error
		}); ok {
			_ = diagnostic.SaveSeriesDiagnostic(ctx, j, phase, reason)
		}
		s.logger.Error("series discovery interrupted", "job_id", j.ID, "phase", phase, "reason", reason)
		_ = s.repo.FinishBackground(ctx, j, code)
	}
	r, e := d.SeriesRun(ctx, j.ID, false)
	if e != nil {
		fail(e)
		return
	}
	for at := r.FeaturesDone; at < len(r.Pool.Editions); at++ {
		input := r.Pool.Editions[at]
		if ctx.Err() != nil {
			return
		}
		var result FeatureResult
		_, e = s.callFeature(ctx, model.EnhancementSeries, "series_features", map[string]any{"book": input.Book}, &result)
		if e != nil {
			if ctx.Err() == nil {
				if diagnostic, ok := s.repo.(interface {
					SaveSeriesDiagnostic(context.Context, model.Job, string, string) error
				}); ok {
					_ = diagnostic.SaveSeriesDiagnostic(ctx, j, phase, "runtime_unavailable")
				}
				_ = s.repo.ControlJob(ctx, j.ID, "pause", "series_runtime_unavailable")
			}
			return
		}
		var packed *PackedVector
		if f, ok := result.Features["minilm"]; ok {
			var value []float32
			if json.Unmarshal(f.Value, &value) != nil || len(value) != 384 {
				fail(model.ErrInput)
				return
			}
			v := PackVector(value)
			packed = &v
			r.Features[input.Book.ID] = v
		}
		if e = d.SaveSeriesFeature(ctx, j, at, input, packed); e != nil {
			fail(e)
			return
		}
	}
	s.seriesMu.Lock()
	defer s.seriesMu.Unlock()
	phase = "restore_checkpoint"
	key, e := s.prepareSeries(ctx, r, j.Generation, true)
	if e != nil {
		fail(e)
		return
	}
	phase = "compare"
	seen := map[[2]string]bool{}
	for _, edge := range r.Edges {
		seen[edge] = true
	}
	for row := r.NextRow; row < len(r.Pool.Works); row++ {
		if ctx.Err() != nil {
			return
		}
		var block SeriesBlock
		if _, e = s.callFeature(ctx, model.EnhancementSeries, "series_row", map[string]any{"pool_key": key, "row": row}, &block); e != nil {
			fail(e)
			return
		}
		if e = s.checkpointSeries(ctx, j, key); e != nil {
			fail(e)
			return
		}
		if e = d.SaveSeriesBlock(ctx, j, block); e != nil {
			fail(e)
			return
		}
		for _, edge := range block.Edges {
			if !seen[edge] {
				r.Edges = append(r.Edges, edge)
				seen[edge] = true
			}
		}
	}
	phase = "generate_proposals"
	var result SeriesResult
	if _, e = s.callFeature(ctx, model.EnhancementSeries, "series_cluster", map[string]any{"pool_key": key, "edges": r.Edges}, &result); e != nil {
		fail(e)
		return
	}
	if e = s.checkpointSeries(ctx, j, key); e != nil {
		fail(e)
		return
	}
	if result.Algorithm != SeriesAlgorithm {
		fail(ErrWorker)
		return
	}
	phase = "publish"
	if e = d.PublishSeries(ctx, j, r, result, false); e != nil {
		fail(e)
		return
	}
	phase = "finalize_evidence"
	if cache, ok := s.repo.(SeriesIncrementalRepository); ok {
		if e = cache.CompleteSeriesEvidence(ctx, j); e != nil {
			fail(e)
			return
		}
	}
	s.retireSeriesBlocks(ctx)
	_ = s.repo.FinishBackground(ctx, j, "")
}
func (s *Service) refreshSeries(ctx context.Context, d SeriesRepository) bool {
	id, e := d.SeriesRefreshJob(ctx)
	if e != nil || id == "" {
		return false
	}
	r, e := d.SeriesRun(ctx, id, true)
	if e != nil {
		if ctx.Err() == nil {
			_ = d.StopSeriesRefresh(ctx, id, "series_inputs_changed_start_new_discovery")
		}
		return false
	}
	j, e := s.repo.Job(ctx, id)
	if e != nil {
		return false
	}
	s.seriesMu.Lock()
	defer s.seriesMu.Unlock()
	key, e := s.prepareSeries(ctx, r, j.Generation)
	if e != nil {
		if ctx.Err() == nil {
			_ = d.StopSeriesRefresh(ctx, id, "series_refresh_unavailable_start_new_discovery")
		}
		return false
	}
	var result SeriesResult
	if _, e = s.callFeature(ctx, model.EnhancementSeries, "series_cluster", map[string]any{"pool_key": key, "edges": r.Edges}, &result); e != nil {
		if ctx.Err() == nil {
			_ = d.StopSeriesRefresh(ctx, id, "series_refresh_unavailable_start_new_discovery")
		}
		return false
	}
	if e = d.PublishSeries(ctx, *j, r, result, true); e != nil {
		if ctx.Err() == nil {
			_ = d.StopSeriesRefresh(ctx, id, "series_refresh_changed_start_new_discovery")
		}
		return false
	}
	return true
}
func validSeriesSelection(in SeriesSelection) error {
	if in.Revision < 1 || len(in.IDs) > 1000 || len(in.Order) > 1000 {
		return model.ErrInput
	}
	for _, ids := range [][]string{in.IDs, in.Order} {
		seen := map[string]bool{}
		for _, id := range ids {
			if id == "" || len(id) > 128 || seen[id] {
				return model.ErrInput
			}
			seen[id] = true
		}
	}
	if in.Name != nil && (strings.TrimSpace(*in.Name) == "" || utf8.RuneCountInString(*in.Name) > 200) {
		return model.ErrInput
	}
	return nil
}
func (s *Service) ResolveSeries(ctx context.Context, id string, in SeriesSelection) (SeriesProposal, error) {
	ctx, done, err := s.beginFeature(ctx, model.EnhancementSeries)
	if err != nil {
		return SeriesProposal{}, err
	}
	defer done()
	if e := validSeriesSelection(in); e != nil {
		return SeriesProposal{}, e
	}
	d, ok := s.repo.(SeriesRepository)
	if !ok {
		return SeriesProposal{}, ErrUnavailable
	}
	p, e := d.SeriesProposal(ctx, id)
	if e != nil {
		return p, e
	}
	if p.State != "pending" || p.Revision != in.Revision {
		return p, model.ErrConflict
	}
	if len(in.IDs) == 0 {
		return p, model.ErrInput
	}
	for _, i := range in.IDs {
		if !slices.Contains(p.NewIDs, i) {
			return p, model.ErrInput
		}
	}
	if p.Kind == "create_series" && len(in.IDs) < 2 {
		return p, model.ErrInput
	}
	if !s.seriesMu.TryLock() {
		return p, model.ErrBusy
	}
	defer s.seriesMu.Unlock()
	r, e := d.SeriesRun(ctx, p.JobID, false)
	if e != nil {
		return p, e
	}
	j, e := s.repo.Job(ctx, p.JobID)
	if e != nil {
		return p, e
	}
	key, e := s.prepareSeries(ctx, r, j.Generation)
	if e != nil {
		return p, e
	}
	var valid struct {
		Valid bool `json:"valid"`
	}
	if _, e = s.callFeature(ctx, model.EnhancementSeries, "series_validate", map[string]any{"pool_key": key, "work_ids": append(slices.Clone(p.ExistingIDs), in.IDs...)}, &valid); e != nil || !valid.Valid {
		if errors.Is(e, model.ErrEnhancementDisabled) {
			return p, e
		}
		return p, ErrSelection
	}
	return d.ResolveSeries(ctx, id, in)
}
func (s *Service) DecideSeries(ctx context.Context, account, id, action string, in SeriesSelection) (SeriesProposal, error) {
	if action == "accept" {
		child, done, err := s.beginFeature(ctx, model.EnhancementSeries)
		if err != nil {
			return SeriesProposal{}, err
		}
		ctx = child
		defer done()
	}
	if e := validSeriesSelection(in); e != nil {
		return SeriesProposal{}, e
	}
	if !slices.Contains([]string{"accept", "dismiss", "restore", "undo"}, action) {
		return SeriesProposal{}, model.ErrInput
	}
	d, ok := s.repo.(SeriesRepository)
	if !ok {
		return SeriesProposal{}, ErrUnavailable
	}
	if in.Customize {
		if action != "accept" {
			return SeriesProposal{}, model.ErrInput
		}
		p, e := d.SeriesProposal(ctx, id)
		if e != nil {
			return p, e
		}
		if p.Kind != "create_series" {
			return p, model.ErrInput
		}
		if p.State == "pending" {
			if p.Revision != in.Revision {
				return p, model.ErrConflict
			}
			if in.Name == nil || strings.TrimSpace(*in.Name) == "" || utf8.RuneCountInString(strings.TrimSpace(*in.Name)) > 200 || len(in.IDs) < 2 || !sameSeriesSelection(in.IDs, in.Order) {
				return p, model.ErrInput
			}
			seen := map[string]bool{}
			for _, id := range in.IDs {
				if seen[id] || !slices.Contains(p.NewIDs, id) {
					return p, model.ErrInput
				}
				seen[id] = true
			}
		}
		if p.State == "pending" && !sameSeriesSelection(in.IDs, p.NewIDs) {
			if len(in.IDs) < 2 || s.runner == nil || !s.runner.Enabled() {
				return p, model.ErrInput
			}
			if !s.seriesMu.TryLock() {
				return p, model.ErrBusy
			}
			defer s.seriesMu.Unlock()
			r, e := d.SeriesRun(ctx, p.JobID, false)
			if e != nil {
				return p, e
			}
			j, e := s.repo.Job(ctx, p.JobID)
			if e != nil {
				return p, e
			}
			key, e := s.prepareSeries(ctx, r, j.Generation)
			if e != nil {
				return p, e
			}
			var valid struct {
				Valid bool `json:"valid"`
			}
			if _, e = s.callFeature(ctx, model.EnhancementSeries, "series_validate", map[string]any{"pool_key": key, "work_ids": in.IDs}, &valid); e != nil || !valid.Valid {
				if errors.Is(e, model.ErrEnhancementDisabled) {
					return p, e
				}
				return p, ErrSelection
			}
		}
	}
	p, e := d.DecideSeries(ctx, account, id, action, in)
	if e == nil {
		s.Wake()
	}
	return p, e
}
func validSeriesPage(page, size int) bool {
	return page > 0 && page <= 1_000_000 && size > 0 && size <= 100
}
func (s *Service) SeriesPage(ctx context.Context, job, library, kind, state string, page, size int, filters ...SeriesProposalFilter) (SeriesProposalPage, error) {
	if !validSeriesPage(page, size) || !slices.Contains([]string{"", "create_series", "add_to_series"}, kind) {
		return SeriesProposalPage{}, model.ErrInput
	}
	if state == "" {
		state = "pending"
	}
	if !slices.Contains([]string{"pending", "dismissed", "accepted", "undone", "superseded"}, state) {
		return SeriesProposalPage{}, model.ErrInput
	}
	if len(filters) > 1 || (len(filters) == 1 && len(filters[0].TargetSeriesID) > 128) {
		return SeriesProposalPage{}, model.ErrInput
	}
	d, ok := s.repo.(SeriesRepository)
	if !ok {
		return SeriesProposalPage{}, ErrUnavailable
	}
	return d.SeriesProposals(ctx, job, library, kind, state, page, size, filters...)
}
func (s *Service) GetSeriesProposal(ctx context.Context, id string) (SeriesProposal, error) {
	d, ok := s.repo.(SeriesRepository)
	if !ok {
		return SeriesProposal{}, ErrUnavailable
	}
	return d.SeriesProposal(ctx, id)
}
func (s *Service) SeriesMemberPage(ctx context.Context, id string, page, size int) (SeriesMemberPage, error) {
	if !validSeriesPage(page, size) {
		return SeriesMemberPage{}, model.ErrInput
	}
	d, ok := s.repo.(SeriesRepository)
	if !ok {
		return SeriesMemberPage{}, ErrUnavailable
	}
	return d.SeriesMembers(ctx, id, page, size)
}
func (s *Service) SeriesPendingPage(ctx context.Context, id string, page, size int) (SeriesPendingPage, error) {
	if !validSeriesPage(page, size) {
		return SeriesPendingPage{}, model.ErrInput
	}
	d, ok := s.repo.(SeriesRepository)
	if !ok {
		return SeriesPendingPage{}, ErrUnavailable
	}
	return d.SeriesPending(ctx, id, page, size)
}

func sameSeriesSelection(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a = slices.Clone(a)
	b = slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
