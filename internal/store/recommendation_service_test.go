package store

import (
	"context"
	"encoding/json"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
	"sync"
	"testing"
	"time"
)

type recommendationRunner struct {
	mu             sync.Mutex
	blockOperation string
	blockQuery     string
	entered        chan struct{}
	blocked        bool
	active         int
	maximum        int
}

func (*recommendationRunner) Enabled() bool { return true }
func (*recommendationRunner) Close() error  { return nil }
func (r *recommendationRunner) Call(ctx context.Context, operation string, input any, output any) (rec.Resources, error) {
	if operation == "capabilities" {
		raw, _ := json.Marshal(map[string]any{"available": true})
		return rec.Resources{}, json.Unmarshal(raw, output)
	}
	r.mu.Lock()
	r.active++
	r.maximum = max(r.maximum, r.active)
	block := !r.blocked && operation == r.blockOperation
	if block && r.blockQuery != "" {
		block = input.(map[string]any)["query_edition_id"] == r.blockQuery
	}
	if block {
		r.blocked = true
		close(r.entered)
	}
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.active--; r.mu.Unlock() }()
	if block {
		<-ctx.Done()
		return rec.Resources{}, ctx.Err()
	}
	if operation == "features" {
		*output.(*rec.FeatureResult) = rec.FeatureResult{Features: map[string]rec.Feature{}}
		return rec.Resources{CacheHits: 1}, nil
	}
	payload := input.(map[string]any)
	query := payload["query_edition_id"].(string)
	works := payload["works"].([]rec.WorkInput)
	result := rec.Result{Algorithm: rec.Algorithm, MaxResults: 20, Items: []rec.Candidate{}}
	for _, work := range works {
		own := false
		for _, id := range work.EditionIDs {
			if id == query {
				own = true
			}
		}
		if own {
			continue
		}
		result.Items = append(result.Items, rec.Candidate{QueryID: query, WorkID: work.ID, Tier: "strong", SupportingID: work.EditionIDs[0], PreferredID: work.PreferredEditionID, Algorithm: rec.Algorithm})
		break
	}
	result.CandidateCount = len(result.Items)
	*output.(*rec.Result) = result
	return rec.Resources{OperationSeconds: .001}, nil
}
func waitRecommendationJob(t *testing.T, s *Store, id, state string) *model.Job {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		j, e := s.Job(context.Background(), id)
		if e != nil {
			t.Fatal(e)
		}
		if j.State == state {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	j, _ := s.Job(context.Background(), id)
	t.Fatal("job did not settle", j)
	return nil
}
func stopRecommendations(t *testing.T, s *rec.Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e := s.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
}
func TestRecommendationPauseImmediateResumeWaitsForOldIO(t *testing.T) {
	store, _ := orgFixture(t)
	runner := &recommendationRunner{blockOperation: "features", entered: make(chan struct{})}
	svc := rec.New(store, nil, runner, t.TempDir(), nil)
	svc.Start(context.Background())
	defer stopRecommendations(t, svc)
	j, e := svc.StartJob(context.Background(), []string{"book"})
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-runner.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start")
	}
	if e = svc.Control(context.Background(), j.ID, "pause"); e != nil {
		t.Fatal(e)
	}
	if e = svc.Control(context.Background(), j.ID, "resume"); e != nil {
		t.Fatal(e)
	}
	done := waitRecommendationJob(t, store, j.ID, "completed")
	if done.Done != 1 || done.Generation < 5 {
		t.Fatal(done)
	}
	runner.mu.Lock()
	maximum := runner.maximum
	runner.mu.Unlock()
	if maximum != 1 {
		t.Fatal("old and new inference overlapped", maximum)
	}
}
func TestRecommendationCancelImmediateNewJobRejectsOldResult(t *testing.T) {
	store, _ := orgFixture(t)
	runner := &recommendationRunner{blockOperation: "features", entered: make(chan struct{})}
	svc := rec.New(store, nil, runner, t.TempDir(), nil)
	svc.Start(context.Background())
	defer stopRecommendations(t, svc)
	old, e := svc.StartJob(context.Background(), []string{"book"})
	if e != nil {
		t.Fatal(e)
	}
	<-runner.entered
	if e = svc.Control(context.Background(), old.ID, "cancel"); e != nil {
		t.Fatal(e)
	}
	next, e := svc.StartJob(context.Background(), []string{"book"})
	if e != nil {
		t.Fatal(e)
	}
	waitRecommendationJob(t, store, next.ID, "completed")
	page, _ := store.RecommendationPage(context.Background(), old.ID, "", "", 1, 20)
	if page.Total != 0 {
		t.Fatal("old result published", page)
	}
}
func TestRecommendationRestartPreservesPublishedCheckpoint(t *testing.T) {
	store, _ := orgFixture(t)
	runner := &recommendationRunner{blockOperation: "recommend", blockQuery: "b2", entered: make(chan struct{})}
	svc := rec.New(store, nil, runner, t.TempDir(), nil)
	svc.Start(context.Background())
	j, e := svc.StartJob(context.Background(), []string{"book", "b2"})
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-runner.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("second member not started")
	}
	stopRecommendations(t, svc)
	midway, _ := store.Job(context.Background(), j.ID)
	if midway.Done != 1 || midway.State != "running" {
		t.Fatal(midway)
	}
	resumed := rec.New(store, nil, &recommendationRunner{}, t.TempDir(), nil)
	resumed.Start(context.Background())
	defer stopRecommendations(t, resumed)
	done := waitRecommendationJob(t, store, j.ID, "completed")
	if done.Done != 2 {
		t.Fatal(done)
	}
	page, e := store.RecommendationPage(context.Background(), j.ID, "", "", 1, 20)
	if e != nil || page.Total != 2 {
		t.Fatal(page, e)
	}
}
