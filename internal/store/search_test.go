package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/search"
	"testing"
)

func TestSearchFoldingSnapshotsAndSaved(t *testing.T) {
	s, o := orgFixture(t)
	ctx := context.Background()
	svc := search.New(s)
	group := changeWork(t, o, model.WorkCommand{Action: "work_merge", ID: "i:b1", WorkIDs: []string{"i:b2"}})
	run := func(v model.SearchInput) model.SearchResult {
		t.Helper()
		r, e := svc.Search(ctx, "a", v)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	r := run(model.SearchInput{Query: "第2", Collapse: "versions"})
	if r.Total != 1 || r.Items[0].ID != group.ID || r.Items[0].Representative.ID != "b1" || !r.Items[0].OtherVersion {
		t.Fatal(r)
	}
	m, e := svc.Matches(ctx, "a", model.SearchInput{Query: "第2", Collapse: "versions", Snapshot: r.Snapshot}, "work", group.ID)
	if e != nil || m.Total != 1 || m.Items[0].ID != "b2" {
		t.Fatal(m, e)
	}
	yes := true
	if _, e = s.SetUserState(ctx, "a", "b2", model.StatePatch{Favorite: &yes}); e != nil {
		t.Fatal(e)
	}
	favorites := run(model.SearchInput{Scope: "favorites", Collapse: "versions"})
	if favorites.Total != 1 || favorites.Items[0].Favorite != "partial" || favorites.Items[0].Representative.ID != "b2" {
		t.Fatal(favorites)
	}
	if _, e = s.SetUserState(ctx, "a", "b1", model.StatePatch{Hidden: &yes}); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Search(ctx, "a", model.SearchInput{Snapshot: r.Snapshot, Page: 2}); !errors.Is(e, model.ErrSnapshot) {
		t.Fatal(e)
	}
	r = run(model.SearchInput{Query: "第2", Collapse: "versions"})
	if r.Items[0].Representative.ID != "b2" {
		t.Fatal(r)
	}
	r = run(model.SearchInput{Scope: "hidden"})
	if r.Total != 1 || r.Items[0].Representative.ID != "b1" {
		t.Fatal(r)
	}
	r = run(model.SearchInput{Query: `"' OR 1=1 --"`})
	if r.Total != 0 {
		t.Fatal("SQL injection", r)
	}
	if e = s.ApplyOverrides(ctx, "b10", 1, map[string]model.Override{"title_original": {Op: "set", Value: json.RawMessage(`"Ｏｒｉｇｉｎａｌ 日本"`)}, "tags": {Op: "set", Value: json.RawMessage(`[{"namespace":"artist","name":"ANON"}]`)}}); e != nil {
		t.Fatal(e)
	}
	r = run(model.SearchInput{Query: `original artist:ano*`, Collapse: "none"})
	if r.Total != 1 || r.Items[0].Representative.ID != "b10" {
		t.Fatal(r)
	}
	r = run(model.SearchInput{Query: `title:original`})
	if r.Total != 0 {
		t.Fatal(r)
	}
	saved, e := svc.Save(ctx, "a", "", "匿名条件", model.SearchInput{Query: "original", Page: 8, Snapshot: r.Snapshot}, 0)
	if e != nil || saved.Conditions.Page != 0 || saved.Conditions.Snapshot != "" {
		t.Fatal(saved, e)
	}
	if _, e = svc.Save(ctx, "a", saved.ID, "stale", saved.Conditions, 0); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	if e = svc.Delete(ctx, "another", saved.ID, 1); !errors.Is(e, model.ErrNotFound) {
		t.Fatal(e)
	}
	list, e := svc.List(ctx, "a")
	if e != nil || len(list) != 1 {
		t.Fatal(list, e)
	}
	if e = svc.Delete(ctx, "a", saved.ID, 1); e != nil {
		t.Fatal(e)
	}
}
func TestSearchSeriesAndCount(t *testing.T) {
	s, o := orgFixture(t)
	ctx := context.Background()
	svc := search.New(s)
	changeWork(t, o, model.WorkCommand{Action: "series_create", LibraryID: "lib", Name: "匿名", WorkIDs: []string{"i:b1", "i:b2", "i:b10"}})
	r, e := svc.Search(ctx, "a", model.SearchInput{Query: "第", PageSize: 1})
	if e != nil || r.Total != 1 || len(r.Items) != 1 || r.Items[0].Kind != "series" || r.Items[0].MatchCount != 3 {
		t.Fatal(r, e)
	}
	r, e = svc.Search(ctx, "a", model.SearchInput{Query: "第", Collapse: "none", Sort: "title", Order: "asc", PageSize: 1, Page: 2})
	if e != nil || r.Total != 3 || r.Items[0].Representative.ID != "b2" {
		t.Fatal(r, e)
	}
}
func TestBoundedMatchesAndInvalidation(t *testing.T) {
	s, o := orgFixture(t)
	ctx := context.Background()
	svc := search.New(s)
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	for i := 1; i <= 110; i++ {
		id := fmt.Sprintf("many-%03d", i)
		b := model.Edition{ID: id, LibraryID: "lib", Revision: 1, File: model.File{Path: fmt.Sprintf("many/%03d.cbz", i), FilenameTitle: fmt.Sprintf("第%d卷", i), Size: 1, Mtime: 1, ContentRevision: 1, State: "available", IndexedAt: int64(i)}}
		if e = saveEditionIndex(ctx, tx, b); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = tx.Exec(`INSERT INTO series VALUES('many-series','lib','Many',1); INSERT INTO series_members SELECT 'many-series',work_id,CAST(substr(id,6) AS INTEGER) FROM editions WHERE id LIKE 'many-%'`); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	r, e := svc.Search(ctx, "a", model.SearchInput{Query: "第", Collapse: "series"})
	if e != nil {
		t.Fatal(e)
	}
	var hit model.SearchHit
	for _, h := range r.Items {
		if h.ID == "many-series" {
			hit = h
		}
	}
	if hit.MatchCount != 110 || len(hit.MatchIDs) != 100 {
		t.Fatal(hit)
	}
	m, e := svc.Matches(ctx, "a", model.SearchInput{Query: "第", Collapse: "series", Snapshot: r.Snapshot, Page: 2, PageSize: 100}, "series", "many-series")
	if e != nil || m.Total != 110 || len(m.Items) != 10 {
		t.Fatal(m, e)
	}
	// Job-only traffic must not expire catalog pagination.
	if _, e = s.Write.Exec("UPDATE jobs SET updated_at=updated_at+1"); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Search(ctx, "a", model.SearchInput{Snapshot: r.Snapshot, Page: 2}); e != nil {
		t.Fatal(e)
	}
	g, e := o.WorkContext(ctx, "a", nil, nil, []string{"many-series"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ApplyOverrides(ctx, "many-001", 1, map[string]model.Override{"title": {Op: "set", Value: json.RawMessage(`"新标题"`)}}); e != nil {
		t.Fatal(e)
	}
	if _, e = o.ChangeWork(ctx, "a", model.WorkCommand{Action: "series_sort", ID: "many-series", Expected: g.Expected, Snapshot: g.Catalog}, false); !errors.Is(e, model.ErrSnapshot) {
		t.Fatal(e)
	}
}
