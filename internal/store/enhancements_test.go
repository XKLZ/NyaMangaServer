package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
)

func schemaEighteen(t *testing.T) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{}
	names, _ := fs.ReadDir(migrations.Files, ".")
	for _, n := range names {
		if n.Name() >= "019" {
			continue
		}
		b, e := migrations.Files.ReadFile(n.Name())
		if e != nil {
			t.Fatal(e)
		}
		files[n.Name()] = &fstest.MapFile{Data: b}
	}
	return files
}
func TestEnhancementDefaultsAndUpgradeIdentity(t *testing.T) {
	ctx := context.Background()
	fresh, e := Open(ctx, t.TempDir(), migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer fresh.Close()
	state, e := fresh.Enhancements(ctx)
	if e != nil || state.Features[model.EnhancementWork].Enabled || state.Features[model.EnhancementSeries].Enabled || !state.Features[model.EnhancementDictionary].Enabled {
		t.Fatal(state, e)
	}
	dir := t.TempDir()
	old, e := Open(ctx, dir, schemaEighteen(t))
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, old)
	if e = old.Initialize(ctx, Account{ID: "anon", PasswordHash: "anonymous", Revision: 1}, Session{Hash: make([]byte, 32), AccountID: "anon", Created: 1, Expires: 2}); e != nil {
		t.Fatal(e)
	}
	if _, e = old.CreateMetadataJob(ctx, "paused-task", "", []string{"book"}, nil); e != nil {
		t.Fatal(e)
	}
	old.ControlJob(ctx, "paused-task", "pause", "user_paused")
	setTagNames(t, old, "book", "parody", "anonymous", "character", "hero")
	book, _ := old.Edition(ctx, "book")
	if e = old.ApplyOverrides(ctx, "book", book.Revision, map[string]model.Override{"title_original": {Op: "set", Value: json.RawMessage(`"匿名保留原题"`)}}); e != nil {
		t.Fatal(e)
	}
	translated := "人工译名"
	if e = old.TranslateTag(ctx, tagID("parody", "anonymous"), "zh-CN", 0, &translated); e != nil {
		t.Fatal(e)
	}
	if _, e = old.SetTagImage(ctx, tagID("parody", "anonymous"), "foreground", 0, model.TagImage{Width: 2, Height: 3, Format: "png", Asset: &model.Asset{ID: "retained-tag-asset", Key: "assets/tags/ab/anonymous.png", Digest: "anonymous-digest", Size: 20}}); e != nil {
		t.Fatal(e)
	}
	assetFile := filepath.Join(dir, "assets", "tags", "ab", "anonymous.png")
	os.MkdirAll(filepath.Dir(assetFile), 0700)
	assetBytes := []byte("anonymous retained asset")
	os.WriteFile(assetFile, assetBytes, 0600)
	tableRows, e := old.Read.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name!='schema_migrations' ORDER BY name")
	if e != nil {
		t.Fatal(e)
	}
	tableNames := []string{}
	for tableRows.Next() {
		var name string
		tableRows.Scan(&name)
		tableNames = append(tableNames, name)
	}
	tableRows.Close()
	allBefore := map[string][][]any{}
	for _, name := range tableNames {
		allBefore[name] = dumpRows(t, old, preservedTableQuery(name))
	}
	before := dumpRows(t, old, "SELECT * FROM accounts")
	tasks := dumpRows(t, old, "SELECT * FROM jobs")
	ledger := dumpRows(t, old, "SELECT * FROM schema_migrations ORDER BY version")
	old.Close()
	bad := schemaEighteen(t)
	raw, _ := migrations.Files.ReadFile("019_enhancements.sql")
	bad["019_enhancements.sql"] = &fstest.MapFile{Data: append(append([]byte{}, raw...), []byte("INSERT INTO missing VALUES(1);")...)}
	if db, e := Open(ctx, dir, bad); e == nil {
		db.Close()
		t.Fatal("failed migration committed")
	}
	for i := 0; i < 2; i++ {
		up, e := Open(ctx, dir, migrationsThrough(t, 24))
		if e != nil {
			t.Fatal(e)
		}
		state, e = up.Enhancements(ctx)
		if e != nil || !state.Features[model.EnhancementWork].Enabled || !state.Features[model.EnhancementSeries].Enabled {
			t.Fatal(state, e)
		}
		if !reflect.DeepEqual(before, dumpRows(t, up, "SELECT * FROM accounts")) || !reflect.DeepEqual(tasks, dumpRows(t, up, "SELECT * FROM jobs")) || !reflect.DeepEqual(ledger, dumpRows(t, up, "SELECT * FROM schema_migrations WHERE version<=18 ORDER BY version")) {
			t.Fatal("upgrade changed existing state")
		}
		for _, name := range tableNames {
			if !reflect.DeepEqual(allBefore[name], dumpRows(t, up, preservedTableQuery(name))) {
				t.Fatal("old table changed", name)
			}
		}
		retained, err := os.ReadFile(assetFile)
		if err != nil || string(retained) != string(assetBytes) {
			t.Fatal("asset file changed", err)
		}
		var n int
		up.Read.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&n)
		if n != 0 {
			t.Fatal(n)
		}
		up.Close()
	}
}
func enhancementBool(v bool) *bool { return &v }
func TestEnhancementImpactCASAndWorkerGenerations(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	seedBook(t, s)
	for _, pair := range [][2]string{{"work", "work_recommendation"}, {"series", "series_recommendation"}, {"meta", "metadata"}, {"paused", "work_recommendation"}} {
		state := "queued"
		if pair[0] == "paused" {
			state = "paused"
		}
		if _, e := s.Write.Exec("INSERT INTO jobs(id,type,state,stage,generation,created_at,updated_at) VALUES(?,?,?,'processing',1,1,1)", pair[0], pair[1], state); e != nil {
			t.Fatal(e)
		}
	}
	running, e := s.Claim(ctx, "work")
	if e != nil {
		t.Fatal(e)
	}
	state, _ := s.Enhancements(ctx)
	v := state.Features[model.EnhancementWork]
	patch := model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementWork, Enabled: enhancementBool(false)}
	if _, e = s.ChangeEnhancement(ctx, patch); !errors.Is(e, model.ErrEnhancementImpact) {
		t.Fatal(e)
	}
	same, _ := s.Job(ctx, "work")
	if same.State != "running" {
		t.Fatal("unconfirmed pause")
	}
	patch.ConfirmPause = true
	patch.ImpactToken = "stale"
	if _, e = s.ChangeEnhancement(ctx, patch); !errors.Is(e, model.ErrEnhancementImpact) {
		t.Fatal(e)
	}
	patch.ImpactToken = v.ImpactToken
	if _, e = s.Write.Exec("INSERT INTO jobs(id,type,state,stage,generation,created_at,updated_at) VALUES('late-work','work_recommendation','queued','processing',1,1,1)"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeEnhancement(ctx, patch); !errors.Is(e, model.ErrEnhancementImpact) {
		t.Fatal("late task was silently paused", e)
	}
	state, _ = s.Enhancements(ctx)
	patch.ImpactToken = state.Features[model.EnhancementWork].ImpactToken
	if _, e = s.Write.Exec("CREATE TRIGGER fail_enhancement_event BEFORE INSERT ON events WHEN NEW.type='enhancements' BEGIN SELECT RAISE(ABORT,'anonymous disk fault');END"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeEnhancement(ctx, patch); e == nil {
		t.Fatal("failed transaction accepted")
	}
	unchanged, _ := s.Enhancements(ctx)
	oldJob, _ := s.Job(ctx, "work")
	if !unchanged.Features[model.EnhancementWork].Enabled || oldJob.State != "running" || oldJob.Generation != running.Generation {
		t.Fatal("partial disable escaped rollback")
	}
	s.Write.Exec("DROP TRIGGER fail_enhancement_event")
	transition, e := s.ChangeEnhancement(ctx, patch)
	if e != nil || transition == nil {
		t.Fatal(transition, e)
	}
	paused, _ := s.Job(ctx, "work")
	if paused.State != "paused" || paused.Generation == running.Generation {
		t.Fatal(paused)
	}
	for _, id := range []string{"series", "meta", "paused"} {
		j, _ := s.Job(ctx, id)
		if id == "paused" {
			if j.Generation != 1 || j.State != "paused" {
				t.Fatal(j)
			}
		} else if j.State != "queued" {
			t.Fatal(j)
		}
	}
	if e = s.CheckExecution(ctx, *running); e == nil {
		t.Fatal("old worker can publish")
	}
	if e = s.ControlJob(ctx, "work", "resume", ""); !errors.Is(e, model.ErrEnhancementDisabled) {
		t.Fatal(e)
	}
	if _, e = s.CreateDiscoveryJob(ctx, "bypass", "lib"); !errors.Is(e, model.ErrEnhancementDisabled) {
		t.Fatal(e)
	}
	if e = s.FinishEnhancement(ctx, *transition, "drain_failed"); e != nil {
		t.Fatal(e)
	}
	state, _ = s.Enhancements(ctx)
	if state.Features[model.EnhancementWork].State != "failed" || state.Features[model.EnhancementWork].Enabled {
		t.Fatal(state)
	}
	retry, e := s.ChangeEnhancement(ctx, model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementWork, RetryTransitionID: transition.ID})
	if e != nil || retry.Attempt != 2 {
		t.Fatal(retry, e)
	}
	if e = s.FinishEnhancement(ctx, *transition, ""); !errors.Is(e, model.ErrEnhancementTransition) {
		t.Fatal("old completion cleared retry", e)
	}
	if e = s.FinishEnhancement(ctx, *retry, ""); e != nil {
		t.Fatal(e)
	}
	state, _ = s.Enhancements(ctx)
	if _, e = s.ChangeEnhancement(ctx, model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementWork, Enabled: enhancementBool(true)}); e != nil {
		t.Fatal(e)
	}
	j, _ := s.Job(ctx, "work")
	if j.State != "paused" {
		t.Fatal("enable resumed job", j)
	}
}
func TestEnhancementDictionaryMaskAndSnapshotABA(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	seedBook(t, s)
	setTagNames(t, s, "book", "artist", "remote", "artist", "manual")
	v := model.TranslationVersion{ID: "anonymous", Digest: "digest", CacheKey: "anonymous.json"}
	s.Write.Exec("INSERT INTO translation_versions VALUES(?,?,?,?,1)", v.ID, "anonymous", v.Digest, v.CacheKey)
	s.Write.Exec("UPDATE translation_state SET active_version=?", v.ID)
	for _, name := range []string{"remote", "manual"} {
		s.Write.Exec("INSERT INTO tag_translations VALUES(?,'zh-CN','remote',?,'远端译名',1)", tagID("artist", name), v.ID)
	}
	text := "人工译名"
	if e := s.TranslateTag(ctx, tagID("artist", "manual"), "zh-CN", 0, &text); e != nil {
		t.Fatal(e)
	}
	before, _ := s.MetadataDetails(ctx, "book")
	original, _ := s.Edition(ctx, "book")
	catIn := tagInput("lib")
	page, e := s.TagCatalog(ctx, catIn)
	if e != nil {
		t.Fatal(e)
	}
	state, _ := s.Enhancements(ctx)
	tr, e := s.ChangeEnhancement(ctx, model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementDictionary, Enabled: enhancementBool(false)})
	if e != nil {
		t.Fatal(e)
	}
	s.FinishEnhancement(ctx, *tr, "")
	tags, e := s.Tags(ctx, "远端译名", "", "zh-CN")
	if e != nil || len(tags) != 0 {
		t.Fatal("legacy remote leak", tags, e)
	}
	details, e := s.MetadataDetails(ctx, "book")
	if e != nil {
		t.Fatal(e)
	}
	for _, tag := range details.Tags {
		if tag.Name == "remote" && (tag.Origin != "original" || tag.Translation != "remote" || tag.DictionaryMatch != nil) {
			t.Fatal(tag)
		}
		if tag.Name == "manual" && tag.Translation != text {
			t.Fatal(tag)
		}
	}
	suggestions, e := s.TagSuggestions(ctx, model.TagSuggestionInput{Page: 1, PageSize: 50})
	if e != nil || suggestions.DictionaryState != "disabled" {
		t.Fatal(suggestions, e)
	}
	after, _ := s.Edition(ctx, "book")
	if original.Revision != after.Revision || !reflect.DeepEqual(before.Overrides, details.Overrides) {
		t.Fatal("dictionary toggle changed manual CAS")
	}
	catIn.Snapshot = page.Snapshot
	if _, e = s.TagCatalog(ctx, catIn); !errors.Is(e, model.ErrSnapshot) {
		t.Fatal(e)
	}
	state, _ = s.Enhancements(ctx)
	tr, e = s.ChangeEnhancement(ctx, model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementDictionary, Enabled: enhancementBool(true)})
	if e != nil {
		t.Fatal(e)
	}
	s.FinishEnhancement(ctx, *tr, "")
	if _, e = s.TagCatalog(ctx, catIn); !errors.Is(e, model.ErrSnapshot) {
		t.Fatal("off/on snapshot ABA", e)
	}
	raw, _ := json.Marshal(details)
	if len(raw) == 0 {
		t.Fatal("empty read")
	}
}

