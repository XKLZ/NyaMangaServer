package store

import (
	"context"
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"

	rec "nyamangaserver.local/server/internal/recommendation"
	"nyamangaserver.local/server/internal/testfixture"
	"nyamangaserver.local/server/migrations"
)

func TestSeriesIdentityUpgradePreservesLegacyKeysAndRollsBack(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := Open(ctx, dir, schemaUntil(t, 14))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("INSERT INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision) VALUES('lib','lib','anonymous','utf8','identity',1);INSERT INTO accounts VALUES(1,'account','anonymous',1,1)"); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"e0000", "e0001"} {
		if e = testfixture.SeedEdition(ctx, s.Write, id, "lib", id+".cbz", id); e != nil {
			t.Fatal(e)
		}
	}
	j, r, result := prepareScopedSeries(t, s, "legacy-job", "legacy-batch", [][]int{{0, 1}})
	p, e := seriesPlan(result.Proposals[0], r, true)
	if e != nil {
		t.Fatal(e)
	}
	p.ID = "import-series:" + rec.Fingerprint([]string{"legacy-batch", p.Key})
	p.State = "dismissed"
	if _, e = s.Write.Exec(`INSERT INTO series_proposals(id,job_id,library_id,proposal_key,kind,target_series_id,tier,primary_plan,revision,state,plan_json,created_at) VALUES(?,?,?,?,'create_series',?,'strong',1,1,'dismissed',?,1)`, p.ID, j.ID, "lib", p.Key, p.Target, storedSeries(p)); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("INSERT INTO import_proposals VALUES('legacy-batch','series',?)", p.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("INSERT INTO series_proposal_decisions VALUES(?,?,'account','dismissed','before','after',1)", p.Key, p.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishBackground(ctx, *j, ""); e != nil {
		t.Fatal(e)
	}
	closeScopedImport(t, s, "legacy-batch")
	legacyCols := "id,job_id,library_id,proposal_key,kind,target_series_id,tier,primary_plan,customized,revision,state,plan_json,created_at"
	before := dumpRows(t, s, "SELECT "+legacyCols+" FROM series_proposals ORDER BY id")
	decisions := dumpRows(t, s, "SELECT * FROM series_proposal_decisions ORDER BY proposal_key")
	editions := dumpRows(t, s, "SELECT * FROM editions ORDER BY id")
	s.Close()
	broken := schemaUntil(t, 15)
	raw, _ := migrations.Files.ReadFile("015_series_proposal_identity.sql")
	broken["015_series_proposal_identity.sql"] = &fstest.MapFile{Data: append(raw, []byte("\nINSERT INTO missing_table VALUES(1);")...)}
	if db, e := Open(ctx, dir, broken); e == nil {
		db.Close()
		t.Fatal("bad 015 committed")
	}
	s, e = Open(ctx, dir, schemaUntil(t, 14))
	if e != nil {
		t.Fatal(e)
	}
	var n int
	s.Read.QueryRow("SELECT count(*) FROM pragma_table_info('series_proposals') WHERE name='match_key'").Scan(&n)
	if n != 0 {
		t.Fatal("015 leaked column")
	}
	s.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, dumpRows(t, s, "SELECT "+legacyCols+" FROM series_proposals ORDER BY id")) || !reflect.DeepEqual(decisions, dumpRows(t, s, "SELECT * FROM series_proposal_decisions ORDER BY proposal_key")) || !reflect.DeepEqual(editions, dumpRows(t, s, "SELECT * FROM editions ORDER BY id")) {
		t.Fatal("migration rewrote old data")
	}
	restored, e := s.SeriesProposal(ctx, p.ID)
	if e != nil || restored.Key != p.Key || restored.StorageKey != p.Key || restored.Scope != "import:legacy-batch" || restored.State != "dismissed" {
		t.Fatal(restored, e)
	}
	var legacy bool
	if e = s.Read.QueryRow("SELECT legacy FROM series_match_feedback").Scan(&legacy); e != nil || !legacy {
		t.Fatal(legacy, e)
	}
	// The same semantic plan now publishes safely under standalone storage identity.
	next := finishScopedSeries(t, s, "standalone", "", [][]int{{0, 1}})
	if len(next) != 1 || next[0].State != "dismissed" || next[0].StorageKey == p.Key {
		t.Fatal(next)
	}
	s.Close()
	snapshots, _ := filepath.Glob(filepath.Join(dir, "upgrade-snapshots", "*.sqlite"))
	if len(snapshots) == 0 {
		t.Fatal("missing consistent snapshot")
	}
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.Version != 29 {
		t.Fatal(s.Version)
	}
	after, _ := filepath.Glob(filepath.Join(dir, "upgrade-snapshots", "*.sqlite"))
	if len(after) != len(snapshots) {
		t.Fatal("repeat startup migrated again")
	}
	if _, e = fs.Stat(migrations.Files, "015_series_proposal_identity.sql"); e != nil {
		t.Fatal(e)
	}
}
