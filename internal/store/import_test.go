package store

import (
	"context"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
	"testing"
)

func importScan(t *testing.T, s *Store, id string) *model.Job {
	t.Helper()
	ctx := context.Background()
	_, e := s.CreateImport(ctx, id, "lib", "request-"+id)
	if e != nil {
		t.Fatal(e)
	}
	requests, e := s.ImportRequests(ctx)
	if e != nil || len(requests) != 1 {
		t.Fatal(requests, e)
	}
	if e = s.MaterializeImport(ctx, requests[0]); e != nil {
		t.Fatal(e)
	}
	j, e := s.Claim(ctx, requests[0].JobID)
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func TestImportScanRecordsOnlyNewAndChangedInPublishTransaction(t *testing.T) {
	s := discoveryStore(t, 2)
	_, err := s.Write.Exec("INSERT INTO file_import_state SELECT edition_id,content_revision FROM files")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	j := importScan(t, s, "batch")
	entries := []model.Item{{Ordinal: 1, Path: "e0000.cbz"}, {Ordinal: 2, Path: "e0001.cbz"}, {Ordinal: 3, Path: "new.cbz"}}
	if e := s.StageEntries(ctx, *j, entries); e != nil {
		t.Fatal(e)
	}
	if e := s.Freeze(ctx, *j); e != nil {
		t.Fatal(e)
	}
	for i, v := range entries {
		b := model.Edition{ID: "new", LibraryID: "lib", Path: v.Path, ContentRevision: 1, State: "available", FilenameTitle: "anonymous"}
		if i < 2 {
			old, e := s.Edition(ctx, []string{"e0000", "e0001"}[i])
			if e != nil {
				t.Fatal(e)
			}
			b = *old
		}
		if i == 1 {
			b.ContentRevision++
		}
		if e := s.Publish(ctx, *j, v, b); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.Finish(ctx, *j, true, ""); e != nil {
		t.Fatal(e)
	}
	if e := s.ReconcileImports(ctx); e != nil {
		t.Fatal(e)
	}
	b, e := s.ImportBatch(ctx, "batch")
	if e != nil || b.Stage != "metadata" || b.Total != 2 {
		t.Fatal(b, e)
	}
	page, e := s.ImportMembers(ctx, "batch", "", 1, 50)
	if e != nil || page.Total != 2 {
		t.Fatal(page, e)
	}
	if e = s.AdvanceImport(ctx, "batch", b.Revision); !errors.Is(e, model.ErrConflict) {
		t.Fatal("gate bypass", e)
	}
}
func TestImportCandidateDoesNotAdoptUntilReview(t *testing.T) {
	s := discoveryStore(t, 1)
	ctx := context.Background()
	j := importScan(t, s, "candidate")
	if _, e := s.Write.Exec("INSERT INTO import_members(batch_id,edition_id,change_kind,content_revision) VALUES('candidate','e0000','changed',1)"); e != nil {
		t.Fatal(e)
	}
	if e := s.Finish(ctx, *j, false, ""); e != nil {
		t.Fatal(e)
	}
	if e := s.ReconcileImports(ctx); e != nil {
		t.Fatal(e)
	}
	reqs, e := s.ImportRequests(ctx)
	if e != nil || len(reqs) != 1 {
		t.Fatal(reqs, e)
	}
	if e = s.MaterializeImport(ctx, reqs[0]); e != nil {
		t.Fatal(e)
	}
	m, e := s.Claim(ctx, reqs[0].JobID)
	if e != nil {
		t.Fatal(e)
	}
	items, e := s.Items(ctx, m.ID, 1, 10, true)
	if e != nil {
		t.Fatal(e)
	}
	title, category := "anonymous full metadata", "Manga"
	rating := 4.5
	result := model.RemoteMetadata{ExternalID: "100", URL: "https://e-hentai.org/g/100/abcdef1234/", Method: "hash", Raw: json.RawMessage(`{}`), Fields: model.Metadata{Title: &title, Category: &category, Rating: &rating, Tags: []model.TagName{}}}
	if e = s.PublishMetadata(ctx, *m, items[0], &result, "", nil); e != nil {
		t.Fatal(e)
	}
	var n int
	s.Read.QueryRow("SELECT count(*) FROM edition_sources").Scan(&n)
	if n != 0 {
		t.Fatal("unreviewed source leaked")
	}
	if e = s.FinishBackground(ctx, *m, ""); e != nil {
		t.Fatal(e)
	}
	if e = s.ReconcileImports(ctx); e != nil {
		t.Fatal(e)
	}
	page, e := s.ImportMembers(ctx, "candidate", "", 1, 50)
	if e != nil {
		t.Fatal(e)
	}
	v := page.Items[0]
	if v.Candidate == nil || v.Candidate.Result.Fields.Rating == nil {
		t.Fatal(v)
	}
	if e = s.ReviewImport(ctx, "candidate", []model.ImportReview{{ID: v.ID, ExpectedRevision: v.Revision, CandidateRevision: v.Candidate.Revision, Decision: "accept"}}); e != nil {
		t.Fatal(e)
	}
	_, details, e := s.EditionWithMetadata(ctx, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	if details.Fields.Category == nil || *details.Fields.Category != category || *details.Fields.Rating != rating {
		t.Fatal(details)
	}
	b, _ := s.ImportBatch(ctx, "candidate")
	if e = s.AdvanceImport(ctx, b.ID, b.Revision); e != nil {
		t.Fatal(e)
	}
	b, _ = s.ImportBatch(ctx, b.ID)
	if b.Stage != "versions" || b.State != "review" {
		t.Fatal(b)
	}
}

func TestImportRefetchGenerationAndAtomicBulkReview(t *testing.T) {
	s := discoveryStore(t, 2)
	ctx := context.Background()
	j := importScan(t, s, "refetch")
	s.Write.Exec("INSERT INTO import_members(batch_id,edition_id,change_kind,content_revision) VALUES('refetch','e0000','changed',1),('refetch','e0001','changed',1)")
	if e := s.Finish(ctx, *j, false, ""); e != nil {
		t.Fatal(e)
	}
	if e := s.ReconcileImports(ctx); e != nil {
		t.Fatal(e)
	}
	reqs, _ := s.ImportRequests(ctx)
	if e := s.MaterializeImport(ctx, reqs[0]); e != nil {
		t.Fatal(e)
	}
	m, e := s.Claim(ctx, reqs[0].JobID)
	if e != nil {
		t.Fatal(e)
	}
	items, _ := s.Items(ctx, m.ID, 1, 10, true)
	rows, _ := s.ImportMembers(ctx, "refetch", "", 1, 10)
	first := rows.Items[0]
	if _, e = s.RefetchImport(ctx, "refetch", first.ID, "https://e-hentai.org/g/101/abcdef1234/", first.Revision); e != nil {
		t.Fatal(e)
	}
	title := "old request must not publish"
	remote := model.RemoteMetadata{ExternalID: "100", URL: "https://e-hentai.org/g/100/abcdef1234/", Raw: json.RawMessage(`{}`), Fields: model.Metadata{Title: &title}}
	if e = s.PublishMetadata(ctx, *m, items[0], &remote, "", nil); e != nil {
		t.Fatal(e)
	}
	var count int
	s.Read.QueryRow("SELECT count(*) FROM import_candidates").Scan(&count)
	if count != 0 {
		t.Fatal("late response replaced new request")
	}
	if e = s.PublishMetadata(ctx, *m, items[1], nil, "no_match", nil); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishBackground(ctx, *m, ""); e != nil {
		t.Fatal(e)
	}
	s.Write.Exec("UPDATE import_batches SET state='review' WHERE id='refetch';DELETE FROM import_child_requests WHERE job_id NOT IN(SELECT id FROM jobs)")
	s.Write.Exec("UPDATE import_members SET active_job_id=NULL WHERE edition_id='e0000'")
	rows, _ = s.ImportMembers(ctx, "refetch", "", 1, 10)
	err := s.ReviewImport(ctx, "refetch", []model.ImportReview{{ID: rows.Items[0].ID, ExpectedRevision: rows.Items[0].Revision, Decision: "skip"}, {ID: rows.Items[1].ID, ExpectedRevision: 999, Decision: "keep"}})
	if !errors.Is(err, model.ErrConflict) {
		t.Fatal(err)
	}
	s.Read.QueryRow("SELECT count(*) FROM import_members WHERE metadata_decision!=''").Scan(&count)
	if count != 0 {
		t.Fatal("partial batch review committed")
	}
}
func TestImportWorkProposalPublicationDoesNotSupersedeGlobalPlans(t *testing.T) {
	s := discoveryStore(t, 4)
	ctx := context.Background()
	finishDiscovery(t, s, "global", []rec.PairEvidence{evidence(0, 1), evidence(2, 3)})
	global, e := s.WorkProposals(ctx, "global", "lib", "", "pending", true, 1, 100)
	if e != nil || global.Total == 0 {
		t.Fatal(global, e)
	}
	s.Write.Exec("INSERT INTO import_batches(id,library_id,request_key,stage) VALUES('scoped','lib','request-scoped','versions');INSERT INTO import_members(batch_id,edition_id,change_kind,content_revision) VALUES('scoped','e0000','new',1);INSERT INTO import_child_requests(job_id,batch_id,stage) VALUES('scoped-job','scoped','versions')")
	finishDiscovery(t, s, "scoped-job", []rec.PairEvidence{evidence(0, 1), evidence(2, 3)})
	after, e := s.WorkProposals(ctx, "global", "lib", "", "pending", true, 1, 100)
	if e != nil || after.Total != global.Total {
		t.Fatal("global proposals changed", after, e)
	}
	scoped, e := s.ImportWorkProposals(ctx, "scoped", "pending", 1, 1)
	if e != nil || scoped.Total != 1 || len(scoped.Items) != 1 {
		t.Fatal(scoped, e)
	}
	if scoped.Items[0].ID == global.Items[0].ID {
		t.Fatal("batch reused mutable global plan")
	}
}

func TestImportAdjustedProposalKeepsScopeAndStoppedBatchCannotAccept(t *testing.T) {
	s := discoveryStore(t, 3)
	ctx := context.Background()
	s.Write.Exec("INSERT INTO import_batches(id,library_id,request_key,stage,state) VALUES('subset','lib','request-subset','versions','processing');INSERT INTO import_members(batch_id,edition_id,change_kind,content_revision) VALUES('subset','e0000','new',1);INSERT INTO import_child_requests(job_id,batch_id,stage) VALUES('subset-job','subset','versions')")
	finishDiscovery(t, s, "subset-job", []rec.PairEvidence{evidence(0, 1), evidence(0, 2), evidence(1, 2)})
	plans, e := s.ImportWorkProposals(ctx, "subset", "pending", 1, 100)
	if e != nil || len(plans.Items) != 1 {
		t.Fatal(plans, e)
	}
	p := plans.Items[0]
	subset, e := s.ResolveWorkProposal(ctx, p.ID, rec.ProposalSelection{Revision: p.Revision, IDs: []string{"e0000", "e0001"}})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CheckImportProposal(ctx, "subset", "versions", subset.ID); e != nil {
		t.Fatal("resolved plan escaped scope", e)
	}
	b, _ := s.ImportBatch(ctx, "subset")
	if e = s.ControlImport(ctx, b.ID, "cancel", b.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DecideWorkProposal(ctx, "account", subset.ID, "accept", rec.ProposalSelection{Revision: subset.Revision, IDs: subset.NewIDs}); !errors.Is(e, model.ErrConflict) {
		t.Fatal("cancelled batch accepted", e)
	}
}

func TestImportCancellationKeepsPublishedDataAndRejectsOldGeneration(t *testing.T) {
	s := discoveryStore(t, 1)
	ctx := context.Background()
	j := importScan(t, s, "cancel-flow")
	s.Write.Exec("INSERT INTO import_members(batch_id,edition_id,change_kind,content_revision) VALUES('cancel-flow','e0000','changed',1)")
	b, _ := s.ImportBatch(ctx, "cancel-flow")
	if e := s.ControlImport(ctx, b.ID, "pause", b.Revision); e != nil {
		t.Fatal(e)
	}
	if e := s.Finish(ctx, *j, false, ""); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal("paused generation committed", e)
	}
	b, _ = s.ImportBatch(ctx, b.ID)
	if e := s.ControlImport(ctx, b.ID, "resume", b.Revision); e != nil {
		t.Fatal(e)
	}
	b, _ = s.ImportBatch(ctx, b.ID)
	if e := s.ControlImport(ctx, b.ID, "cancel", b.Revision); e != nil {
		t.Fatal(e)
	}
	var n int
	s.Read.QueryRow("SELECT count(*) FROM editions WHERE id='e0000'").Scan(&n)
	if n != 1 {
		t.Fatal("cancel rolled back published Edition")
	}
	if e := s.Finish(ctx, *j, false, ""); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal(e)
	}
}

func TestImportIncludesPreviouslyScannedButUnreviewedEditionAfterCancel(t *testing.T) {
	s := discoveryStore(t, 1)
	ctx := context.Background()
	first := importScan(t, s, "first-cancelled")
	b, _ := s.ImportBatch(ctx, "first-cancelled")
	if e := s.ControlImport(ctx, b.ID, "cancel", b.Revision); e != nil {
		t.Fatal(e)
	}
	_ = first
	next := importScan(t, s, "second-import")
	item := model.Item{Ordinal: 1, Path: "e0000.cbz"}
	if e := s.StageEntries(ctx, *next, []model.Item{item}); e != nil {
		t.Fatal(e)
	}
	if e := s.Freeze(ctx, *next); e != nil {
		t.Fatal(e)
	}
	edition, e := s.Edition(ctx, "e0000")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Publish(ctx, *next, item, *edition); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = s.Read.QueryRow("SELECT count(*) FROM import_members WHERE batch_id='second-import' AND change_kind='new'").Scan(&n); e != nil || n != 1 {
		t.Fatal("unreviewed scan disappeared from later import", n, e)
	}
}

func TestImportResumeRejectsDisabledLibrary(t *testing.T) {
	s := discoveryStore(t, 1)
	ctx := context.Background()
	importScan(t, s, "disabled-import")
	b, _ := s.ImportBatch(ctx, "disabled-import")
	if e := s.ControlImport(ctx, b.ID, "pause", b.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Write.Exec("UPDATE libraries SET disabled=1 WHERE id='lib'"); e != nil {
		t.Fatal(e)
	}
	b, _ = s.ImportBatch(ctx, b.ID)
	if e := s.ControlImport(ctx, b.ID, "resume", b.Revision); !errors.Is(e, model.ErrDisabled) {
		t.Fatal("disabled library resumed", e)
	}
	after, _ := s.ImportBatch(ctx, b.ID)
	if after.State != "paused" || after.Revision != b.Revision {
		t.Fatal("failed resume changed batch", after)
	}
}
