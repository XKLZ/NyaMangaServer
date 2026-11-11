package store

import (
	"context"
	"encoding/json"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
	"nyamangaserver.local/server/internal/search"
	"reflect"
	"testing"
)

func setSummaryOriginal(t *testing.T, s *Store, id, value string) {
	t.Helper()
	b, e := s.Edition(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(value)
	op := "set"
	if value == "" {
		op = "empty"
		raw = nil
	}
	if e = s.ApplyOverrides(context.Background(), id, b.Revision, map[string]model.Override{"title_original": {Op: op, Value: raw}}); e != nil {
		t.Fatal(e)
	}
}
func TestTitleSummariesPreferredEffectiveAndFallback(t *testing.T) {
	s, id := tagWorkFixture(t)
	ctx := context.Background()
	setSummaryOriginal(t, s, "primary", "首選原題")
	setSummaryOriginal(t, s, "a-spare", "封面原題")
	setSummaryOriginal(t, s, "z-tagged", "標籤原題")
	in := tagInput("a")
	in.PageSize = 1
	first, e := s.TagCatalogWorks(ctx, id, in)
	if e != nil {
		t.Fatal(e)
	}
	w := first.Items[0]
	if w.Name != "Manual Work" || w.TitleOriginal != "首選原題" || w.Cover.EditionID != "primary" {
		t.Fatal(w)
	}
	in.Page = 2
	in.Snapshot = first.Snapshot
	second, e := s.TagCatalogWorks(ctx, id, in)
	if e != nil || second.Total != 2 || len(second.Items) != 1 || second.Items[0].TitleOriginal != "" {
		t.Fatal(second, e)
	}
	if _, e = s.Write.Exec("UPDATE editions SET trashed_at=1 WHERE id='primary'"); e != nil {
		t.Fatal(e)
	}
	in = tagInput("a")
	got, e := s.TagCatalogWorks(ctx, id, in)
	if e != nil {
		t.Fatal(e)
	}
	if got.Items[0].Cover.EditionID != "a-spare" || got.Items[0].TitleOriginal != "首選原題" {
		t.Fatal(got)
	}
	setSummaryOriginal(t, s, "primary", "")
	got, e = s.TagCatalogWorks(ctx, id, in)
	if e != nil || got.Items[0].TitleOriginal != "" || got.Items[0].Name != "Manual Work" {
		t.Fatal(got, e)
	}
	ed, e := s.TagCatalogEditions(ctx, id, in)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, x := range ed.Items {
		if x.ID == "z-tagged" {
			found = true
			if x.TitleOriginal != "標籤原題" {
				t.Fatal(x)
			}
		}
	}
	if !found {
		t.Fatal(ed)
	}
}
func TestTitleSummariesLiveImportAndSearch(t *testing.T) {
	s := reviewFixture(t, 3)
	ctx := context.Background()
	var frozen string
	if e := s.Read.QueryRow("SELECT result_json FROM import_candidates WHERE edition_id='e0000'").Scan(&frozen); e != nil {
		t.Fatal(e)
	}

	sourceTitle, sourceOriginal := "Source heading", "來源原題"
	tx, err := s.Write.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = publishSource(ctx, tx, "e0000", model.RemoteMetadata{ExternalID: "summary-source", URL: "https://e-hentai.org/g/123/0123456789/", Method: "manual", Fields: model.Metadata{Title: &sourceTitle, TitleOriginal: &sourceOriginal, Tags: []model.TagName{}}, Raw: json.RawMessage(`{}`)}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	sourced, err := s.Edition(ctx, "e0000")
	if err != nil || sourced.TitleOriginal != sourceOriginal || sourced.Title != sourceTitle {
		t.Fatal(sourced, err)
	}
	setSummaryOriginal(t, s, "e0000", "人工原題")
	page, e := s.ImportMembers(ctx, "review-ux", "all", 1, 2)
	if e != nil || page.Total != 3 || len(page.Items) != 2 {
		t.Fatal(page, e)
	}
	if page.Items[0].TitleOriginal != "人工原題" || page.Items[1].TitleOriginal != "" || *page.Items[0].Candidate.Fields.TitleOriginal != "匿名原文 0" {
		t.Fatal(page)
	}
	var after string
	s.Read.QueryRow("SELECT result_json FROM import_candidates WHERE edition_id='e0000'").Scan(&after)
	if after != frozen {
		t.Fatal("candidate rewritten")
	}
	b, e := s.Edition(ctx, "e0000")
	if e != nil || b.TitleOriginal != "人工原題" {
		t.Fatal(b, e)
	}
	// Non-admin lists also carry the raw effective original, not the normalized search key.
	svc := search.New(s)
	got, e := svc.Search(ctx, "", model.SearchInput{LibraryID: b.LibraryID, Collapse: "none"})
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, x := range got.Items {
		if x.Representative.ID == b.ID {
			found = true
			if x.Representative.TitleOriginal != "人工原題" {
				t.Fatal(x)
			}
		}
	}
	if !found {
		t.Fatal(got)
	}
	setSummaryOriginal(t, s, "e0000", "")
	cleared, err := s.Edition(ctx, "e0000")
	if err != nil || cleared.TitleOriginal != "" || cleared.Title != sourceTitle {
		t.Fatal(cleared, err)
	}
}
func TestTitleSummariesFrozenHistoricalFallback(t *testing.T) {
	s, _ := tagWorkFixture(t)
	ctx := context.Background()
	b, _ := s.Edition(ctx, "z-tagged")
	plan, e := s.CreateMaintenancePlan(ctx, model.MaintenanceInput{Operation: "trash", BookIDs: []string{"z-tagged"}, Expected: map[string]int64{"book:z-tagged": b.Revision}})
	if e != nil {
		t.Fatal(e)
	}
	before, _ := json.Marshal(plan)
	setSummaryOriginal(t, s, "z-tagged", "今日原題")
	loaded, e := s.MaintenancePlan(ctx, plan.ID)
	if e != nil {
		t.Fatal(e)
	}
	after, _ := json.Marshal(loaded)
	if string(before) != string(after) {
		t.Fatal("historical impact changed")
	}
	raw := `{"members":[{"id":"z-tagged","title":"Historical title"}]}`
	p, e := scanProposal(s.Read.QueryRow("SELECT ?,'pending',1,1", raw))
	if e != nil {
		t.Fatal(e)
	}
	members := append([]rec.ProposalMember(nil), p.Members...)
	tx, e := s.Read.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	plans := []rec.Proposal{p}
	if e = refreshProposalLabelViews(ctx, tx, plans); e != nil {
		t.Fatal(e)
	}
	if plans[0].Members[0].TitleOriginal != "" || !reflect.DeepEqual(members[0].Title, plans[0].Members[0].Title) {
		t.Fatal(plans)
	}
}
