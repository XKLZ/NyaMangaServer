package recommendation

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Runner interface {
	Enabled() bool
	Call(context.Context, string, any, any) (Resources, error)
	Close() error
}
type Catalog interface {
	Image(context.Context, string, int64, int, bool) ([]byte, string, error)
}
type Service struct {
	operationMu  sync.Mutex
	operations   map[int64]featureOperation
	operationID  int64
	repo         Repository
	catalog      Catalog
	runner       Runner
	data         string
	logger       *slog.Logger
	wake         chan struct{}
	mu           sync.Mutex
	cancel       context.CancelFunc
	done         chan struct{}
	active       string
	activeCancel context.CancelFunc
	seriesMu     sync.Mutex
	capabilities map[string]any
}

func New(repo Repository, catalog Catalog, runner Runner, data string, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, catalog: catalog, runner: runner, data: data, logger: logger, wake: make(chan struct{}, 1)}
}
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) Capabilities(ctx context.Context) map[string]any {
	out := map[string]any{"resource_state": "not_configured", "enabled": s.runner != nil && s.runner.Enabled(), "available": false, "algorithm_version": Algorithm, "score_semantics": "matching_signal_not_probability", "parameters": map[string]any{"strong_threshold": .75, "candidate_threshold": .65, "strategy": "sscd_union_fixed_v2"}, "nas_performance_verified": false}
	workEnabled := s.requireFeature(ctx, model.EnhancementWork) == nil
	seriesEnabled := s.requireFeature(ctx, model.EnhancementSeries) == nil
	out["work_enabled"], out["series_enabled"] = workEnabled, seriesEnabled
	if !workEnabled && !seriesEnabled {
		out["enabled"] = false
	}
	_, discovery := s.repo.(DiscoveryRepository)
	out["discovery"] = discovery
	out["proposals"] = discovery
	_, series := s.repo.(SeriesRepository)
	out["series_discovery"] = series
	out["series_proposals"] = series
	if s.repo != nil {
		if index, e := s.repo.RecommendationIndexState(ctx); e == nil {
			out["index_state"] = index
		} else {
			out["index_state"] = map[string]any{"state": "unavailable"}
		}
	}
	if s.runner == nil || !s.runner.Enabled() {
		out["reason"] = "not_configured"
		return out
	}
	// Read-only status never launches Python or probes a model.
	out["reason"] = "runtime_unverified"
	out["resource_state"] = "configured_unverified"
	if probe, ok := s.runner.(interface{ ConfigurationState() string }); ok {
		state := probe.ConfigurationState()
		out["resource_state"] = state
		if state == "unavailable" {
			out["available"] = false
			out["reason"] = "runtime_unavailable"
		}
	}
	if !workEnabled && !seriesEnabled {
		out["available"] = false
		out["reason"] = "enhancement_disabled"
	}
	return out
}

