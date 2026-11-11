package organization

import (
	"errors"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func TestSeriesTransferValidatesBothAggregates(t *testing.T) {
	base := func() (model.WorkContext, model.WorkCommand) {
		g := model.WorkContext{Works: []model.Work{{ID: "w", LibraryID: "lib", Name: "W", Revision: 1, SeriesID: "from", Position: 1, EditionCount: 1, PreferredEditionID: "e"}}, Series: []model.Series{{ID: "from", LibraryID: "lib", Revision: 1, WorkIDs: []string{"w"}}, {ID: "to", LibraryID: "lib", Revision: 1, WorkIDs: []string{}}}, Expected: map[string]int64{"work:w": 1, "series:from": 1, "series:to": 1}}
		c := model.WorkCommand{Action: "series_transfer", ID: "from", TargetSeriesID: "to", WorkIDs: []string{"w"}, Expected: map[string]int64{"work:w": 1, "series:from": 1, "series:to": 1}}
		return g, c
	}
	for _, key := range []string{"work:w", "series:from", "series:to"} {
		g, c := base()
		delete(c.Expected, key)
		if _, e := PlanWork(g, c); !errors.Is(e, model.ErrConflict) {
			t.Fatal(key, e)
		}
	}
	g, c := base()
	g.Series[1].LibraryID = "other"
	if _, e := PlanWork(g, c); !errors.Is(e, model.ErrLibraryConflict) {
		t.Fatal(e)
	}
	g, c = base()
	v, e := PlanWork(g, c)
	if e != nil || len(v.Context.Series[0].WorkIDs) != 0 || len(v.Context.Series[1].WorkIDs) != 1 || g.Series[0].WorkIDs[0] != "w" {
		t.Fatal(v, e)
	}
}
