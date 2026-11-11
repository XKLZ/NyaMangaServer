package store

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	rec "nyamangaserver.local/server/internal/recommendation"
	"nyamangaserver.local/server/migrations"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func schemaNine(t *testing.T) fstest.MapFS {
	f := schemaEight(t)
	raw, _ := migrations.Files.ReadFile("009_work_discovery.sql")
	f["009_work_discovery.sql"] = &fstest.MapFile{Data: raw}
	return f
}

func TestSeriesMigrationPreservesAllOldTablesAndRollback(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := Open(ctx, dir, schemaNine(t))
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, s)
	seedEdition(t, s, "second", "lib", "second.cbz", "Second")
	if _, e = s.Write.Exec(`INSERT INTO accounts VALUES(1,'account','private-anonymous',1,1);INSERT INTO sessions VALUES(zeroblob(32),'account',1,9999999999)`); e != nil {
		t.Fatal(e)
	}
	finishDiscovery(t, s, "historic", []rec.PairEvidence{evidence(0, 1)})
	if _, e = s.Write.Exec(`INSERT INTO jobs(id,type,state,stage,generation,total,created_at,updated_at) VALUES('paused','metadata','paused','processing',7,1,1,2);
 INSERT INTO job_items(job_id,ordinal,state,edition_id,input_json) VALUES('paused',1,'pending','book','{"anonymous":"preserve"}');
 INSERT INTO job_resources VALUES('metadata:book:book','paused',7);
 INSERT INTO source_previews VALUES('preview','book','paused',NULL,1,1,1,100,'pending');
 INSERT INTO maintenance_plans VALUES('plan','trash','{}','{}',100,'paused',1);
 INSERT INTO maintenance_blocks VALUES('lib','paused');`); e != nil {
		t.Fatal(e)
	}
	rows, e := s.Read.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name<>'schema_migrations' ORDER BY name")
	if e != nil {
		t.Fatal(e)
	}
	tables := []string{}
	for rows.Next() {
		var name string
		rows.Scan(&name)
		tables = append(tables, name)
	}
	rows.Close()
	before := map[string][][]any{}
	columns := map[string]string{}
	for _, name := range tables {
		info, e := s.Read.Query("PRAGMA table_info(" + name + ")")
		if e != nil {
			t.Fatal(e)
		}
		cols := []string{}
		for info.Next() {
			var cid, notnull, pk int
			var col, typ string
			var def any
			if e = info.Scan(&cid, &col, &typ, &notnull, &def, &pk); e != nil {
				t.Fatal(e)
			}
			cols = append(cols, col)
		}
		info.Close()
		columns[name] = strings.Join(cols, ",")
	}
	for _, name := range tables {
		before[name] = dumpRows(t, s, "SELECT "+columns[name]+" FROM "+name+" ORDER BY rowid")
	}
	s.Close()
	broken := schemaNine(t)
	raw, _ := migrations.Files.ReadFile("010_series_discovery.sql")
	broken["010_series_discovery.sql"] = &fstest.MapFile{Data: append(slices.Clone(raw), []byte("\nINSERT INTO series_pair_rows VALUES('missing',0,'[]');")...)}
	if db, e := Open(ctx, dir, broken); e == nil {
		db.Close()
		t.Fatal("bad migration passed")
	}
	old, e := Open(ctx, dir, schemaNine(t))
	if e != nil {
		t.Fatal(e)
	}
	if old.Version != 9 {
		t.Fatal(old.Version)
	}
	for _, name := range tables {
		if got := dumpRows(t, old, "SELECT "+columns[name]+" FROM "+name+" ORDER BY rowid"); !reflect.DeepEqual(got, before[name]) {
			t.Fatal("rollback changed", name)
		}
	}
	old.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.Version != 27 {
		t.Fatal(s.Version)
	}
	for _, name := range tables {
		if got := dumpRows(t, s, "SELECT "+columns[name]+" FROM "+name+" ORDER BY rowid"); !reflect.DeepEqual(got, before[name]) {
			t.Fatal("upgrade changed", name)
		}
	}
	fk, e := s.Read.Query("PRAGMA foreign_key_check")
	if e != nil {
		t.Fatal(e)
	}
	if fk.Next() {
		t.Fatal("foreign keys")
	}
	fk.Close()
	snapshots, _ := filepath.Glob(filepath.Join(dir, "upgrade-snapshots", "*.sqlite"))
	s.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	after, _ := filepath.Glob(filepath.Join(dir, "upgrade-snapshots", "*.sqlite"))
	if len(after) != len(snapshots) {
		t.Fatal("repeat startup snapshot")
	}
}

