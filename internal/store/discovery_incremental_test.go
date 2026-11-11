package store

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
	"nyamangaserver.local/server/internal/testfixture"
	"reflect"
	"testing"
	"time"
)

func TestIncrementalDiscoveryServiceReusesAllPairsAndOnlyChangedEdition(t *testing.T) {
	ctx := context.Background()
	s := discoveryStore(t, 4)
	runner := &discoveryCountingRunner{}
	svc := rec.New(s, nil, runner, t.TempDir(), nil)
	svc.Start(ctx)
	defer svc.Shutdown(ctx)
	run := func(full bool) *rec.DiscoveryRun {
		t.Helper()
		j, e := svc.StartDiscovery(ctx, "lib", full)
		if e != nil {
			t.Fatal(e)
		}
		for deadline := time.Now().Add(8 * time.Second); ; {
			state, e := s.Job(ctx, j.ID)
			if e != nil {
				t.Fatal(e)
			}
			if state.State == "completed" {
				break
			}
			if state.State == "paused" || state.State == "failed" || time.Now().After(deadline) {
				t.Fatal(state)
			}
			time.Sleep(10 * time.Millisecond)
		}
		r, e := s.DiscoveryRun(ctx, j.ID)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	first := run(false)
	if first.ComputedPairs != 6 || first.ReusedPairs != 0 {
		t.Fatal(first)
	}
	second := run(false)
	if second.ComputedPairs != 0 || second.ReusedPairs != 6 {
		t.Fatal(second)
	}
	if _, e := s.Write.Exec("UPDATE files SET filename_title='Changed anonymous title' WHERE edition_id='e0001'"); e != nil {
		t.Fatal(e)
	}
	third := run(false)
	if third.ComputedPairs != 3 || third.ReusedPairs != 3 {
		t.Fatal(third)
	}
	if e := testfixture.SeedEdition(ctx, s.Write, "e0004", "lib", "anonymous-new.cbz", "Anonymous new"); e != nil {
		t.Fatal(e)
	}
	added := run(false)
	if added.ComputedPairs != 4 || added.ReusedPairs != 6 {
		t.Fatal(added)
	}
	forced := run(true)
	if forced.ComputedPairs != 10 || forced.ReusedPairs != 0 {
		t.Fatal(forced)
	}
	a, _ := s.RecommendationPool(ctx, added.JobID)
	b, _ := s.RecommendationPool(ctx, forced.JobID)
	ga, e := discoveryGraph(ctx, s.Read, added.JobID, a)
	if e != nil {
		t.Fatal(e)
	}
	gb, e := discoveryGraph(ctx, s.Read, forced.JobID, b)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(ga, gb) {
		t.Fatal("incremental and full evidence differ")
	}
}

func TestIncrementalFeatureRepairResetsCheckpointAndRejectsOldGeneration(t *testing.T) {
	ctx := context.Background()
	s := discoveryStore(t, 3)
	finishDiscovery(t, s, "baseline", nil)
	j, e := s.CreateDiscoveryJob(ctx, "resumable", "lib")
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	var base string
	if e = s.Read.QueryRow("SELECT base_job_id FROM work_discovery_runs WHERE job_id=?", j.ID).Scan(&base); e != nil || base != "baseline" {
		t.Fatal(base, e)
	}
	if _, e = s.Write.Exec("DELETE FROM jobs WHERE id='baseline'"); e == nil {
		t.Fatal("baseline was not protected")
	}
	pool, e := s.RecommendationPool(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	input := pool.Editions[0]
	result := rec.FeatureResult{}
	if e = s.SaveDiscoveryFeatures(ctx, *j, input, "", result, false); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveDiscoveryBlock(ctx, *j, rec.PairBlock{Row: 0, NextRow: 1, Compared: 2, HardBits: []byte{0}}); e != nil {
		t.Fatal(e)
	}
	if e = s.ControlJob(ctx, j.ID, "pause", ""); e != nil {
		t.Fatal(e)
	}
	if e = s.ControlJob(ctx, j.ID, "resume", ""); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveDiscoveryFeatures(ctx, *j, input, "", result, false); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal("old feature worker committed", e)
	}
	current, e := s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	result.MissingCover = true // a repaired/missing feature changes all dependent rows
	if e = s.SaveDiscoveryFeatures(ctx, *current, input, "", result, false); e != nil {
		t.Fatal(e)
	}
	run, e := s.DiscoveryRun(ctx, j.ID)
	if e != nil || run.NextRow != 0 || run.ComputedPairs != 0 || run.PreparedFeatures != 1 {
		t.Fatal(run, e)
	}
	var n int
	if e = s.Read.QueryRow("SELECT count(*) FROM work_pair_blocks WHERE job_id=?", j.ID).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	state, e := s.Job(ctx, j.ID)
	if e != nil || state.Done != 0 {
		t.Fatal(state, e)
	}
	if e = s.ControlJob(ctx, j.ID, "cancel", ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateDiscoveryJob(ctx, "replacement", "lib"); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveDiscoveryFeatures(ctx, *current, input, "", result, false); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal("cancelled worker committed", e)
	}
}

// A subprocess can lose its in-memory pool between calls. The committed SQL
// checkpoint survives and must not be confused with a bad source image.
type lostPoolRunner struct {
	discoveryCountingRunner
	lost   bool
	repeat bool
}

func (r *lostPoolRunner) Call(ctx context.Context, op string, in, out any) (rec.Resources, error) {
	if op == "discovery_row" && in.(map[string]any)["row"] == 1 && (!r.lost || r.repeat) {
		r.lost = true
		return rec.Resources{}, errors.Join(rec.ErrWorker, rec.ErrPoolLost)
	}
	return r.discoveryCountingRunner.Call(ctx, op, in, out)
}
func TestDiscoveryRebuildsLostWorkerPoolWithoutRepeatingCommittedRows(t *testing.T) {
	ctx := context.Background()
	s := discoveryStore(t, 4)
	r := &lostPoolRunner{}
	svc := rec.New(s, nil, r, t.TempDir(), nil)
	svc.Start(ctx)
	defer stopRecommendations(t, svc)
	j, e := svc.StartDiscovery(ctx, "lib")
	if e != nil {
		t.Fatal(e)
	}
	waitRecommendationJob(t, s, j.ID, "completed")
	run, e := s.DiscoveryRun(ctx, j.ID)
	if e != nil || run.ComputedPairs != 6 || run.NextRow != 4 {
		t.Fatal(run, e)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.lost || r.prepare != 2 || r.rows != 3 {
		t.Fatal(r.lost, r.prepare, r.rows)
	}
}
func TestDiscoveryRepeatedWorkerFailurePausesAndPartialCheckpointCanBeReused(t *testing.T) {
	ctx := context.Background()
	s := discoveryStore(t, 4)
	r := &lostPoolRunner{repeat: true}
	svc := rec.New(s, nil, r, t.TempDir(), nil)
	svc.Start(ctx)
	j, e := svc.StartDiscovery(ctx, "lib")
	if e != nil {
		t.Fatal(e)
	}
	state := waitRecommendationJob(t, s, j.ID, "paused")
	if state.Error != "discovery_worker_unavailable" {
		t.Fatal(state)
	}
	run, e := s.DiscoveryRun(ctx, j.ID)
	if e != nil || run.NextRow != 1 || run.FailureRow != 1 || run.FailureStage != "pairs" || run.FailureReason != "discovery_worker_unavailable" {
		t.Fatal(run, e)
	}
	if e = svc.Control(ctx, j.ID, "cancel"); e != nil {
		t.Fatal(e)
	}
	stopRecommendations(t, svc)
	fresh := rec.New(s, nil, &discoveryCountingRunner{}, t.TempDir(), nil)
	fresh.Start(ctx)
	defer stopRecommendations(t, fresh)
	next, e := fresh.StartDiscovery(ctx, "lib")
	if e != nil {
		t.Fatal(e)
	}
	waitRecommendationJob(t, s, next.ID, "completed")
	run, e = s.DiscoveryRun(ctx, next.ID)
	if e != nil || run.ReusedPairs != 3 || run.ComputedPairs != 3 {
		t.Fatal(run, e)
	}
}
func TestDiscoveryDoesNotTrustUnversionedLegacyCheckpoint(t *testing.T) {
	ctx := context.Background()
	s := discoveryStore(t, 3)
	finishDiscovery(t, s, "legacy", nil)
	if _, e := s.Write.Exec("UPDATE work_discovery_runs SET execution_version='' WHERE job_id='legacy'"); e != nil {
		t.Fatal(e)
	}
	j, e := s.CreateDiscoveryJob(ctx, "new", "lib")
	if e != nil {
		t.Fatal(e)
	}
	var base *string
	if e = s.Read.QueryRow("SELECT base_job_id FROM work_discovery_runs WHERE job_id=?", j.ID).Scan(&base); e != nil || base != nil {
		t.Fatal(base, e)
	}
	run, e := s.DiscoveryRun(ctx, j.ID)
	if e != nil || run.BaselineState != "no_compatible_baseline" {
		t.Fatal(run, e)
	}
}
