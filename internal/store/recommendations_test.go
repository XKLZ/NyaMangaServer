package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	rec "nyamangaserver.local/server/internal/recommendation"
	"nyamangaserver.local/server/migrations"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func schemaSeven(t *testing.T) fstest.MapFS {
	t.Helper()
	out := schemaSix(t)
	raw, e := migrations.Files.ReadFile("007_work_edition_file.sql")
	if e != nil {
		t.Fatal(e)
	}
	out["007_work_edition_file.sql"] = &fstest.MapFile{Data: raw}
	return out
}
func TestRecommendationUpgradePreservesEveryTaskReference(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	old, e := Open(ctx, dir, schemaSeven(t))
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, old)
	_, e = old.Write.Exec(`INSERT INTO jobs(id,type,state,stage,generation,total,created_at,updated_at) VALUES('old','metadata','paused','processing',4,1,1,2),('child','metadata','completed','finished',2,1,2,3);
 UPDATE jobs SET parent_job_id='old' WHERE id='child';
 INSERT INTO job_items(job_id,ordinal,state,edition_id,content_revision,input_json,attempt,preview_id) VALUES('old',1,'pending','book',1,'{"force_refresh":true}',2,'preview');
 INSERT INTO job_resources VALUES('metadata:book:book','old',4);
 INSERT INTO scan_entries VALUES('old',1,'anonymous.cbz',5,6);
 INSERT INTO maintenance_plans VALUES('plan','trash','{}','{}',123,'old',1);
 INSERT INTO maintenance_blocks VALUES('lib','old');
 INSERT INTO source_previews VALUES('preview','book','old',NULL,1,1,1,123,'pending');`)
	if e != nil {
		t.Fatal(e)
	}
	old.Close()
	s, e := Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.Version != 29 {
		t.Fatal(s.Version)
	}
	j, e := s.Job(ctx, "old")
	if e != nil || j.Generation != 4 || j.State != "paused" || j.Mode != "edition" {
		t.Fatal(j, e)
	}
	items, e := s.Items(ctx, "old", 1, 100, false)
	if e != nil || len(items) != 1 || items[0].Attempt != 2 || items[0].PreviewID != "preview" {
		t.Fatal(items, e)
	}
	for _, table := range []string{"job_resources", "scan_entries", "maintenance_plans", "maintenance_blocks", "source_previews"} {
		var count int
		if e = s.Read.QueryRow("SELECT count(*) FROM " + table).Scan(&count); e != nil || count != 1 {
			t.Fatal(table, count, e)
		}
	}
	rows, e := s.Read.Query("PRAGMA foreign_key_check")
	if e != nil {
		t.Fatal(e)
	}
	if rows.Next() {
		t.Fatal("foreign key corruption")
	}
	rows.Close()
	snapshots, _ := filepath.Glob(filepath.Join(dir, "upgrade-snapshots", "*.sqlite"))
	if len(snapshots) != 1 {
		t.Fatal(snapshots)
	}
	var tables int
	s.Read.QueryRow("SELECT count(*) FROM sqlite_master WHERE name LIKE 'v7_%'").Scan(&tables)
	if tables != 0 {
		t.Fatal(tables)
	}
	s.Close()
	again, e := Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	again.Close()
	after, _ := filepath.Glob(filepath.Join(dir, "upgrade-snapshots", "*.sqlite"))
	if len(after) != 1 {
		t.Fatal("duplicate-start snapshot", after)
	}
}
func TestRecommendationUpgradeRollbackIncludesForeignKeyValidation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	old, e := Open(ctx, dir, schemaSeven(t))
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, old)
	old.Close()
	files := schemaSeven(t)
	raw, _ := migrations.Files.ReadFile("008_work_recommendations.sql")
	files["008_work_recommendations.sql"] = &fstest.MapFile{Data: append(raw, []byte("\nINSERT INTO recommendation_materials VALUES('missing','missing','treec-validation','hash','old','input');")...)}
	if s, e := Open(ctx, dir, files); e == nil {
		s.Close()
		t.Fatal("invalid FK migration accepted")
	}
	db, e := sql.Open("sqlite", filepath.Join(dir, "app.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	var version, count int
	db.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&version)
	db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='recommendation_batches'").Scan(&count)
	db.Close()
	if version != 7 || count != 0 {
		t.Fatal(version, count)
	}
	s, e := Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
}
func preparedRecommendation(t *testing.T) (*Store, model.Job, model.Item, rec.Record) {
	t.Helper()
	ctx := context.Background()
	s, _ := orgFixture(t)
	j, e := s.CreateRecommendationJob(ctx, "recommend", "", []string{"book"})
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	items, e := s.Items(ctx, j.ID, 1, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	result := rec.Result{Algorithm: rec.Algorithm, CandidateCount: 1, MaxResults: 20, Items: []rec.Candidate{{WorkID: "i:b1", QueryID: "book", Tier: "strong", SupportingID: "b1", PreferredID: "b1", Algorithm: rec.Algorithm}}}
	if e = s.PublishRecommendations(ctx, *j, items[0], result); e != nil {
		t.Fatal(e)
	}
	page, e := s.RecommendationPage(ctx, j.ID, "", "", 1, 20)
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	return s, *j, items[0], page.Items[0]
}
func TestRecommendationAcceptAtomicIdempotentAndBoundToTarget(t *testing.T) {
	ctx := context.Background()
	s, _, _, row := preparedRecommendation(t)
	c := model.WorkCommand{Action: "edition_move", EditionID: "book", TargetWorkID: "i:b1"}
	p, e := s.RecommendationPreview(ctx, "a", row.ID, c, organization.PlanWork)
	if e != nil {
		t.Fatal(e)
	}
	bad := rec.AcceptInput{Revision: p.Revision, Command: p.Organization.Command}
	bad.Command.TargetWorkID = "i:b2"
	if _, e = s.AcceptRecommendation(ctx, "a", row.ID, bad, organization.PlanWork); !errors.Is(e, model.ErrInput) {
		t.Fatal(e)
	}
	in := rec.AcceptInput{Revision: p.Revision, Command: p.Organization.Command, Note: "anonymous"}
	// A database error after organization writes must roll back every relationship.
	_, e = s.Write.Exec("CREATE TRIGGER reject_feedback BEFORE INSERT ON recommendation_feedback BEGIN SELECT RAISE(ABORT,'anonymous failure');END")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcceptRecommendation(ctx, "a", row.ID, in, organization.PlanWork); e == nil {
		t.Fatal("feedback failure committed organization")
	}
	ed, _ := s.Edition(ctx, "book")
	if ed.WorkID != "i:book" {
		t.Fatal(ed.WorkID)
	}
	s.Write.Exec("DROP TRIGGER reject_feedback")
	first, e := s.AcceptRecommendation(ctx, "a", row.ID, in, organization.PlanWork)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.AcceptRecommendation(ctx, "a", row.ID, in, organization.PlanWork)
	if e != nil || rec.Fingerprint(first) != rec.Fingerprint(again) {
		t.Fatal(again, e)
	}
	var n int
	s.Read.QueryRow("SELECT count(*) FROM works WHERE id='i:book'").Scan(&n)
	if n != 0 {
		t.Fatal("empty source Work retained")
	}
	page, _ := s.RecommendationPage(ctx, "recommend", "", "", 1, 20)
	if !page.Items[0].Accepted || page.Items[0].Feedback.Relation != "same_work" {
		t.Fatal(page)
	}
}
func TestRecommendationInputCASIgnoresFavoriteButRejectsMetadata(t *testing.T) {
	ctx := context.Background()
	s, _, _, row := preparedRecommendation(t)
	c := model.WorkCommand{Action: "edition_move", EditionID: "book", TargetWorkID: "i:b1"}
	_, e := s.Write.Exec("INSERT INTO edition_user_states(account_id,edition_id,favorite,hidden,revision) VALUES('a','book',1,0,1)")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.RecommendationPreview(ctx, "a", row.ID, c, organization.PlanWork); e != nil {
		t.Fatal("favorite invalidated candidate", e)
	}
	_, e = s.Write.Exec("UPDATE editions SET revision=revision+1 WHERE id='b1'")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.RecommendationPreview(ctx, "a", row.ID, c, organization.PlanWork); !errors.Is(e, rec.ErrStale) {
		t.Fatal(e)
	}
}
func TestRecommendationStaleGenerationAndCheckpoints(t *testing.T) {
	ctx := context.Background()
	s, _ := orgFixture(t)
	j, e := s.CreateRecommendationJob(ctx, "rec", "", []string{"book"})
	if e != nil {
		t.Fatal(e)
	}
	old, _ := s.Claim(ctx, j.ID)
	items, _ := s.Items(ctx, j.ID, 1, 1, true)
	if e = s.ControlJob(ctx, j.ID, "pause", ""); e != nil {
		t.Fatal(e)
	}
	if e = s.ControlJob(ctx, j.ID, "resume", ""); e != nil {
		t.Fatal(e)
	}
	next, _ := s.Claim(ctx, j.ID)
	if e = s.PublishRecommendations(ctx, *old, items[0], rec.Result{Algorithm: rec.Algorithm}); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal(e)
	}
	if e = s.FinishBackground(ctx, *old, ""); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal(e)
	}
	var generation int64
	s.Read.QueryRow("SELECT generation FROM job_resources WHERE job_id=?", next.ID).Scan(&generation)
	if generation != next.Generation {
		t.Fatal(generation, next)
	}
	if e = s.PublishRecommendations(ctx, *next, items[0], rec.Result{Algorithm: rec.Algorithm, Items: []rec.Candidate{}, MaxResults: 20}); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishBackground(ctx, *next, ""); e != nil {
		t.Fatal(e)
	}
	page, e := s.RecommendationPage(ctx, j.ID, "", "", 1, 20)
	if e != nil || page.Total != 0 || page.QuerySummaries["book"] == nil {
		t.Fatal(page, e)
	}
}
func TestRecommendationMaintenanceAndPermanentPurgeDoNotLeakReferences(t *testing.T) {
	ctx := context.Background()
	s, j, _, row := preparedRecommendation(t)
	// A target-only maintenance operation invalidates jobs whose frozen pool includes it.
	if _, e := s.CreateRecommendationJob(ctx, "second", "", []string{"b2"}); e != nil {
		t.Fatal(e)
	}
	tx, _ := s.Write.BeginTx(ctx, nil)
	if e := pauseAffected(ctx, tx, []string{"b1"}, ""); e != nil {
		t.Fatal(e)
	}
	if e := tx.Commit(); e != nil {
		t.Fatal(e)
	}
	paused, _ := s.Job(ctx, "second")
	if paused.State != "paused" {
		t.Fatal(paused)
	}
	// Freeze history references without preventing target Work/Edition cleanup.
	if e := s.RecommendationMiss(ctx, "a", j.ID, rec.MissInput{QueryID: "book", TargetID: "i:b2", Note: "miss"}); e != nil {
		t.Fatal(e)
	}
	tx, _ = s.Write.BeginTx(ctx, nil)
	tx.ExecContext(ctx, "UPDATE editions SET trashed_at=1 WHERE id='b1'")
	if e := purgeBook(ctx, tx, "b1"); e != nil {
		t.Fatal(e)
	}
	if e := tx.Commit(); e != nil {
		t.Fatal(e)
	}
	_, e := s.RecommendationPreview(ctx, "a", row.ID, model.WorkCommand{Action: "edition_move", EditionID: "book", TargetWorkID: "i:b1"}, organization.PlanWork)
	if !errors.Is(e, rec.ErrStale) {
		t.Fatal(e)
	}
	tx, _ = s.Write.BeginTx(ctx, nil)
	tx.ExecContext(ctx, "UPDATE editions SET trashed_at=1 WHERE id='b2'")
	if e = purgeBook(ctx, tx, "b2"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var raw string
	s.Read.QueryRow("SELECT input_snapshot_json FROM recommendation_misses").Scan(&raw)
	if !strings.Contains(raw, "i:b2") {
		t.Fatal(raw)
	}
	var snapshot map[string]any
	if json.Unmarshal([]byte(raw), &snapshot) != nil {
		t.Fatal(raw)
	}
}

func TestRecommendationFeedbackRejectsChangedInputs(t *testing.T) {
	s, _, _, row := preparedRecommendation(t)
	ctx := context.Background()
	s.Write.Exec("UPDATE editions SET revision=revision+1 WHERE id='book'")
	if e := s.RecommendationFeedback(ctx, "a", row.ID, rec.Feedback{Relation: "same_work", Expected: 1}); !errors.Is(e, rec.ErrStale) {
		t.Fatal(e)
	}
	var count int
	s.Read.QueryRow("SELECT count(*) FROM recommendation_feedback").Scan(&count)
	if count != 0 {
		t.Fatal(count)
	}
}
func TestRecommendationMissRequiresFrozenCandidatePool(t *testing.T) {
	s, j, _, _ := preparedRecommendation(t)
	ctx := context.Background()
	seedEdition(t, s, "new", "lib", "new.cbz", "New")
	if e := s.RecommendationMiss(ctx, "a", j.ID, rec.MissInput{QueryID: "book", TargetID: "i:new"}); !errors.Is(e, rec.ErrStale) {
		t.Fatal(e)
	}
	if e := s.RecommendationMiss(ctx, "a", j.ID, rec.MissInput{QueryID: "book", TargetID: "i:b2"}); e != nil {
		t.Fatal(e)
	}
	s.Write.Exec("UPDATE editions SET revision=revision+1 WHERE id='book'")
	if e := s.RecommendationMiss(ctx, "a", j.ID, rec.MissInput{QueryID: "book", TargetID: "i:b10"}); !errors.Is(e, rec.ErrStale) {
		t.Fatal(e)
	}
	misses, e := s.RecommendationMisses(ctx, j.ID, 1, 20)
	if e != nil || misses.Total != 1 || len(misses.Items) != 1 {
		t.Fatal(misses, e)
	}
	page, e := s.RecommendationPage(ctx, j.ID, "", "", 1, 20)
	if e != nil || page.Summary.Misses != 1 || page.Summary.Layers[0].Unreviewed != 1 || page.Summary.Layers[0].ObservedPrecision != nil {
		t.Fatal(page, e)
	}
}