func finishSeries(t *testing.T, s *Store, id string, groups [][]int) (*model.Job, []rec.SeriesProposal) {
	return finishSeriesVersion(t, s, id, groups, rec.SeriesPresentationVersion)
}
func finishSeriesVersion(t *testing.T, s *Store, id string, groups [][]int, version string) (*model.Job, []rec.SeriesProposal) {
	t.Helper()
	ctx := context.Background()
	j, e := s.CreateSeriesDiscovery(ctx, id, "lib", "")
	if e != nil {
		t.Fatal(e)
	}
	if version == "" {
		if _, e = s.Write.Exec("UPDATE series_discovery_runs SET context_json=json_remove(context_json,'$.series_presentation_version') WHERE job_id=?", id); e != nil {
			t.Fatal(e)
		}
	}
	j, e = s.Claim(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.SeriesRun(ctx, id, false)
	if e != nil {
		t.Fatal(e)
	}
	for at, ed := range r.Pool.Editions {
		if e = s.SaveSeriesFeature(ctx, *j, at, ed, nil); e != nil {
			t.Fatal(e)
		}
	}
	for row := range r.Pool.Works {
		if e = s.SaveSeriesBlock(ctx, *j, rec.SeriesBlock{Row: row, NextRow: row + 1, Edges: [][2]string{}}); e != nil {
			t.Fatal(e)
		}
	}
	result := rec.SeriesResult{Algorithm: rec.SeriesAlgorithm, Proposals: []rec.SeriesRawPlan{}, Pending: []rec.SeriesPending{}}
	for _, indices := range groups {
		ids := []string{}
		for _, n := range indices {
			ids = append(ids, r.Pool.Works[n].ID)
		}
		result.Proposals = append(result.Proposals, rec.SeriesRawPlan{Kind: "create_series", WorkIDs: ids, NewIDs: ids, Name: "Anonymous series", Tier: "strong", Risks: []string{}})
	}
	if e = s.PublishSeries(ctx, *j, r, result, false); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishBackground(ctx, *j, ""); e != nil {
		t.Fatal(e)
	}
	page, e := s.SeriesProposals(ctx, id, "lib", "", "pending", 1, 100)
	if e != nil {
		t.Fatal(e)
	}
	return j, page.Items
}

func TestSeriesCreateResolveDismissRestoreAcceptUndoAndPersistence(t *testing.T) {
	s := discoveryStore(t, 4)
	ctx := context.Background()
	_, plans := finishSeries(t, s, "series", [][]int{{0, 1, 2}})
	p := plans[0]
	members, e := s.SeriesMembers(ctx, p.ID, 2, 2)
	if e != nil || members.Total != 3 || len(members.Items) != 1 {
		t.Fatal(members, e)
	}
	if _, e = s.DecideSeries(ctx, "account", p.ID, "accept", rec.SeriesSelection{Revision: p.Revision, IDs: p.NewIDs[:2]}); !errors.Is(e, rec.ErrSelection) {
		t.Fatal(e)
	}
	name := "Edited series"
	resolved, e := s.ResolveSeries(ctx, p.ID, rec.SeriesSelection{Revision: p.Revision, IDs: p.NewIDs[:2], Name: &name, Order: slices.Clone(p.NewIDs[:2])})
	if e != nil {
		t.Fatal(e)
	}
	p, e = s.DecideSeries(ctx, "account", resolved.ID, "dismiss", rec.SeriesSelection{Revision: resolved.Revision})
	if e != nil {
		t.Fatal(e)
	}
	p, e = s.DecideSeries(ctx, "account", p.ID, "restore", rec.SeriesSelection{Revision: p.Revision})
	if e != nil {
		t.Fatal(e)
	}
	accepted, e := s.DecideSeries(ctx, "account", p.ID, "accept", rec.SeriesSelection{Revision: p.Revision, IDs: p.NewIDs})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DecideSeries(ctx, "account", p.ID, "accept", rec.SeriesSelection{Revision: p.Revision, IDs: p.NewIDs}); e != nil {
		t.Fatal("idempotence", e)
	}
	series, e := s.SeriesDetail(ctx, accepted.Target)
	if e != nil || series.Name != name || series.MemberCount != 2 {
		t.Fatal(series, e)
	}
	// Later Edition edits are independent of series Undo and must be retained.
	var editedEdition string
	if e = s.Read.QueryRow("SELECT preferred_edition_id FROM works WHERE id=?", accepted.Order[0]).Scan(&editedEdition); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("UPDATE files SET filename_title='Later user title' WHERE edition_id=?", editedEdition); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DecideSeries(ctx, "account", accepted.ID, "undo", rec.SeriesSelection{Revision: accepted.Revision}); e != nil {
		t.Fatal(e)
	}
	var title string
	if e = s.Read.QueryRow("SELECT filename_title FROM files WHERE edition_id=?", editedEdition).Scan(&title); e != nil || title != "Later user title" {
		t.Fatal("Undo overwrote Edition data", title, e)
	}
	var count int
	s.Read.QueryRow("SELECT count(*) FROM series").Scan(&count)
	if count != 0 {
		t.Fatal(count)
	}
	s.Read.QueryRow("SELECT count(*) FROM works").Scan(&count)
	if count != 4 {
		t.Fatal("Works changed", count)
	}
	s.Read.QueryRow("SELECT count(*) FROM editions").Scan(&count)
	if count != 4 {
		t.Fatal("Editions changed", count)
	}
	// Restart reads the same data; no scan or recommendation task is needed.
	var file string
	s.Read.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&file)
	s.Close()
	reopened, e := Open(ctx, filepath.Dir(file), migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	history, e := reopened.SeriesProposal(ctx, accepted.ID)
	if e != nil || history.State != "undone" {
		t.Fatal(history, e)
	}
}

func TestSeriesTaskGenerationAndInputsGuard(t *testing.T) {
	s := discoveryStore(t, 3)
	ctx := context.Background()
	j, e := s.CreateSeriesDiscovery(ctx, "run", "lib", "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateSeriesDiscovery(ctx, "duplicate", "lib", ""); !errors.Is(e, model.ErrJobActive) {
		t.Fatal(e)
	}
	old, _ := s.Claim(ctx, j.ID)
	r, _ := s.SeriesRun(ctx, j.ID, false)
	if e = s.PublishSeries(ctx, *old, r, rec.SeriesResult{Algorithm: rec.SeriesAlgorithm}, false); !errors.Is(e, model.ErrConflict) {
		t.Fatal("premature publish", e)
	}
	s.ControlJob(ctx, j.ID, "pause", "")
	s.ControlJob(ctx, j.ID, "resume", "")
	fresh, _ := s.Claim(ctx, j.ID)
	if e = s.SaveSeriesFeature(ctx, *old, 0, r.Pool.Editions[0], nil); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal(e)
	}
	if e = s.SaveSeriesFeature(ctx, *fresh, 0, r.Pool.Editions[0], nil); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveSeriesBlock(ctx, *fresh, rec.SeriesBlock{Row: 0, NextRow: 1}); !errors.Is(e, model.ErrInput) {
		t.Fatal("features incomplete", e)
	}
	s.ControlJob(ctx, j.ID, "cancel", "")
	if _, e = s.CreateSeriesDiscovery(ctx, "new", "lib", ""); e != nil {
		t.Fatal(e)
	}
}

