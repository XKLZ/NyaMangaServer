package maintenance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"nyamangaserver.local/server/internal/metadata"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/tagimages"
	"os"
	"path/filepath"
	"testing"
)

func TestHealthPreferredAndSeriesWhitelistRollback(t *testing.T) {
	legacy, db, cat, lib, _ := fixture(t)
	indexed(t, db, cat, lib)
	ctx := context.Background()
	h := NewHealth(db, legacy.c, cat.Pool)
	books, _, e := db.Editions(ctx, lib.ID, "", 1, 100)
	if e != nil || len(books) < 3 {
		t.Fatal(books, e)
	}
	// A private synthetic corruption models a database whose existing FK was damaged.
	c, e := db.Write.Conn(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); e != nil {
		t.Fatal(e)
	}
	if _, e = c.ExecContext(ctx, "UPDATE works SET preferred_edition_id='anonymous-missing' WHERE id=?", books[0].WorkID); e != nil {
		t.Fatal(e)
	}
	if _, e = c.ExecContext(ctx, "PRAGMA foreign_keys=ON"); e != nil {
		t.Fatal(e)
	}
	c.Close()
	if _, e = db.Write.Exec("INSERT INTO series VALUES('anonymous-series',?,'Anonymous',1)", lib.ID); e != nil {
		t.Fatal(e)
	}
	for i, b := range books {
		if _, e = db.Write.Exec("INSERT INTO series_members VALUES('anonymous-series',?,?)", b.WorkID, []int{1, 4, 10}[i]); e != nil {
			t.Fatal(e)
		}
	}
	var catalogBefore int64
	db.Read.QueryRow("SELECT revision FROM catalog_state WHERE singleton=1").Scan(&catalogBefore)
	p := healthPlanFor(t, h, db, "all")
	if len(p.Actions) != 2 {
		t.Fatal(p)
	}
	r := healthExecute(t, h, p)
	h.Tick(ctx)
	r, _ = db.HealthRepair(ctx, r.ID)
	if r.State != "completed" {
		t.Fatal(r)
	}
	var catalogAfter int64
	db.Read.QueryRow("SELECT revision FROM catalog_state WHERE singleton=1").Scan(&catalogAfter)
	if catalogAfter <= catalogBefore {
		t.Fatal("catalog snapshot not invalidated")
	}
	for _, kind := range []string{"work", "series", "health_state", "health_repair"} {
		var n int
		if e := db.Read.QueryRow("SELECT count(*) FROM events WHERE type=?", kind).Scan(&n); e != nil || n == 0 {
			t.Fatal("missing notification", kind, n, e)
		}
	}
	var preferred string
	db.Read.QueryRow("SELECT preferred_edition_id FROM works WHERE id=?", books[0].WorkID).Scan(&preferred)
	if preferred != books[0].ID {
		t.Fatal(preferred)
	}
	var positions string
	db.Read.QueryRow("SELECT group_concat(position) FROM(SELECT position FROM series_members WHERE series_id='anonymous-series' ORDER BY position)").Scan(&positions)
	if positions != "1,2,3" {
		t.Fatal(positions)
	}
	rules, e := db.HealthRules(ctx, model.HealthObject{Kind: "series", ID: "anonymous-series"})
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range rules {
		if v.Status != "normal" {
			t.Fatal(v)
		}
	}
	r = healthRollback(t, h, db, r.ID)
	if r.State != "rolled_back" {
		t.Fatal(r)
	}
	db.Read.QueryRow("SELECT group_concat(position) FROM(SELECT position FROM series_members WHERE series_id='anonymous-series' ORDER BY position)").Scan(&positions)
	if positions != "1,4,10" {
		t.Fatal(positions)
	}
	db.Read.QueryRow("SELECT preferred_edition_id FROM works WHERE id=?", books[0].WorkID).Scan(&preferred)
	if preferred != "anonymous-missing" {
		t.Fatal(preferred)
	}
	var foreign int
	db.Write.QueryRow("PRAGMA foreign_keys").Scan(&foreign)
	if foreign != 1 {
		t.Fatal("connection foreign keys not restored")
	}
}
func TestHealthEvidenceRetainsManualInputsAndDecisions(t *testing.T) {
	legacy, db, cat, lib, _ := fixture(t)
	b := indexed(t, db, cat, lib)
	ctx := context.Background()
	h := NewHealth(db, legacy.c, cat.Pool)
	tags, _ := json.Marshal([]model.TagName{{Namespace: "parody", Name: "Anonymous"}, {Namespace: "character", Name: "Hero"}})
	if e := db.ApplyOverrides(ctx, b.ID, b.Revision, map[string]model.Override{"tags": {Op: "set", Value: tags}, "title": {Op: "empty"}}); e != nil {
		t.Fatal(e)
	}
	var p, c string
	db.Read.QueryRow("SELECT id FROM tags WHERE namespace='parody'").Scan(&p)
	db.Read.QueryRow("SELECT id FROM tags WHERE namespace='character'").Scan(&c)
	if _, e := db.DecideCharacterRelation(ctx, p, c, 0, "excluded"); e != nil {
		t.Fatal(e)
	}
	var before string
	db.Read.QueryRow("SELECT group_concat(field||':'||mode||':'||value_json) FROM(SELECT * FROM field_overrides WHERE edition_id=? ORDER BY field)", b.ID).Scan(&before)
	if _, e := db.Write.Exec("DELETE FROM tag_character_evidence WHERE edition_id=?", b.ID); e != nil {
		t.Fatal(e)
	}
	plan := healthPlanFor(t, h, db, "tag_evidence")
	r := healthExecute(t, h, plan)
	h.Tick(ctx)
	r, _ = db.HealthRepair(ctx, r.ID)
	if r.State != "completed" {
		t.Fatal(r)
	}
	var n int
	db.Read.QueryRow("SELECT count(*) FROM tag_character_evidence WHERE edition_id=?", b.ID).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
	r = healthRollback(t, h, db, r.ID)
	if r.State != "rolled_back" {
		t.Fatal(r)
	}
	var after string
	db.Read.QueryRow("SELECT group_concat(field||':'||mode||':'||value_json) FROM(SELECT * FROM field_overrides WHERE edition_id=? ORDER BY field)", b.ID).Scan(&after)
	if after != before {
		t.Fatal("manual overrides changed")
	}
	var decision string
	db.Read.QueryRow("SELECT decision FROM parody_character_decisions WHERE parody_id=? AND character_id=?", p, c).Scan(&decision)
	if decision != "excluded" {
		t.Fatal(decision)
	}
}
func TestHealthTagCacheRepairKeepsOriginal(t *testing.T) {
	legacy, db, cat, _, _ := fixture(t)
	ctx := context.Background()
	h := NewHealth(db, legacy.c, cat.Pool)
	images := tagimages.New(db, legacy.c, cat.Pool)
	h.TagBytes = images.HealthCacheBytes
	if _, e := db.Write.Exec("INSERT INTO tags VALUES('anonymous-tag','parody','Anonymous')"); e != nil {
		t.Fatal(e)
	}
	var raw bytes.Buffer
	png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 12, 18)))
	original, e := images.Upload(ctx, "anonymous-tag", "foreground", 0, "image/png", raw.Bytes())
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = images.Content(ctx, "anonymous-tag", "foreground", 1); e != nil {
		t.Fatal(e)
	}
	key, _, e := images.HealthCacheBytes(ctx, "anonymous-tag", "foreground")
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(legacy.c.DataDir, key)
	bad := []byte("anonymous damaged cache")
	os.WriteFile(path, bad, 0600)
	p := healthPlanFor(t, h, db, "tag_cache")
	r := healthExecute(t, h, p)
	h.Tick(ctx)
	r, _ = db.HealthRepair(ctx, r.ID)
	if r.State != "completed" {
		t.Fatal(r)
	}
	restored, _ := os.ReadFile(path)
	if bytes.Equal(restored, bad) {
		t.Fatal("cache was not restored")
	}
	orig, _ := os.ReadFile(filepath.Join(legacy.c.DataDir, original.Asset.Key))
	if !bytes.Equal(orig, raw.Bytes()) {
		t.Fatal("original asset changed")
	}
	r = healthRollback(t, h, db, r.ID)
	if r.State != "rolled_back" {
		t.Fatal(r)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, bad) {
		t.Fatal("cache rollback failed")
	}
}
func TestHealthDictionaryRepairOfflineKeepsManualNames(t *testing.T) {
	legacy, db, cat, _, _ := fixture(t)
	ctx := context.Background()
	h := NewHealth(db, legacy.c, cat.Pool)
	raw := []byte(`{"data":[{"namespace":"parody","data":{"Anonymous":{"name":"匿名"}}}]}`)
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	if _, e := db.Write.Exec("INSERT INTO tags VALUES('anonymous-tag','parody','Anonymous');INSERT INTO translation_versions VALUES('anonymous-version','anonymous',?,? ,1);UPDATE translation_state SET active_version='anonymous-version'", digest, digest+".json"); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(legacy.c.DataDir, "cache", "tag-translations")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, digest+".json"), raw, 0600)
	text := "Manual alias"
	if e := db.TranslateTag(ctx, "anonymous-tag", "zh-CN", 0, &text); e != nil {
		t.Fatal(e)
	}
	meta := metadata.New(db, cat, nil, legacy.c.DataDir, legacy.logger)
	h.DictionaryBytes = meta.HealthDictionary
	p := healthPlanFor(t, h, db, "dictionary")
	r := healthExecute(t, h, p)
	h.Tick(ctx)
	r, _ = db.HealthRepair(ctx, r.ID)
	if r.State != "completed" {
		t.Fatal(r)
	}
	if e := db.HealthDictionaryValid(ctx); e != nil {
		t.Fatal(e)
	}
	later := "Later manual alias"
	if e := db.TranslateTag(ctx, "anonymous-tag", "zh-CN", 1, &later); e != nil {
		t.Fatal(e)
	}
	r = healthRollback(t, h, db, r.ID)
	if r.State != "rolled_back" {
		t.Fatal(r)
	}
	var got string
	db.Read.QueryRow("SELECT text FROM tag_translations WHERE tag_id='anonymous-tag' AND origin='manual'").Scan(&got)
	if got != later {
		t.Fatal(got)
	}
}
