package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
)

func TestSeriesPresentationFrozenVersionAndInheritedDismissal(t *testing.T) {
	s := discoveryStore(t, 3)
	ctx := context.Background()
	for id, title := range map[string]string{"e0000": "[Event] Memory 3", "e0001": "【Creator】 Memory", "e0002": "Memory 2 (Japanese)"} {
		if _, e := s.Write.Exec("UPDATE files SET filename_title=? WHERE edition_id=?", title, id); e != nil {
			t.Fatal(e)
		}
	}
	_, plans := finishSeriesVersion(t, s, "old-style", [][]int{{0, 1, 2}}, "")
	old := plans[0]
	if old.Name != "Anonymous series" || old.Presentation != "" || old.OrderBasis != "" {
		t.Fatal(old)
	}
	name := "Old manual [name]"
	old, e := s.ResolveSeries(ctx, old.ID, rec.SeriesSelection{Revision: old.Revision, IDs: old.NewIDs, Name: &name})
	if e != nil {
		t.Fatal(e)
	}
	old, e = s.DecideSeries(ctx, "account", old.ID, "dismiss", rec.SeriesSelection{Revision: old.Revision})
	if e != nil {
		t.Fatal(e)
	}
	before := dumpRows(t, s, "SELECT * FROM series_proposals WHERE id='"+old.ID+"'")
	decision := dumpRows(t, s, "SELECT * FROM series_proposal_decisions WHERE proposal_key='"+old.StorageKey+"'")
	_, _ = finishSeries(t, s, "new-style", [][]int{{0, 1, 2}})
	page, e := s.SeriesProposals(ctx, "new-style", "lib", "", "dismissed", 1, 10)
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	p := page.Items[0]
	if p.ID == old.ID || p.Name != "Memory" || p.OrderBasis != "contextual" || p.NameRequired || !slices.Equal(p.Order, []string{"i:e0001", "i:e0002", "i:e0000"}) {
		t.Fatal(p)
	}
	if !reflect.DeepEqual(before, dumpRows(t, s, "SELECT * FROM series_proposals WHERE id='"+old.ID+"'")) || !reflect.DeepEqual(decision, dumpRows(t, s, "SELECT * FROM series_proposal_decisions WHERE proposal_key='"+old.StorageKey+"'")) {
		t.Fatal("legacy history changed")
	}
	p, e = s.DecideSeries(ctx, "account", p.ID, "restore", rec.SeriesSelection{Revision: p.Revision})
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.SeriesRun(ctx, "new-style", true)
	if e != nil {
		t.Fatal(e)
	}
	j, _ := s.Job(ctx, "new-style")
	result := rec.SeriesResult{Algorithm: rec.SeriesAlgorithm, Proposals: []rec.SeriesRawPlan{{Kind: "create_series", WorkIDs: p.Order, NewIDs: p.NewIDs, Name: "ignored runtime name", Tier: "strong", Risks: []string{}}}}
	if e = s.PublishSeries(ctx, *j, r, result, true); e != nil {
		t.Fatal(e)
	}
	after, e := s.SeriesProposal(ctx, p.ID)
	if e != nil || after.State != "pending" || after.Revision != p.Revision {
		t.Fatal("restoration lost", after, e)
	}
	legacy, e := s.SeriesRun(ctx, "old-style", true)
	if e != nil || legacy.Presentation != "" {
		t.Fatal("legacy refresh upgraded", legacy.Presentation, e)
	}
	if _, e = s.Write.Exec("UPDATE series_discovery_runs SET context_json=json_set(context_json,'$.series_presentation_version','unknown') WHERE job_id='new-style'"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SeriesRun(ctx, "new-style", false); !errors.Is(e, rec.ErrStale) {
		t.Fatal("unknown rule version accepted", e)
	}
}

func TestSeriesPresentationNameRequiredAndManualOverride(t *testing.T) {
	s := discoveryStore(t, 2)
	ctx := context.Background()
	if _, e := s.Write.Exec("UPDATE files SET filename_title='【Only annotation】'"); e != nil {
		t.Fatal(e)
	}
	_, plans := finishSeries(t, s, "unnamed", [][]int{{0, 1}})
	p := plans[0]
	if !p.NameRequired || p.Name != "" {
		t.Fatal(p)
	}
	if _, e := s.DecideSeries(ctx, "account", p.ID, "accept", rec.SeriesSelection{Revision: p.Revision, IDs: p.NewIDs}); !errors.Is(e, model.ErrInput) {
		t.Fatal(e)
	}
	name := "User [chosen] Series"
	order := slices.Clone(p.Order)
	slices.Reverse(order)
	resolved, e := s.ResolveSeries(ctx, p.ID, rec.SeriesSelection{Revision: p.Revision, IDs: p.NewIDs, Name: &name, Order: order})
	if e != nil || resolved.NameRequired || resolved.Name != name || resolved.OrderBasis != "manual" {
		t.Fatal(resolved, e)
	}
	accepted, e := s.DecideSeries(ctx, "account", resolved.ID, "accept", rec.SeriesSelection{Revision: resolved.Revision, IDs: resolved.NewIDs})
	if e != nil {
		t.Fatal(e)
	}
	series, e := s.SeriesDetail(ctx, accepted.Target)
	if e != nil || series.Name != name {
		t.Fatal(series, e)
	}
}
