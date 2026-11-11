package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
)

func failImportRecommendation(t *testing.T, s *Store, stage string) string {
	t.Helper()
	ctx := context.Background()
	id := "failed-" + stage
	if _, e := s.Write.Exec("UPDATE import_batches SET stage=?,state='processing' WHERE id='review-ux'", stage); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Write.Exec("INSERT INTO import_child_requests(job_id,batch_id,stage) VALUES(?,'review-ux',?)", id, stage); e != nil {
		t.Fatal(e)
	}
	requests, e := s.ImportRequests(ctx)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, r := range requests {
		if r.JobID == id {
			found = true
			if e = s.MaterializeImport(ctx, r); e != nil {
				t.Fatal(e)
			}
		}
	}
	if !found {
		t.Fatal("missing reservation")
	}
	j, e := s.Claim(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	if stage == "series" {
		if e = s.SaveSeriesDiagnostic(ctx, *j, "restore_checkpoint", "invalid_checkpoint"); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.FinishBackground(ctx, *j, "anonymous_recommendation_failed"); e != nil {
		t.Fatal(e)
	}
	if e = s.ReconcileImports(ctx); e != nil {
		t.Fatal(e)
	}
	return id
}

func TestImportFailedRecommendationsRequireRetryOrExplicitSkip(t *testing.T) {
	for _, stage := range []string{"versions", "series"} {
		t.Run(stage, func(t *testing.T) {
			s := organizationReviewFixture(t, 2, nil)
			ctx := context.Background()
			if stage == "series" {
				if e := completeReview(t, s); e != nil {
					t.Fatal(e)
				}
				// The normal advance reserved the first attempt; use it in this fixture.
				if _, e := s.Write.Exec("DELETE FROM import_child_requests WHERE batch_id='review-ux' AND stage='series'"); e != nil {
					t.Fatal(e)
				}
			}
			before := dumpRows(t, s, "SELECT * FROM editions ORDER BY id")
			failed := failImportRecommendation(t, s, stage)
			b, e := s.ImportBatch(ctx, "review-ux")
			if e != nil {
				t.Fatal(e)
			}
			if b.State != "failed" || !b.CanRetry || b.CurrentFailure == nil || b.CurrentFailure.JobID != failed || len(b.TaskFailures) != 1 {
				t.Fatal(b)
			}
			if stage == "series" && b.CurrentFailure.Phase != "restore_checkpoint" {
				t.Fatal(b.CurrentFailure)
			}
			v, e := s.OrganizationReview(ctx, b.ID)
			if e != nil || v.CanComplete || !v.CanRetry || v.Failure == nil {
				t.Fatal(v, e)
			}
			if e = completeReview(t, s); e == nil {
				t.Fatal("failure passed completion gate")
			}
			if e = s.ControlImport(ctx, b.ID, "skip_recommendation", b.Revision-1); !errors.Is(e, model.ErrConflict) {
				t.Fatal(e)
			}
			// A failed write must leave the decision and review state untouched.
			if _, e = s.Write.Exec("CREATE TRIGGER fail_skip BEFORE UPDATE OF stage_decisions_json ON import_batches BEGIN SELECT RAISE(ABORT,'anonymous failure'); END"); e != nil {
				t.Fatal(e)
			}
			if e = s.ControlImport(ctx, b.ID, "skip_recommendation", b.Revision); e == nil {
				t.Fatal("expected rollback")
			}
			unchanged, _ := s.ImportBatch(ctx, b.ID)
			if unchanged.State != "failed" || len(unchanged.StageDecisions) != 0 {
				t.Fatal(unchanged)
			}
			s.Write.Exec("DROP TRIGGER fail_skip")
			if e = s.ControlImport(ctx, b.ID, "skip_recommendation", b.Revision); e != nil {
				t.Fatal(e)
			}
			b, e = s.ImportBatch(ctx, b.ID)
			if e != nil {
				t.Fatal(e)
			}
			if b.State != "review" || b.Stage != stage || b.CurrentFailure != nil || b.CanRetry || b.StageDecisions[stage].JobID != failed || len(b.SkippedStages) != 1 {
				t.Fatal(b)
			}
			v, e = s.OrganizationReview(ctx, b.ID)
			if e != nil || !v.CanComplete || !v.RecommendationSkipped || v.CanRetry {
				t.Fatal(v, e)
			}
			// Reads/reconciliation preserve explicit skip without automatically advancing.
			if e = s.ReconcileImports(ctx); e != nil {
				t.Fatal(e)
			}
			again, _ := s.ImportBatch(ctx, b.ID)
			if again.Stage != stage || again.State != "review" {
				t.Fatal(again)
			}
			if !reflect.DeepEqual(before, dumpRows(t, s, "SELECT * FROM editions ORDER BY id")) {
				t.Fatal("skip changed organization")
			}
			if e = completeReview(t, s); e != nil {
				t.Fatal(e)
			}
			if stage == "series" {
				done, _ := s.ImportBatch(ctx, b.ID)
				if done.State != "completed" || len(done.TaskFailures) != 1 || len(done.SkippedStages) != 1 {
					t.Fatal(done)
				}
			}
		})
	}
}

func TestImportRetrySupersedesFailureWithoutRewritingHistory(t *testing.T) {
	for _, stage := range []string{"versions", "series"} {
		t.Run(stage, func(t *testing.T) {
			s := organizationReviewFixture(t, 2, nil)
			ctx := context.Background()
			if stage == "series" {
				if e := completeReview(t, s); e != nil {
					t.Fatal(e)
				}
				s.Write.Exec("DELETE FROM import_child_requests WHERE batch_id='review-ux' AND stage='series'")
			}
			if _, e := s.Write.Exec("INSERT INTO import_child_requests(job_id,batch_id,stage) VALUES('abandoned-reservation','review-ux',?)", stage); e != nil {
				t.Fatal(e)
			}
			failed := failImportRecommendation(t, s, stage)
			b, _ := s.ImportBatch(ctx, "review-ux")
			if e := s.ControlImport(ctx, b.ID, "retry", b.Revision); e != nil {
				t.Fatal(e)
			}
			if e := s.ReconcileImports(ctx); e != nil {
				t.Fatal(e)
			}
			b, _ = s.ImportBatch(ctx, b.ID)
			if b.State != "processing" || b.CurrentFailure != nil || b.CanRetry {
				t.Fatal(b)
			}
			requests, e := s.ImportRequests(ctx)
			if e != nil {
				t.Fatal(e)
			}
			var next string
			for _, r := range requests {
				if r.Stage == stage {
					next = r.JobID
					if e = s.MaterializeImport(ctx, r); e != nil {
						t.Fatal(e)
					}
				}
			}
			if next == "" || next == failed {
				t.Fatal("retry did not reserve new identity")
			}
			j, e := s.Claim(ctx, next)
			if e != nil {
				t.Fatal(e)
			}
			if stage == "versions" {
				pool, err := s.RecommendationPool(ctx, j.ID)
				if err != nil {
					t.Fatal(err)
				}
				for i := range pool.Editions {
					if e = s.SaveDiscoveryBlock(ctx, *j, rec.PairBlock{Row: i, NextRow: i + 1, Compared: len(pool.Editions) - i - 1, HardBits: make([]byte, (len(pool.Editions)-i-1+7)/8)}); e != nil {
						t.Fatal(e)
					}
				}
				if e = s.PublishDiscoveryProposals(ctx, *j); e != nil {
					t.Fatal(e)
				}
			} else {
				run, err := s.SeriesRun(ctx, j.ID, false)
				if err != nil {
					t.Fatal(err)
				}
				for i, ed := range run.Pool.Editions {
					if e = s.SaveSeriesFeature(ctx, *j, i, ed, nil); e != nil {
						t.Fatal(e)
					}
				}
				for i := range run.Pool.Works {
					if e = s.SaveSeriesBlock(ctx, *j, rec.SeriesBlock{Row: i, NextRow: i + 1}); e != nil {
						t.Fatal(e)
					}
				}
				if e = s.PublishSeries(ctx, *j, run, rec.SeriesResult{Algorithm: rec.SeriesAlgorithm, Proposals: []rec.SeriesRawPlan{}, Pending: []rec.SeriesPending{}}, false); e != nil {
					t.Fatal(e)
				}
			}
			if e = s.FinishBackground(ctx, *j, ""); e != nil {
				t.Fatal(e)
			}
			if e = s.ReconcileImports(ctx); e != nil {
				t.Fatal(e)
			}
			b, e = s.ImportBatch(ctx, b.ID)
			if e != nil || b.State != "review" || b.CurrentFailure != nil || len(b.TaskFailures) != 1 {
				t.Fatal(b, e)
			}
			v, e := s.OrganizationReview(ctx, b.ID)
			if e != nil || !v.CanComplete || v.Failure != nil || v.RecommendationSkipped {
				t.Fatal(v, e)
			}
			old, _ := s.Job(ctx, failed)
			if old.State != "failed" {
				t.Fatal("historical failure rewritten", old)
			}
			if e = completeReview(t, s); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestImportHistoryFiltersLibraryCountsAndCurrentBatch(t *testing.T) {
	s := reviewFixture(t, 3)
	ctx := context.Background()
	if _, e := s.Write.Exec(`UPDATE files SET file_state='missing' WHERE edition_id='e0000'; UPDATE editions SET trashed_at=1 WHERE id='e0002'; UPDATE libraries SET disabled=1 WHERE id='lib';
 INSERT INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision,disabled) VALUES('other','other','other','utf8','identity',1,1);
 INSERT INTO import_batches(id,library_id,request_key,state,stage) VALUES('old','lib','old','completed','finished'),('other-batch','other','other','failed','scan')`); e != nil {
		t.Fatal(e)
	}
	libs, e := s.Libraries(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, l := range libs {
		if l.ID == "lib" {
			if l.EditionCount != 2 || l.WorkCount != 2 || l.ActiveImport == nil || l.ActiveImport.ID != "review-ux" || !l.Disabled {
				t.Fatal(l)
			}
		} else if l.EditionCount != 0 || l.WorkCount != 0 || l.ActiveImport == nil {
			t.Fatal(l)
		}
	}
	// Library and state filtering precede count and pagination.
	page, e := s.ImportBatches(ctx, 1, 1, model.ImportFilter{LibraryID: "lib", State: "completed"})
	if e != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "old" {
		t.Fatal(page, e)
	}
	page, e = s.ImportBatches(ctx, 2, 1, model.ImportFilter{LibraryID: "lib"})
	if e != nil || page.Total != 2 || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	page, e = s.ImportBatches(ctx, 1, 25, model.ImportFilter{LibraryID: "other", State: "review"})
	if e != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatal(page, e)
	}
}
