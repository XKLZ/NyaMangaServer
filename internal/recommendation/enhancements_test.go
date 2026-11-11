package recommendation

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"sync/atomic"
	"testing"
)

type gateRepository struct {
	Repository
	work   atomic.Bool
	series atomic.Bool
}

func (r *gateRepository) EnhancementAllowed(_ context.Context, feature string) error {
	ok := r.work.Load()
	if feature == model.EnhancementSeries {
		ok = r.series.Load()
	}
	if !ok {
		return model.ErrEnhancementDisabled
	}
	return nil
}

type leasedRunner struct {
	entered chan string
	closed  atomic.Int32
}

func (r *leasedRunner) Enabled() bool { return true }
func (r *leasedRunner) Close() error  { r.closed.Add(1); return nil }
func (r *leasedRunner) Call(ctx context.Context, op string, _ any, _ any) (Resources, error) {
	r.entered <- op
	<-ctx.Done()
	return Resources{}, ctx.Err()
}
func TestEnhancementDrainDoesNotCancelOtherSharedWorkerOwner(t *testing.T) {
	repo := &gateRepository{}
	repo.work.Store(true)
	repo.series.Store(true)
	runner := &leasedRunner{entered: make(chan string, 4)}
	svc := New(repo, nil, runner, t.TempDir(), nil)
	workDone := make(chan error, 1)
	seriesDone := make(chan error, 1)
	workCtx, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()
	seriesCtx, cancelSeries := context.WithCancel(context.Background())
	defer cancelSeries()
	go func() { _, e := svc.callFeature(workCtx, model.EnhancementWork, "work", nil, nil); workDone <- e }()
	<-runner.entered
	go func() {
		_, e := svc.callFeature(seriesCtx, model.EnhancementSeries, "series", nil, nil)
		seriesDone <- e
	}()
	<-runner.entered
	repo.work.Store(false)
	if e := svc.DrainFeature(context.Background(), model.EnhancementWork); e != nil {
		t.Fatal(e)
	}
	if e := <-workDone; !errors.Is(e, context.Canceled) && !errors.Is(e, model.ErrEnhancementDisabled) {
		t.Fatal(e)
	}
	select {
	case <-seriesDone:
		t.Fatal("unrelated worker cancelled")
	default:
	}
	if runner.closed.Load() != 0 {
		t.Fatal("worker owned by series released")
	}
	if _, e := svc.callFeature(context.Background(), model.EnhancementWork, "bypass", nil, nil); !errors.Is(e, model.ErrEnhancementDisabled) {
		t.Fatal(e)
	}
	cancelSeries()
	<-seriesDone
	if e := svc.DrainFeature(context.Background(), model.EnhancementSeries); e != nil {
		t.Fatal(e)
	}
	if runner.closed.Load() != 1 {
		t.Fatal(runner.closed.Load())
	}
}
