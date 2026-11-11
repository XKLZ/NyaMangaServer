package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func searchFixture(t *testing.T) (*Store, model.SourceSearch) {
	t.Helper()
	s := reviewFixture(t, 2)
	v, e := s.CreateSourceSearch(context.Background(), "review-ux", "e0000", "eh", model.SourceSearchInput{Mode: "title", Query: "匿名 題名", RequestKey: "query-one", ExpectedRevision: 1})
	if e != nil {
		t.Fatal(e)
	}
	return s, v
}
func searchResults(n int) model.SearchPage {
	p := model.SearchPage{Items: []model.Candidate{}}
	for i := 0; i < n; i++ {
		p.Items = append(p.Items, model.Candidate{URL: fmt.Sprintf("https://e-hentai.org/g/%d/0123456789/", i+1), Title: fmt.Sprintf("Anonymous %d", i)})
	}
	return p
}
func TestSourceSearchStagingPaginationAndSelection(t *testing.T) {
	ctx := context.Background()
	s, v := searchFixture(t)
	again, e := s.CreateSourceSearch(ctx, "review-ux", "e0000", "eh", model.SourceSearchInput{Mode: "title", Query: "匿名 題名", RequestKey: "query-one", ExpectedRevision: 1})
	if e != nil || again.ID != v.ID {
		t.Fatal(again, e)
	}
	j, e := s.Claim(ctx, v.JobID)
	if e != nil {
		t.Fatal(e)
	}
	op := model.SearchOperation{SearchID: v.ID, Page: 1}
	p := searchResults(17)
	p.Next = "25"
	if e = s.PublishSearchPage(ctx, *j, op, p); e != nil {
		t.Fatal(e)
	}
	page, e := s.SourceSearchResults(ctx, "review-ux", "e0000", v.ID, 2)
	if e != nil || len(page.Items) != 7 || page.Search.Loaded != 17 || page.Search.Continuation != "more" {
		t.Fatal(page, e)
	}
	title := "New candidate"
	r := model.RemoteMetadata{URL: p.Items[0].URL, ExternalID: "1", Fields: model.Metadata{Title: &title, Tags: []model.TagName{}}, Raw: json.RawMessage(`{"filecount":"99"}`)}
	if e = s.PublishSearchResults(ctx, *j, op, []model.FetchResult{{URL: r.URL, Metadata: &r}}); e != nil {
		t.Fatal(e)
	}
	before, e := s.ImportCandidateDetail(ctx, "review-ux", "e0000")
	if e != nil || before.URL == r.URL {
		t.Fatal(before, e)
	}
	if e = s.FailBackgroundItem(ctx, *j, model.Item{Ordinal: 1}, ""); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishBackground(ctx, *j, ""); e != nil {
		t.Fatal(e)
	}
	page, e = s.SourceSearchResults(ctx, "review-ux", "e0000", v.ID, 1)
	if e != nil || page.Items[0].PageCount == nil || *page.Items[0].PageCount != 99 {
		t.Fatal(page, e)
	}
	sel := model.SearchSelect{ResultID: page.Items[0].ID, ExpectedRevision: page.Search.Revision, ResultRevision: page.Items[0].Revision, MemberRevision: 1, EditionRevision: page.Search.EditionRevision}
	if e = s.SelectSourceSearch(ctx, "review-ux", "e0000", v.ID, sel); e != nil {
		t.Fatal(e)
	}
	if e = s.SelectSourceSearch(ctx, "review-ux", "e0000", v.ID, sel); e != nil {
		t.Fatal("replay", e)
	}
	staged, e := s.ImportCandidateDetail(ctx, "review-ux", "e0000")
	if e != nil || staged.URL != r.URL || staged.Revision != before.Revision+1 {
		t.Fatal(staged, e)
	}
	var count int
	s.Read.QueryRow("SELECT count(*) FROM edition_sources").Scan(&count)
	if count != 0 {
		t.Fatal("search adopted a source")
	}
	// Existing review transaction can adopt a chosen candidate later.
	if e = s.ReviewImport(ctx, "review-ux", []model.ImportReview{{ID: "e0000", ExpectedRevision: 2, EditionRevision: sel.EditionRevision, CandidateRevision: staged.Revision, Decision: "accept"}}); e != nil {
		t.Fatal(e)
	}
	s.Read.QueryRow("SELECT count(*) FROM edition_sources").Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
}
func TestSourceSearchOldWorkerAndConflict(t *testing.T) {
	ctx := context.Background()
	s, v := searchFixture(t)
	j, e := s.Claim(ctx, v.JobID)
	if e != nil {
		t.Fatal(e)
	}
	op := model.SearchOperation{SearchID: v.ID, Page: 1}
	second, e := s.CreateSourceSearch(ctx, "review-ux", "e0000", "eh", model.SourceSearchInput{Mode: "title", Query: "other", RequestKey: "query-two", ExpectedRevision: 1})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.PublishSearchPage(ctx, *j, op, searchResults(1)); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal("old query published", e)
	}
	if _, e = s.SourceSearch(ctx, "review-ux", "e0001", second.ID); !errors.Is(e, model.ErrNotFound) {
		t.Fatal("cross-member read", e)
	}
	next, e := s.Claim(ctx, second.JobID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("UPDATE files SET content_revision=content_revision+1 WHERE edition_id='e0000'"); e != nil {
		t.Fatal(e)
	}
	if e = s.PublishSearchPage(ctx, *next, model.SearchOperation{SearchID: second.ID, Page: 1}, searchResults(1)); !errors.Is(e, model.ErrConflict) {
		t.Fatal("changed file published", e)
	}
}
func TestSourceSearchPartialFailuresAndControl(t *testing.T) {
	ctx := context.Background()
	s, v := searchFixture(t)
	j, e := s.Claim(ctx, v.JobID)
	if e != nil {
		t.Fatal(e)
	}
	op := model.SearchOperation{SearchID: v.ID, Page: 1}
	p := searchResults(2)
	if e = s.PublishSearchPage(ctx, *j, op, p); e != nil {
		t.Fatal(e)
	}
	if e = s.PublishSearchResults(ctx, *j, op, []model.FetchResult{{URL: p.Items[0].URL, Error: "provider_rejected"}}); e != nil {
		t.Fatal(e)
	}
	rows, e := s.SourceSearchResults(ctx, "review-ux", "e0000", v.ID, 1)
	if e != nil || rows.Items[0].State != "failed" || rows.Items[1].State != "pending" {
		t.Fatal(rows, e)
	}
	// Batch cancellation invalidates search workers as well as regular children.
	b, e := s.ImportBatch(ctx, "review-ux")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ControlImport(ctx, b.ID, "cancel", b.Revision); e != nil {
		t.Fatal(e)
	}
	if e = s.PublishSearchPage(ctx, *j, op, p); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal(e)
	}
}

