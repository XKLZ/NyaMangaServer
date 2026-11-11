package store

import (
	"context"
	"encoding/json"
	rec "nyamangaserver.local/server/internal/recommendation"
	"reflect"
	"strings"
	"testing"
)

func seedSeriesManagement(t *testing.T, s *Store) {
	t.Helper()
	_, e := s.Write.Exec(`INSERT INTO series VALUES('a','lib','A',1),('b','lib','B',1),('empty','lib','Empty',1);
 INSERT INTO series_members VALUES('a','i:e0001',1),('a','i:e0000',2),('b','i:e0002',1);
 UPDATE files SET content_revision=7 WHERE edition_id='e0001';
 INSERT INTO cover_selections(edition_id,mode,content_revision,revision) VALUES('e0001','auto',7,9);`)
	if e != nil {
		t.Fatal(e)
	}
}
func TestSeriesManagementCoversOrderPreferredAndReadOnlySnapshots(t *testing.T) {
	ctx := context.Background()
	s := discoveryStore(t, 5)
	seedSeriesManagement(t, s)
	before := dumpRows(t, s, "SELECT * FROM series ORDER BY id")
	page, e := s.SeriesPage(ctx, "lib", "", 1, 100)
	if e != nil || page.Total != 3 || len(page.Items) != 3 {
		t.Fatal(page, e)
	}
	c := page.Items[0].Cover
	if c == nil || c.WorkID != "i:e0001" || c.EditionID != "e0001" || c.ContentRevision != 7 || c.CoverRevision != 9 {
		t.Fatal(c)
	}
	if page.Items[2].Cover != nil || page.Items[2].MemberCount != 0 {
		t.Fatal("empty series cover", page.Items[2])
	}
	// Query-only covers must not enter organization snapshots or task JSON.
	raw, _ := json.Marshal(page.Items[0])
	if strings.Contains(string(raw), `"cover"`) {
		t.Fatal("presentation changed frozen JSON", string(raw))
	}
	second, e := s.SeriesPage(ctx, "lib", "", 2, 1)
	if e != nil || second.Total != 3 || len(second.Items) != 1 || second.Items[0].ID != "b" {
		t.Fatal(second, e)
	}
	detail, e := s.SeriesDetail(ctx, "a")
	if e != nil || !reflect.DeepEqual(detail.Cover, c) {
		t.Fatal(detail, e)
	}
	members, e := s.SeriesWorkPage(ctx, "a", "", 1, 1)
	if e != nil || members.Total != 2 || members.Items[0].ID != c.WorkID || members.Items[0].ContentRevision != 7 || members.Items[0].CoverRevision != 9 {
		t.Fatal(members, e)
	}
	if !reflect.DeepEqual(before, dumpRows(t, s, "SELECT * FROM series ORDER BY id")) {
		t.Fatal("query changed series")
	}
	// Rebinding a valid preferred Edition changes the summary without any cache IO.
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{"UPDATE editions SET work_id='i:e0001' WHERE id='e0004'", "DELETE FROM works WHERE id='i:e0004'", "UPDATE works SET preferred_edition_id='e0004',revision=revision+1 WHERE id='i:e0001'", "UPDATE files SET content_revision=44 WHERE edition_id='e0004'", "INSERT INTO cover_selections(edition_id,mode,content_revision,revision) VALUES('e0004','auto',44,17)"} {
		if _, e = tx.ExecContext(ctx, q); e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	detail, e = s.SeriesDetail(ctx, "a")
	if e != nil || detail.Cover.EditionID != "e0004" || detail.Cover.ContentRevision != 44 || detail.Cover.CoverRevision != 17 {
		t.Fatal(detail, e)
	}
	members, e = s.SeriesWorkPage(ctx, "a", "", 1, 1)
	if e != nil || members.Items[0].ContentRevision != 44 || members.Items[0].CoverRevision != 17 || members.Items[0].EditionCount != 2 {
		t.Fatal(members, e)
	}
	if _, e = s.Write.Exec("UPDATE series_members SET position=-position WHERE series_id='a';UPDATE series_members SET position=CASE work_id WHEN 'i:e0000' THEN 1 ELSE 2 END WHERE series_id='a'"); e != nil {
		t.Fatal(e)
	}
	detail, e = s.SeriesDetail(ctx, "a")
	if e != nil || detail.Cover.EditionID != "e0000" {
		t.Fatal("cover ignored current order", detail, e)
	}
	empty, e := s.SeriesDetail(ctx, "empty")
	if e != nil || empty.Cover != nil {
		t.Fatal(empty, e)
	}
}
func TestSeriesManagementTargetAlternativesBeforePagination(t *testing.T) {
	ctx := context.Background()
	s := discoveryStore(t, 5)
	seedSeriesManagement(t, s)
	add := func(id, target, kind, state string, primary bool, order []string) {
		t.Helper()
		p := rec.SeriesProposal{ID: id, Target: target, Kind: kind, LibraryID: "lib", Name: "Anonymous", Order: order, NewIDs: []string{"i:e0004"}, ExistingIDs: []string{}, Revision: 1, Tier: "strong"}
		if _, e := s.Write.Exec("INSERT INTO series_proposals(id,library_id,proposal_key,kind,target_series_id,tier,primary_plan,revision,state,plan_json,created_at) VALUES(?,'lib',?,?,?,'strong',?,1,?,?,1)", id, id, kind, target, primary, state, storedSeries(p)); e != nil {
			t.Fatal(e)
		}
	}
	add("a1", "a", "add_to_series", "pending", true, []string{"i:e0004", "i:e0001", "i:e0000"})
	add("a2", "a", "add_to_series", "pending", false, []string{"i:e0004", "i:e0001", "i:e0000"})
	add("b1", "b", "add_to_series", "pending", true, []string{"i:e0002", "i:e0004"})
	add("old", "a", "add_to_series", "dismissed", false, []string{"i:e0001", "i:e0004"})
	add("new", "", "create_series", "pending", true, []string{"i:e0003", "i:e0004"})
	before := dumpRows(t, s, "SELECT * FROM series_proposals ORDER BY id")
	legacy, e := s.SeriesProposals(ctx, "", "lib", "", "pending", 1, 100)
	if e != nil || legacy.Total != 3 {
		t.Fatal(legacy, e)
	}
	f := rec.SeriesProposalFilter{TargetSeriesID: "a", Alternatives: true}
	page, e := s.SeriesProposals(ctx, "", "lib", "add_to_series", "pending", 2, 1, f)
	if e != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].ID != "a2" {
		t.Fatal("filter applied after pagination", page, e)
	}
	p := page.Items[0]
	if p.Cover == nil || p.Cover.EditionID != "e0001" {
		t.Fatal("join used new member instead of target cover", p)
	}
	f.Alternatives = false
	page, e = s.SeriesProposals(ctx, "", "lib", "add_to_series", "pending", 1, 1, f)
	if e != nil || page.Total != 1 || page.Items[0].ID != "a1" {
		t.Fatal(page, e)
	}
	history, e := s.SeriesProposals(ctx, "", "lib", "", "dismissed", 1, 25, f)
	if e != nil || history.Total != 1 || history.Items[0].ID != "old" {
		t.Fatal("historical visibility changed", history, e)
	}
	for _, target := range []string{"missing", "a' OR 1=1 --"} {
		page, e = s.SeriesProposals(ctx, "", "lib", "", "pending", 1, 25, rec.SeriesProposalFilter{TargetSeriesID: target, Alternatives: true})
		if e != nil || page.Total != 0 {
			t.Fatal(page, e)
		}
	}
	created, e := s.SeriesProposal(ctx, "new")
	if e != nil || created.Cover == nil || created.Cover.WorkID != "i:e0003" {
		t.Fatal(created, e)
	}
	raw, _ := json.Marshal(created)
	if strings.Contains(string(raw), `"cover"`) {
		t.Fatal("cover leaked into frozen plan JSON")
	}
	if !reflect.DeepEqual(before, dumpRows(t, s, "SELECT * FROM series_proposals ORDER BY id")) {
		t.Fatal("read changed stored proposals")
	}
}