func TestEnhancementImportFrozenOptionalStagesAndMetadataGate(t *testing.T) {
	s := discoveryStore(t, 1)
	ctx := context.Background()
	s.Write.Exec("UPDATE enhancement_settings SET enabled=0 WHERE feature IN('work_recommendations','series_recommendations')")
	b, e := s.CreateImport(ctx, "off-batch", "lib", "request-off-batch")
	if e != nil {
		t.Fatal(e)
	}
	if b.EnhancementStages["versions"].Enabled || b.EnhancementStages["series"].State != "disabled_skipped" {
		t.Fatal(b)
	}
	s.Write.Exec("UPDATE import_batches SET stage='metadata',state='review' WHERE id=?", b.ID)
	s.Write.Exec("INSERT INTO import_members(batch_id,edition_id,change_kind,content_revision) VALUES(?,'e0000','new',1)", b.ID)
	b, _ = s.ImportBatch(ctx, b.ID)
	if e = s.AdvanceImport(ctx, b.ID, b.Revision); !errors.Is(e, model.ErrConflict) {
		t.Fatal("metadata gate bypass", e)
	}
	if e = s.ReviewImport(ctx, b.ID, []model.ImportReview{{ID: "e0000", ExpectedRevision: 1, Decision: "skip"}}); e != nil {
		t.Fatal(e)
	}
	// Opening later never adds a stage to this already-created batch.
	s.Write.Exec("UPDATE enhancement_settings SET enabled=1")
	b, _ = s.ImportBatch(ctx, b.ID)
	if e = s.AdvanceImport(ctx, b.ID, b.Revision); e != nil {
		t.Fatal(e)
	}
	b, _ = s.ImportBatch(ctx, b.ID)
	if b.State != "completed" {
		t.Fatal(b)
	}
	var count int
	s.Read.QueryRow("SELECT count(*) FROM import_child_requests WHERE batch_id=? AND stage IN('versions','series')", b.ID).Scan(&count)
	if count != 0 {
		t.Fatal(count)
	}
	// An originally required stage is paused, never turned into no suggestions.
	b, e = s.CreateImport(ctx, "required-batch", "lib", "request-required-batch")
	if e != nil {
		t.Fatal(e)
	}
	s.Write.Exec("UPDATE import_batches SET stage='versions',state='processing' WHERE id=?", b.ID)
	state, _ := s.Enhancements(ctx)
	tr, e := s.ChangeEnhancement(ctx, model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementWork, Enabled: enhancementBool(false)})
	if e != nil {
		t.Fatal(e)
	}
	s.FinishEnhancement(ctx, *tr, "")
	b, _ = s.ImportBatch(ctx, b.ID)
	if b.State != "paused" || b.Error != "enhancement_disabled" || b.EnhancementStages["versions"].State != "blocked" {
		t.Fatal(b)
	}
	if e = s.ControlImport(ctx, b.ID, "resume", b.Revision); !errors.Is(e, model.ErrEnhancementDisabled) {
		t.Fatal(e)
	}
	if e = s.ControlImport(ctx, b.ID, "skip_recommendation", b.Revision); e != nil {
		t.Fatal(e)
	}
	b, _ = s.ImportBatch(ctx, b.ID)
	if b.State != "review" || b.StageDecisions["versions"].Decision != "skipped_disabled" {
		t.Fatal(b)
	}
}

