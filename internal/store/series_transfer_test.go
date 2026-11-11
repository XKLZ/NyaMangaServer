package store

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func TestSeriesTransferAtomic(t *testing.T) {
	s, o := orgFixture(t)
	ctx := context.Background()
	source := changeWork(t, o, model.WorkCommand{Action: "series_create", LibraryID: "lib", Name: "Source", WorkIDs: []string{"i:b1", "i:b2"}})
	target := changeWork(t, o, model.WorkCommand{Action: "series_create", LibraryID: "lib", Name: "Target"})
	c := model.WorkCommand{Action: "series_transfer", ID: source.ID, TargetSeriesID: target.ID, WorkIDs: []string{"i:b1"}}
	c.Expected = commandContext(t, o, c).Expected
	p, e := o.ChangeWork(ctx, "a", c, true)
	if e != nil {
		t.Fatal(e)
	}
	s.Write.Exec("CREATE TRIGGER fail_series_member BEFORE INSERT ON series_members BEGIN SELECT RAISE(ABORT,'fixture failure');END")
	if _, e = o.ChangeWork(ctx, "a", p.Command, false); e == nil {
		t.Fatal("failure accepted")
	}
	s.Write.Exec("DROP TRIGGER fail_series_member")
	before, e := s.SeriesWorkPage(ctx, source.ID, "", 1, 50)
	if e != nil || before.Total != 2 {
		t.Fatal(before, e)
	}
	if _, e = o.ChangeWork(ctx, "a", p.Command, false); e != nil {
		t.Fatal(e)
	}
	if _, e = o.ChangeWork(ctx, "a", p.Command, false); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	page, e := s.SeriesWorkPage(ctx, target.ID, "", 1, 50)
	if e != nil || page.Total != 1 || page.Items[0].ID != "i:b1" || page.Expected["series:"+target.ID] != 2 {
		t.Fatal(page, e)
	}
	if e = checkOrganization(ctx, s.Read); e != nil {
		t.Fatal(e)
	}
}
