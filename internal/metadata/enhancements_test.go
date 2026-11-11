package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func TestEnhancementDictionaryReuseInstalledIndexWithoutNetwork(t *testing.T) {
	s, db, remote, _ := setup(t)
	ctx := context.Background()
	job, e := s.StartTranslation(ctx)
	if e != nil {
		t.Fatal(e)
	}
	job, e = db.Claim(ctx, job.ID)
	if e != nil {
		t.Fatal(e)
	}
	items, e := db.Items(ctx, job.ID, 1, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.processTranslation(ctx, *job, &items[0]); e != nil {
		t.Fatal(e)
	}
	if e = db.FinishBackground(ctx, *job, ""); e != nil {
		t.Fatal(e)
	}
	state, _ := db.Enhancements(ctx)
	off := false
	tr, e := db.ChangeEnhancement(ctx, model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementDictionary, Enabled: &off})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DrainDictionary(ctx); e != nil {
		t.Fatal(e)
	}
	db.FinishEnhancement(ctx, *tr, "")
	if _, e = s.StartTranslation(ctx); !errors.Is(e, model.ErrEnhancementDisabled) {
		t.Fatal(e)
	}
	s.PrepareDictionary(ctx)
	book, _ := db.Edition(ctx, "book")
	if e = s.ApplyOverrides(ctx, "book", book.Revision, map[string]model.Override{"tags": {Op: "set", Value: json.RawMessage(`[{"namespace":"artist","name":"unused"}]`)}}); e != nil {
		t.Fatal(e)
	}
	if tags, e := s.Tags(ctx, "未使用", "", "zh-CN"); e != nil || len(tags) != 0 {
		t.Fatal("remote leaked while off", tags, e)
	}
	v, e := db.TranslationVersion(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(s.dir, v.CacheKey)); e != nil {
		t.Fatal(e)
	}
	before, _ := db.Edition(ctx, "book")
	state, _ = db.Enhancements(ctx)
	on := true
	tr, e = db.ChangeEnhancement(ctx, model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementDictionary, Enabled: &on})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RestoreDictionary(ctx); e != nil {
		t.Fatal(e)
	}
	if e = db.FinishEnhancement(ctx, *tr, ""); e != nil {
		t.Fatal(e)
	}
	tags, e := s.Tags(ctx, "未使用", "", "zh-CN")
	if e != nil || len(tags) != 1 || tags[0].Origin != "remote" {
		t.Fatal(tags, e)
	}
	after, _ := db.Edition(ctx, "book")
	if before.Revision != after.Revision {
		t.Fatal("toggle raised metadata revision")
	}
	if remote.downloads.Load() != 1 || remote.fetch.Load() != 0 || remote.search.Load() != 0 {
		t.Fatal("toggle caused network", remote.downloads.Load())
	}
}
