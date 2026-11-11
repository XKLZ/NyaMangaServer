package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func tagID(ns, name string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(ns+"\x00"+name))) }
func tagInput(lib string) model.TagCatalogInput {
	return model.TagCatalogInput{LibraryID: lib, Page: 1, PageSize: 50, Sort: "name", Order: "asc", Partition: "all"}
}
func tagEdition(t *testing.T, s *Store, id, lib string, tags ...string) {
	t.Helper()
	seedEdition(t, s, id, lib, id+".cbz", id)
	setTagNames(t, s, id, tags...)
}
func setTagNames(t *testing.T, s *Store, id string, tags ...string) {
	t.Helper()
	names := []model.TagName{}
	for i := 0; i < len(tags); i += 2 {
		names = append(names, model.TagName{Namespace: tags[i], Name: tags[i+1]})
	}
	b, _ := json.Marshal(names)
	edition, e := s.Edition(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ApplyOverrides(context.Background(), id, edition.Revision, map[string]model.Override{"tags": {Op: "set", Value: b}}); e != nil {
		t.Fatal(e)
	}
}
func tagLibraries(t *testing.T, s *Store) {
	t.Helper()
	for _, id := range []string{"a", "b"} {
		if _, e := s.Write.Exec(`INSERT INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision) VALUES(?,?,?,'utf8','anonymous',1)`, id, id, id); e != nil {
			t.Fatal(e)
		}
	}
}
func relations(t *testing.T, s *Store, lib, p string) map[string]model.CharacterRelation {
	t.Helper()
	v, e := s.TagCatalogRelations(context.Background(), tagID("parody", p), tagInput(lib))
	if e != nil {
		t.Fatal(e)
	}
	out := map[string]model.CharacterRelation{}
	for _, r := range v.Items {
		out[r.Character.Name] = r
	}
	return out
}
func TestTagCatalogCountsTranslationsAndSnapshots(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	tagLibraries(t, s)
	tagEdition(t, s, "one", "a", "artist", "author", "language", "chinese", "custom", "unmapped", "parody", "alpha", "character", "hero")
	tagEdition(t, s, "two", "a", "artist", "author", "parody", "alpha", "character", "hero")
	// Two Editions in one real Work must count once without relation fan-out.
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(`UPDATE editions SET work_id='i:one' WHERE id='two';DELETE FROM works WHERE id='i:two';UPDATE files SET file_state='missing' WHERE edition_id='two'`); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = s.Initialize(ctx, Account{ID: "catalog-account", PasswordHash: "anonymous", Revision: 1}, Session{Hash: make([]byte, 32), AccountID: "catalog-account", Created: 1, Expires: 2}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("INSERT INTO edition_user_states VALUES('catalog-account','one',0,1,1);UPDATE files SET file_state='corrupt' WHERE edition_id='one'"); e != nil {
		t.Fatal(e)
	}
	tagEdition(t, s, "other", "b", "artist", "author")
	in := tagInput("a")
	v, e := s.TagCatalog(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	if v.Total != 5 {
		t.Fatal(v)
	}
	for _, tally := range v.Items {
		if tally.Namespace == "artist" && (tally.EditionCount != 2 || tally.WorkCount != 1) {
			t.Fatal(tally)
		}
	}
	if e = s.TranslateTag(ctx, tagID("artist", "author"), "zh-CN", 0, strptr("作者译名")); e != nil {
		t.Fatal(e)
	}
	in.Query = "作者"
	v, e = s.TagCatalog(ctx, in)
	if e != nil || v.Total != 1 || v.Items[0].TranslationOrigin != "manual" {
		t.Fatal(v, e)
	}
	in = tagInput("a")
	in.Category = "content"
	v, e = s.TagCatalog(ctx, in)
	if e != nil || v.Total != 2 {
		t.Fatal(v, e)
	}
	in = tagInput("a")
	in.PageSize = 1
	first, e := s.TagCatalog(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	in.Snapshot = first.Snapshot
	in.Page = 2
	if _, e = s.TagCatalog(ctx, in); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DecideCharacterRelation(ctx, tagID("parody", "alpha"), tagID("character", "hero"), 0, "excluded"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.TagCatalog(ctx, in); !errors.Is(e, model.ErrSnapshot) {
		t.Fatal("relation did not invalidate snapshot", e)
	}
	if _, e = s.Write.Exec("UPDATE libraries SET disabled=1 WHERE id='a'"); e != nil {
		t.Fatal(e)
	}
	in = tagInput("a")
	v, e = s.TagCatalog(ctx, in)
	if e != nil || v.Total != 5 {
		t.Fatal("disabled library unreadable", e)
	}
	if _, e = s.Write.Exec("UPDATE libraries SET trashed_at=1 WHERE id='a'"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.TagCatalog(ctx, in); !errors.Is(e, model.ErrNotFound) {
		t.Fatal("trashed library readable", e)
	}
}
func strptr(s string) *string { return &s }
func TestTagCatalogDeterministicInferenceIsolationAndNoPropagation(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	tagLibraries(t, s)
	tagEdition(t, s, "seed", "a", "parody", "alpha", "character", "hero")
	tagEdition(t, s, "multi", "a", "parody", "alpha", "parody", "beta", "character", "hero", "character", "new")
	tagEdition(t, s, "no-chain", "a", "parody", "beta", "parody", "gamma", "character", "new", "character", "third")
	// An otherwise reliable same-name role from a different library is isolated.
	tagEdition(t, s, "foreign", "b", "parody", "beta", "character", "hero")
	a := relations(t, s, "a", "alpha")
	if a["hero"].EditionCount != 2 || a["hero"].SeedEditionCount != 1 {
		t.Fatal(a)
	}
	b := relations(t, s, "a", "beta")
	if b["new"].EditionCount != 1 || b["new"].InferredEditionCount != 1 || b["new"].SeedEditionCount != 0 || b["new"].State != "inferred" {
		t.Fatal(b)
	}
	c := relations(t, s, "a", "gamma")
	if len(c) != 0 {
		t.Fatal("inference propagated", c)
	}
	in := tagInput("a")
	in.RelatedTagID = tagID("character", "new")
	in.Partition = "inferred"
	ed, e := s.TagCatalogEditions(ctx, tagID("parody", "beta"), in)
	if e != nil || ed.Total != 1 || ed.Items[0].ID != "multi" {
		t.Fatal(ed, e)
	}
	found := false
	for _, r := range ed.Items[0].Relations {
		if r.CharacterID == tagID("character", "new") && r.ParodyID == tagID("parody", "beta") && r.State == "inferred" {
			found = true
		}
	}
	if !found {
		t.Fatal("suppression target absent", ed)
	}
	// A local denial blocks only this Edition and preserves the global inferred pair.
	denial, e := s.SetCharacterSuppression(ctx, "multi", tagID("parody", "beta"), tagID("character", "new"), 0, true)
	if e != nil || denial.Revision != 1 {
		t.Fatal(denial, e)
	}
	in.Partition = "suppressed"
	ed, e = s.TagCatalogEditions(ctx, tagID("parody", "beta"), in)
	if e != nil || ed.Total != 1 {
		t.Fatal(ed, e)
	}
	b = relations(t, s, "a", "beta")
	if b["new"].EditionCount != 0 || b["new"].SuppressedEditionCount != 1 {
		t.Fatal(b)
	}
	if _, e = s.SetCharacterSuppression(ctx, "multi", tagID("parody", "beta"), tagID("character", "new"), 1, false); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetCharacterSuppression(ctx, "multi", tagID("parody", "beta"), tagID("character", "new"), 0, true); !errors.Is(e, model.ErrConflict) {
		t.Fatal("suppression ABA accepted", e)
	}
	if _, e = s.DecideCharacterRelation(ctx, tagID("parody", "beta"), tagID("character", "new"), 0, "excluded"); e != nil {
		t.Fatal(e)
	}
	if r := relations(t, s, "a", "beta")["new"]; r.State != "excluded" || r.EditionCount != 0 || r.InferredEditionCount != 0 {
		t.Fatal("excluded residual assigned", r)
	}
	if _, e = s.DecideCharacterRelation(ctx, tagID("parody", "beta"), tagID("character", "new"), 1, "auto"); e != nil {
		t.Fatal(e)
	}
	// Add a competing seed in this library; the multi book cannot pick by ID.
	tagEdition(t, s, "competitor", "a", "parody", "beta", "character", "hero")
	in = tagInput("a")
	in.RelatedTagID = tagID("character", "hero")
	in.Partition = "competing"
	ed, e = s.TagCatalogEditions(ctx, tagID("parody", "alpha"), in)
	if e != nil || ed.Total != 1 || ed.Items[0].ID != "multi" {
		t.Fatal(ed, e)
	}
	in = tagInput("a")
	in.Partition = "unclassified"
	ed, e = s.TagCatalogEditions(ctx, tagID("parody", "alpha"), in)
	if e != nil || ed.Total != 1 {
		t.Fatal("unclassified vanished", ed, e)
	}
	// Removing the competing seed via trash makes it stop contributing immediately.
	if _, e = s.Write.Exec("UPDATE editions SET trashed_at=1 WHERE id='competitor'"); e != nil {
		t.Fatal(e)
	}
	if relations(t, s, "a", "beta")["new"].InferredEditionCount != 1 {
		t.Fatal("trash still contributes")
	}
	if _, e = s.Write.Exec("UPDATE editions SET trashed_at=NULL WHERE id='competitor'"); e != nil {
		t.Fatal(e)
	}
	if relations(t, s, "a", "beta")["new"].InferredEditionCount != 0 {
		t.Fatal("restore did not update")
	}
}
func TestTagCatalogHumanDecisionsPlaceholdersAndCAS(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	tagLibraries(t, s)
	tagEdition(t, s, "one", "a", "parody", "original", "parody", "origin", "parody", "alpha", "character", "hero")
	tagEdition(t, s, "foreign", "b", "parody", "beta", "character", "other")
	p, c := tagID("parody", "alpha"), tagID("character", "other")
	if _, e := s.DecideCharacterRelation(ctx, p, c, 0, "confirmed"); e != nil {
		t.Fatal(e)
	}
	row := relations(t, s, "a", "alpha")["other"]
	if row.Decision != "confirmed" || row.EditionCount != 0 {
		t.Fatal(row)
	}
	// A manually managed pair can be found even when neither tag is used here.
	v, e := s.TagCatalog(ctx, func() model.TagCatalogInput { in := tagInput("b"); in.IncludeUnused = true; return in }())
	if e != nil || v.Total < 3 {
		t.Fatal(v, e)
	}
	if _, e = s.DecideCharacterRelation(ctx, p, c, 1, "auto"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DecideCharacterRelation(ctx, p, c, 0, "excluded"); !errors.Is(e, model.ErrConflict) {
		t.Fatal("relation ABA accepted", e)
	}
	if _, e = s.DecideCharacterRelation(ctx, tagID("parody", "original"), tagID("character", "hero"), 0, "confirmed"); !errors.Is(e, model.ErrInput) {
		t.Fatal("placeholder accepted", e)
	}
	a := relations(t, s, "a", "alpha")
	if a["hero"].EvidenceEditionCount != 1 {
		t.Fatal("placeholder counted as second parody", a)
	}
	if _, e = s.DecideCharacterRelation(ctx, p, tagID("character", "hero"), 0, "excluded"); e != nil {
		t.Fatal(e)
	}
	if a = relations(t, s, "a", "alpha"); a["hero"].EditionCount != 0 || a["hero"].EvidenceEditionCount != 1 {
		t.Fatal("exclusion lost raw evidence or assigned", a)
	}
	// Neither manual confirmation nor exclusion changes tags or organization.
	edition, _ := s.Edition(ctx, "one")
	if edition.Revision != 2 || edition.OrganizationRevision != 1 {
		t.Fatal(edition)
	}
	in := tagInput("a")
	in.RelatedTagID = tagID("character", "hero")
	in.Partition = "evidence"
	evidence, e := s.TagCatalogEditions(ctx, p, in)
	if e != nil || evidence.Total != 1 {
		t.Fatal(evidence, e)
	}
}

func TestTagCatalogSourceRefreshRollbackRecycleAndPurge(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	tagLibraries(t, s)
	seedEdition(t, s, "one", "a", "one.cbz", "Anonymous")
	seedEdition(t, s, "two", "a", "two.cbz", "Anonymous")
	publish := func(id, parody string) error {
		tx, e := s.Write.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		e = publishSource(ctx, tx, id, model.RemoteMetadata{ExternalID: "anonymous-shared", URL: "https://e-hentai.org/g/1/0123456789/", Method: "manual", Raw: json.RawMessage(`{}`), Fields: model.Metadata{Tags: []model.TagName{{Namespace: "parody", Name: parody}, {Namespace: "character", Name: "hero"}}}})
		if e != nil {
			return e
		}
		return tx.Commit()
	}
	if e := publish("one", "alpha"); e != nil {
		t.Fatal(e)
	}
	if e := publish("two", "alpha"); e != nil {
		t.Fatal(e)
	}
	if e := publish("one", "beta"); e != nil {
		t.Fatal(e)
	}
	if len(relations(t, s, "a", "alpha")) != 0 || relations(t, s, "a", "beta")["hero"].SeedEditionCount != 2 {
		t.Fatal("shared source evidence stale")
	}
	if _, e := s.Write.Exec(`CREATE TRIGGER evidence_failure BEFORE INSERT ON tag_character_evidence BEGIN SELECT RAISE(ABORT,'anonymous evidence failure');END;`); e != nil {
		t.Fatal(e)
	}
	before, _ := s.Edition(ctx, "one")
	if e := s.ApplyOverrides(ctx, "one", before.Revision, map[string]model.Override{"tags": {Op: "set", Value: json.RawMessage(`[{"namespace":"parody","name":"alpha"},{"namespace":"character","name":"hero"}]`)}}); e == nil {
		t.Fatal("failed evidence update did not roll back metadata")
	}
	after, _ := s.Edition(ctx, "one")
	if after.Revision != before.Revision {
		t.Fatal("revision escaped rollback")
	}
	if _, e := s.Write.Exec("DROP TRIGGER evidence_failure"); e != nil {
		t.Fatal(e)
	}
	d, e := s.MetadataDetails(ctx, "one")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RemoveSource(ctx, "one", after.Revision, d.SelectionRevision, false); e != nil {
		t.Fatal(e)
	}
	if relations(t, s, "a", "beta")["hero"].SeedEditionCount != 1 {
		t.Fatal("unlinked source contributes")
	}
	p, c := tagID("parody", "beta"), tagID("character", "hero")
	if _, e = s.SetCharacterSuppression(ctx, "two", p, c, 0, true); e != nil {
		t.Fatal(e)
	}
	// Retained denial can be found and restored even after tags change.
	setTagNames(t, s, "two", "artist", "anonymous")
	in := tagInput("a")
	in.Partition = "suppressed"
	in.RelatedTagID = c
	denied, e := s.TagCatalogEditions(ctx, p, in)
	if e != nil || denied.Total != 1 || denied.Items[0].Relations[0].SuppressionRevision != 1 {
		t.Fatal(denied, e)
	}
	if _, e = s.SetCharacterSuppression(ctx, "two", p, c, 1, false); e != nil {
		t.Fatal("cannot restore latent denial", e)
	}
	setTagNames(t, s, "two", "parody", "beta", "character", "hero")
	for _, operation := range []string{"trash", "restore", "trash", "purge"} {
		b, _ := s.Edition(ctx, "two")
		plan, e := s.CreateMaintenancePlan(ctx, model.MaintenanceInput{Operation: operation, BookIDs: []string{"two"}, Expected: map[string]int64{"book:two": b.Revision}})
		if e != nil {
			t.Fatal(operation, e)
		}
		applyPlan(t, s, plan)
		want := 0
		if operation == "restore" {
			want = 1
		}
		if got := relations(t, s, "a", "beta")["hero"].SeedEditionCount; got != want {
			t.Fatal(operation, got)
		}
	}
	var n int
	if e = s.Read.QueryRow("SELECT count(*) FROM edition_character_suppressions WHERE edition_id='two'").Scan(&n); e != nil || n != 0 {
		t.Fatal("purge did not cascade suppression", n, e)
	}
}
func TestTagCatalogDictionaryAndKnownOutsideContradiction(t *testing.T) {
	s, _ := openTest(t)
	tagLibraries(t, s)
	ctx := context.Background()
	tagEdition(t, s, "seed", "a", "parody", "alpha", "character", "hero")
	tagEdition(t, s, "outsider", "a", "parody", "elsewhere", "character", "other")
	tagEdition(t, s, "multi", "a", "parody", "alpha", "parody", "beta", "character", "hero", "character", "other")
	in := tagInput("a")
	in.Partition = "unclassified"
	v, e := s.TagCatalogEditions(ctx, tagID("parody", "beta"), in)
	if e != nil || v.Total != 1 {
		t.Fatal(v, e)
	}
	found := false
	for _, r := range v.Items[0].Relations {
		if r.CharacterID == tagID("character", "other") && r.State == "known_elsewhere" {
			found = true
		}
	}
	if !found {
		t.Fatal("contradiction missing", v)
	}
	if _, e = s.Write.Exec("UPDATE libraries SET disabled=1 WHERE id='a'"); e != nil {
		t.Fatal(e)
	}
	if r := relations(t, s, "a", "alpha")["hero"]; r.SeedEditionCount != 0 || r.EditionCount != 1 {
		t.Fatal("disabled library seed contributes", r)
	}
	j, item := dictionaryJob(t, s, "catalog-dictionary")
	if e = s.PublishDictionary(ctx, j, item, model.TranslationVersion{ID: "catalog-v1", Digest: "anonymous", Source: "anonymous", CacheKey: "anonymous"}, map[model.TagName]string{{Namespace: "parody", Name: "alpha"}: "原作译文", {Namespace: "artist", Name: "dictionary-only"}: "零使用词条"}); e != nil {
		t.Fatal(e)
	}
	in = tagInput("a")
	in.Query = "原作译文"
	list, e := s.TagCatalog(ctx, in)
	if e != nil || list.Total != 1 || list.Items[0].TranslationOrigin != "remote" {
		t.Fatal(list, e)
	}
	in.Query = "零使用"
	list, e = s.TagCatalog(ctx, in)
	if e != nil || list.Total != 0 {
		t.Fatal("whole dictionary leaked into directory", list, e)
	}
}

func TestTagCatalogRelationConcurrentCASAndDisabledLocalWrites(t *testing.T) {
	s, _ := openTest(t)
	tagLibraries(t, s)
	ctx := context.Background()
	tagEdition(t, s, "one", "a", "parody", "alpha", "character", "hero")
	p, c := tagID("parody", "alpha"), tagID("character", "hero")
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, e := s.DecideCharacterRelation(ctx, p, c, 0, "confirmed"); results <- e }()
	}
	successes, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		e := <-results
		if e == nil {
			successes++
		} else if errors.Is(e, model.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal(successes, conflicts)
	}
	if _, e := s.Write.Exec("UPDATE libraries SET disabled=1 WHERE id='a'"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SetCharacterSuppression(ctx, "one", p, c, 0, true); !errors.Is(e, model.ErrDisabled) {
		t.Fatal("disabled library modified", e)
	}
	// A global relationship is shared data and has no implicit current-library scope.
	if _, e := s.DecideCharacterRelation(ctx, p, c, 1, "excluded"); e != nil {
		t.Fatal(e)
	}
}

func TestTagCatalogOverlappingPartitionsAndTranslationInvalidation(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	tagLibraries(t, s)
	tagEdition(t, s, "one", "a", "parody", "alpha", "character", "hero", "character", "second")
	p := tagID("parody", "alpha")
	in := tagInput("a")
	in.PageSize = 1
	first, e := s.TagCatalogRelations(ctx, p, in)
	if e != nil || first.Total != 2 {
		t.Fatal(first, e)
	}
	in.Page = 2
	in.Snapshot = first.Snapshot
	next, e := s.TagCatalogRelations(ctx, p, in)
	if e != nil || next.Items[0].Character.ID == first.Items[0].Character.ID {
		t.Fatal(next, e)
	}
	// Two overlapping character partitions each have one Edition; the anchor has one.
	if first.Items[0].EditionCount+next.Items[0].EditionCount != 2 {
		t.Fatal(first, next)
	}
	detail, e := s.TagCatalogDetail(ctx, p, tagInput("a"))
	if e != nil || detail.EditionCount != 1 {
		t.Fatal(detail, e)
	}
	if e = s.TranslateTag(ctx, p, "zh-CN", 0, strptr("人工译名")); e != nil {
		t.Fatal(e)
	}
	if _, e = s.TagCatalogRelations(ctx, p, in); !errors.Is(e, model.ErrSnapshot) {
		t.Fatal("translation did not expire relation page", e)
	}
	rev, e := catalogRevision(ctx, s.Read)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DecideCharacterRelation(ctx, p, tagID("character", "hero"), 0, "confirmed"); e != nil {
		t.Fatal(e)
	}
	after, e := catalogRevision(ctx, s.Read)
	if e != nil || rev != after {
		t.Fatal("relation decision invalidated unrelated catalog / recommendation inputs", e)
	}
}