func (s *Service) StartJob(ctx context.Context, ids []string) (*model.Job, error) {
	return s.create(ctx, ids, "")
}
func (s *Service) create(ctx context.Context, ids []string, parent string) (*model.Job, error) {
	if len(ids) < 1 || len(ids) > 1000 {
		return nil, model.ErrInput
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || len(id) > 128 || seen[id] {
			return nil, model.ErrInput
		}
		seen[id] = true
	}
	if e := s.requireFeature(ctx, model.EnhancementWork); e != nil {
		return nil, e
	}
	if e := s.probeFeature(ctx, model.EnhancementWork); e != nil {
		return nil, e
	}
	id, e := model.NewID()
	if e != nil {
		return nil, e
	}
	j, e := s.repo.CreateRecommendationJob(ctx, id, parent, ids)
	if e == nil {
		s.Wake()
	}
	return j, e
}
func (s *Service) Control(ctx context.Context, id, action string) error {
	j, e := s.repo.Job(ctx, id)
	if e != nil {
		return e
	}
	if j.Type != "work_recommendation" && j.Type != "series_recommendation" {
		return model.ErrInput
	}
	if e = s.repo.ControlJob(ctx, id, action, ""); e != nil {
		return e
	}
	if action != "resume" {
		s.mu.Lock()
		if s.active == id && s.activeCancel != nil {
			s.activeCancel()
		}
		s.mu.Unlock()
	}
	s.Wake()
	return nil
}
func (s *Service) Retry(ctx context.Context, id string, ordinals []int) (*model.Job, error) {
	j, e := s.repo.Job(ctx, id)
	if e != nil {
		return nil, e
	}
	if j.Type == "series_recommendation" {
		if len(ordinals) > 0 {
			return nil, model.ErrInput
		}
		if j.State == "queued" || j.State == "running" || j.State == "paused" {
			return nil, model.ErrConflict
		}
		if j.LibraryID == "" {
			return nil, model.ErrInput
		}
		return s.StartSeries(ctx, j.LibraryID, j.ID)
	}
	if d, ok := s.repo.(DiscoveryRepository); ok {
		run, err := d.DiscoveryRun(ctx, id)
		if err != nil {
			return nil, err
		}
		if run != nil {
			if len(ordinals) > 0 {
				return nil, model.ErrInput
			}
			if j.State == "queued" || j.State == "running" || j.State == "paused" {
				return nil, model.ErrConflict
			}
			return s.startDiscovery(ctx, run.LibraryID, id)
		}
	}
	if j.Type != "work_recommendation" || len(ordinals) > 1000 {
		return nil, model.ErrInput
	}
	if j.State == "queued" || j.State == "running" || j.State == "paused" {
		return nil, model.ErrConflict
	}
	selected := map[int]bool{}
	for _, ordinal := range ordinals {
		if ordinal < 1 || selected[ordinal] {
			return nil, model.ErrInput
		}
		selected[ordinal] = true
	}
	matched := 0
	ids := []string{}
	for page := 1; ; page++ {
		items, e := s.repo.Items(ctx, id, page, 100, false)
		if e != nil {
			return nil, e
		}
		for _, item := range items {
			chosen := selected[item.Ordinal] || len(selected) == 0 && item.State != "completed"
			if !chosen {
				continue
			}
			if item.State == "completed" {
				return nil, model.ErrInput
			}
			matched++
			if item.BookID != nil {
				ids = append(ids, *item.BookID)
			}
		}
		if len(items) < 100 {
			break
		}
	}
	if len(selected) > 0 && matched != len(selected) {
		return nil, model.ErrInput
	}
	return s.create(ctx, ids, id)
}
func (s *Service) Page(ctx context.Context, job, edition, tier string, page, size int) (Page, error) {
	if page < 1 || page > 1000000 || size < 1 || size > 50 || (tier != "" && tier != "strong" && tier != "supplementary") {
		return Page{}, model.ErrInput
	}
	return s.repo.RecommendationPage(ctx, job, edition, tier, page, size)
}
func validNote(s string) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= 2000 && len(s) <= 8000
}
func (s *Service) Feedback(ctx context.Context, account, id string, in Feedback) error {
	if !slices.Contains([]string{"same_work", "same_series", "unrelated", "uncertain"}, in.Relation) || in.Expected < 1 || !validNote(in.Note) || len(in.Labels) > 16<<10 {
		return model.ErrInput
	}
	if len(in.Labels) > 0 {
		var value map[string]any
		if json.Unmarshal(in.Labels, &value) != nil || value == nil {
			return model.ErrInput
		}
	}
	return s.repo.RecommendationFeedback(ctx, account, id, in)
}
func (s *Service) Preview(ctx context.Context, account, id string, c model.WorkCommand) (Preview, error) {
	if len(c.Expected) > 2500 || len(id) > 128 || len(c.EditionID) > 128 || len(c.TargetWorkID) > 128 || len(c.RemainingPreferredEditionID) > 128 || !utf8.ValidString(c.RemainingPreferredEditionID) {
		return Preview{}, model.ErrInput
	}
	return s.repo.RecommendationPreview(ctx, account, id, c, organization.PlanWork)
}
func (s *Service) Accept(ctx context.Context, account, id string, in AcceptInput) (model.WorkResult, error) {
	if in.Revision < 1 || !validNote(in.Note) || len(in.Command.Expected) > 2500 {
		return model.WorkResult{}, model.ErrInput
	}
	return s.repo.AcceptRecommendation(ctx, account, id, in, organization.PlanWork)
}
func (s *Service) Miss(ctx context.Context, account, job string, in MissInput) error {
	if in.QueryID == "" || in.TargetID == "" || !validNote(in.Note) {
		return model.ErrInput
	}
	return s.repo.RecommendationMiss(ctx, account, job, in)
}
func (s *Service) Start(parent context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.loop(ctx)
}
func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done == nil {
		if s.runner != nil {
			return s.runner.Close()
		}
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) Drain(ctx context.Context) error {
	for {
		s.mu.Lock()
		id, cancel := s.active, s.activeCancel
		s.mu.Unlock()
		if id == "" {
			return nil
		}
		j, e := s.repo.Job(ctx, id)
		if e == nil && j.State != "paused" && j.State != "cancelled" {
			return nil
		}
		if cancel != nil {
			cancel()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
func (s *Service) loop(ctx context.Context) {
	defer close(s.done)
	defer func() {
		if s.runner != nil {
			_ = s.runner.Close()
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if !model.AutomaticWorkAllowed(ctx, s.repo) {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.wake:
			}
			continue
		}
		if d, ok := s.repo.(DiscoveryRepository); ok && s.backgroundAllowed(ctx, model.EnhancementWork) == nil {
			if opctx, done, err := s.beginFeature(ctx, model.EnhancementWork); err == nil {
				refreshed, err := d.RebuildDiscoveryPending(opctx)
				done()
				if err == nil && refreshed {
					continue
				}
			}
		}
		if d, ok := s.repo.(SeriesRepository); ok && s.runner != nil && s.runner.Enabled() && s.backgroundAllowed(ctx, model.EnhancementSeries) == nil {
			if opctx, done, err := s.beginFeature(ctx, model.EnhancementSeries); err == nil {
				refreshed := s.refreshSeries(opctx, d)
				done()
				if refreshed {
					continue
				}
			}
		}
		jobs, e := s.repo.RecommendationJobs(ctx)
		if e == nil && len(jobs) > 0 {
			s.execute(ctx, jobs[0].ID)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
	}
}
func (s *Service) execute(parent context.Context, id string) {
	if r, ok := s.repo.(interface {
		BeginHealthIO(context.Context) (func(), error)
	}); ok {
		release, e := r.BeginHealthIO(parent)
		if e != nil {
			return
		}
		defer release()
	}

	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	s.active = id
	s.activeCancel = cancel
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); s.active = ""; s.activeCancel = nil; s.mu.Unlock() }()
	scopedContext, contextErr := s.repo.JobContext(ctx, id)
	if contextErr != nil {
		return
	}
	ctx = scopedContext
	j, e := s.repo.Claim(ctx, id)
	if e != nil {
		return
	}
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current, e := s.repo.Job(ctx, id)
				if e != nil || current.Generation != j.Generation || current.State != "running" {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-watchDone }()
	fail := func(code string) {
		if ctx.Err() == nil {
			_ = s.repo.FinishBackground(ctx, *j, code)
		}
	}
	defer func() {
		if recover() != nil {
			s.logger.Error("recommendation worker failed", "job_id", id)
			fail("worker_failed")
		}
	}()
	if j.Type == "series_recommendation" {
		if d, ok := s.repo.(SeriesRepository); ok {
			s.executeSeries(ctx, *j, d)
		} else {
			fail("series_runtime_unavailable")
		}
		return
	}
	if d, ok := s.repo.(DiscoveryRepository); ok {
		mode, err := d.DiscoveryRun(ctx, id)
		if err != nil {
			fail("discovery_state_unavailable")
			return
		}
		if mode != nil {
			s.discover(ctx, *j, d)
			return
		}
	}
	if s.runner == nil || !s.runner.Enabled() {
		_ = s.repo.ControlJob(ctx, id, "pause", "recommendation_runtime_unavailable")
		return
	}
	pool, e := s.repo.RecommendationPool(ctx, id)
	if e != nil {
		fail("input_unavailable")
		return
	}
	invalidInputs := map[string]bool{}
	features := map[string]map[string]any{"phash": {}, "sscd": {}, "minilm": {}}
	for position, input := range pool.Editions {
		if ctx.Err() != nil {
			return
		}
		if e = s.repo.ManagementGuard(ctx, input.Book.ID); e != nil {
			continue
		}
		raw, imageError := s.image(ctx, input)
		if errors.Is(imageError, ErrStale) {
			invalidInputs[input.Book.ID] = true
			_ = s.repo.RecommendationProgress(ctx, *j, position+1, Resources{}, map[string]string{input.Book.ID: "source_input_changed"})
			continue
		}
		payload := map[string]any{"book": input.Book}
		if len(raw) > 0 {
			payload["image_base64"] = base64.StdEncoding.EncodeToString(raw)
		}
		var result FeatureResult
		resources, e := s.callFeature(ctx, model.EnhancementWork, "features", payload, &result)
		if e != nil && ctx.Err() == nil && len(raw) > 0 {
			delete(payload, "image_base64")
			first := resources
			resources, e = s.callFeature(ctx, model.EnhancementWork, "features", payload, &result)
			resources = addResources(first, resources)
		}
		if ctx.Err() != nil {
			return
		}
		if e != nil {
			_ = s.repo.RecommendationProgress(ctx, *j, 0, resources, nil)
			_ = s.repo.ControlJob(ctx, id, "pause", "recommendation_runtime_unavailable")
			return
		}
		if e = s.repo.SaveRecommendationFeatures(ctx, *j, input, result); e != nil {
			if errors.Is(e, ErrStale) || errors.Is(e, model.ErrDisabled) || errors.Is(e, model.ErrTrashed) || errors.Is(e, model.ErrNotFound) {
				continue
			}
			fail("storage_error")
			return
		}
		for algorithm, feature := range result.Features {
			if algorithm == "phash" {
				var value string
				if json.Unmarshal(feature.Value, &value) == nil {
					features[algorithm][input.Book.ID] = value
				}
			} else {
				var value []float32
				if json.Unmarshal(feature.Value, &value) == nil {
					features[algorithm][input.Book.ID] = PackVector(value)
				}
			}
		}
		if e = s.repo.RecommendationProgress(ctx, *j, position+1, resources, nil); e != nil {
			return
		}
	}
	byID := map[string]InputEdition{}
	for _, input := range pool.Editions {
		byID[input.Book.ID] = input
	}
	for {
		if ctx.Err() != nil {
			return
		}
		items, e := s.repo.Items(ctx, id, 1, 1, true)
		if e != nil {
			fail("storage_error")
			return
		}
		if len(items) == 0 {
			fail("")
			return
		}
		item := items[0]
		if item.BookID == nil {
			_ = s.repo.FailBackgroundItem(ctx, *j, item, "resource_removed")
			continue
		}
		input, ok := byID[*item.BookID]
		if !ok || invalidInputs[*item.BookID] {
			_ = s.repo.FailBackgroundItem(ctx, *j, item, "input_changed")
			continue
		}
		books := []BookInput{}
		works := []WorkInput{}
		selected := map[string]bool{}
		for _, ed := range pool.Editions {
			if ed.Book.LibraryID == input.Book.LibraryID && !invalidInputs[ed.Book.ID] {
				books = append(books, ed.Book)
				selected[ed.Book.ID] = true
			}
		}
		for _, work := range pool.Works {
			valid := len(work.EditionIDs) > 0
			for _, id := range work.EditionIDs {
				if !selected[id] {
					valid = false
					break
				}
			}
			if valid {
				works = append(works, work.WorkInput)
			}
		}
		subset := map[string]map[string]any{}
		for algorithm, values := range features {
			subset[algorithm] = map[string]any{}
			for id, value := range values {
				if selected[id] {
					subset[algorithm][id] = value
				}
			}
		}
		var result Result
		resources, e := s.callFeature(ctx, model.EnhancementWork, "recommend", map[string]any{"books": books, "works": works, "query_edition_id": *item.BookID, "features": subset}, &result)
		if ctx.Err() != nil {
			return
		}
		if e != nil {
			_ = s.repo.RecommendationProgress(ctx, *j, 0, resources, nil)
			_ = s.repo.ControlJob(ctx, id, "pause", "recommendation_runtime_unavailable")
			return
		}
		if e = s.repo.RecommendationProgress(ctx, *j, len(pool.Editions), resources, nil); e != nil {
			return
		}
		e = s.repo.PublishRecommendations(ctx, *j, item, result)
		if errors.Is(e, ErrStale) || errors.Is(e, model.ErrNotFound) || errors.Is(e, model.ErrDisabled) || errors.Is(e, model.ErrTrashed) {
			e = s.repo.FailBackgroundItem(ctx, *j, item, "recommendation_input_changed")
		}
		if e != nil {
			if errors.Is(e, model.ErrStaleJob) {
				return
			}
			fail("storage_error")
			return
		}
	}
}
func (s *Service) image(ctx context.Context, input InputEdition) ([]byte, error) {
	if input.Material == nil {
		if s.catalog == nil {
			return nil, nil
		}
		raw, _, e := s.catalog.Image(ctx, input.Book.ID, input.ContentRevision, 1, false)
		if errors.Is(e, comic.ErrChanged) {
			return nil, ErrStale
		}
		if e != nil || len(raw) > 10<<20 {
			return nil, nil
		}
		return raw, nil
	}
	a := input.Material
	key := filepath.FromSlash(a.Key)
	if filepath.IsAbs(key) || key == "." || strings.HasPrefix(key, "..") || !strings.HasPrefix(filepath.ToSlash(key), "assets/") {
		return nil, nil
	}
	path := filepath.Join(s.data, key)
	if appdata.NoSymlinks(path) != nil {
		return nil, nil
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, nil
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() != a.Size || info.Size() > 10<<20 {
		return nil, nil
	}
	raw, e := io.ReadAll(io.LimitReader(f, (10<<20)+1))
	if e != nil {
		return nil, nil
	}
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != a.Digest {
		return nil, ErrStale
	}
	return raw, nil
}

func (s *Service) Misses(ctx context.Context, job string, page, size int) (MissPage, error) {
	if page < 1 || page > 1000000 || size < 1 || size > 50 {
		return MissPage{}, model.ErrInput
	}
	return s.repo.RecommendationMisses(ctx, job, page, size)
}

// Export is deliberately paged. It is feedback evidence, not an application backup.
func (s *Service) Export(ctx context.Context, job string, page, size int) (map[string]any, error) {
	records, e := s.Page(ctx, job, "", "", page, size)
	if e != nil {
		return nil, e
	}
	misses, e := s.Misses(ctx, job, page, size)
	if e != nil {
		return nil, e
	}
	return map[string]any{"schema": "work-recommendation-feedback-v1", "algorithm_version": Algorithm, "job_id": job, "recommendations": records, "misses": misses, "seen_development_pairs_are_not_holdout": true}, nil
}

func addResources(a, b Resources) Resources {
	return Resources{CacheHits: a.CacheHits + b.CacheHits, ComputedInputs: a.ComputedInputs + b.ComputedInputs, ModelLoads: a.ModelLoads + b.ModelLoads, InferenceSeconds: a.InferenceSeconds + b.InferenceSeconds, OperationSeconds: a.OperationSeconds + b.OperationSeconds, PeakRSS: max(a.PeakRSS, b.PeakRSS)}
}
