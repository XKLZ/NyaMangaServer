package metadata

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOfflineDictionaryRecoveryAndCacheCheckpoint(t *testing.T) {
	s, db, remote, _ := setup(t)
	ctx := context.Background()
	j, e := s.StartTranslation(ctx)
	if e != nil {
		t.Fatal(e)
	}
	j, e = db.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	items, e := db.Items(ctx, j.ID, 1, 10, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Write.Exec(`CREATE TRIGGER stop_dictionary BEFORE INSERT ON dictionary_entries BEGIN SELECT RAISE(ABORT,'anonymous interruption');END;`); e != nil {
		t.Fatal(e)
	}
	if e = s.processTranslation(ctx, *j, &items[0]); e == nil {
		t.Fatal("index fault ignored")
	}
	if remote.downloads.Load() != 1 {
		t.Fatal(remote.downloads.Load())
	}
	if e = db.ControlJob(ctx, j.ID, "pause", ""); e != nil {
		t.Fatal(e)
	}
	if e = db.ControlJob(ctx, j.ID, "resume", ""); e != nil {
		t.Fatal(e)
	}
	j, e = db.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	items, e = db.Items(ctx, j.ID, 1, 10, true)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = db.Write.Exec("DROP TRIGGER stop_dictionary")
	if e = s.processTranslation(ctx, *j, &items[0]); e != nil {
		t.Fatal(e)
	}
	if e = db.FinishBackground(ctx, *j, ""); e != nil {
		t.Fatal(e)
	}
	if remote.downloads.Load() != 1 {
		t.Fatal("resume downloaded validated cache again", remote.downloads.Load())
	}
	v, e := db.TranslationVersion(ctx)
	if e != nil || v == nil {
		t.Fatal(v, e)
	}
	path := filepath.Join(s.dir, v.CacheKey)
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	s.PrepareDictionary(ctx)
	if ready, e := db.DictionaryAvailable(ctx); e != nil || !ready {
		t.Fatal("SQL index depends on cache", ready, e)
	}
	_, _ = db.Write.Exec("UPDATE dictionary_state SET active_generation=NULL,previous_generation=NULL;DELETE FROM dictionary_generations")
	s.PrepareDictionary(ctx)
	if ready, e := db.DictionaryAvailable(ctx); e != nil || ready {
		t.Fatal(ready, e)
	}
	if e = os.WriteFile(path, []byte(`{"corrupt":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	s.PrepareDictionary(ctx)
	if ready, e := db.DictionaryAvailable(ctx); e != nil || ready {
		t.Fatal("corrupt cache activated", ready, e)
	}
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	s.PrepareDictionary(ctx)
	if ready, e := db.DictionaryAvailable(ctx); e != nil || !ready {
		t.Fatal("offline recovery failed", ready, e)
	}
	if remote.downloads.Load() != 1 || remote.fetch.Load() != 0 || remote.search.Load() != 0 {
		t.Fatal("startup caused network request")
	}
}
