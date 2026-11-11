package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func dictionaryJob(t *testing.T, s *Store, id string) (model.Job, model.Item) {
	t.Helper()
	ctx := context.Background()
	j, e := s.CreateTranslationJob(ctx, id, "")
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	items, e := s.Items(ctx, j.ID, 1, 10, true)
	if e != nil || len(items) != 1 {
		t.Fatal(items, e)
	}
	return *j, items[0]
}
func TestDictionaryIndexSuggestionsOwnershipAndSnapshots(t *testing.T) {
	s, _ := openTest(t)
	seedBook(t, s)
	ctx := context.Background()
	if e := s.ApplyOverrides(ctx, "book", 1, map[string]model.Override{"tags": {Op: "set", Value: json.RawMessage(`[{"namespace":"artist","name":"used"},{"namespace":"artist","name":"custom"}]`)}}); e != nil {
		t.Fatal(e)
	}
	dict := map[model.TagName]string{{Namespace: "artist", Name: "used"}: "常用", {Namespace: "artist", Name: "zero"}: "零使用", {Namespace: "artist", Name: "blank"}: ""}
	j, item := dictionaryJob(t, s, "dictionary1")
	v := model.TranslationVersion{ID: "v1", Digest: "digest-v1", Source: "anonymous", CacheKey: "anonymous"}
	if e := s.PublishDictionary(ctx, j, item, v, dict); e != nil {
		t.Fatal(e)
	}
	if e := s.FinishBackground(ctx, j, ""); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := s.Read.QueryRow("SELECT count(*) FROM tags").Scan(&count); e != nil || count != 2 {
		t.Fatal("dictionary polluted business tags", count, e)
	}
	query := func(in model.TagSuggestionInput) model.TagSuggestions {
		t.Helper()
		in, e := model.ValidateTagSuggestions(in)
		if e != nil {
			t.Fatal(e)
		}
		out, e := s.TagSuggestions(ctx, in)
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	out := query(model.TagSuggestionInput{Namespace: "artist"})
	if out.Total != 4 || out.DictionaryState != "ready" || out.Items[0].Name != "custom" && out.Items[0].Name != "used" || out.Items[0].Usage != 1 {
		t.Fatal(out)
	}
	for _, i := range out.Items {
		if i.Name == "blank" && (i.DictionaryMatch == nil || !*i.DictionaryMatch || i.Origin != "original") {
			t.Fatal("blank translation is still a dictionary member", i)
		}
	}
	out = query(model.TagSuggestionInput{Query: "零使用"})
	if out.Total != 1 || out.Items[0].LocalExists || out.Items[0].TagID != nil {
		t.Fatal(out)
	}
	out = query(model.TagSuggestionInput{PageSize: 1, Exclude: []model.TagName{{Namespace: "artist", Name: "custom"}}})
	if out.Total != 3 || out.Items[0].Name != "used" {
		t.Fatal(out)
	}
	frozen := model.TagSuggestionInput{Page: 2, PageSize: 1, Snapshot: out.Snapshot, Exclude: []model.TagName{{Namespace: "artist", Name: "custom"}}}
	if page := query(frozen); len(page.Items) != 1 || page.Items[0].Name != "blank" {
		t.Fatal(page)
	}
	d, e := s.MetadataDetails(ctx, "book")
	if e != nil {
		t.Fatal(e)
	}
	var used model.Tag
	for _, tag := range d.Tags {
		if tag.Name == "used" {
			used = tag
		}
	}
	manual := "人工译名"
	if e = s.TranslateTag(ctx, used.ID, "zh-CN", 0, &manual); e != nil {
		t.Fatal(e)
	}
	if _, e = s.TagSuggestions(ctx, frozen); !errors.Is(e, model.ErrSnapshot) {
		t.Fatal("manual translation did not expire snapshot", e)
	}
	out = query(model.TagSuggestionInput{Query: "人工译名"})
	if out.Total != 1 || out.Items[0].Origin != "manual" {
		t.Fatal(out)
	}
	_, _ = s.Write.Exec("UPDATE libraries SET disabled=1 WHERE id='lib'")
	out = query(model.TagSuggestionInput{})
	for _, i := range out.Items {
		if i.Usage != 0 {
			t.Fatal("disabled counted", i)
		}
	}
	// A valid persisted SQL generation does not require its source cache.
	ready, e := s.DictionaryAvailable(ctx)
	if e != nil || !ready {
		t.Fatal(ready, e)
	}
	_, _ = s.Write.Exec("UPDATE dictionary_state SET active_generation=NULL")
	out = query(model.TagSuggestionInput{})
	if out.DictionaryState != "unavailable" || out.Total != 2 || out.Items[0].DictionaryMatch != nil {
		t.Fatal(out)
	}
}
func TestDictionaryCheckpointCancellationAndRetention(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	dict := map[model.TagName]string{}
	for i := 0; i < 1001; i++ {
		dict[model.TagName{Namespace: "artist", Name: fmt.Sprintf("tag%04d", i)}] = "匿名"
	}
	j, item := dictionaryJob(t, s, "checkpoint")
	v := model.TranslationVersion{ID: "v1", Digest: "digest1"}
	if _, e := s.Write.Exec(`CREATE TRIGGER fail_dictionary BEFORE INSERT ON dictionary_entries WHEN NEW.name='tag1000' BEGIN SELECT RAISE(ABORT,'anonymous fault');END;`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.stageDictionary(ctx, &j, v, dict); e == nil {
		t.Fatal("injected failure ignored")
	}
	var checkpoint int
	if e := s.Read.QueryRow("SELECT checkpoint FROM dictionary_generations").Scan(&checkpoint); e != nil || checkpoint != 1000 {
		t.Fatal(checkpoint, e)
	}
	_, _ = s.Write.Exec("DROP TRIGGER fail_dictionary")
	if e := s.ControlJob(ctx, j.ID, "pause", ""); e != nil {
		t.Fatal(e)
	}
	if e := s.PublishDictionary(ctx, j, item, v, dict); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal(e)
	}
	if e := s.ControlJob(ctx, j.ID, "resume", ""); e != nil {
		t.Fatal(e)
	}
	claimed, e := s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.PublishDictionary(ctx, *claimed, item, v, dict); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishBackground(ctx, *claimed, ""); e != nil {
		t.Fatal(e)
	}
	for i := 2; i <= 3; i++ {
		job, it := dictionaryJob(t, s, fmt.Sprintf("next%d", i))
		ver := model.TranslationVersion{ID: fmt.Sprintf("v%d", i), Digest: fmt.Sprintf("digest%d", i)}
		if e = s.PublishDictionary(ctx, job, it, ver, map[model.TagName]string{{Namespace: "artist", Name: "next"}: "下一个"}); e != nil {
			t.Fatal(e)
		}
		if e = s.FinishBackground(ctx, job, ""); e != nil {
			t.Fatal(e)
		}
	}
	var generations int
	_ = s.Read.QueryRow("SELECT count(*) FROM dictionary_generations").Scan(&generations)
	if generations != 2 {
		t.Fatal(generations)
	}
}
