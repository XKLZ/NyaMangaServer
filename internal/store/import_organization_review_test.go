package store

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
	"testing"
)

func organizationReviewFixture(t *testing.T, n int, pairs []rec.PairEvidence) *Store {
	t.Helper()
	s := reviewFixture(t, n)
	ctx := context.Background()
	rows, e := s.ImportMembers(ctx, "review-ux", "", 1, 100)
	if e != nil {
		t.Fatal(e)
	}
	in := []model.ImportReview{}
	for _, m := range rows.Items {
		in = append(in, model.ImportReview{ID: m.ID, ExpectedRevision: m.Revision, Decision: "skip"})
	}
	if e = s.ReviewImport(ctx, "review-ux", in); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("UPDATE import_batches SET stage='versions',state='review' WHERE id='review-ux'"); e != nil {
		t.Fatal(e)
	}
	if pairs != nil {
		s.Write.Exec("UPDATE import_batches SET state='processing' WHERE id='review-ux'; INSERT INTO import_child_requests(job_id,batch_id,stage) VALUES('org-review-job','review-ux','versions')")
		finishDiscovery(t, s, "org-review-job", pairs)
		if e = s.ReconcileImports(ctx); e != nil {
			t.Fatal(e)
		}
	}
	return s
}
func completeReview(t *testing.T, s *Store) error {
	t.Helper()
	v, e := s.OrganizationReview(context.Background(), "review-ux")
	if e != nil {
		return e
	}
	return s.CompleteOrganizationReview(context.Background(), "review-ux", model.CompleteOrganizationReview{Stage: v.Stage, Revision: v.Revision, Token: v.Token})
}
func rebuildReview(t *testing.T, s *Store) {
	t.Helper()
	for range 10 {
		ok, e := s.RebuildDiscoveryPending(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if !ok {
			return
		}
	}
	t.Fatal("revalidation did not settle")
}
func TestImportOrganizationReviewNoSuggestionsAndAtomicCompletion(t *testing.T) {
	s := organizationReviewFixture(t, 1, nil)
	ctx := context.Background()
	before, _ := s.OrganizationReview(ctx, "review-ux")
	again, _ := s.OrganizationReview(ctx, "review-ux")
	if !before.CanComplete || before.Token != again.Token {
		t.Fatal(before, again)
	}
	// Force failure after filling all default dispositions. None may escape.
	s.Write.Exec("CREATE TRIGGER fail_review BEFORE UPDATE OF stage ON import_batches WHEN NEW.stage='series' BEGIN SELECT RAISE(ABORT,'anonymous failure'); END")
	if e := completeReview(t, s); e == nil {
		t.Fatal("expected rollback")
	}
	var decision string
	s.Read.QueryRow("SELECT version_decision FROM import_members WHERE batch_id='review-ux'").Scan(&decision)
	if decision != "" {
		t.Fatal("partial default review")
	}
	s.Write.Exec("DROP TRIGGER fail_review")
	in := model.CompleteOrganizationReview{Stage: before.Stage, Revision: before.Revision, Token: before.Token}
	if e := s.CompleteOrganizationReview(ctx, "review-ux", in); e != nil {
		t.Fatal(e)
	}
	if e := s.CompleteOrganizationReview(ctx, "review-ux", in); !errors.Is(e, model.ErrConflict) {
		t.Fatal("duplicate advanced twice", e)
	}
	if e := completeReview(t, s); e != nil {
		t.Fatal("empty series gate", e)
	}
	b, _ := s.ImportBatch(ctx, "review-ux")
	if b.Stage != "finished" || b.State != "completed" {
		t.Fatal(b)
	}
}
func TestImportOrganizationReviewRequiresPendingPlanAndRevalidation(t *testing.T) {
	for _, accept := range []bool{false, true} {
		t.Run(map[bool]string{false: "dismiss", true: "accept"}[accept], func(t *testing.T) {
			s := organizationReviewFixture(t, 3, []rec.PairEvidence{evidence(0, 1)})
			ctx := context.Background()
			q, e := s.ImportWorkProposals(ctx, "review-ux", "pending", 1, 100, false)
			if e != nil || q.Total != 1 {
				t.Fatal(q, e)
			}
			if e = completeReview(t, s); !errors.Is(e, model.ErrBusy) {
				t.Fatal(e)
			}
			action := "dismiss"
			if accept {
				action = "accept"
			}
			p := q.Items[0]
			if _, e = s.DecideWorkProposal(ctx, "account", p.ID, action, rec.ProposalSelection{Revision: p.Revision, IDs: p.NewIDs}); e != nil {
				t.Fatal(e)
			}
			v, e := s.OrganizationReview(ctx, "review-ux")
			if e != nil || !v.Revalidating || v.CanComplete {
				t.Fatal(v, e)
			}
			if e = completeReview(t, s); !errors.Is(e, model.ErrBusy) {
				t.Fatal(e)
			}
			rebuildReview(t, s)
			if e = completeReview(t, s); e != nil {
				t.Fatal(e)
			}
			var work string
			s.Read.QueryRow("SELECT work_id FROM editions WHERE id='e0002'").Scan(&work)
			if work != "i:e0002" {
				t.Fatal("unrelated edition moved", work)
			}
		})
	}
}
func TestImportOrganizationReviewStaleAndUpstreamTokens(t *testing.T) {
	s := organizationReviewFixture(t, 2, nil)
	ctx := context.Background()
	v, _ := s.OrganizationReview(ctx, "review-ux")
	s.Write.Exec("UPDATE files SET content_revision=content_revision+1 WHERE edition_id='e0000'")
	if e := s.CompleteOrganizationReview(ctx, "review-ux", model.CompleteOrganizationReview{Stage: v.Stage, Revision: v.Revision, Token: v.Token}); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	if e := completeReview(t, s); !errors.Is(e, model.ErrConflict) {
		t.Fatal("fresh snapshot hid stale upstream", e)
	}
	var n int
	s.Read.QueryRow("SELECT count(*) FROM import_members WHERE version_decision<>''").Scan(&n)
	if n != 0 {
		t.Fatal("default decisions escaped rollback")
	}
}
func TestImportOrganizationReviewPreservesLegacyDecision(t *testing.T) {
	s := organizationReviewFixture(t, 2, []rec.PairEvidence{evidence(0, 1)})
	ctx := context.Background()
	m, _ := s.ImportMembers(ctx, "review-ux", "", 1, 100)
	if e := s.ReviewImport(ctx, "review-ux", []model.ImportReview{{ID: m.Items[0].ID, ExpectedRevision: m.Items[0].Revision, Decision: "confirm"}}); e != nil {
		t.Fatal(e)
	}
	p, _ := s.ImportWorkProposals(ctx, "review-ux", "pending", 1, 100, false)
	if _, e := s.DecideWorkProposal(ctx, "account", p.Items[0].ID, "accept", rec.ProposalSelection{Revision: p.Items[0].Revision, IDs: p.Items[0].NewIDs}); e != nil {
		t.Fatal(e)
	}
	rebuildReview(t, s)
	if e := completeReview(t, s); !errors.Is(e, model.ErrConflict) {
		t.Fatal("legacy token overwritten", e)
	}
}

func TestImportOrganizationReviewRetiresOnlyUnselectedAlternatives(t *testing.T) {
	s := organizationReviewFixture(t, 2, []rec.PairEvidence{evidence(0, 1)})
	ctx := context.Background()
	p, e := s.ImportWorkProposals(ctx, "review-ux", "pending", 1, 100, false)
	if e != nil {
		t.Fatal(e)
	}
	id := p.Items[0].ID
	_, e = s.Write.Exec(`INSERT INTO work_proposals(id,job_id,library_id,proposal_key,kind,target_work_id,tier,score,primary_plan,state,plan_json,revision,created_at)
 SELECT id||'-alternative',job_id,library_id,proposal_key||'-alternative',kind,target_work_id,tier,score,0,'pending',json_set(plan_json,'$.id',id||'-alternative'),1,created_at FROM work_proposals WHERE id=?`, id)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("INSERT INTO import_proposals VALUES('review-ux','versions',?)", id+"-alternative"); e != nil {
		t.Fatal(e)
	}
	only, e := s.ImportWorkProposals(ctx, "review-ux", "pending", 1, 1, true, true)
	if e != nil || only.Total != 1 || only.Items[0].ID != id+"-alternative" {
		t.Fatal(only, e)
	}
	if _, e = s.DecideWorkProposal(ctx, "account", id, "dismiss", rec.ProposalSelection{Revision: p.Items[0].Revision}); e != nil {
		t.Fatal(e)
	}
	// This test freezes the already generated anonymous alternate; no model runs.
	s.Write.Exec("UPDATE work_discovery_runs SET refresh_required=0")
	v, e := s.OrganizationReview(ctx, "review-ux")
	if e != nil || v.Pending != 0 || !v.CanComplete {
		t.Fatal(v, e)
	}
	if e = completeReview(t, s); e != nil {
		t.Fatal(e)
	}
	var state string
	s.Read.QueryRow("SELECT state FROM work_proposals WHERE id=?", id+"-alternative").Scan(&state)
	if state != "superseded" {
		t.Fatal(state)
	}
	var n int
	s.Read.QueryRow("SELECT count(*) FROM work_proposal_decisions WHERE proposal_id=?", id+"-alternative").Scan(&n)
	if n != 0 {
		t.Fatal("alternative became a human rejection")
	}
}

func TestImportOrganizationReviewHistoricalReadAndUndoScope(t *testing.T) {
	s := organizationReviewFixture(t, 2, []rec.PairEvidence{evidence(0, 1)})
	ctx := context.Background()
	q, _ := s.ImportWorkProposals(ctx, "review-ux", "pending", 1, 100, false)
	id := q.Items[0].ID
	s.Write.Exec("UPDATE import_batches SET state='completed',stage='finished' WHERE id='review-ux'")
	if e := s.CheckImportProposal(ctx, "review-ux", "versions", id, "read"); e != nil {
		t.Fatal(e)
	}
	if e := s.CheckImportProposal(ctx, "review-ux", "versions", id, "undo"); e != nil {
		t.Fatal(e)
	}
	if e := s.CheckImportProposal(ctx, "review-ux", "versions", id, "accept"); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	if e := s.CheckImportProposal(ctx, "unrelated-batch", "versions", id, "undo"); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
}

func TestImportOrganizationReviewSnapshotCoversEditionEdits(t *testing.T) {
	s := organizationReviewFixture(t, 1, nil)
	ctx := context.Background()
	v, _ := s.OrganizationReview(ctx, "review-ux")
	s.Write.Exec("UPDATE editions SET revision=revision+1 WHERE id='e0000'")
	if e := s.CompleteOrganizationReview(ctx, "review-ux", model.CompleteOrganizationReview{Stage: v.Stage, Revision: v.Revision, Token: v.Token}); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
}