func TestSeriesMaintenanceOfNonPreferredEditionPausesWholeWork(t *testing.T) {
	s := discoveryStore(t, 3)
	ctx := context.Background()
	c, e := s.WorkContext(ctx, "account", []string{"e0000", "e0001"}, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeWork(ctx, "account", model.WorkCommand{Action: "work_merge", ID: "i:e0000", WorkIDs: []string{"i:e0001"}, Expected: c.Expected}, false, organization.PlanWork); e != nil {
		t.Fatal(e)
	}
	j, e := s.CreateSeriesDiscovery(ctx, "maintenance-series", "lib", "")
	if e != nil {
		t.Fatal(e)
	}
	old, e := s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = pauseAffected(ctx, tx, []string{"e0001"}, ""); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	paused, _ := s.Job(ctx, j.ID)
	if paused.State != "paused" || paused.Generation <= old.Generation {
		t.Fatal(paused)
	}
	r, _ := s.SeriesRun(ctx, j.ID, false)
	if e = s.SaveSeriesFeature(ctx, *old, 0, r.Pool.Editions[0], nil); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal(e)
	}
}

func TestSeriesJoinPreservesOrderAndUndoRejectsLaterEdits(t *testing.T) {
	s := discoveryStore(t, 4)
	ctx := context.Background()
	_, plans := finishSeries(t, s, "run", [][]int{{0, 1}})
	p := plans[0]
	p, e := s.DecideSeries(ctx, "account", p.ID, "accept", rec.SeriesSelection{Revision: p.Revision, IDs: p.NewIDs})
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.SeriesRun(ctx, "run", true)
	if e != nil {
		t.Fatal(e)
	}
	newID := r.Pool.Works[2].ID
	existing, _ := loadSeries(ctx, s.Read, p.Target)
	raw := rec.SeriesRawPlan{Kind: "join_series", Target: existing.ID, WorkIDs: append(slices.Clone(existing.WorkIDs), newID), NewIDs: []string{newID}, Tier: "strong", Order: rec.SeriesOrder{}}
	j, _ := s.Job(ctx, "run")
	if e = s.PublishSeries(ctx, *j, r, rec.SeriesResult{Algorithm: rec.SeriesAlgorithm, Proposals: []rec.SeriesRawPlan{raw}}, true); e != nil {
		t.Fatal(e)
	}
	page, _ := s.SeriesProposals(ctx, "run", "lib", "add_to_series", "pending", 1, 50)
	if len(page.Items) != 1 {
		t.Fatal(page)
	}
	join := page.Items[0]
	if !slices.Equal(join.Order[:len(existing.WorkIDs)], existing.WorkIDs) {
		t.Fatal(join.Order)
	}
	join, e = s.DecideSeries(ctx, "account", join.ID, "accept", rec.SeriesSelection{Revision: join.Revision, IDs: join.NewIDs})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DecideSeries(ctx, "account", join.ID, "undo", rec.SeriesSelection{Revision: join.Revision}); e != nil {
		t.Fatal(e)
	}
	current, _ := loadSeries(ctx, s.Read, existing.ID)
	if !slices.Equal(current.WorkIDs, existing.WorkIDs) {
		t.Fatal(current)
	}
	// A user rename is a later edit to the same aggregate and must block old Undo.
	c, e := s.WorkContext(ctx, "account", nil, nil, []string{p.Target})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeWork(ctx, "account", model.WorkCommand{Action: "series_rename", ID: p.Target, Name: "User changed", Expected: c.Expected}, false, organization.PlanWork); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DecideSeries(ctx, "account", p.ID, "undo", rec.SeriesSelection{Revision: p.Revision}); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
}

