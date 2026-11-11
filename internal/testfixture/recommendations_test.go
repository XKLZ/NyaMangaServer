package testfixture

import (
	"context"
	"errors"
	rec "nyamangaserver.local/server/internal/recommendation"
	"testing"
	"time"
)

func TestRecommendationFixtureIsOfflineAndCancellationAware(t *testing.T) {
	r := &RecommendationRunner{Delay: time.Second}
	var cap map[string]any
	if _, e := r.Call(context.Background(), "capabilities", nil, &cap); e != nil || cap["anonymous_fixture"] != true {
		t.Fatal(cap, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var f rec.FeatureResult
	if _, e := r.Call(ctx, "features", nil, &f); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := r.Call(context.Background(), "unknown", nil, &f); e == nil {
		t.Fatal("unknown fixture operation accepted")
	}
}
