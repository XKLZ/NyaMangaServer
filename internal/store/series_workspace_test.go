package store

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
	"slices"
	"testing"
)

func TestSeriesCustomizedAcceptIsAtomicAndUndoable(t *testing.T) {
	s := discoveryStore(t, 4)
	ctx := context.Background()
	_, plans := finishSeries(t, s, "custom", [][]int{{0, 1, 2}})
	p := plans[0]
	name := "Edited series"
	order := []string{p.NewIDs[2], p.NewIDs[0]}
	in := rec.SeriesSelection{Customize: true, Revision: p.Revision, IDs: order, Order: order, Name: &name}
	if _, e := s.Write.Exec(`CREATE TRIGGER fail_series_member BEFORE INSERT ON series_members BEGIN SELECT RAISE(ABORT,'anonymous failure');END`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DecideSeries(ctx, "account", p.ID, "accept", in); e == nil {
		t.Fatal("expected rollback")
	}
	fresh, e := s.SeriesProposal(ctx, p.ID)
	if e != nil || fresh.State != "pending" || fresh.Revision != p.Revision {
		t.Fatal(fresh, e)
	}
	var count int
	s.Read.QueryRow("SELECT count(*) FROM series").Scan(&count)
	if count != 0 {
		t.Fatal("partial series", count)
	}
	s.Write.Exec("DROP TRIGGER fail_series_member")
	got, e := s.DecideSeries(ctx, "account", p.ID, "accept", in)
	if e != nil || got.ID != p.ID || got.Name != name || !slices.Equal(got.Order, order) || len(got.NewIDs) != 2 {
		t.Fatal(got, e)
	}
	members, e := s.SeriesMembers(ctx, p.ID, 1, 25)
	if e != nil || len(members.Items) != 2 || members.Items[0].WorkID != order[0] {
		t.Fatal(members, e)
	}
	again, e := s.DecideSeries(ctx, "account", p.ID, "accept", in)
	if e != nil || again.Revision != got.Revision {
		t.Fatal("idempotent retry", again, e)
	}
	if _, e = s.DecideSeries(ctx, "account", got.ID, "undo", rec.SeriesSelection{Revision: got.Revision}); e != nil {
		t.Fatal(e)
	}
	s.Read.QueryRow("SELECT count(*) FROM series").Scan(&count)
	if count != 0 {
		t.Fatal(count)
	}
}
func TestSeriesBatchDropOrderPreservesExistingAndRejectsChangedRetry(t *testing.T) {
	s := discoveryStore(t, 4)
	ctx := context.Background()
	_, plans := finishSeries(t, s, "drop", [][]int{{0, 1}})
	p := plans[0]
	p, e := s.DecideSeries(ctx, "account", p.ID, "accept", rec.SeriesSelection{Revision: p.Revision, IDs: p.NewIDs})
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.SeriesRun(ctx, "drop", true)
	if e != nil {
		t.Fatal(e)
	}
	existing, _ := loadSeries(ctx, s.Read, p.Target)
	added := []string{r.Pool.Works[2].ID, r.Pool.Works[3].ID}
	all := append(slices.Clone(existing.WorkIDs), added...)
	j, _ := s.Job(ctx, "drop")
	raw := rec.SeriesRawPlan{Kind: "join_series", Target: existing.ID, WorkIDs: all, NewIDs: added, Tier: "strong"}
	if e = s.PublishSeries(ctx, *j, r, rec.SeriesResult{Algorithm: rec.SeriesAlgorithm, Proposals: []rec.SeriesRawPlan{raw}}, true); e != nil {
		t.Fatal(e)
	}
	page, _ := s.SeriesProposals(ctx, "drop", "lib", "add_to_series", "pending", 1, 25)
	join := page.Items[0]
	bad := []string{existing.WorkIDs[1], added[0], existing.WorkIDs[0], added[1]}
	if _, e = s.DecideSeries(ctx, "account", join.ID, "accept", rec.SeriesSelection{Revision: join.Revision, IDs: join.NewIDs, Order: bad}); !errors.Is(e, rec.ErrSelection) {
		t.Fatal(e)
	}
	order := []string{existing.WorkIDs[0], added[0], added[1], existing.WorkIDs[1]}
	got, e := s.DecideSeries(ctx, "account", join.ID, "accept", rec.SeriesSelection{Revision: join.Revision, IDs: join.NewIDs, Order: order})
	if e != nil || !slices.Equal(got.Order, order) {
		t.Fatal(got, e)
	}
	if _, e = s.DecideSeries(ctx, "account", got.ID, "accept", rec.SeriesSelection{Revision: join.Revision, IDs: join.NewIDs, Order: all}); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	compact, err := s.SeriesDetail(ctx, p.Target)
	if err != nil || len(compact.WorkIDs) != 0 {
		t.Fatal(compact, err)
	}
	full, err := s.SeriesDetail(ctx, p.Target, false)
	if err != nil || !slices.Equal(full.WorkIDs, order) {
		t.Fatal(full, err)
	}
	current, _ := loadSeries(ctx, s.Read, p.Target)
	if !slices.Equal(current.WorkIDs, order) {
		t.Fatal(current)
	}
	previews, e := s.SeriesProposal(ctx, got.ID)
	if e != nil || len(previews.NewPreviews) != 2 || previews.NewPreviews[0].WorkID != added[0] {
		t.Fatal(previews, e)
	}
	if _, e = s.DecideSeries(ctx, "account", got.ID, "undo", rec.SeriesSelection{Revision: got.Revision}); e != nil {
		t.Fatal(e)
	}
	current, _ = loadSeries(ctx, s.Read, p.Target)
	if !slices.Equal(current.WorkIDs, existing.WorkIDs) {
		t.Fatal(current)
	}
}

func TestSeriesCustomizedAcceptRejectsInvalidAndFrozenInputs(t *testing.T) {
	s := discoveryStore(t, 4)
	ctx := context.Background()
	_, plans := finishSeries(t, s, "invalid-custom", [][]int{{0, 1, 2}})
	p := plans[0]
	name := "Valid"
	valid := rec.SeriesSelection{Customize: true, Revision: p.Revision, IDs: slices.Clone(p.NewIDs[:2]), Order: slices.Clone(p.NewIDs[:2]), Name: &name}
	for _, tc := range []struct {
		name   string
		change func(*rec.SeriesSelection)
	}{
		{"one", func(v *rec.SeriesSelection) { v.IDs = v.IDs[:1]; v.Order = v.Order[:1] }},
		{"duplicate", func(v *rec.SeriesSelection) {
			v.IDs = []string{p.NewIDs[0], p.NewIDs[0]}
			v.Order = slices.Clone(v.IDs)
		}},
		{"foreign", func(v *rec.SeriesSelection) { v.IDs = []string{p.NewIDs[0], "unknown"}; v.Order = slices.Clone(v.IDs) }},
		{"empty name", func(v *rec.SeriesSelection) { x := " "; v.Name = &x }},
		{"missing name", func(v *rec.SeriesSelection) { v.Name = nil }},
		{"incomplete order", func(v *rec.SeriesSelection) { v.Order = v.Order[:1] }},
		{"old revision", func(v *rec.SeriesSelection) { v.Revision++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := valid
			tc.change(&v)
			if _, e := s.DecideSeries(ctx, "account", p.ID, "accept", v); e == nil {
				t.Fatal("accepted invalid draft")
			}
			fresh, e := s.SeriesProposal(ctx, p.ID)
			if e != nil || fresh.State != "pending" || fresh.Revision != p.Revision {
				t.Fatal(fresh, e)
			}
		})
	}
	// Even an excluded Work remains part of the original frozen input snapshot.
	if _, e := s.Write.Exec("UPDATE works SET revision=revision+1 WHERE id=?", p.NewIDs[2]); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DecideSeries(ctx, "account", p.ID, "accept", valid); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	var count int
	if e := s.Read.QueryRow("SELECT count(*) FROM series").Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
}
