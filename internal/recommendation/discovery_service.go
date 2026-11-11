package recommendation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
)

func (s *Service) StartDiscovery(ctx context.Context, library string, full ...bool) (*model.Job, error) {
	return s.startDiscovery(ctx, library, "", full...)
}
func (s *Service) startDiscovery(ctx context.Context, library, parent string, full ...bool) (*model.Job, error) {
	d, ok := s.repo.(DiscoveryRepository)
	if !ok {
		return nil, ErrUnavailable
	}
	if library == "" || len(library) > 128 {
		return nil, model.ErrInput
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
	options := []string{parent}
	if len(full) > 0 && full[0] {
		options = append(options, "full")
	}
	j, e := d.CreateDiscoveryJob(ctx, id, library, options...)
	if e == nil {
		s.Wake()
	}
	return j, e
}
func (s *Service) discover(ctx context.Context, j model.Job, d DiscoveryRepository) {
	stage := "loading"
	rowAt := 0
	fail := func(err error) {
		if ctx.Err() != nil {
			return
		}
		code := "discovery_failed"
		switch {
		case errors.Is(err, ErrWorkerBudget), errors.Is(err, model.ErrCapacity):
			code = "discovery_resource_budget_exceeded"
		case errors.Is(err, ErrWorker), errors.Is(err, ErrUnavailable):
			code = "discovery_worker_unavailable"
		case errors.Is(err, model.ErrInput):
			code = "discovery_invalid_checkpoint"
		case errors.Is(err, ErrStale):
			code = "discovery_inputs_changed"
		}
		s.logger.Error("work discovery interrupted", "job_id", j.ID, "stage", stage, "row", rowAt, "reason", code)
		if diagnostics, ok := s.repo.(DiscoveryDiagnosticsRepository); ok {
			_ = diagnostics.SaveDiscoveryDiagnostic(ctx, j, stage, code, rowAt)
		}
		if code == "discovery_worker_unavailable" || code == "discovery_resource_budget_exceeded" || code == "discovery_inputs_changed" {
			_ = s.repo.ControlJob(ctx, j.ID, "pause", code)
		} else {
			_ = s.repo.FinishBackground(ctx, j, code)
		}
	}
	run, e := d.DiscoveryRun(ctx, j.ID)
	if e != nil || run == nil {
		fail(e)
		return
	}
	if run.EvidenceVersion != EvidenceVersion {
		fail(model.ErrInput)
		return
	}
	pool, e := s.repo.RecommendationPool(ctx, j.ID)
	if e != nil {
		fail(e)
		return
	}
	if run.NextRow == len(pool.Editions) {
		stage = "proposals"
		if e = d.PublishDiscoveryProposals(ctx, j); e != nil {
			if errors.Is(e, model.ErrConflict) {
				_ = s.repo.ControlJob(ctx, j.ID, "pause", "organization_changed_resume_to_refresh")
			} else {
				fail(e)
			}
			return
		}
		if diagnostics, ok := s.repo.(DiscoveryDiagnosticsRepository); ok {
			_ = diagnostics.SaveDiscoveryDiagnostic(ctx, j, "", "", 0)
		}
		_ = s.repo.FinishBackground(ctx, j, "")
		return
	}
	if s.runner == nil || !s.runner.Enabled() {
		_ = s.repo.ControlJob(ctx, j.ID, "pause", "recommendation_runtime_unavailable")
		return
	}
	if preprocessing, ok := s.repo.(DiscoveryPreprocessingRepository); ok {
		books := []BookInput{}
		for _, ed := range pool.Editions {
			books = append(books, ed.Book)
		}
		var parsed struct {
			Fingerprints map[string]string `json:"fingerprints"`
			Version      string            `json:"version"`
		}
		if _, e = s.callFeature(ctx, model.EnhancementWork, "discovery_preprocessing", map[string]any{"books": books}, &parsed); e != nil {
			fail(e)
			return
		}
		if len(parsed.Fingerprints) != len(books) || parsed.Version != "work-parsed-views-v1" {
			fail(model.ErrInput)
			return
		}
		for i := range pool.Editions {
			key := parsed.Fingerprints[pool.Editions[i].Book.ID]
			if key == "" {
				fail(model.ErrInput)
				return
			}
			pool.Editions[i].PreprocessingKey = key
		}
		if e = preprocessing.SaveDiscoveryPreprocessing(ctx, j, parsed.Fingerprints, parsed.Version); e != nil {
			fail(e)
			return
		}
	}
	stage = "features"
	features := map[string]map[string]any{"phash": {}, "sscd": {}, "minilm": {}}
	for at, input := range pool.Editions {
		rowAt = at
		if ctx.Err() != nil {
			return
		}
		if e = s.repo.ManagementGuard(ctx, input.Book.ID); e != nil {
			fail(e)
			return
		}
		var result FeatureResult
		var resources Resources
		var reused bool
		key := s.discoveryImageKey(input)
		cache, incremental := s.repo.(DiscoveryCacheRepository)
		if incremental && run.ExecutionVersion == DiscoveryExecutionVersion && key != "" {
			cached, hit, err := cache.CachedDiscoveryFeatures(ctx, input.Book.ID, key)
			if err != nil {
				fail(err)
				return
			}
			if hit && !cached.MissingCover {
				var response struct {
					Valid  bool          `json:"valid"`
					Result FeatureResult `json:"result"`
				}
				resources, e = s.callFeature(ctx, model.EnhancementWork, "cached_features", map[string]any{"features": cached.Features, "missing_cover": cached.MissingCover}, &response)
				if e == nil && response.Valid {
					result = response.Result
					reused = true
				}
			}
		}
		if !reused {
			raw, imageErr := s.image(ctx, input)
			if errors.Is(imageErr, ErrStale) {
				fail(imageErr)
				return
			}
			payload := map[string]any{"book": input.Book, "repair_cache": run.ExecutionVersion == DiscoveryExecutionVersion}
			if len(raw) > 0 {
				payload["image_base64"] = base64.StdEncoding.EncodeToString(raw)
			}
			resources, e = s.callFeature(ctx, model.EnhancementWork, "features", payload, &result)
			if e != nil && ctx.Err() == nil && len(raw) > 0 {
				delete(payload, "image_base64")
				first := resources
				resources, e = s.callFeature(ctx, model.EnhancementWork, "features", payload, &result)
				resources = addResources(first, resources)
			}
		}
		if ctx.Err() != nil {
			return
		}
		if e != nil {
			_ = s.repo.RecommendationProgress(ctx, j, at, resources, nil)
			fail(e)
			return
		}
		if e = s.repo.SaveRecommendationFeatures(ctx, j, input, result); e != nil {
			fail(e)
			return
		}
		if incremental && run.ExecutionVersion == DiscoveryExecutionVersion {
			if e = cache.SaveDiscoveryFeatures(ctx, j, input, key, result, reused); e != nil {
				fail(e)
				return
			}
		}
		diagnostics := map[string]string{}
		if result.MissingCover {
			diagnostics[input.Book.ID] = "cover_feature_unavailable"
		}
		for a, f := range result.Features {
			if a == "phash" {
				var value string
				if json.Unmarshal(f.Value, &value) != nil {
					fail(model.ErrInput)
					return
				}
				features[a][input.Book.ID] = value
			} else {
				var value []float32
				if json.Unmarshal(f.Value, &value) != nil {
					fail(model.ErrInput)
					return
				}
				features[a][input.Book.ID] = PackVector(value)
			}
		}
		if e = s.repo.RecommendationProgress(ctx, j, at+1, resources, diagnostics); e != nil {
			return
		}
	}
	books := []BookInput{}
	for _, v := range pool.Editions {
		books = append(books, v.Book)
	}
	key := Fingerprint([]string{j.ID, EvidenceVersion, fmt.Sprint(j.Generation)})
	var prepared struct {
		Books int    `json:"books"`
		Key   string `json:"pool_key"`
	}
	reuse := &PairReuse{Graph: PairGraph{Hard: map[int][]byte{}}, Matching: map[string]string{}, Current: map[string]string{}}
	reuse.Prepare()
	if cache, ok := s.repo.(DiscoveryCacheRepository); ok && run.ExecutionVersion == DiscoveryExecutionVersion {
		reuse, e = cache.DiscoveryReuse(ctx, j.ID)
		if e != nil {
			fail(e)
			return
		}
	}
	run, e = d.DiscoveryRun(ctx, j.ID)
	if e != nil {
		fail(e)
		return
	}
	ids := []string{}
	for _, book := range books {
		ids = append(ids, book.ID)
	}
	ready := false
	prepare := func() error {
		if ready {
			return nil
		}
		resources, err := s.callFeature(ctx, model.EnhancementWork, "discovery_prepare", map[string]any{"books": books, "features": features, "pool_key": key}, &prepared)
		if err != nil {
			return err
		}
		if prepared.Books != len(books) || prepared.Key != key {
			return model.ErrInput
		}
		if err = s.repo.RecommendationProgress(ctx, j, len(books), resources, nil); err != nil {
			return err
		}
		ready = true
		return nil
	}
	stage = "pairs"
	for row := run.NextRow; row < len(books); row++ {
		rowAt = row
		if ctx.Err() != nil {
			return
		}
		block, targets := reuse.Row(ids, row)
		if len(targets) > 0 {
			if e = prepare(); e != nil {
				fail(e)
				return
			}
			var fresh PairBlock
			payload := map[string]any{"pool_key": key, "row": row}
			if run.ExecutionVersion == DiscoveryExecutionVersion {
				payload["targets"] = targets
			}
			resources, err := s.callFeature(ctx, model.EnhancementWork, "discovery_row", payload, &fresh)
			// The subprocess may have restarted between rows (including after
			// cancellation of an unrelated capabilities request). Its in-memory
			// pool is not a durable checkpoint. Rebuild it once, then retry only
			// the uncommitted row; never repeat committed pair blocks.
			if errors.Is(err, ErrPoolLost) && ctx.Err() == nil {
				ready = false
				if prepareErr := prepare(); prepareErr != nil {
					fail(prepareErr)
					return
				}
				retryResources, retryErr := s.callFeature(ctx, model.EnhancementWork, "discovery_row", payload, &fresh)
				resources = addResources(resources, retryResources)
				err = retryErr
			}
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				fail(err)
				return
			}
			if fresh.Row != row || fresh.NextRow != row+1 || len(fresh.HardBits) != len(block.HardBits) {
				fail(model.ErrInput)
				return
			}
			selected := map[int]bool{}
			for _, i := range targets {
				selected[i] = true
			}
			for _, edge := range fresh.Edges {
				if !selected[edge.B] {
					fail(model.ErrInput)
					return
				}
				block.Edges = append(block.Edges, edge)
			}
			for i, b := range fresh.HardBits {
				block.HardBits[i] |= b
			}
			if e = s.repo.RecommendationProgress(ctx, j, len(books), resources, nil); e != nil {
				return
			}
		}
		if e = d.SaveDiscoveryBlock(ctx, j, block); e != nil {
			fail(e)
			return
		}
	}
	stage = "proposals"
	if e = d.PublishDiscoveryProposals(ctx, j); e != nil {
		if errors.Is(e, model.ErrConflict) {
			_ = s.repo.ControlJob(ctx, j.ID, "pause", "organization_changed_resume_to_refresh")
		} else {
			fail(e)
		}
		return
	}
	if diagnostics, ok := s.repo.(DiscoveryDiagnosticsRepository); ok {
		_ = diagnostics.SaveDiscoveryDiagnostic(ctx, j, "", "", 0)
	}
	_ = s.repo.FinishBackground(ctx, j, "")
}
func (s *Service) Proposals(ctx context.Context, job, library, kind, state string, alternatives bool, page, size int, target ...string) (ProposalPage, error) {
	d, ok := s.repo.(DiscoveryRepository)
	if !ok {
		return ProposalPage{}, ErrUnavailable
	}
	if page < 1 || page > 1_000_000 || size < 1 || size > 100 || (kind != "" && kind != "create_work" && kind != "add_to_work") {
		return ProposalPage{}, model.ErrInput
	}
	if state == "" {
		state = "pending"
	}
	switch state {
	case "pending", "dismissed", "accepted", "undone":
	default:
		return ProposalPage{}, model.ErrInput
	}
	return d.WorkProposals(ctx, job, library, kind, state, alternatives, page, size, target...)
}
func (s *Service) Proposal(ctx context.Context, id string) (Proposal, error) {
	d, ok := s.repo.(DiscoveryRepository)
	if !ok {
		return Proposal{}, ErrUnavailable
	}
	return d.WorkProposal(ctx, id)
}
func validateSelection(in ProposalSelection) error {
	if in.Revision < 1 || len(in.IDs) > 100 {
		return model.ErrInput
	}
	seen := map[string]bool{}
	for _, id := range in.IDs {
		if id == "" || len(id) > 128 || seen[id] {
			return model.ErrInput
		}
		seen[id] = true
	}
	return nil
}
func (s *Service) ResolveProposal(ctx context.Context, id string, in ProposalSelection) (Proposal, error) {
	if len(in.IDs) == 0 {
		return Proposal{}, model.ErrInput
	}
	if e := validateSelection(in); e != nil {
		return Proposal{}, e
	}
	d, ok := s.repo.(DiscoveryRepository)
	if !ok {
		return Proposal{}, ErrUnavailable
	}
	return d.ResolveWorkProposal(ctx, id, in)
}
func (s *Service) DecideProposal(ctx context.Context, account, id, action string, in ProposalSelection) (Proposal, error) {
	if action == "accept" && len(in.IDs) == 0 {
		return Proposal{}, model.ErrInput
	}
	if e := validateSelection(in); e != nil {
		return Proposal{}, e
	}
	d, ok := s.repo.(DiscoveryRepository)
	if !ok {
		return Proposal{}, ErrUnavailable
	}
	switch action {
	case "accept", "dismiss", "restore", "undo":
	default:
		return Proposal{}, model.ErrInput
	}
	p, e := d.DecideWorkProposal(ctx, account, id, action, in)
	if e == nil {
		s.Wake()
	}
	return p, e
}

func (s *Service) ProposalAlternatives(ctx context.Context, id string, page, size int) (ProposalPage, error) {
	if page < 1 || size < 1 || size > 100 {
		return ProposalPage{}, model.ErrInput
	}
	d, ok := s.repo.(DiscoveryRepository)
	if !ok {
		return ProposalPage{}, ErrUnavailable
	}
	return d.WorkProposalAlternatives(ctx, id, page, size)
}

func (s *Service) DiscoveryStatus(ctx context.Context, id string) (*DiscoveryRun, error) {
	d, ok := s.repo.(DiscoveryRepository)
	if !ok {
		return nil, ErrUnavailable
	}
	run, e := d.DiscoveryRun(ctx, id)
	if e != nil {
		return nil, e
	}
	if run == nil {
		return nil, model.ErrNotFound
	}
	return run, nil
}
