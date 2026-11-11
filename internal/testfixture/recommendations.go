package testfixture

import (
	"context"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
	"slices"
	"strings"
	"sync"
	"time"
)

// RecommendationRunner is deterministic acceptance data, not algorithm evaluation.
// It exists only in the anonymous fixture command's dependency graph.
type RecommendationRunner struct {
	Delay             time.Duration
	mu                sync.Mutex
	books             []rec.BookInput
	key               string
	discoveryScenario bool
	seriesWorks       []rec.WorkInput
	seriesAnchors     []model.Series
	seriesKey         string
}

func (*RecommendationRunner) Enabled() bool { return true }
func (*RecommendationRunner) Close() error  { return nil }
func (r *RecommendationRunner) Call(ctx context.Context, operation string, input, output any) (rec.Resources, error) {
	if operation != "capabilities" {
		timer := time.NewTimer(r.Delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return rec.Resources{}, ctx.Err()
		case <-timer.C:
		}
	}
	var result any
	switch operation {
	case "capabilities":
		result = map[string]any{"available": true, "anonymous_fixture": true, "cpu_threads": 1}
	case "discovery_preprocessing":
		raw, _ := json.Marshal(input)
		var in struct {
			Books []rec.BookInput `json:"books"`
		}
		_ = json.Unmarshal(raw, &in)
		m := map[string]string{}
		for _, b := range in.Books {
			m[b.ID] = rec.Fingerprint([]string{b.Title, b.TitleOriginal})
		}
		result = map[string]any{"version": "work-parsed-views-v1", "fingerprints": m}
	case "series_restore":
		result = map[string]any{"restored": 0}
	case "series_checkpoint":
		result = rec.SeriesCheckpoint{Signals: []rec.SeriesSignal{}, Routes: map[string]json.RawMessage{}}
	case "features", "series_features":
		result = rec.FeatureResult{Features: map[string]rec.Feature{}}
		f := result.(rec.FeatureResult)
		for _, name := range []string{"phash", "sscd", "minilm"} {
			if operation == "series_features" && name != "minilm" {
				continue
			}
			var value any = "0000000000000001"
			if name != "phash" {
				n := 512
				if name == "minilm" {
					n = 384
				}
				v := make([]float32, n)
				v[0] = 1
				value = v
			}
			raw, _ := json.Marshal(value)
			f.Features[name] = rec.Feature{Fingerprint: "anonymous-" + name, InputSHA: "anonymous-input", AlgorithmVersion: "anonymous-fixture-v1", ModelSHA: "anonymous-model", Value: raw, Digest: "anonymous-digest"}
		}
		result = f
	case "series_prepare":
		raw, _ := json.Marshal(input)
		var payload struct {
			Works  []rec.WorkInput `json:"works"`
			Series []model.Series  `json:"series"`
			Key    string          `json:"pool_key"`
		}
		if e := json.Unmarshal(raw, &payload); e != nil {
			return rec.Resources{}, e
		}
		r.mu.Lock()
		r.seriesWorks = payload.Works
		r.seriesAnchors = payload.Series
		r.seriesKey = payload.Key
		r.mu.Unlock()
		result = map[string]any{"works": len(payload.Works), "pool_key": payload.Key}
	case "series_row", "series_cluster", "series_validate":
		raw, _ := json.Marshal(input)
		var payload struct {
			Key string   `json:"pool_key"`
			Row int      `json:"row"`
			IDs []string `json:"work_ids"`
		}
		if e := json.Unmarshal(raw, &payload); e != nil {
			return rec.Resources{}, e
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if payload.Key != r.seriesKey {
			return rec.Resources{}, errors.New("anonymous series key changed")
		}
		if operation == "series_validate" {
			result = map[string]any{"valid": len(payload.IDs) >= 2}
			break
		}
		ids := []string{}
		for _, w := range r.seriesWorks {
			ids = append(ids, w.ID)
		}
		if operation == "series_row" {
			b := rec.SeriesBlock{Row: payload.Row, NextRow: payload.Row + 1, Edges: [][2]string{}}
			for _, other := range ids[payload.Row+1:] {
				b.Edges = append(b.Edges, [2]string{ids[payload.Row], other})
			}
			result = b
			break
		}
		out := rec.SeriesResult{Algorithm: rec.SeriesAlgorithm, Proposals: []rec.SeriesRawPlan{}, Pending: []rec.SeriesPending{}, Unavailable: []string{}}
		if len(r.seriesAnchors) == 0 && len(ids) >= 2 {
			selected := slices.Clone(ids[:min(3, len(ids))])
			out.Proposals = append(out.Proposals, rec.SeriesRawPlan{Kind: "create_series", Name: "匿名系列", WorkIDs: selected, NewIDs: selected, Tier: "strong", Risks: []string{}})
			// Larger anonymous fixtures exercise independent review items and a
			// paginated editor. This is deterministic UI data, not a classifier.
			if len(ids) >= 6 {
				rest := slices.Clone(ids[3:])
				out.Proposals = append(out.Proposals, rec.SeriesRawPlan{Kind: "create_series", Name: "匿名续系列", WorkIDs: rest, NewIDs: rest, Tier: "strong", Risks: []string{}})
			} else if len(ids) > 3 {
				q := ids[3]
				out.Pending = append(out.Pending, rec.SeriesPending{WorkID: q, Reason: "multiple_series_targets_similarly_supported", Options: []rec.SeriesOption{{Kind: "create_series", Target: "anonymous-alternative", TargetWorkIDs: selected, WorkIDs: append(slices.Clone(selected), q), Tier: "supplementary", Risks: []string{}}}})
			}
		} else if len(r.seriesAnchors) > 0 {
			s := r.seriesAnchors[0]
			added := []string{}
			for _, i := range ids {
				if !slices.Contains(s.WorkIDs, i) {
					added = append(added, i)
				}
			}
			if len(added) > 0 {
				out.Proposals = append(out.Proposals, rec.SeriesRawPlan{Kind: "join_series", Target: s.ID, Name: s.Name, WorkIDs: append(slices.Clone(s.WorkIDs), added...), NewIDs: added, Tier: "strong", Risks: []string{}})
			}
		}
		result = out
	case "discovery_prepare":
		raw, _ := json.Marshal(input)
		var payload struct {
			Books []rec.BookInput `json:"books"`
			Key   string          `json:"pool_key"`
		}
		if e := json.Unmarshal(raw, &payload); e != nil {
			return rec.Resources{}, e
		}
		r.mu.Lock()
		r.books = payload.Books
		r.key = payload.Key
		r.discoveryScenario = false
		for _, b := range payload.Books {
			if strings.Contains(b.Title, "匿名替代") {
				r.discoveryScenario = true
			}
		}
		r.mu.Unlock()
		result = map[string]any{"books": len(payload.Books), "pool_key": payload.Key}
	case "cached_features":
		result = map[string]any{"valid": false}
	case "discovery_row":
		raw, _ := json.Marshal(input)
		var payload struct {
			Row     int    `json:"row"`
			Key     string `json:"pool_key"`
			Targets *[]int `json:"targets"`
		}
		if e := json.Unmarshal(raw, &payload); e != nil {
			return rec.Resources{}, e
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if payload.Key != r.key || payload.Row < 0 || payload.Row >= len(r.books) {
			return rec.Resources{}, errors.New("invalid anonymous discovery state")
		}
		row := rec.PairBlock{Row: payload.Row, NextRow: payload.Row + 1, Compared: len(r.books) - payload.Row - 1, HardBits: make([]byte, (len(r.books)-payload.Row-1+7)/8), Edges: []rec.PairEvidence{}}
		for other := payload.Row + 1; other < len(r.books); other++ {
			if payload.Targets != nil && !slices.Contains(*payload.Targets, other) {
				continue
			}
			score := .91
			tier := "strong"
			if r.discoveryScenario {
				left, right := r.books[payload.Row], r.books[other]
				if left.Pages == 0 || right.Pages == 0 {
					continue
				}
				if strings.Contains(left.Title, "匿名替代") || strings.Contains(right.Title, "匿名替代") {
					counterpart := left.Title
					if strings.Contains(left.Title, "匿名替代") {
						counterpart = right.Title
					}
					if !strings.Contains(counterpart, "合成2") {
						continue
					}
					score = .68
					tier = "supplementary"
				}
			}
			if r.books[payload.Row].Pages == 0 || r.books[other].Pages == 0 {
				score = .68
				tier = "supplementary"
			}
			row.Edges = append(row.Edges, rec.PairEvidence{A: payload.Row, B: other, Tier: tier, Score: &score, Signal: json.RawMessage(`{"risks":[],"anonymous_fixture":true}`)})
		}
		result = row
	case "recommend":
		raw, _ := json.Marshal(input)
		var payload struct {
			Books []rec.BookInput `json:"books"`
			Works []rec.WorkInput `json:"works"`
			Query string          `json:"query_edition_id"`
		}
		if e := json.Unmarshal(raw, &payload); e != nil {
			return rec.Resources{}, e
		}
		books := map[string]rec.BookInput{}
		for _, b := range payload.Books {
			books[b.ID] = b
		}
		out := rec.Result{Algorithm: rec.Algorithm, MaxResults: 20, Items: []rec.Candidate{}}
		for _, w := range payload.Works {
			own := false
			for _, id := range w.EditionIDs {
				if id == payload.Query {
					own = true
				}
			}
			if own || len(w.EditionIDs) == 0 {
				continue
			}
			support := w.EditionIDs[0]
			if books[support].Pages == 0 {
				continue
			}
			score := .91
			tier := "strong"
			if len(out.Items) > 0 {
				tier = "supplementary"
				score = .68
			}
			label, _ := json.Marshal(map[string]any{"editions": []any{map[string]any{"edition_id": payload.Query, "languages": []string{}, "existing_custom_labels": []string{}, "suggested_presets": []any{map[string]string{"id": "other", "label": "其他"}}, "needs_manual_review": true, "properties": map[string]any{}}}})
			evidence, _ := json.Marshal([]any{map[string]any{"edition_id": support, "signal": map[string]any{"sscd": score, "title_similarity": .8, "page_ratio": 1}, "decision": map[string]any{"tier": tier, "reasons": []string{"anonymous_fixture_not_model_quality"}}}})
			out.Items = append(out.Items, rec.Candidate{WorkID: w.ID, QueryID: payload.Query, Tier: tier, Score: &score, SupportingID: support, PreferredID: w.PreferredEditionID, Risks: []string{}, Conflicting: []string{}, Algorithm: rec.Algorithm, LabelSuggestions: label, Evidence: evidence})
		}
		out.CandidateCount = len(out.Items)
		if len(out.Items) > 20 {
			out.Truncated = true
			out.Items = out.Items[:20]
		}
		result = out
	default:
		return rec.Resources{}, errors.New("unsupported anonymous operation")
	}
	raw, e := json.Marshal(result)
	if e != nil {
		return rec.Resources{}, e
	}
	e = json.Unmarshal(raw, output)
	return rec.Resources{CacheHits: 1}, e
}
