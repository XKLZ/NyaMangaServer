package store

import (
	"context"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"os"
	"testing"
	"time"
)

func cleanupBatchFixture(t *testing.T) (*Store, model.Job, model.MaintenancePlan) {
	t.Helper()
	s, _ := openTest(t)
	ctx := context.Background()
	p, e := s.CreateMaintenancePlan(ctx, model.MaintenanceInput{Operation: "cleanup", Options: json.RawMessage(`{"categories":["tmp"]}`), Expected: map[string]int64{}})
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.ExecuteMaintenance(ctx, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	return s, *j, p
}
func TestCleanupBatchRollbackMissingAndGeneration(t *testing.T) {
	s, j, p := cleanupBatchFixture(t)
	ctx := context.Background()
	unlinked := false
	files := []model.StorageDeletion{{Kind: "tmp", Key: "tmp/a", Size: 10, Ordinal: 0, Remove: func() (bool, error) { unlinked = true; return true, nil }}, {Kind: "tmp", Key: "tmp/b", Size: 20, Ordinal: 1, Remove: func() (bool, error) { return false, errors.New("anonymous failure") }}}
	if _, e := s.DeleteStorageBatch(ctx, j, files); e == nil || !unlinked {
		t.Fatal(e, unlinked)
	}
	counts, e := s.CleanupProgress(ctx, j)
	if e != nil || counts["next_index"] != 0 {
		t.Fatal(counts, e)
	}
	files[0].Remove = func() (bool, error) { return false, os.ErrNotExist }
	files[1].Remove = func() (bool, error) { return true, nil }
	if _, e = s.DeleteStorageBatch(ctx, j, files); e != nil {
		t.Fatal(e)
	}
	counts, e = s.CleanupProgress(ctx, j)
	if e != nil || counts["next_index"] != 2 || counts["released_bytes"] != 20 || counts["unconfirmed_files"] != 1 || counts["committed_batches"] != 1 {
		t.Fatal(counts, e)
	}
	if _, e = s.Write.Exec("UPDATE jobs SET generation=generation+1 WHERE id=?", j.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DeleteStorageBatch(ctx, j, files); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal(e)
	}
	var exists int
	if e = s.Read.QueryRow("SELECT count(*) FROM maintenance_plans WHERE id=?", p.ID).Scan(&exists); e != nil || exists != 1 {
		t.Fatal(e)
	}
}
func TestCleanupBatchBoundAndCancellation(t *testing.T) {
	s, j, _ := cleanupBatchFixture(t)
	ctx := context.Background()
	files := make([]model.StorageDeletion, 65)
	for i := range files {
		files[i] = model.StorageDeletion{Kind: "tmp", Key: "tmp/anonymous", Ordinal: int64(i), Size: 1, Remove: func() (bool, error) { return true, nil }}
	}
	if _, e := s.DeleteStorageBatch(ctx, j, files); !errors.Is(e, model.ErrInput) {
		t.Fatal(e)
	}
	if result, e := s.DeleteStorageBatch(ctx, j, files[:64]); e != nil || len(result) != 64 {
		t.Fatal(len(result), e)
	}
	counts, _ := s.CleanupProgress(ctx, j)
	if counts["committed_batches"] != 1 || counts["next_index"] != 64 {
		t.Fatal(counts)
	}
	files[0].Ordinal = 64
	files[0].Remove = func() (bool, error) { time.Sleep(260 * time.Millisecond); return true, nil }
	files[1].Ordinal = 65
	if result, e := s.DeleteStorageBatch(ctx, j, files[:2]); e != nil || len(result) != 1 {
		t.Fatal(result, e)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	files[0].Ordinal = 65
	files[1].Ordinal = 66
	files[0].Remove = func() (bool, error) { cancel(); return true, nil }
	if _, e := s.DeleteStorageBatch(cancelCtx, j, files[:2]); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	counts, _ = s.CleanupProgress(ctx, j)
	if counts["next_index"] != 65 {
		t.Fatal(counts)
	}
}
func TestCleanupFinalizationStatusIsAtomic(t *testing.T) {
	s, j, _ := cleanupBatchFixture(t)
	ctx := context.Background()
	if e := s.CleanupPhase(ctx, j, "finalizing"); e != nil {
		t.Fatal(e)
	}
	if e := s.FinishBackground(ctx, j, "anonymous_finalization"); e != nil {
		t.Fatal(e)
	}
	var status, phase string
	if e := s.Read.QueryRow("SELECT json_extract(summary_json,'$.summary.cleanup_result.status'),json_extract(summary_json,'$.summary.cleanup_result.phase') FROM maintenance_plans WHERE job_id=?", j.ID).Scan(&status, &phase); e != nil || status != "finalization_failed" || phase != "finalizing" {
		t.Fatal(status, phase, e)
	}
}
