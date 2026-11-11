package organization

import (
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func TestAdditionPreservesTargetSeriesAndRejectsExistingMultiSource(t *testing.T) {
	c := model.WorkContext{Works: []model.Work{{ID: "target", LibraryID: "lib", EditionIDs: []string{"a", "b"}, PreferredEditionID: "a", Revision: 1}, {ID: "source", LibraryID: "lib", EditionIDs: []string{"c"}, PreferredEditionID: "c", SeriesID: "series", Position: 1, Revision: 1}}, Editions: []model.WorkEdition{{ID: "a", WorkID: "target", LibraryID: "lib", Revision: 1}, {ID: "b", WorkID: "target", LibraryID: "lib", Revision: 1}, {ID: "c", WorkID: "source", LibraryID: "lib", Revision: 1}}, Series: []model.Series{{ID: "series", LibraryID: "lib", Revision: 1, WorkIDs: []string{"source"}}}, Expected: map[string]int64{"work:target": 1, "work:source": 1, "edition:a": 1, "edition:b": 1, "edition:c": 1, "series:series": 1}}
	v, e := PlanWorkAddition(c, "target", []string{"c"})
	if e != nil {
		t.Fatal(e)
	}
	if len(v.Context.Works) != 1 || v.Context.Works[0].SeriesID != "" || len(v.Context.Works[0].EditionIDs) != 3 || len(v.Context.Series[0].WorkIDs) != 0 {
		t.Fatal(v)
	}
	c.Works[1].EditionIDs = append(c.Works[1].EditionIDs, "d")
	if _, e = PlanWorkAddition(c, "target", []string{"c"}); e == nil {
		t.Fatal("multi source accepted")
	}
}
