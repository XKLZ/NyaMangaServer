package store

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	"testing"
)

func orgFixture(t *testing.T) (*Store, *organization.Service) {
	t.Helper()
	s, _ := openTest(t)
	seedBook(t, s)
	for _, v := range []struct{ id, title string }{{"b2", "第2卷"}, {"b10", "第10卷"}, {"b1", "第1卷"}} {
		seedEdition(t, s, v.id, "lib", v.id+".cbz", v.title)
	}
	if e := s.Initialize(context.Background(), model.Account{ID: "a", PasswordHash: "anonymous", Revision: 1}, model.Session{Hash: make([]byte, 32), AccountID: "a", Created: 1, Expires: 99}); e != nil {
		t.Fatal(e)
	}
	return s, organization.New(s)
}
func commandContext(t *testing.T, o *organization.Service, c model.WorkCommand) model.WorkContext {
	t.Helper()
	ed := append([]string{}, c.EditionIDs...)
	if c.EditionID != "" {
		ed = append(ed, c.EditionID)
	}
	works := append([]string{}, c.WorkIDs...)
	series := []string{}
	if len(c.Action) >= 5 && c.Action[:5] == "work_" && c.ID != "" {
		works = append(works, c.ID)
	}
	if c.TargetWorkID != "" {
		works = append(works, c.TargetWorkID)
	}
	if len(c.Action) >= 7 && c.Action[:7] == "series_" && c.Action != "series_create" {
		series = append(series, c.ID)
	}
	if c.TargetSeriesID != "" {
		series = append(series, c.TargetSeriesID)
	}
	if len(ed)+len(works)+len(series) == 0 {
		return model.WorkContext{Expected: map[string]int64{}}
	}
	g, e := o.WorkContext(context.Background(), "a", ed, works, series)
	if e != nil {
		t.Fatal(e)
	}
	return g
}
func changeWork(t *testing.T, o *organization.Service, c model.WorkCommand) model.WorkResult {
	t.Helper()
	g := commandContext(t, o, c)
	c.Expected = g.Expected
	if c.Action == "series_sort" && c.Snapshot == "" {
		c.Snapshot = g.Catalog
	}
	p, e := o.ChangeWork(context.Background(), "a", c, true)
	if e != nil {
		t.Fatal("preview", c.Action, e)
	}
	v, e := o.ChangeWork(context.Background(), "a", p.Command, false)
	if e != nil {
		t.Fatal("commit", c.Action, e)
	}
	return v
}
func TestWorkOrganizationLifecycle(t *testing.T) {
	s, o := orgFixture(t)
	ctx := context.Background()
	series := changeWork(t, o, model.WorkCommand{Action: "series_create", LibraryID: "lib", Name: "Series", WorkIDs: []string{"i:b10", "i:b1", "i:b2"}})
	changeWork(t, o, model.WorkCommand{Action: "series_sort", ID: series.ID})
	detail, e := s.WorkContext(ctx, "a", nil, nil, []string{series.ID})
	if e != nil {
		t.Fatal(e)
	}
	want := []string{"i:b1", "i:b2", "i:b10"}
	if !equalStrings(detail.Series[0].WorkIDs, want) {
		t.Fatal(detail.Series)
	}
	changeWork(t, o, model.WorkCommand{Action: "work_name", ID: "i:b10", NameOp: "set", Name: "Preserve target"})
	merged := changeWork(t, o, model.WorkCommand{Action: "work_merge", ID: "i:b10", WorkIDs: []string{"i:b1"}})
	w, e := s.Work(ctx, merged.ID)
	if e != nil || w.PreferredEditionID != "b10" || w.Name != "Preserve target" || w.Position != 1 || w.EditionCount != 2 {
		t.Fatal(w, e)
	}
	c := model.WorkCommand{Action: "edition_move", EditionID: "b10", TargetWorkID: "i:book"}
	g := commandContext(t, o, c)
	c.Expected = g.Expected
	if _, e = o.ChangeWork(ctx, "a", c, true); !errors.Is(e, model.ErrPreferredRequired) {
		t.Fatal(e)
	}
	c.RemainingPreferredEditionID = "b1"
	changeWork(t, o, c)
	w, e = s.Work(ctx, "i:b10")
	if e != nil || w.EditionCount != 1 || w.PreferredEditionID != "b1" || w.SeriesID != series.ID || w.Position != 1 {
		t.Fatal("single-edition work lost", w, e)
	}
	target, e := s.Work(ctx, "i:book")
	if e != nil || target.EditionCount != 2 {
		t.Fatal(target, e)
	}
	split := changeWork(t, o, model.WorkCommand{Action: "work_split", ID: "i:book", EditionIDs: []string{"b10"}})
	fresh, e := s.Work(ctx, split.ID)
	if e != nil || fresh.EditionCount != 1 || fresh.SeriesID != "" {
		t.Fatal(fresh, e)
	}
	changeWork(t, o, model.WorkCommand{Action: "edition_move", EditionID: "b10", TargetWorkID: "i:b10"})
	if _, e = s.Work(ctx, split.ID); !errors.Is(e, model.ErrNotFound) {
		t.Fatal("empty work retained", e)
	}
	if e = checkOrganization(ctx, s.Read); e != nil {
		t.Fatal(e)
	}
}
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func TestWorkConflictsCollisionAndAtomicFailure(t *testing.T) {
	s, o := orgFixture(t)
	ctx := context.Background()
	changeWork(t, o, model.WorkCommand{Action: "work_merge", ID: "i:book", WorkIDs: []string{"i:b2"}})
	c := model.WorkCommand{Action: "work_split", ID: "i:book", EditionIDs: []string{"b2"}, NewWorkID: "i:b1"}
	c.Expected = commandContext(t, o, c).Expected
	if _, e := o.ChangeWork(ctx, "a", c, true); !errors.Is(e, model.ErrMembership) {
		t.Fatal("collision", e)
	}
	c.NewWorkID = "fresh"
	p, e := o.ChangeWork(ctx, "a", c, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("CREATE TRIGGER fail_work_insert BEFORE INSERT ON works BEGIN SELECT RAISE(ABORT,'fixture failure');END"); e != nil {
		t.Fatal(e)
	}
	if _, e = o.ChangeWork(ctx, "a", p.Command, false); e == nil {
		t.Fatal("failure accepted")
	}
	w, _ := s.Work(ctx, "i:book")
	if w.EditionCount != 2 {
		t.Fatal("partial split", w)
	}
	s.Write.Exec("DROP TRIGGER fail_work_insert")
	changeWork(t, o, model.WorkCommand{Action: "work_name", ID: "i:book", NameOp: "set", Name: "Changed"})
	if _, e = o.ChangeWork(ctx, "a", p.Command, false); !errors.Is(e, model.ErrConflict) {
		t.Fatal("stale split", e)
	}
	other := changeWork(t, o, model.WorkCommand{Action: "series_create", LibraryID: "lib", Name: "Other", WorkIDs: []string{"i:b1"}})
	one := changeWork(t, o, model.WorkCommand{Action: "series_create", LibraryID: "lib", Name: "One", WorkIDs: []string{"i:book"}})
	c = model.WorkCommand{Action: "work_merge", ID: "i:book", WorkIDs: []string{"i:b1"}}
	c.Expected = commandContext(t, o, c).Expected
	if _, e = o.ChangeWork(ctx, "a", c, true); !errors.Is(e, model.ErrSeriesConflict) {
		t.Fatal(e)
	}
	moved := changeWork(t, o, model.WorkCommand{Action: "edition_move", EditionID: "b2", TargetWorkID: "i:b1"})
	if moved.Effects[0].FromSeriesID != one.ID || moved.Effects[0].ToSeriesID != other.ID {
		t.Fatal(moved.Effects)
	}
	if e = checkOrganization(ctx, s.Read); e != nil {
		t.Fatal(e)
	}
}
func TestWorkMergeUnassignedTargetInheritsEarliestSeriesPlace(t *testing.T) {
	s, o := orgFixture(t)
	ctx := context.Background()
	series := changeWork(t, o, model.WorkCommand{Action: "series_create", LibraryID: "lib", Name: "Series", WorkIDs: []string{"i:b10", "i:b1", "i:b2"}})
	changeWork(t, o, model.WorkCommand{Action: "work_merge", ID: "i:book", WorkIDs: []string{"i:b10", "i:b2"}})
	w, e := s.Work(ctx, "i:book")
	if e != nil || w.SeriesID != series.ID || w.Position != 1 || w.PreferredEditionID != "book" {
		t.Fatal(w, e)
	}
	split := changeWork(t, o, model.WorkCommand{Action: "work_split", ID: w.ID, EditionIDs: []string{"b2"}, KeepSeries: true})
	fresh, e := s.Work(ctx, split.ID)
	if e != nil || fresh.SeriesID != series.ID || fresh.Position != 2 {
		t.Fatal(fresh, e)
	}
	separated := changeWork(t, o, model.WorkCommand{Action: "work_separate", ID: w.ID, KeepSeries: true})
	if len(separated.Effects) != 1 {
		t.Fatal(separated)
	}
	if e = checkOrganization(ctx, s.Read); e != nil {
		t.Fatal(e)
	}
}
