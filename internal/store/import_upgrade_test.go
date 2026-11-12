package store

import (
	"context"
	"io/fs"
	rec "nyamangaserver.local/server/internal/recommendation"
	"nyamangaserver.local/server/migrations"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

func schemaUntil(t *testing.T, n int) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	entries, _ := fs.ReadDir(migrations.Files, ".")
	for _, v := range entries {
		version, _ := strconv.Atoi(strings.Split(v.Name(), "_")[0])
		if version <= n && strings.HasSuffix(v.Name(), ".sql") {
			raw, _ := migrations.Files.ReadFile(v.Name())
			out[v.Name()] = &fstest.MapFile{Data: raw}
		}
	}
	return out
}
func TestImportUpgradePreservesElevenAndCheckpointFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := Open(ctx, dir, schemaUntil(t, 11))
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, s)
	before := dumpRows(t, s, "SELECT * FROM editions ORDER BY id")
	s.Close()
	broken := schemaUntil(t, 12)
	raw, _ := migrations.Files.ReadFile("012_import_workflow.sql")
	broken["012_import_workflow.sql"] = &fstest.MapFile{Data: append(raw, []byte("\nINSERT INTO missing_table VALUES(1);")...)}
	if s, e := Open(ctx, dir, broken); e == nil {
		s.Close()
		t.Fatal("broken 012 committed")
	}

	twelve, err := Open(ctx, dir, schemaUntil(t, 12))
	if err != nil {
		t.Fatal(err)
	}
	twelve.Close()
	bad13 := schemaUntil(t, 13)
	r13, _ := migrations.Files.ReadFile("013_recommendation_checkpoints.sql")
	bad13["013_recommendation_checkpoints.sql"] = &fstest.MapFile{Data: append(r13, []byte("\nINSERT INTO missing_table VALUES(1);")...)}
	if db, err := Open(ctx, dir, bad13); err == nil {
		db.Close()
		t.Fatal("broken 013 committed")
	}
	twelve, err = Open(ctx, dir, schemaUntil(t, 12))
	if err != nil {
		t.Fatal(err)
	}
	if twelve.Version != 12 {
		t.Fatal(twelve.Version)
	}
	twelve.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, dumpRows(t, s, "SELECT * FROM editions ORDER BY id")) {
		t.Fatal("upgrade changed Editions")
	}
	if s.Version != 28 {
		t.Fatal(s.Version)
	}
	var n int
	if e = s.Read.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	s.Close()
	files, e := filepath.Glob(filepath.Join(dir, "upgrade-snapshots", "*"))
	if e != nil || len(files) == 0 {
		t.Fatal("missing consistent upgrade snapshot", e)
	}
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
}
func TestSeriesEvidenceGenerationPinsBaselineAndRejectsStaleWrites(t *testing.T) {
	s := discoveryStore(t, 3)
	ctx := context.Background()
	j, e := s.CreateSeriesDiscovery(ctx, "first-series", "lib", "")
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.BeginSeriesEvidence(ctx, *j, map[string]string{"w": "profile"}); e != nil {
		t.Fatal(e)
	}
	block := rec.SeriesEvidenceBlock{Key: "block", StorageKey: "indexes/recommendations/series/ab/anonymous.json.gz", SHA256: "digest", Size: 100, Records: 1}
	if e = s.SaveSeriesEvidence(ctx, *j, block, 1, 0); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveSeriesEvidence(ctx, *j, block, 1, 0); e != nil {
		t.Fatal(e)
	}
	stats, _ := s.SeriesEvidenceStats(ctx, j.ID)
	if stats["compressed_bytes"] != int64(100) {
		t.Fatal(stats)
	}
	if e = s.CompleteSeriesEvidence(ctx, *j); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("UPDATE jobs SET state='completed' WHERE id=?", j.ID); e != nil {
		t.Fatal(e)
	}
	next, e := s.CreateSeriesDiscovery(ctx, "next-series", "lib", "")
	if e != nil {
		t.Fatal(e)
	}
	next, _ = s.Claim(ctx, next.ID)
	refs, e := s.BeginSeriesEvidence(ctx, *next, map[string]string{"w": "profile"})
	if e != nil || len(refs) != 1 {
		t.Fatal(refs, e)
	}
	if _, e = s.Write.Exec("DELETE FROM series_evidence_generations WHERE job_id=?", j.ID); e == nil {
		t.Fatal("baseline not pinned")
	}
	if e = s.ControlJob(ctx, next.ID, "cancel", ""); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveSeriesEvidence(ctx, *next, block, 0, 1); e == nil {
		t.Fatal("cancelled generation published")
	}
}

func TestImportStageDecisionUpgradeRollbackAndPersistence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := Open(ctx, dir, schemaUntil(t, 13))
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, s)
	if _, e = s.Write.Exec("INSERT INTO import_batches(id,library_id,request_key,state,stage) SELECT 'historical',id,'historical','completed','finished' FROM libraries LIMIT 1"); e != nil {
		t.Fatal(e)
	}
	editions := dumpRows(t, s, "SELECT * FROM editions ORDER BY id")
	batches := dumpRows(t, s, "SELECT "+importColumns+" FROM import_batches ORDER BY id")
	s.Close()
	broken := schemaUntil(t, 14)
	raw, _ := migrations.Files.ReadFile("014_import_stage_decisions.sql")
	broken["014_import_stage_decisions.sql"] = &fstest.MapFile{Data: append(raw, []byte("\nINSERT INTO missing_table VALUES(1);")...)}
	if db, e := Open(ctx, dir, broken); e == nil {
		db.Close()
		t.Fatal("broken 014 committed")
	}
	s, e = Open(ctx, dir, schemaUntil(t, 13))
	if e != nil {
		t.Fatal(e)
	}
	var n int
	if e = s.Read.QueryRow("SELECT count(*) FROM pragma_table_info('import_batches') WHERE name='stage_decisions_json'").Scan(&n); e != nil || n != 0 {
		t.Fatal("014 column escaped rollback", n, e)
	}
	s.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(editions, dumpRows(t, s, "SELECT * FROM editions ORDER BY id")) || !reflect.DeepEqual(batches, dumpRows(t, s, "SELECT "+importColumns+" FROM import_batches ORDER BY id")) {
		t.Fatal("014 rewrote historical data")
	}
	if _, e = s.Write.Exec(`UPDATE import_batches SET stage_decisions_json='{"series":{"decision":"skipped","job_id":"anonymous","created_at":1}}' WHERE id='historical'`); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	b, e := s.ImportBatch(ctx, "historical")
	if e != nil || b.State != "completed" || b.StageDecisions["series"].JobID != "anonymous" || s.Version != 28 {
		t.Fatal(b, e)
	}
	// A persisted decision without a matching current attempt cannot bypass a gate.
	if len(b.SkippedStages) != 0 {
		t.Fatal("unmatched historical skip became effective")
	}
	snapshots, e := filepath.Glob(filepath.Join(dir, "upgrade-snapshots", "*"))
	if e != nil || len(snapshots) == 0 {
		t.Fatal("missing private upgrade snapshot", e)
	}
}
