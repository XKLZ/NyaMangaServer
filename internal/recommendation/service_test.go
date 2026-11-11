package recommendation

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

type changedCatalog struct{}

func (changedCatalog) Image(context.Context, string, int64, int, bool) ([]byte, string, error) {
	return nil, "", comic.ErrChanged
}
func TestSourceChangeIsNotMissingCover(t *testing.T) {
	s := New(nil, changedCatalog{}, nil, t.TempDir(), nil)
	raw, e := s.image(context.Background(), InputEdition{Book: BookInput{ID: "anonymous"}, ContentRevision: 1})
	if raw != nil || !errors.Is(e, ErrStale) {
		t.Fatal(raw, e)
	}
}

type retryValidationRepo struct{ Repository }

func (retryValidationRepo) Job(context.Context, string) (*model.Job, error) {
	return &model.Job{ID: "anonymous", Type: "work_recommendation", State: "failed"}, nil
}
func (retryValidationRepo) Items(context.Context, string, int, int, bool) ([]model.Item, error) {
	id := "anonymous"
	return []model.Item{{Ordinal: 1, State: "failed", BookID: &id}}, nil
}
func TestRetryRejectsInvalidOrUnmatchedOrdinals(t *testing.T) {
	s := New(retryValidationRepo{}, nil, nil, t.TempDir(), nil)
	for _, values := range [][]int{{0}, {1, 1}, {-1, 1}, {1, 2}, {999}} {
		if _, e := s.Retry(context.Background(), "anonymous", values); !errors.Is(e, model.ErrInput) {
			t.Fatal(values, e)
		}
	}
}
func TestRecommendationPreviewInputBoundaries(t *testing.T) {
	s := New(nil, nil, nil, t.TempDir(), nil)
	c := model.WorkCommand{Expected: map[string]int64{}}
	for i := 0; i < 2501; i++ {
		c.Expected[string(rune(i))] = 1
	}
	if _, e := s.Preview(context.Background(), "a", "id", c); !errors.Is(e, model.ErrInput) {
		t.Fatal(e)
	}
}

type capabilityCounter struct {
	Runner
	calls int
}

func (*capabilityCounter) Enabled() bool { return true }
func (r *capabilityCounter) Call(_ context.Context, op string, _ any, out any) (Resources, error) {
	if op != "capabilities" {
		panic("unexpected operation")
	}
	r.calls++
	*out.(*map[string]any) = map[string]any{"available": true}
	return Resources{}, nil
}
func TestCapabilitiesDoNotTouchActiveWorkerPool(t *testing.T) {
	r := &capabilityCounter{}
	s := New(nil, nil, r, t.TempDir(), nil)
	if s.Capabilities(context.Background())["available"] != false || r.calls != 0 {
		t.Fatal(r.calls)
	}
	s.active = "anonymous-job"
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if s.Capabilities(cancelled)["available"] != false || r.calls != 0 {
		t.Fatal("HTTP probe touched active process", r.calls)
	}
	s.active = ""
	s.Capabilities(context.Background())
	if r.calls != 0 {
		t.Fatal("read-only status started worker", r.calls)
	}
}