func TestSourceSearchUnknownContinuationAndAtomicSelectionConflict(t *testing.T) {
	ctx := context.Background()
	s := reviewFixture(t, 1)
	// An old SHA1 result list is a seed, never an assertion of completeness.
	if _, e := s.Write.Exec(`UPDATE files SET representative_sha1='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',representative_algorithm='content-page8-v1' WHERE edition_id='e0000';INSERT INTO jobs(id,type,state,stage,generation,total,done,created_at,updated_at) VALUES('old-hash','metadata','completed','finished',1,1,1,1,1);INSERT INTO job_items(job_id,ordinal,state,edition_id,content_revision,selection_revision,input_json,candidates_json) VALUES('old-hash',1,'deferred','e0000',1,1,'{"lookup_sha1":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","lookup_algorithm":"content-page8-v1"}','[{"url":"https://e-hentai.org/g/1/0123456789/","title":"Anonymous"}]');UPDATE import_members SET active_job_id='old-hash',fetch_error='multiple_matches' WHERE edition_id='e0000'`); e != nil {
		t.Fatal(e)
	}
	v, e := s.CreateSourceSearch(ctx, "review-ux", "e0000", "eh", model.SourceSearchInput{Mode: "matches", RequestKey: "matches-one", ExpectedRevision: 1})
	if e != nil || v.Loaded != 1 || v.Continuation != "unknown" {
		t.Fatal(v, e)
	}
	j, e := s.Claim(ctx, v.JobID)
	if e != nil {
		t.Fatal(e)
	}
	op := model.SearchOperation{SearchID: v.ID, Page: 1}
	title := "Safe result"
	r := model.RemoteMetadata{URL: "https://e-hentai.org/g/1/0123456789/", ExternalID: "1", Fields: model.Metadata{Title: &title, Tags: []model.TagName{}}, Raw: json.RawMessage(`{}`)}
	if e = s.PublishSearchResults(ctx, *j, op, []model.FetchResult{{URL: r.URL, Metadata: &r}}); e != nil {
		t.Fatal(e)
	}
	if e = s.FailBackgroundItem(ctx, *j, model.Item{Ordinal: 1}, ""); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishBackground(ctx, *j, ""); e != nil {
		t.Fatal(e)
	}
	rows, e := s.SourceSearchResults(ctx, "review-ux", "e0000", v.ID, 1)
	if e != nil {
		t.Fatal(e)
	}
	before, e := s.ImportCandidateDetail(ctx, "review-ux", "e0000")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec(`UPDATE editions SET revision=revision+1 WHERE id='e0000'`); e != nil {
		t.Fatal(e)
	}
	in := model.SearchSelect{ResultID: rows.Items[0].ID, ResultRevision: rows.Items[0].Revision, ExpectedRevision: rows.Search.Revision, MemberRevision: 1, EditionRevision: v.EditionRevision}
	if e = s.SelectSourceSearch(ctx, "review-ux", "e0000", v.ID, in); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	after, e := s.ImportCandidateDetail(ctx, "review-ux", "e0000")
	if e != nil || before.Revision != after.Revision || before.URL != after.URL {
		t.Fatal("conflict replaced old candidate", after, e)
	}
}
