package store

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/search"
	"testing"
)

func TestRepeatedTranslationMaterializationIsIdempotent(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	if _, e := s.Write.Exec(`INSERT INTO tags VALUES('anonymous-tag','artist','anonymous');INSERT INTO translation_versions VALUES('anonymous-v1','anonymous','digest','anonymous.json',1);UPDATE translation_state SET active_version='anonymous-v1'`); e != nil {
		t.Fatal(e)
	}
	key := model.TagName{Namespace: "artist", Name: "anonymous"}
	dictionary := map[model.TagName]string{key: "匿名译文"}
	apply := func(values map[model.TagName]string) error {
		tx, e := s.Write.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if e = materialize(ctx, tx, "anonymous-v1", values); e != nil {
			return e
		}
		return tx.Commit()
	}

	if e := apply(dictionary); e != nil {
		t.Fatal(e)
	}
	revision := func() (int64, string) {
		t.Helper()
		var revision int64
		var text string
		if e := s.Read.QueryRow(`SELECT revision,text FROM tag_translations WHERE tag_id='anonymous-tag' AND origin='remote' AND dataset='anonymous-v1'`).Scan(&revision, &text); e != nil {
			t.Fatal(e)
		}
		return revision, text
	}
	before, text := revision()
	if before != 1 || text != "匿名译文" {
		t.Fatal(before, text)
	}
	if e := apply(dictionary); e != nil {
		t.Fatal(e)
	}
	after, text := revision()
	if after != before || text != "匿名译文" {
		t.Fatal("unchanged dictionary incremented revision", before, after, text)
	}
	dictionary[key] = "更新译文"
	if e := apply(dictionary); e != nil {
		t.Fatal(e)
	}
	changed, text := revision()
	if changed != before+1 || text != "更新译文" {
		t.Fatal("changed translation was not updated", changed, text)
	}
}
func TestSavedManagementConditionsValidationAndOrdinarySearchRejection(t *testing.T) {
	s, _ := orgFixture(t)
	svc := search.New(s)
	ctx := context.Background()
	for _, conditions := range []model.SearchInput{{Visibility: "unknown"}, {MetadataState: "unknown"}} {
		if _, e := svc.Save(ctx, "a", "", "Invalid", conditions, 0); !errors.Is(e, model.ErrInput) {
			t.Fatal("invalid saved enum accepted", conditions, e)
		}
	}
	valid := model.SearchInput{Visibility: "all", MetadataState: "needs_review", IncludeDisabled: true}
	saved, e := svc.Save(ctx, "a", "", "Management", valid, 0)
	if e != nil || saved.Conditions.MetadataState != "needs_review" || saved.Conditions.Visibility != "all" || !saved.Conditions.IncludeDisabled {
		t.Fatal(saved, e)
	}
	for _, conditions := range []model.SearchInput{{Visibility: "trash"}, {MetadataState: "needs_review"}, {IncludeDisabled: true}} {
		if _, e := svc.Search(ctx, "a", conditions); !errors.Is(e, model.ErrInput) {
			t.Fatal("ordinary search silently ignored management filter", conditions, e)
		}
	}
}
