package recommendation

import (
	"context"
	"nyamangaserver.local/server/internal/model"
	"time"
)

type featureRepository interface {
	EnhancementAllowed(context.Context, string) error
}
type featureOperation struct {
	feature string
	cancel  context.CancelFunc
}

func (s *Service) requireFeature(ctx context.Context, feature string) error {
	if r, ok := s.repo.(featureRepository); ok {
		return r.EnhancementAllowed(ctx, feature)
	}
	return nil
}
func (s *Service) beginFeature(ctx context.Context, feature string) (context.Context, func(), error) {
	release := func() {}
	if r, ok := s.repo.(interface {
		BeginHealthIO(context.Context) (func(), error)
	}); ok {
		var e error
		release, e = r.BeginHealthIO(ctx)
		if e != nil {
			return ctx, func() {}, e
		}
	}

	s.operationMu.Lock()
	defer s.operationMu.Unlock()
	if e := s.requireFeature(ctx, feature); e != nil {
		release()
		return ctx, func() {}, e
	}
	if s.operations == nil {
		s.operations = map[int64]featureOperation{}
	}
	s.operationID++
	id := s.operationID
	child, cancel := context.WithCancel(ctx)
	s.operations[id] = featureOperation{feature, cancel}
	return child, func() { release(); cancel(); s.operationMu.Lock(); delete(s.operations, id); s.operationMu.Unlock() }, nil
}
func (s *Service) callFeature(ctx context.Context, feature, op string, in, out any) (Resources, error) {
	ctx, done, e := s.beginFeature(ctx, feature)
	if e != nil {
		return Resources{}, e
	}
	defer done()
	if s.runner == nil || !s.runner.Enabled() {
		return Resources{}, ErrUnavailable
	}
	resources, err := s.runner.Call(ctx, op, in, out)
	if err != nil {
		probe, cancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Millisecond)
		defer cancel()
		if gate := s.requireFeature(probe, feature); gate == model.ErrEnhancementDisabled {
			return resources, gate
		}
	}
	return resources, err
}
func (s *Service) probeFeature(ctx context.Context, feature string) error {
	var result map[string]any
	if _, e := s.callFeature(ctx, feature, "capabilities", nil, &result); e != nil {
		return e
	}
	if result["available"] != true {
		return ErrUnavailable
	}
	s.mu.Lock()
	s.capabilities = result
	s.mu.Unlock()
	return nil
}

// DrainFeature cannot release a worker owned by the other recommendation type.
// Whole refresh/resolve operations are leases too, closing gaps between Calls.
func (s *Service) DrainFeature(ctx context.Context, feature string) error {
	for {
		s.operationMu.Lock()
		busy := false
		for _, op := range s.operations {
			if op.feature == feature {
				op.cancel()
				busy = true
			}
		}
		s.mu.Lock()
		active, cancel := s.active, s.activeCancel
		s.mu.Unlock()
		if active != "" {
			j, e := s.repo.Job(ctx, active)
			if e != nil {
				s.operationMu.Unlock()
				return e
			}
			if model.EnhancementForJob(j.Type) == feature {
				if cancel != nil {
					cancel()
				}
				busy = true
			}
		}
		if feature == model.EnhancementSeries {
			if s.seriesMu.TryLock() {
				s.seriesMu.Unlock()
			} else {
				busy = true
			}
		}
		if !busy {
			var e error
			if len(s.operations) == 0 && active == "" && s.runner != nil {
				e = s.runner.Close()
				s.mu.Lock()
				s.capabilities = nil
				s.mu.Unlock()
			}
			s.operationMu.Unlock()
			return e
		}
		s.operationMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *Service) backgroundAllowed(ctx context.Context, feature string) error {
	if r, ok := s.repo.(interface {
		EnhancementBackgroundAllowed(context.Context, string) error
	}); ok {
		return r.EnhancementBackgroundAllowed(ctx, feature)
	}
	return s.requireFeature(ctx, feature)
}
