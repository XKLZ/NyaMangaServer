package maintenance

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func TestHealthFeatureQuarantineWithoutInference(t *testing.T) {
	legacy, db, cat, lib, _ := fixture(t)
	b := indexed(t, db, cat, lib)
	ctx := context.Background()
	h := NewHealth(db, legacy.c, cat.Pool)
	version := healthFeatureVersion("phash")
	input := fmt.Sprintf("%x", sha256.Sum256([]byte("anonymous input")))
	feature := json.RawMessage(`"0000000000000000"`)
	digest := fmt.Sprintf("%x", sha256.Sum256(feature))
	identity, _ := json.Marshal([]string{version, "none", input})
	fp := fmt.Sprintf("%x", sha256.Sum256(identity))
	value := map[string]any{"fingerprint": fp, "model_sha256": "none", "algorithm_version": version, "input_sha256": input, "feature_digest": digest, "feature": feature}
	raw, _ := json.Marshal(value)
	expected, _ := json.Marshal(value)
	if !validHealthFeature(raw, expected, "phash") {
		t.Fatal("valid scalar phash rejected")
	}
	key := "indexes/recommendations/features/phash/" + fp + ".json"
	path := filepath.Join(legacy.c.DataDir, key)
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, raw, 0600)
	if _, e := db.Write.Exec("INSERT INTO recommendation_features VALUES(?,'phash','anonymous-input',?,'none',?,?,1)", b.ID, fp, version, digest); e != nil {
		t.Fatal(e)
	}
	c, e := h.Create(ctx, model.HealthCheckInput{RequestKey: "valid-feature", Mode: "application"})
	if e != nil {
		t.Fatal(e)
	}
	h.Tick(ctx)
	page, e := db.HealthFindings(ctx, c.ID, "confirmed", "indexes", 1, 100)
	if e != nil || page.Total != 0 {
		t.Fatal(page, e)
	}
	value["feature"] = json.RawMessage(`"f000000000000000"`)
	bad, _ := json.Marshal(value)
	os.WriteFile(path, bad, 0600)
	p := healthPlanFor(t, h, db, "quarantine_recommendation")
	r := healthExecute(t, h, p)
	h.Tick(ctx)
	r, _ = db.HealthRepair(ctx, r.ID)
	if r.State != "completed" {
		t.Fatal(r)
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("invalid cache not quarantined", e)
	}
	var n int
	db.Read.QueryRow("SELECT count(*) FROM recommendation_features WHERE edition_id=?", b.ID).Scan(&n)
	if n != 1 {
		t.Fatal("feature reference was rewritten")
	}
	r = healthRollback(t, h, db, r.ID)
	if r.State != "rolled_back" {
		t.Fatal(r)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, bad) {
		t.Fatal("quarantine before bytes not restored")
	}
}
func TestHealthSeriesBlockBoundedCRCAndRecords(t *testing.T) {
	pack := func(raw string) []byte {
		var b bytes.Buffer
		z := gzip.NewWriter(&b)
		z.Write([]byte(raw))
		z.Close()
		return b.Bytes()
	}
	extra := []byte(`{"records":1,"execution_version":"series-signals-v1"}`)
	valid := pack(`{"signals":[{"a":"a","b":"b","key":"pair","signal":{}}],"computed_pairs":1,"reused_pairs":0}`)
	if status, code := checkHealthSeriesBlock(valid, extra); status != "normal" {
		t.Fatal(status, code)
	}
	bad := append([]byte{}, valid...)
	bad[len(bad)-6] ^= 1
	if status, _ := checkHealthSeriesBlock(bad, extra); status != "confirmed" {
		t.Fatal("gzip CRC ignored", status)
	}
	if status, _ := checkHealthSeriesBlock(valid, []byte(`{"records":0,"execution_version":"series-signals-v1"}`)); status != "confirmed" {
		t.Fatal(status)
	}
	if status, _ := checkHealthSeriesBlock(valid, []byte(`{"records":1,"execution_version":"unknown"}`)); status != "not_checked" {
		t.Fatal(status)
	}
}
