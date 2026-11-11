package store

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func TestEditionLabelsPersistThroughWorkChanges(t *testing.T) {
	s, o := orgFixture(t)
	ctx := context.Background()
	changeWork(t, o, model.WorkCommand{Action: "edition_labels", EditionID: "book", Labels: []string{"中文", "彩色", "中文"}})
	page, e := s.WorkEditionPage(ctx, "a", "i:book", "", 1, 1)
	if e != nil || len(page.Items) != 1 || !equalStrings(page.Items[0].Labels, []string{"中文", "彩色"}) {
		t.Fatal(page, e)
	}
	c := model.WorkCommand{Action: "edition_labels", EditionID: "book", Labels: []string{"修订"}}
	c.Expected = commandContext(t, o, c).Expected
	changeWork(t, o, c)
	if _, e = o.ChangeWork(ctx, "a", c, false); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	changeWork(t, o, model.WorkCommand{Action: "work_merge", ID: "i:b1", WorkIDs: []string{"i:book"}})
	moved, e := s.WorkEditionPage(ctx, "a", "i:b1", "", 1, 100)
	if e != nil || moved.Total != 2 {
		t.Fatal(moved, e)
	}
	for _, ed := range moved.Items {
		if ed.ID == "book" && !equalStrings(ed.Labels, []string{"修订"}) {
			t.Fatal(ed)
		}
	}
	split := changeWork(t, o, model.WorkCommand{Action: "work_split", ID: "i:b1", EditionIDs: []string{"book"}})
	page, e = s.WorkEditionPage(ctx, "a", split.ID, "", 1, 1)
	if e != nil || !equalStrings(page.Items[0].Labels, []string{"修订"}) {
		t.Fatal(page, e)
	}
	c = model.WorkCommand{Action: "edition_labels", EditionID: "book", Labels: []string{""}}
	c.Expected = commandContext(t, o, c).Expected
	if _, e = o.ChangeWork(ctx, "a", c, true); !errors.Is(e, model.ErrInput) {
		t.Fatal(e)
	}
	changeWork(t, o, model.WorkCommand{Action: "edition_labels", EditionID: "book", Labels: []string{}})
}
