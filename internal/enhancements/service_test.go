package enhancements

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
	"sync/atomic"
	"testing"
)

type mockRuntime struct {
	calls   atomic.Int32
	failure bool
}

func (r *mockRuntime) DrainEnhancement(ctx context.Context, _ string) error {
	r.calls.Add(1)
	if r.failure {
		return errors.New("anonymous failure")
	}
	return nil
}
func (r *mockRuntime) RestoreDictionary(ctx context.Context) error {
	return r.DrainEnhancement(ctx, model.EnhancementDictionary)
}
func (r *mockRuntime) EnhancementResources(context.Context) map[string]string {
	return map[string]string{model.EnhancementWork: "configured_unverified", model.EnhancementSeries: "not_configured", model.EnhancementDictionary: "not_downloaded"}
}
func TestEnhancementDurableRecoveryRetryAndReadOnlyStatus(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, e := store.Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	runtime := &mockRuntime{}
	svc := New(db, runtime)
	for i := 0; i < 4; i++ {
		if _, e = svc.Read(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if runtime.calls.Load() != 0 {
		t.Fatal("status started work")
	}
	state, _ := svc.Read(ctx)
	enable := true
	state, pending, e := svc.Change(ctx, model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementWork, Enabled: &enable})
	if e != nil || pending {
		t.Fatal(state, pending, e)
	}
	db.Write.Exec("INSERT INTO jobs(id,type,state,stage,generation,created_at,updated_at) VALUES('queued','work_recommendation','queued','processing',1,1,1)")
	state, _ = svc.Read(ctx)
	disable := false
	state, pending, e = svc.Change(ctx, model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementWork, Enabled: &disable, ConfirmPause: true, ImpactToken: state.Features[model.EnhancementWork].ImpactToken})
	if e != nil || !pending {
		t.Fatal(state, pending, e)
	}
	id := state.Features[model.EnhancementWork].Transition.ID
	db.Close()
	db, e = store.Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	runtime.failure = true
	svc = New(db, runtime)
	if e = svc.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	state, _ = svc.Read(ctx)
	if state.Features[model.EnhancementWork].State != "failed" || state.Features[model.EnhancementWork].Enabled {
		t.Fatal(state)
	}
	if _, _, e = svc.Change(ctx, model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementWork, Enabled: &enable}); !errors.Is(e, model.ErrEnhancementTransition) {
		t.Fatal(e)
	}
	state, pending, e = svc.Change(ctx, model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementWork, RetryTransitionID: id})
	if e != nil || !pending {
		t.Fatal(state, pending, e)
	}
	runtime.failure = false
	if e = svc.Tick(ctx); e != nil {
		t.Fatal(e)
	}
	state, _ = svc.Read(ctx)
	if state.Features[model.EnhancementWork].State != "stable" || state.Features[model.EnhancementWork].Enabled {
		t.Fatal(state)
	}
	job, _ := db.Job(ctx, "queued")
	if job.State != "paused" {
		t.Fatal(job)
	}
}
