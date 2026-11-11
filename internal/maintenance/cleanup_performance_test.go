package maintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in anonymous workload. Compile once before and once after changes so both
// executables use this exact fixture, without reading a live instance.
func TestCleanupPerformance(t *testing.T) {
	if os.Getenv("NYA_CLEANUP_PERFORMANCE") != "1" {
		t.Skip("explicit performance run only")
	}
	s, db, _, _, _ := fixture(t)
	ctx := context.Background()
	old := time.Now().Add(-48 * time.Hour)
	const small = 36000
	for i := 0; i < small+4; i++ {
		raw := []byte(strings.Repeat("x", 128))
		if i >= small {
			raw = []byte(strings.Repeat("x", 8<<20))
		}
		key := fmt.Sprintf("tmp/performance/%05d.bin", i)
		storageWrite(t, s.c.DataDir, key, raw)
		if e := os.Chtimes(filepath.Join(s.c.DataDir, key), old, old); e != nil {
			t.Fatal(e)
		}
	}
	p, e := s.Plan(ctx, model.MaintenanceInput{Operation: "cleanup", Expected: map[string]int64{}, Options: json.RawMessage(`{"categories":["tmp"]}`)})
	if e != nil {
		t.Fatal(e)
	}
	var in struct {
		Files      []cleanupFile `json:"_cleanup_files"`
		Categories []string      `json:"categories"`
	}
	if e = json.Unmarshal(p.Input.Options, &in); e != nil {
		t.Fatal(e)
	}
	for i := range in.Files {
		in.Files[i].Material = model.StorageMaterial{ID: "instance:anonymous-perf", Kind: "instance_recovery", OperationID: "anonymous-perf", State: "offline_unconfirmed"}
	}
	p.Input.Options, _ = json.Marshal(in)
	raw, _ := json.Marshal(p.Input)
	if _, e = db.Write.Exec("UPDATE maintenance_plans SET input_json=? WHERE id=?", string(raw), p.ID); e != nil {
		t.Fatal(e)
	}
	// Match the expensive historical activation read, without private material.
	if e = os.MkdirAll(filepath.Dir(activationPath(s.c)), 0700); e != nil {
		t.Fatal(e)
	}
	activation, _ := json.Marshal(map[string]any{"id": "other-completed-operation", "state": "completed", "keys": []string{strings.Repeat("x", 2500000)}})
	if e = os.WriteFile(activationPath(s.c), activation, 0600); e != nil {
		t.Fatal(e)
	}
	j, e := s.Execute(ctx, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := db.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	started := time.Now()
	if e = s.cleanupFrozen(ctx, *claimed, p.Input.Options); e != nil {
		t.Fatal(e)
	}
	elapsed := time.Since(started)
	result, e := db.CleanupProgress(ctx, *claimed)
	if e != nil {
		t.Fatal(e)
	}
	if result["deleted_files"] != small+4 || result["skipped_files"] != 0 {
		t.Fatal(result)
	}
	t.Logf("files=%d elapsed=%s result=%v", small+4, elapsed, result)
}
