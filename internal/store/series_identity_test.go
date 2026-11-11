package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
)

func prepareScopedSeries(t *testing.T, s *Store, id, batch string, groups [][]int) (*model.Job, rec.SeriesRun, rec.SeriesResult) {
	t.Helper()
	ctx := context.Background()
	if batch != "" {
		if _, e := s.Write.Exec("INSERT INTO import_batches(id,library_id,request_key,stage,state) VALUES(?,'lib',?,'series','processing')", batch, batch); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Write.Exec("INSERT INTO import_works(batch_id,work_id,input_token) SELECT ?,id,'' FROM works WHERE library_id='lib'", batch); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Write.Exec("INSERT INTO import_child_requests(job_id,batch_id,stage) VALUES(?,?,'series')", id, batch); e != nil {
			t.Fatal(e)
		}
	}
	j, e := s.CreateSeriesDiscovery(ctx, id, "lib", "")
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Claim(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.SeriesRun(ctx, id, false)
	if e != nil {
		t.Fatal(e)
	}
	for i, ed := range r.Pool.Editions {
		if e = s.SaveSeriesFeature(ctx, *j, i, ed, nil); e != nil {
			t.Fatal(e)
		}
	}
	for i := range r.Pool.Works {
		if e = s.SaveSeriesBlock(ctx, *j, rec.SeriesBlock{Row: i, NextRow: i + 1}); e != nil {
			t.Fatal(e)
		}
	}
	result := rec.SeriesResult{Algorithm: rec.SeriesAlgorithm, Proposals: []rec.SeriesRawPlan{}, Pending: []rec.SeriesPending{}}
	for _, indices := range groups {
		ids := []string{}
		for _, i := range indices {
			ids = append(ids, r.Pool.Works[i].ID)
		}
		result.Proposals = append(result.Proposals, rec.SeriesRawPlan{Kind: "create_series", WorkIDs: ids, NewIDs: ids, Name: "Anonymous series", Tier: "strong", Risks: []string{}})
	}
	return j, r, result
}
func finishScopedSeries(t *testing.T, s *Store, id, batch string, groups [][]int) []rec.SeriesProposal {
	t.Helper()
	ctx := context.Background()
	j, r, result := prepareScopedSeries(t, s, id, batch, groups)
	if e := s.PublishSeries(ctx, *j, r, result, false); e != nil {
		t.Fatal(e)
	}
	if e := s.FinishBackground(ctx, *j, ""); e != nil {
		t.Fatal(e)
	}
	if batch != "" {
		if _, e := s.Write.Exec("UPDATE import_batches SET state='review' WHERE id=?", batch); e != nil {
			t.Fatal(e)
		}
	}
	rows, e := s.Read.Query("SELECT "+seriesProposalColumns+" FROM series_proposals WHERE job_id=? ORDER BY id", id)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	out := []rec.SeriesProposal{}
	for rows.Next() {
		p, e := scanSeriesProposal(rows)
		if e != nil {
			t.Fatal(e)
		}
		out = append(out, p)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	return out
}
func closeScopedImport(t *testing.T, s *Store, batch string) {
	t.Helper()
	if batch != "" {
		if _, e := s.Write.Exec("UPDATE import_batches SET stage='finished',state='completed' WHERE id=?", batch); e != nil {
			t.Fatal(e)
		}
	}
}
func titleSeriesFixture(t *testing.T, s *Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, e := s.Write.Exec("UPDATE files SET filename_title=? WHERE edition_id=?", fmt.Sprintf("Memory %d", i+1), fmt.Sprintf("e%04d", i)); e != nil {
			t.Fatal(e)
		}
	}
}

func TestSeriesScopedPublicationRetainsDismissalsAndOtherThreePlans(t *testing.T) {
	for _, firstBatch := range []bool{false, true} {
		t.Run(fmt.Sprint(firstBatch), func(t *testing.T) {
			ctx := context.Background()
			s := discoveryStore(t, 8)
			titleSeriesFixture(t, s, 8)
			firstScope, nextScope := "", "batch-two"
			if firstBatch {
				firstScope, nextScope = "batch-one", ""
			}
			first := finishScopedSeries(t, s, "first", firstScope, [][]int{{0, 1}})[0]
			if _, e := s.DecideSeries(ctx, "account", first.ID, "dismiss", rec.SeriesSelection{Revision: first.Revision}); e != nil {
				t.Fatal(e)
			}
			closeScopedImport(t, s, firstScope)
			before := dumpRows(t, s, "SELECT * FROM series_proposals WHERE id='"+first.ID+"'")
			decisions := dumpRows(t, s, "SELECT * FROM series_proposal_decisions ORDER BY proposal_key")
			next := finishScopedSeries(t, s, "second", nextScope, [][]int{{0, 1}, {2, 3}, {4, 5}, {6, 7}})
			pending, dismissed := 0, 0
			var shared rec.SeriesProposal
			for _, p := range next {
				if p.State == "pending" {
					pending++
				}
				if p.State == "dismissed" {
					dismissed++
				}
				if p.Key == first.Key {
					shared = p
				}
			}
			if len(next) != 4 || pending != 3 || dismissed != 1 || shared.ID == first.ID || shared.StorageKey == first.StorageKey || shared.Scope == first.Scope {
				t.Fatal(next)
			}
			if !reflect.DeepEqual(before, dumpRows(t, s, "SELECT * FROM series_proposals WHERE id='"+first.ID+"'")) || !reflect.DeepEqual(decisions, dumpRows(t, s, "SELECT * FROM series_proposal_decisions ORDER BY proposal_key")) {
				t.Fatal("cross-scope publication changed old history")
			}
			restored, e := s.DecideSeries(ctx, "account", shared.ID, "restore", rec.SeriesSelection{Revision: shared.Revision})
			if e != nil || restored.State != "pending" {
				t.Fatal(restored, e)
			}
			closeScopedImport(t, s, nextScope)
			third := finishScopedSeries(t, s, "third", "batch-three", [][]int{{0, 1}})
			if len(third) != 1 || third[0].State != "pending" {
				t.Fatal("old dismissal overrode explicit restoration", third)
			}
			// Same-scope publication uses the same stable ID/key, with no new rows.
			r, e := s.SeriesRun(ctx, "third", true)
			if e != nil {
				t.Fatal(e)
			}
			j, _ := s.Job(ctx, "third")
			if _, e = s.Write.Exec("UPDATE series_discovery_runs SET refresh_required=1 WHERE job_id='third'"); e != nil {
				t.Fatal(e)
			}
			result := rec.SeriesResult{Algorithm: rec.SeriesAlgorithm, Proposals: []rec.SeriesRawPlan{{Kind: "create_series", WorkIDs: third[0].Order, NewIDs: third[0].NewIDs, Tier: "strong"}}}
			if e = s.PublishSeries(ctx, *j, r, result, true); e != nil {
				t.Fatal(e)
			}
			var n int
			s.Read.QueryRow("SELECT count(*) FROM series_proposals WHERE scope_key='import:batch-three'").Scan(&n)
			if n != 1 {
				t.Fatal(n)
			}
		})
	}
}

func TestSeriesGlobalPublicationDoesNotSupersedeBatchDraft(t *testing.T) {
	s := discoveryStore(t, 4)
	ctx := context.Background()
	titleSeriesFixture(t, s, 4)
	p := finishScopedSeries(t, s, "batch-job", "batch", [][]int{{0, 1}})[0]
	name := "Manual series name"
	p, e := s.ResolveSeries(ctx, p.ID, rec.SeriesSelection{Revision: p.Revision, IDs: p.NewIDs, Name: &name})
	if e != nil {
		t.Fatal(e)
	}
	before := dumpRows(t, s, "SELECT * FROM series_proposals WHERE scope_key='import:batch' ORDER BY id")
	global := finishScopedSeries(t, s, "global", "", [][]int{{0, 1}})
	if len(global) != 1 || !global[0].Primary {
		t.Fatal("batch draft suppressed unrelated scope", global)
	}
	_ = finishScopedSeries(t, s, "empty-global", "", nil)
	if !reflect.DeepEqual(before, dumpRows(t, s, "SELECT * FROM series_proposals WHERE scope_key='import:batch' ORDER BY id")) {
		t.Fatal("global publication superseded batch draft")
	}
}

func TestSeriesScopedDecisionsAndUndoSnapshotsStayWithProposal(t *testing.T) {
	s := discoveryStore(t, 2)
	ctx := context.Background()
	titleSeriesFixture(t, s, 2)
	batch := finishScopedSeries(t, s, "batch-job", "batch", [][]int{{0, 1}})[0]
	global := finishScopedSeries(t, s, "global-job", "", [][]int{{0, 1}})[0]
	accepted, e := s.DecideSeries(ctx, "account", batch.ID, "accept", rec.SeriesSelection{Revision: batch.Revision, IDs: batch.NewIDs})
	if e != nil {
		t.Fatal(e)
	}
	snap := dumpRows(t, s, "SELECT before_json,after_json FROM series_proposal_decisions WHERE proposal_id='"+batch.ID+"'")
	if len(snap) != 1 {
		t.Fatal(snap)
	}
	if _, e = s.DecideSeries(ctx, "account", global.ID, "accept", rec.SeriesSelection{Revision: global.Revision, IDs: global.NewIDs}); e == nil {
		t.Fatal("stale proposal accepted")
	}
	if !reflect.DeepEqual(snap, dumpRows(t, s, "SELECT before_json,after_json FROM series_proposal_decisions WHERE proposal_id='"+batch.ID+"'")) {
		t.Fatal("other scope overwrote undo snapshot")
	}
	if _, e = s.DecideSeries(ctx, "account", accepted.ID, "undo", rec.SeriesSelection{Revision: accepted.Revision}); e != nil {
		t.Fatal(e)
	}
	closeScopedImport(t, s, "batch")
	current := finishScopedSeries(t, s, "global-again", "", [][]int{{0, 1}})[0]
	if _, e = s.DecideSeries(ctx, "account", current.ID, "accept", rec.SeriesSelection{Revision: current.Revision, IDs: current.NewIDs}); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(snap, dumpRows(t, s, "SELECT before_json,after_json FROM series_proposal_decisions WHERE proposal_id='"+batch.ID+"'")) {
		t.Fatal("new acceptance replaced old snapshot")
	}
	var n int
	s.Read.QueryRow("SELECT count(*) FROM series_proposal_decisions").Scan(&n)
	if n != 2 {
		t.Fatal(n)
	}
}

func TestSeriesPublishFailureIsAtomicAndClassified(t *testing.T) {
	s := discoveryStore(t, 4)
	ctx := context.Background()
	j, r, result := prepareScopedSeries(t, s, "rollback", "batch", [][]int{{0, 1}, {2, 3}})
	if _, e := s.Write.Exec("CREATE TRIGGER reject_series BEFORE INSERT ON series_proposal_members WHEN NEW.work_id='i:e0003' BEGIN SELECT RAISE(ABORT,'anonymous constraint'); END"); e != nil {
		t.Fatal(e)
	}
	e := s.PublishSeries(ctx, *j, r, result, false)
	var publication *rec.SeriesPublicationError
	if !errors.As(e, &publication) || publication.Reason != "database_constraint" {
		t.Fatal(e)
	}
	for _, table := range []string{"series_proposals", "series_proposal_members", "import_proposals"} {
		var n int
		s.Read.QueryRow("SELECT count(*) FROM " + table).Scan(&n)
		if n != 0 {
			t.Fatal("partial publication", table, n)
		}
	}
	s.Write.Exec("DROP TRIGGER reject_series")
	if e = s.ControlJob(ctx, j.ID, "cancel", ""); e != nil {
		t.Fatal(e)
	}
	if e = s.PublishSeries(ctx, *j, r, result, false); e == nil {
		t.Fatal("stale worker published")
	}
}

func TestSeriesConcurrentPublishIsIdempotentAndUndoRejectsLaterEdits(t *testing.T) {
	s := discoveryStore(t, 2)
	ctx := context.Background()
	titleSeriesFixture(t, s, 2)
	j, r, result := prepareScopedSeries(t, s, "concurrent", "", [][]int{{0, 1}})
	done := make(chan error, 2)
	for range 2 {
		go func() { done <- s.PublishSeries(ctx, *j, r, result, false) }()
	}
	for range 2 {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	if e := s.FinishBackground(ctx, *j, ""); e != nil {
		t.Fatal(e)
	}
	page, e := s.SeriesProposals(ctx, j.ID, "lib", "", "pending", 1, 100)
	if e != nil || page.Total != 1 {
		t.Fatal(page, e)
	}
	p := page.Items[0]
	accepted, e := s.DecideSeries(ctx, "account", p.ID, "accept", rec.SeriesSelection{Revision: p.Revision, IDs: p.NewIDs})
	if e != nil {
		t.Fatal(e)
	}
	before := dumpRows(t, s, "SELECT * FROM series_proposal_decisions ORDER BY proposal_key")
	if _, e = s.Write.Exec("UPDATE series SET name='Later manual edit',revision=revision+1 WHERE id=?", accepted.Target); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DecideSeries(ctx, "account", accepted.ID, "undo", rec.SeriesSelection{Revision: accepted.Revision}); !errors.Is(e, model.ErrConflict) {
		t.Fatal("undo overwrote later organization edit", e)
	}
	if !reflect.DeepEqual(before, dumpRows(t, s, "SELECT * FROM series_proposal_decisions ORDER BY proposal_key")) {
		t.Fatal("failed undo changed its snapshot")
	}
}
