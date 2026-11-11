// Package enhancements owns global optional capabilities and durable switching.
package enhancements

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"sync"
	"time"
)

type Repository interface {
	EnhancementContext(context.Context, string) (context.Context, error)
	RequireAdministrator(context.Context) error
	Enhancements(context.Context) (model.Enhancements, error)
	ChangeEnhancement(context.Context, model.EnhancementPatch) (*model.EnhancementTransition, error)
	PendingEnhancements(context.Context) ([]model.EnhancementTransition, error)
	FinishEnhancement(context.Context, model.EnhancementTransition, string) error
}
type Runtime interface {
	DrainEnhancement(context.Context, string) error
	RestoreDictionary(context.Context) error
	EnhancementResources(context.Context) map[string]string
}
type Service struct {
	repo    Repository
	runtime Runtime
	wake    chan struct{}
	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	work    sync.Mutex
}

func New(r Repository, runtime Runtime) *Service {
	return &Service{repo: r, runtime: runtime, wake: make(chan struct{}, 1)}
}
func (s *Service) Read(ctx context.Context) (model.Enhancements, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.Enhancements{}, e
	}
	v, e := s.repo.Enhancements(ctx)
	if e != nil {
		return v, e
	}
	resources := s.runtime.EnhancementResources(ctx)
	for key, f := range v.Features {
		f.ResourceState = resources[key]
		if key == model.EnhancementGallery {
			f.ResourceState = "ready"
		}
		if f.State == "enabling" {
			f.ResourceState = "restoring"
		}
		v.Features[key] = f
	}
	return v, nil
}
func (s *Service) Change(ctx context.Context, in model.EnhancementPatch) (model.Enhancements, bool, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.Enhancements{}, false, e
	}
	t, e := s.repo.ChangeEnhancement(ctx, in)
	if e != nil {
		return model.Enhancements{}, false, e
	}
	pending := t != nil
	if t != nil && t.AffectedTasks == 0 && !t.TargetEnabled && s.work.TryLock() {
		probe, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
		e = s.runtime.DrainEnhancement(probe, t.Feature)
		cancel()
		if e == nil {
			e = s.repo.FinishEnhancement(ctx, *t, "")
			pending = e != nil
		}
		s.work.Unlock()
		if e != nil && !errors.Is(e, context.DeadlineExceeded) && !errors.Is(e, context.Canceled) {
			return model.Enhancements{}, true, e
		}
	}
	if in.Feature == model.EnhancementGallery {
		v, e := s.Read(ctx)
		return v, false, e
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	v, e := s.Read(ctx)
	return v, pending, e
}
func (s *Service) Tick(ctx context.Context) error {
	if !model.AutomaticWorkAllowed(ctx, s.repo) {
		return nil
	}
	if r, ok := s.repo.(interface {
		HealthState(context.Context) (model.HealthState, error)
	}); ok {
		state, e := r.HealthState(ctx)
		if e != nil {
			return e
		}
		if state.Phase != "idle" {
			return nil
		}
	}
	s.work.Lock()
	defer s.work.Unlock()
	pending, e := s.repo.PendingEnhancements(ctx)
	if e != nil {
		return e
	}
	for _, t := range pending {
		workerCtx, authErr := s.repo.EnhancementContext(ctx, t.ID)
		if authErr != nil {
			if e = s.repo.FinishEnhancement(ctx, t, "authorization_revoked"); e != nil {
				return e
			}
			continue
		}
		op, cancel := context.WithTimeout(workerCtx, 10*time.Second)
		if t.TargetEnabled {
			e = s.runtime.RestoreDictionary(op)
		} else {
			e = s.runtime.DrainEnhancement(op, t.Feature)
		}
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		code := ""
		if e != nil {
			code = "drain_failed"
			if t.TargetEnabled {
				code = "offline_restore_unavailable"
			}
		}
		if e = s.repo.FinishEnhancement(workerCtx, t, code); e != nil {
			return e
		}
	}
	return nil
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
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			_ = s.Tick(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.wake:
			}
		}
	}()
}
func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
