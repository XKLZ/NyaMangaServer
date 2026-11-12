package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	rec "nyamangaserver.local/server/internal/recommendation"
	"nyamangaserver.local/server/internal/testfixture"
	"nyamangaserver.local/server/migrations"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func schemaEight(t *testing.T) fstest.MapFS {
	f := schemaSeven(t)
	b, _ := migrations.Files.ReadFile("008_work_recommendations.sql")
	f["008_work_recommendations.sql"] = &fstest.MapFile{Data: b}
	return f
}
func discoveryStore(t *testing.T, n int) *Store {
	t.Helper()
	s, e := Open(context.Background(), t.TempDir(), migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	// Existing recommendation/import scenarios explicitly opt into optional stages.
	if _, e = s.Write.Exec("UPDATE enhancement_settings SET enabled=1,runtime_armed=1"); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	if _, e = s.Write.Exec("INSERT INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision) VALUES('lib','lib','anonymous','utf8','identity',1);INSERT INTO accounts VALUES(1,'account','anonymous',1,1)"); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("e%04d", i)
		if e = testfixture.SeedEdition(context.Background(), s.Write, id, "lib", id+".cbz", id); e != nil {
			t.Fatal(e)
		}
	}
	return s
}
func finishDiscovery(t *testing.T, s *Store, id string, edges []rec.PairEvidence) *model.Job {
	t.Helper()
	ctx := context.Background()
	j, e := s.CreateDiscoveryJob(ctx, id, "lib")
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Claim(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	pool, e := s.RecommendationPool(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	for i := range pool.Editions {
		row := rec.PairBlock{Row: i, NextRow: i + 1, Compared: len(pool.Editions) - i - 1, HardBits: make([]byte, (len(pool.Editions)-i-1+7)/8)}
		for _, edge := range edges {
			if edge.A == i {
				row.Edges = append(row.Edges, edge)
			}
		}
		if e = s.SaveDiscoveryBlock(ctx, *j, row); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.PublishDiscoveryProposals(ctx, *j); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishBackground(ctx, *j, ""); e != nil {
		t.Fatal(e)
	}
	return j
}
func evidence(a, b int) rec.PairEvidence {
	v := .9
	return rec.PairEvidence{A: a, B: b, Tier: "strong", Score: &v, Signal: json.RawMessage(`{"risks":[]}`)}
}
func TestDiscoveryUpgradePreservesEightAndRollsBack(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := Open(ctx, dir, schemaEight(t))
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, s)
	s.Close()
	broken := schemaEight(t)
	b, _ := migrations.Files.ReadFile("009_work_discovery.sql")
	broken["009_work_discovery.sql"] = &fstest.MapFile{Data: append(b, []byte("\nINSERT INTO work_pair_blocks VALUES('missing',0,X'00');")...)}
	if s, e = Open(ctx, dir, broken); e == nil {
		s.Close()
		t.Fatal("invalid migration passed")
	}
	db, e := sql.Open("sqlite", filepath.Join(dir, "app.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	var version int
	db.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&version)
	db.Close()
	if version != 8 {
		t.Fatal(version)
	}
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.Version != 30 {
		t.Fatal(s.Version)
	}
	var n int
	s.Read.QueryRow("SELECT count(*) FROM editions WHERE id='book'").Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
}
func TestDiscoveryMoreThan1000AndStaleBlockGuard(t *testing.T) {
	ctx := context.Background()
	s := discoveryStore(t, 1001)
	j, e := s.CreateDiscoveryJob(ctx, "large", "lib")
	if e != nil || j.Total == nil || *j.Total != 1001 {
		t.Fatal(j, e)
	}
	if _, e = s.CreateDiscoveryJob(ctx, "duplicate", "lib"); !errors.Is(e, model.ErrJobActive) {
		t.Fatal(e)
	}
	old, e := s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ControlJob(ctx, j.ID, "pause", ""); e != nil {
		t.Fatal(e)
	}
	if e = s.ControlJob(ctx, j.ID, "resume", ""); e != nil {
		t.Fatal(e)
	}
	block := rec.PairBlock{Row: 0, NextRow: 1, Compared: 1000, HardBits: make([]byte, 125)}
	if e = s.SaveDiscoveryBlock(ctx, *old, block); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal(e)
	}
	current, e := s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SaveDiscoveryBlock(ctx, *current, block); e != nil {
		t.Fatal(e)
	}
	run, e := s.DiscoveryRun(ctx, j.ID)
	if e != nil || run.NextRow != 1 {
		t.Fatal(run, e)
	}
	if e = s.ControlJob(ctx, j.ID, "cancel", ""); e != nil {
		t.Fatal(e)
	}
	replacement, e := s.CreateDiscoveryJob(ctx, "replacement", "lib")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SaveDiscoveryBlock(ctx, *current, rec.PairBlock{Row: 1, NextRow: 2, Compared: 999, HardBits: make([]byte, 125)}); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal("cancelled worker committed", e)
	}
	var owner string
	s.Read.QueryRow("SELECT job_id FROM job_resources WHERE resource_key='recommendation:edition:e0000'").Scan(&owner)
	if owner != replacement.ID {
		t.Fatal("new task resource ownership lost", owner)
	}
}
func TestDiscoveryStablePlansAtomicAcceptUndoAndJobCleanup(t *testing.T) {
	ctx := context.Background()
	s := discoveryStore(t, 4)
	edges := []rec.PairEvidence{evidence(0, 1), evidence(2, 3)}
	finishDiscovery(t, s, "first", edges)
	page, e := s.WorkProposals(ctx, "", "lib", "", "pending", true, 1, 25)
	if e != nil || len(page.Items) != 2 {
		t.Fatal(page, e)
	}
	ab, cd := page.Items[0], page.Items[1]
	if ab.NewIDs[0] != "e0000" {
		ab, cd = cd, ab
	}
	finishDiscovery(t, s, "second", edges)
	again, e := s.WorkProposals(ctx, "", "lib", "", "pending", true, 1, 25)
	if e != nil || again.Total != 2 {
		t.Fatal(again, e)
	}
	for _, p := range again.Items {
		if p.ID == ab.ID && p.Revision != ab.Revision {
			t.Fatal("unrelated rediscovery revision", p)
		}
	}
	if _, e = s.Write.Exec(`CREATE TRIGGER reject_decision BEFORE INSERT ON work_proposal_decisions BEGIN SELECT RAISE(ABORT,'anonymous fault'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DecideWorkProposal(ctx, "account", ab.ID, "accept", rec.ProposalSelection{Revision: ab.Revision, IDs: ab.NewIDs}); e == nil {
		t.Fatal("transaction fault lost")
	}
	var works int
	s.Read.QueryRow("SELECT count(*) FROM works").Scan(&works)
	if works != 4 {
		t.Fatal("partial organization", works)
	}
	s.Write.Exec("DROP TRIGGER reject_decision")
	accepted, e := s.DecideWorkProposal(ctx, "account", ab.ID, "accept", rec.ProposalSelection{Revision: ab.Revision, IDs: ab.NewIDs})
	if e != nil || accepted.State != "accepted" {
		t.Fatal(accepted, e)
	}
	if _, e = s.DecideWorkProposal(ctx, "account", ab.ID, "accept", rec.ProposalSelection{Revision: ab.Revision, IDs: ab.NewIDs}); e != nil {
		t.Fatal("idempotence", e)
	}
	var beforeEvents, afterEvents int
	s.Read.QueryRow("SELECT count(*) FROM events WHERE type='work_proposal' AND resource_id=?", cd.ID).Scan(&beforeEvents)
	if _, e = s.RebuildDiscoveryPending(ctx); e != nil {
		t.Fatal(e)
	}
	s.Read.QueryRow("SELECT count(*) FROM events WHERE type='work_proposal' AND resource_id=?", cd.ID).Scan(&afterEvents)
	if beforeEvents != afterEvents {
		t.Fatal("unrelated proposal emitted refresh", beforeEvents, afterEvents)
	}
	currentCD, e := s.WorkProposal(ctx, cd.ID)
	if e != nil || currentCD.Revision != cd.Revision {
		t.Fatal("unrelated proposal invalidated", currentCD, e)
	}
	if _, e = s.DecideWorkProposal(ctx, "account", cd.ID, "dismiss", rec.ProposalSelection{Revision: cd.Revision}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("DELETE FROM job_items;DELETE FROM job_resources;DELETE FROM jobs"); e != nil {
		t.Fatal("history prevented cleanup", e)
	}
	// Ordinary metadata and personal state do not block relationship-only Undo.
	s.Write.Exec("UPDATE editions SET revision=revision+1 WHERE id='e0000';INSERT INTO edition_user_states VALUES('account','e0000',1,1,1)")
	undone, e := s.DecideWorkProposal(ctx, "account", ab.ID, "undo", rec.ProposalSelection{Revision: accepted.Revision})
	if e != nil || undone.State != "undone" {
		t.Fatal(undone, e)
	}
	var revision int64
	s.Read.QueryRow("SELECT revision FROM works WHERE id=?", ab.Target).Scan(&revision)
	if revision <= 2 {
		t.Fatal("ABA", revision)
	}
	finishDiscovery(t, s, "third", edges)
	page, e = s.WorkProposals(ctx, "", "lib", "", "pending", true, 1, 25)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range page.Items {
		if p.Key == cd.Key {
			t.Fatal("dismiss lost after job cleanup")
		}
	}
}
func TestProposalResolveDoesNotSilentlyChangeRetainedWork(t *testing.T) {
	ctx := context.Background()
	s := discoveryStore(t, 3)
	finishDiscovery(t, s, "clique", []rec.PairEvidence{evidence(0, 1), evidence(0, 2), evidence(1, 2)})
	page, e := s.WorkProposals(ctx, "clique", "", "", "pending", true, 1, 25)
	if e != nil || page.Total != 1 {
		t.Fatal(page, e)
	}
	p := page.Items[0]
	ids := []string{"e0001", "e0002"}
	if _, e = s.DecideWorkProposal(ctx, "account", p.ID, "accept", rec.ProposalSelection{Revision: p.Revision, IDs: ids}); !errors.Is(e, rec.ErrSelection) {
		t.Fatal(e)
	}
	resolved, e := s.ResolveWorkProposal(ctx, p.ID, rec.ProposalSelection{Revision: p.Revision, IDs: ids})
	if e != nil || resolved.Target == p.Target || len(resolved.NewIDs) != 2 {
		t.Fatal(resolved, e)
	}
	if _, e = s.DecideWorkProposal(ctx, "account", resolved.ID, "accept", rec.ProposalSelection{Revision: resolved.Revision, IDs: ids}); e != nil {
		t.Fatal(e)
	}
}

func TestProposalSelectionAndUndoConflict(t *testing.T) {
	ctx := context.Background()
	s := discoveryStore(t, 2)
	finishDiscovery(t, s, "guard", []rec.PairEvidence{evidence(0, 1)})
	page, _ := s.WorkProposals(ctx, "guard", "", "", "pending", true, 1, 25)
	p := page.Items[0]
	for _, ids := range [][]string{nil, {}, {"e0000", "e0000"}, {"e0000", "foreign"}, {"e0000"}} {
		in := rec.ProposalSelection{Revision: p.Revision, IDs: ids}
		if _, e := s.ResolveWorkProposal(ctx, p.ID, in); !errors.Is(e, model.ErrInput) {
			t.Fatal("resolve", ids, e)
		}
		if _, e := s.DecideWorkProposal(ctx, "account", p.ID, "accept", in); !errors.Is(e, model.ErrInput) {
			t.Fatal("accept", ids, e)
		}
	}
	var n int
	s.Read.QueryRow("SELECT count(*) FROM works").Scan(&n)
	if n != 2 {
		t.Fatal("invalid input changed organization", n)
	}
	accepted, e := s.DecideWorkProposal(ctx, "account", p.ID, "accept", rec.ProposalSelection{Revision: p.Revision, IDs: p.NewIDs})
	if e != nil {
		t.Fatal(e)
	}
	s.Write.Exec("UPDATE works SET revision=revision+1 WHERE id=?", p.Target)
	if _, e = s.DecideWorkProposal(ctx, "account", p.ID, "undo", rec.ProposalSelection{Revision: accepted.Revision}); !errors.Is(e, model.ErrConflict) {
		t.Fatal("later organization edit overwritten", e)
	}
}

type discoveryCountingRunner struct {
	testfixture.RecommendationRunner
	mu            sync.Mutex
	prepare, rows int
	invalid       bool
}

func (r *discoveryCountingRunner) Call(ctx context.Context, op string, in, out any) (rec.Resources, error) {
	r.mu.Lock()
	if op == "discovery_prepare" {
		r.prepare++
	}
	if op == "discovery_row" {
		r.rows++
		raw, _ := json.Marshal(in)
		var obj map[string]any
		json.Unmarshal(raw, &obj)
		if (len(obj) != 2 && len(obj) != 3) || obj["books"] != nil || obj["features"] != nil {
			r.invalid = true
		}
	}
	r.mu.Unlock()
	return r.RecommendationRunner.Call(ctx, op, in, out)
}
func TestDiscoveryServiceSendsPoolOnceAndRowsOnly(t *testing.T) {
	ctx := context.Background()
	s := discoveryStore(t, 4)
	runner := &discoveryCountingRunner{}
	service := rec.New(s, nil, runner, t.TempDir(), nil)
	service.Start(ctx)
	defer service.Shutdown(ctx)
	j, e := service.StartDiscovery(ctx, "lib")
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		current, e := s.Job(ctx, j.ID)
		if e != nil {
			t.Fatal(e)
		}
		if current.State == "completed" {
			break
		}
		if current.State == "failed" || current.State == "paused" || time.Now().After(deadline) {
			t.Fatal(current)
		}
		time.Sleep(10 * time.Millisecond)
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.prepare != 1 || runner.rows != 3 || runner.invalid {
		t.Fatal(runner.prepare, runner.rows, runner.invalid)
	}
	page, e := s.WorkProposals(ctx, j.ID, "", "", "pending", true, 1, 25)
	if e != nil || page.Total != 1 || len(page.Items[0].Members) != 4 {
		t.Fatal(page, e)
	}
}

func TestDiscoveryAdditionUndoTouchesOnlyChangedSeries(t *testing.T) {
	for _, sourceSeries := range []bool{false, true} {
		t.Run(fmt.Sprint(sourceSeries), func(t *testing.T) {
			ctx := context.Background()
			s := discoveryStore(t, 3)
			c, e := s.WorkContext(ctx, "account", []string{"e0000", "e0001"}, nil, nil)
			if e != nil {
				t.Fatal(e)
			}
			_, e = s.ChangeWork(ctx, "account", model.WorkCommand{Action: "work_merge", ID: "i:e0000", WorkIDs: []string{"i:e0001"}, Expected: c.Expected}, false, organization.PlanWork)
			if e != nil {
				t.Fatal(e)
			}
			owner := "i:e0000"
			if sourceSeries {
				owner = "i:e0002"
			}
			if _, e = s.Write.Exec("INSERT INTO series VALUES('s','lib','Anonymous',1);INSERT INTO series_members VALUES('s',?,1)", owner); e != nil {
				t.Fatal(e)
			}
			finishDiscovery(t, s, "add", []rec.PairEvidence{evidence(0, 2), evidence(1, 2)})
			page, e := s.WorkProposals(ctx, "add", "", "add_to_work", "pending", true, 1, 25)
			if e != nil || page.Total != 1 {
				t.Fatal(page, e)
			}
			p := page.Items[0]
			accepted, e := s.DecideWorkProposal(ctx, "account", p.ID, "accept", rec.ProposalSelection{Revision: p.Revision, IDs: p.NewIDs})
			if e != nil {
				t.Fatal(e)
			}
			var rev int64
			s.Read.QueryRow("SELECT revision FROM series WHERE id='s'").Scan(&rev)
			if !sourceSeries && rev != 1 {
				t.Fatal("unrelated target series changed", rev)
			}
			if !sourceSeries {
				if _, e = s.Write.Exec("UPDATE series SET name='Later rename',revision=revision+1 WHERE id='s'"); e != nil {
					t.Fatal(e)
				}
			}
			_, e = s.DecideWorkProposal(ctx, "account", p.ID, "undo", rec.ProposalSelection{Revision: accepted.Revision})
			if e != nil {
				t.Fatal(e)
			}
			var after int64
			var name, work string
			s.Read.QueryRow("SELECT revision,name FROM series WHERE id='s'").Scan(&after, &name)
			s.Read.QueryRow("SELECT work_id FROM series_members WHERE series_id='s'").Scan(&work)
			if work != owner {
				t.Fatal("series member not restored", work, owner)
			}
			if !sourceSeries && (after != 2 || name != "Later rename") {
				t.Fatal("unrelated series edit reverted", after, name)
			}
			if sourceSeries && after <= rev {
				t.Fatal("changed series ABA", after, rev)
			}
		})
	}
}
func TestDiscoveryRetryRetainsModeParentAndRejectsMemberRetry(t *testing.T) {
	s := discoveryStore(t, 2)
	ctx := context.Background()
	finishDiscovery(t, s, "parent", []rec.PairEvidence{evidence(0, 1)})
	service := rec.New(s, nil, &testfixture.RecommendationRunner{}, t.TempDir(), nil)
	if _, e := service.Retry(ctx, "parent", []int{1}); !errors.Is(e, model.ErrInput) {
		t.Fatal(e)
	}
	j, e := service.Retry(ctx, "parent", nil)
	if e != nil || j.Mode != "library" || j.ParentID == nil || *j.ParentID != "parent" {
		t.Fatal(j, e)
	}
}

func TestResolvedSelectionSurvivesUnrelatedRegroup(t *testing.T) {
	s := discoveryStore(t, 5)
	ctx := context.Background()
	finishDiscovery(t, s, "select", []rec.PairEvidence{evidence(0, 1), evidence(0, 2), evidence(1, 2), evidence(3, 4)})
	page, _ := s.WorkProposals(ctx, "select", "", "", "pending", true, 1, 25)
	var abc, other rec.Proposal
	for _, p := range page.Items {
		if len(p.NewIDs) == 3 {
			abc = p
		} else {
			other = p
		}
	}
	selected, e := s.ResolveWorkProposal(ctx, abc.ID, rec.ProposalSelection{Revision: abc.Revision, IDs: []string{"e0000", "e0001"}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DecideWorkProposal(ctx, "account", other.ID, "accept", rec.ProposalSelection{Revision: other.Revision, IDs: other.NewIDs}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.RebuildDiscoveryPending(ctx); e != nil {
		t.Fatal(e)
	}
	current, e := s.WorkProposal(ctx, selected.ID)
	if e != nil || current.State != "pending" || current.Revision != selected.Revision {
		t.Fatal("selection invalidated", current, e)
	}
	if _, e = s.DecideWorkProposal(ctx, "account", selected.ID, "accept", rec.ProposalSelection{Revision: selected.Revision, IDs: selected.NewIDs}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.WorkProposal(ctx, "absent"); !errors.Is(e, model.ErrNotFound) {
		t.Fatal(e)
	}
}

func TestDiscoveryFeatureCASSeparatesOrganizationFromActualInputs(t *testing.T) {
	s := discoveryStore(t, 2)
	ctx := context.Background()
	j, e := s.CreateDiscoveryJob(ctx, "features", "lib")
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	pool, e := s.RecommendationPool(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	input := pool.Editions[0]
	if _, e = s.Write.Exec("UPDATE editions SET organization_revision=organization_revision+1 WHERE id=?", input.Book.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveRecommendationFeatures(ctx, *j, input, rec.FeatureResult{}); e != nil {
		t.Fatal("organization change rejected reusable features", e)
	}
	if _, e = s.Write.Exec("UPDATE files SET filename_title='Changed matching title' WHERE edition_id=?", input.Book.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveRecommendationFeatures(ctx, *j, input, rec.FeatureResult{}); !errors.Is(e, rec.ErrStale) {
		t.Fatal("actual input change accepted", e)
	}
	if _, e = s.Write.Exec("UPDATE work_discovery_runs SET evidence_bytes=? WHERE job_id=?", rec.MaxDiscoveryEvidenceBytes, j.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveDiscoveryBlock(ctx, *j, rec.PairBlock{Row: 0, NextRow: 1, Compared: 1, HardBits: []byte{0}, Edges: []rec.PairEvidence{evidence(0, 1)}}); !errors.Is(e, model.ErrCapacity) {
		t.Fatal("evidence byte budget ignored", e)
	}
}

func TestResolvedSelectionCanBeReusedAfterUndo(t *testing.T) {
	s := discoveryStore(t, 3)
	ctx := context.Background()
	finishDiscovery(t, s, "repeat-select", []rec.PairEvidence{evidence(0, 1), evidence(0, 2), evidence(1, 2)})
	page, _ := s.WorkProposals(ctx, "", "lib", "create_work", "pending", true, 1, 25)
	original := page.Items[0]
	selected, e := s.ResolveWorkProposal(ctx, original.ID, rec.ProposalSelection{Revision: original.Revision, IDs: []string{"e0001", "e0002"}})
	if e != nil {
		t.Fatal(e)
	}
	ignored, e := s.DecideWorkProposal(ctx, "account", selected.ID, "dismiss", rec.ProposalSelection{Revision: selected.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ResolveWorkProposal(ctx, original.ID, rec.ProposalSelection{Revision: original.Revision, IDs: selected.NewIDs}); !errors.Is(e, model.ErrConflict) {
		t.Fatal("resolve silently restored ignored selection", e)
	}
	selected, e = s.DecideWorkProposal(ctx, "account", ignored.ID, "restore", rec.ProposalSelection{Revision: ignored.Revision})
	if e != nil {
		t.Fatal(e)
	}
	accepted, e := s.DecideWorkProposal(ctx, "account", selected.ID, "accept", rec.ProposalSelection{Revision: selected.Revision, IDs: selected.NewIDs})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DecideWorkProposal(ctx, "account", selected.ID, "undo", rec.ProposalSelection{Revision: accepted.Revision}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.RebuildDiscoveryPending(ctx); e != nil {
		t.Fatal(e)
	}
	original, e = s.WorkProposal(ctx, original.ID)
	if e != nil {
		t.Fatal(e)
	}
	selected, e = s.ResolveWorkProposal(ctx, original.ID, rec.ProposalSelection{Revision: original.Revision, IDs: []string{"e0001", "e0002"}})
	if e != nil || selected.State != "pending" {
		t.Fatal(selected, e)
	}
	if _, e = s.DecideWorkProposal(ctx, "account", selected.ID, "accept", rec.ProposalSelection{Revision: selected.Revision, IDs: selected.NewIDs}); e != nil {
		t.Fatal(e)
	}
}
