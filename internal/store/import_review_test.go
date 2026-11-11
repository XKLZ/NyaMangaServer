package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func reviewFixture(t *testing.T, n int) *Store {
	t.Helper()
	s := discoveryStore(t, n)
	ctx := context.Background()
	j := importScan(t, s, "review-ux")
	if e := s.Finish(ctx, *j, false, ""); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Write.Exec("UPDATE import_batches SET stage='metadata',state='review' WHERE id='review-ux'"); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("e%04d", i)
		b, e := s.Edition(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.Write.Exec("INSERT INTO import_members(batch_id,edition_id,change_kind,content_revision) VALUES('review-ux',?,'new',?)", id, b.ContentRevision); e != nil {
			t.Fatal(e)
		}
		title := fmt.Sprintf("Anonymous title %d", i)
		original := fmt.Sprintf("匿名原文 %d", i)
		result := model.RemoteMetadata{ExternalID: fmt.Sprint(1000 + i), URL: fmt.Sprintf("https://e-hentai.org/g/%d/abcdef1234/", 1000+i), Method: "sha1", Raw: json.RawMessage(`{}`), Fields: model.Metadata{Title: &title, TitleOriginal: &original, Tags: []model.TagName{{Namespace: "artist", Name: "anonymous artist"}}}}
		raw, _ := json.Marshal(result)
		input, _ := json.Marshal(model.MetadataInput{LookupSHA1: b.SHA1, LookupAlgorithm: b.RepresentativeAlgorithm})
		if _, e = s.Write.Exec("INSERT INTO import_candidates(batch_id,edition_id,request_generation,content_revision,selection_revision,input_json,result_json) VALUES('review-ux',?,0,?,1,?,?)", id, b.ContentRevision, string(input), string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	return s
}
func TestImportReviewSelectionAndBatchDisplayAreReadOnly(t *testing.T) {
	s := reviewFixture(t, 60)
	ctx := context.Background()
	if _, e := s.Write.Exec("UPDATE import_members SET fetch_error='no_match' WHERE edition_id='e0059'"); e != nil {
		t.Fatal(e)
	}
	tagID := fmt.Sprintf("%x", sha256.Sum256([]byte("artist\x00anonymous artist")))
	if _, e := s.Write.Exec("INSERT INTO tags VALUES(?,'artist','anonymous artist')", tagID); e != nil {
		t.Fatal(e)
	}
	translation := "匿名作者"
	if e := s.TranslateTag(ctx, tagID, "zh-CN", 0, &translation); e != nil {
		t.Fatal(e)
	}
	page, e := s.ImportSelections(ctx, "review-ux", "ready", 1, 25)
	if e != nil || page.Total != 59 || len(page.Items) != 25 || !page.Items[0].Acceptable {
		t.Fatal(page, e)
	}
	last, e := s.ImportSelections(ctx, "review-ux", "ready", 3, 25)
	if e != nil || len(last.Items) != 9 {
		t.Fatal(last, e)
	}
	members, e := s.ImportMembers(ctx, "review-ux", "all", 1, 25)
	if e != nil || len(members.Items) != 25 {
		t.Fatal(e)
	}
	first := members.Items[0]
	if first.EditionID == "" || first.EditionRevision < 1 || first.Status != "ready" || first.HasMetadata || len(first.Candidate.Tags) != 1 {
		t.Fatal(first)
	}
	if first.Candidate.Tags[0].Translation != "匿名作者" {
		t.Fatal(first.Candidate.Tags)
	}
	if len(first.Candidate.Result.Raw) != 0 {
		t.Fatal("list loaded raw upstream response")
	}
	b, e := s.ImportBatch(ctx, "review-ux")
	if e != nil || b.Counts["ready"] != 59 || b.Counts["no_match"] != 1 {
		t.Fatal(b, e)
	}
	var n int
	s.Read.QueryRow("SELECT count(*) FROM edition_sources").Scan(&n)
	if n != 0 {
		t.Fatal("query published candidate")
	}
	// A new candidate revision invalidates identities frozen by an earlier query.
	if _, e = s.Write.Exec("UPDATE import_candidates SET revision=revision+1 WHERE edition_id=?", page.Items[0].ID); e != nil {
		t.Fatal(e)
	}
	v := page.Items[0]
	if e = s.ReviewImport(ctx, "review-ux", []model.ImportReview{{ID: v.ID, ExpectedRevision: v.Revision, CandidateRevision: v.CandidateRevision, Decision: "accept"}}); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
}
func TestImportManualReviewPreservesOverridesAndRollsBack(t *testing.T) {
	s := reviewFixture(t, 2)
	ctx := context.Background()
	id := "e0000"
	if e := s.ApplyOverrides(ctx, id, 1, map[string]model.Override{"title": {Op: "set", Value: json.RawMessage(`"Protected title"`)}}); e != nil {
		t.Fatal(e)
	}
	editor, e := s.ImportEditor(ctx, "review-ux", id)
	if e != nil || *editor.Metadata.Fields.Title != "Protected title" || editor.Metadata.Fields.TitleOriginal == nil {
		t.Fatal(editor, e)
	}
	in := model.ImportManualReview{ExpectedRevision: editor.Edition.Revision, MemberRevision: editor.MemberRevision, CandidateRevision: editor.CandidateRevision, Fields: map[string]model.Override{"description": {Op: "set", Value: json.RawMessage(`"Edited in review"`)}}}
	if _, e = s.Write.Exec(`CREATE TRIGGER fail_manual_review BEFORE UPDATE ON import_members BEGIN SELECT RAISE(ABORT,'anonymous rollback');END`); e != nil {
		t.Fatal(e)
	}
	if e = s.ReviewImportManual(ctx, "review-ux", id, in); e == nil {
		t.Fatal("expected rollback")
	}
	b, details, e := s.EditionWithMetadata(ctx, id)
	if e != nil || b.Revision != in.ExpectedRevision || details.Source != nil || details.Overrides["description"].Op != "" {
		t.Fatal(b, details, e)
	}
	s.Write.Exec("DROP TRIGGER fail_manual_review")
	if e = s.ReviewImportManual(ctx, "review-ux", id, in); e != nil {
		t.Fatal(e)
	}
	_, details, e = s.EditionWithMetadata(ctx, id)
	if e != nil || details.Source == nil || *details.Fields.Title != "Protected title" || *details.Fields.Description != "Edited in review" || details.Fields.TitleOriginal == nil {
		t.Fatal(details, e)
	}
	if e = s.ReviewImportManual(ctx, "review-ux", id, in); !errors.Is(e, model.ErrConflict) {
		t.Fatal("stale editor accepted", e)
	}
	// No candidate: edit existing fields and confirm without creating a source.
	id = "e0001"
	s.Write.Exec("DELETE FROM import_candidates WHERE edition_id=?", id)
	editor, e = s.ImportEditor(ctx, "review-ux", id)
	if e != nil {
		t.Fatal(e)
	}
	in = model.ImportManualReview{ExpectedRevision: editor.Edition.Revision, MemberRevision: editor.MemberRevision, Fields: map[string]model.Override{"title": {Op: "set", Value: json.RawMessage(`"Manual entry"`)}}}
	if e = s.ReviewImportManual(ctx, "review-ux", id, in); e != nil {
		t.Fatal(e)
	}
	_, details, e = s.EditionWithMetadata(ctx, id)
	if e != nil || details.Source != nil || *details.Fields.Title != "Manual entry" {
		t.Fatal(details, e)
	}
}

func TestImportReviewFrozenEditionAndManualCandidateConflict(t *testing.T) {
	s := reviewFixture(t, 2)
	ctx := context.Background()
	selected, e := s.ImportSelections(ctx, "review-ux", "ready", 1, 100)
	if e != nil {
		t.Fatal(e)
	}
	v := selected.Items[0]
	if _, e = s.Write.Exec("UPDATE editions SET revision=revision+1 WHERE id=?", v.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.ReviewImport(ctx, "review-ux", []model.ImportReview{{ID: v.ID, ExpectedRevision: v.Revision, EditionRevision: v.EditionRevision, CandidateRevision: v.CandidateRevision, Decision: "accept"}}); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	editor, e := s.ImportEditor(ctx, "review-ux", v.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("UPDATE files SET content_revision=content_revision+1 WHERE edition_id=?", v.ID); e != nil {
		t.Fatal(e)
	}
	in := model.ImportManualReview{ExpectedRevision: editor.Edition.Revision, MemberRevision: editor.MemberRevision, CandidateRevision: editor.CandidateRevision, Fields: map[string]model.Override{"title": {Op: "set", Value: json.RawMessage(`"Must roll back"`)}}}
	if e = s.ReviewImportManual(ctx, "review-ux", v.ID, in); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	_, details, e := s.EditionWithMetadata(ctx, v.ID)
	if e != nil || details.Source != nil || len(details.Overrides) > 0 {
		t.Fatal(details, e)
	}
	states, e := s.ImportSelections(ctx, "review-ux", "all", 1, 100)
	if e != nil || states.Items[0].Status != "stale" || states.Items[0].Acceptable {
		t.Fatal(states, e)
	}
}

func TestImportBulkFrozenVersionsPermitOwnSharedSourceChanges(t *testing.T) {
	s := reviewFixture(t, 2)
	ctx := context.Background()
	// Both editions already reference the gallery that will be refreshed twice.
	for _, id := range []string{"e0000", "e0001"} {
		c, e := s.ImportCandidateDetail(ctx, "review-ux", id)
		if e != nil {
			t.Fatal(e)
		}
		c.Result.ExternalID = "shared"
		raw, _ := json.Marshal(c.Result)
		if _, e = s.Write.Exec("UPDATE import_candidates SET result_json=? WHERE edition_id=?", string(raw), id); e != nil {
			t.Fatal(e)
		}
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"e0000", "e0001"} {
		c, e := importCandidate(ctx, tx, "review-ux", id)
		if e != nil {
			t.Fatal(e)
		}
		if e = publishSource(ctx, tx, id, c.Result); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	selected, e := s.ImportSelections(ctx, "review-ux", "ready", 1, 100)
	if e != nil || len(selected.Items) != 2 {
		t.Fatal(selected, e)
	}
	reviews := []model.ImportReview{}
	for _, v := range selected.Items {
		reviews = append(reviews, model.ImportReview{ID: v.ID, ExpectedRevision: v.Revision, EditionRevision: v.EditionRevision, CandidateRevision: v.CandidateRevision, Decision: "accept"})
	}
	if e = s.ReviewImport(ctx, "review-ux", reviews); e != nil {
		t.Fatal("self-conflict", e)
	}
	b, e := s.ImportBatch(ctx, "review-ux")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.AdvanceImport(ctx, "review-ux", b.Revision); e != nil {
		t.Fatal("tokens do not describe final atomic result", e)
	}
}