func TestSeriesOldMetadataAndCrossSeriesAreRejected(t *testing.T) {
	s := discoveryStore(t, 3)
	ctx := context.Background()
	_, plans := finishSeries(t, s, "run", [][]int{{0, 1}})
	p := plans[0]
	var edition string
	s.Read.QueryRow("SELECT preferred_edition_id FROM works WHERE id=?", p.NewIDs[0]).Scan(&edition)
	if _, e := s.Write.Exec("UPDATE files SET filename_title='Changed title' WHERE edition_id=?", edition); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DecideSeries(ctx, "account", p.ID, "accept", rec.SeriesSelection{Revision: p.Revision, IDs: p.NewIDs}); !errors.Is(e, rec.ErrStale) {
		t.Fatal(e)
	}
}

func TestSeriesTransactionFaultAndConcurrentCompetingAccept(t *testing.T) {
	s := discoveryStore(t, 3)
	ctx := context.Background()
	_, plans := finishSeries(t, s, "run", [][]int{{0, 1}})
	p := plans[0]
	if _, e := s.Write.Exec(`CREATE TRIGGER anonymous_series_failure BEFORE INSERT ON series BEGIN SELECT RAISE(ABORT,'anonymous disk fault'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DecideSeries(ctx, "account", p.ID, "accept", rec.SeriesSelection{Revision: p.Revision, IDs: p.NewIDs}); e == nil {
		t.Fatal("fault swallowed")
	}
	var count int
	s.Read.QueryRow("SELECT count(*) FROM series").Scan(&count)
	if count != 0 {
		t.Fatal("partial series")
	}
	s.Read.QueryRow("SELECT count(*) FROM series_proposal_decisions").Scan(&count)
	if count != 0 {
		t.Fatal("partial decision")
	}
	s.Write.Exec("DROP TRIGGER anonymous_series_failure")
	r, _ := s.SeriesRun(ctx, "run", false)
	other := ""
	for _, w := range r.Pool.Works {
		if !slices.Contains(p.NewIDs, w.ID) {
			other = w.ID
		}
	}
	alt, e := seriesPlan(rec.SeriesRawPlan{Kind: "create_series", WorkIDs: []string{p.NewIDs[0], other}, NewIDs: []string{p.NewIDs[0], other}, Name: "Alternative", Tier: "strong"}, r, false)
	if e != nil {
		t.Fatal(e)
	}
	tx, _ := s.Write.BeginTx(ctx, nil)
	if e = saveSeriesPlan(ctx, tx, alt, false); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, candidate := range []rec.SeriesProposal{p, alt} {
		wg.Add(1)
		go func(v rec.SeriesProposal) {
			defer wg.Done()
			_, e := s.DecideSeries(ctx, "account", v.ID, "accept", rec.SeriesSelection{Revision: v.Revision, IDs: v.NewIDs})
			errs <- e
		}(candidate)
	}
	wg.Wait()
	close(errs)
	success := 0
	for e := range errs {
		if e == nil {
			success++
		} else if !errors.Is(e, model.ErrConflict) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatal("competing accepted", success)
	}
}

func TestSeriesUnrelatedRefreshPreservesCustomDraftRevision(t *testing.T) {
	s := discoveryStore(t, 6)
	ctx := context.Background()
	_, plans := finishSeries(t, s, "run", [][]int{{0, 1, 2}, {3, 4}})
	p, other := plans[0], plans[1]
	if len(p.NewIDs) < 3 {
		p, other = other, p
	}
	name := "Draft name"
	draft, e := s.ResolveSeries(ctx, p.ID, rec.SeriesSelection{Revision: p.Revision, IDs: p.NewIDs[:2], Name: &name})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DecideSeries(ctx, "account", other.ID, "dismiss", rec.SeriesSelection{Revision: other.Revision}); e != nil {
		t.Fatal(e)
	}
	r, _ := s.SeriesRun(ctx, "run", true)
	j, _ := s.Job(ctx, "run")
	groupA := append([]string{}, p.NewIDs...)
	groupB := append([]string{}, other.NewIDs...)
	result := rec.SeriesResult{Algorithm: rec.SeriesAlgorithm, Proposals: []rec.SeriesRawPlan{{Kind: "create_series", WorkIDs: groupA, NewIDs: groupA, Name: p.Name, Tier: "strong"}, {Kind: "create_series", WorkIDs: groupB, NewIDs: groupB, Name: other.Name, Tier: "strong"}}}
	if e = s.PublishSeries(ctx, *j, r, result, true); e != nil {
		t.Fatal(e)
	}
	after, e := s.SeriesProposal(ctx, draft.ID)
	if e != nil || after.Revision != draft.Revision || after.Name != name || !after.Primary {
		t.Fatal(after, e)
	}
	page, e := s.SeriesProposals(ctx, "run", "lib", "", "pending", 1, 100)
	if e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	for _, v := range page.Items {
		for _, i := range v.Order {
			if seen[i] {
				t.Fatal("duplicate primary after custom resolve")
			}
			seen[i] = true
		}
	}
}

func TestSeriesFailureDiagnosticsAreSanitizedAndGenerationGuarded(t *testing.T) {
	s := discoveryStore(t, 2)
	ctx := context.Background()
	j, e := s.CreateSeriesDiscovery(ctx, "diagnostic-series", "lib", "")
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SaveSeriesDiagnostic(ctx, *j, "restore_checkpoint", "invalid_checkpoint"); e != nil {
		t.Fatal(e)
	}
	stats, e := s.SeriesEvidenceStats(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	details, ok := stats["diagnostics"].(map[string]string)
	if !ok || details["phase"] != "restore_checkpoint" || details["reason"] != "invalid_checkpoint" {
		t.Fatal(stats)
	}
	if e = s.SaveSeriesDiagnostic(ctx, *j, "/private/anonymous-source", "raw exception"); !errors.Is(e, model.ErrInput) {
		t.Fatal("unbounded diagnostic accepted", e)
	}
	if e = s.ControlJob(ctx, j.ID, "cancel", ""); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveSeriesDiagnostic(ctx, *j, "publish", "resource_budget"); e == nil {
		t.Fatal("stale worker overwrote diagnostic")
	}
	stats, e = s.SeriesEvidenceStats(ctx, j.ID)
	if e != nil || stats["diagnostics"].(map[string]string)["phase"] != "restore_checkpoint" {
		t.Fatal(stats, e)
	}
}