func TestEnhancementEnableDoesNotArmAutomaticRevalidation(t *testing.T) {
	s := discoveryStore(t, 2)
	ctx := context.Background()
	state, _ := s.Enhancements(ctx)
	tr, e := s.ChangeEnhancement(ctx, model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementWork, Enabled: enhancementBool(false)})
	if e != nil {
		t.Fatal(e)
	}
	s.FinishEnhancement(ctx, *tr, "")
	state, _ = s.Enhancements(ctx)
	if _, e = s.ChangeEnhancement(ctx, model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementWork, Enabled: enhancementBool(true)}); e != nil {
		t.Fatal(e)
	}
	if e = s.EnhancementBackgroundAllowed(ctx, model.EnhancementWork); !errors.Is(e, model.ErrEnhancementDisabled) {
		t.Fatal("enable armed background work", e)
	}
	var n int
	s.Read.QueryRow("SELECT count(*) FROM jobs WHERE type='work_recommendation'").Scan(&n)
	if n != 0 {
		t.Fatal("toggle created tasks")
	}
	if _, e = s.CreateDiscoveryJob(ctx, "explicit-discovery", "lib"); e != nil {
		t.Fatal(e)
	}
	if e = s.EnhancementBackgroundAllowed(ctx, model.EnhancementWork); e != nil {
		t.Fatal(e)
	}
}

func preservedTableQuery(name string) string {
	if name == "events" {
		return "SELECT id,type,resource_id,revision,created_at FROM events ORDER BY rowid"
	}
	return fmt.Sprintf("SELECT * FROM %q ORDER BY rowid", name)
}
