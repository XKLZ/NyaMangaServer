package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupReconcileStrictPreviewAndIdempotence(t *testing.T) {
	for _, variant := range []string{"complete", "incomplete", "skipped", "receipt", "active", "wrong_error", "rollback"} {
		t.Run(variant, func(t *testing.T) {
			s, db, cat, _, _ := fixture(t)
			ctx := context.Background()
			storageWrite(t, s.c.DataDir, "tmp/old", []byte("anonymous"))
			old := time.Now().Add(-48 * time.Hour)
			os.Chtimes(filepath.Join(s.c.DataDir, "tmp/old"), old, old)
			p, e := s.Plan(ctx, model.MaintenanceInput{Operation: "cleanup", Expected: map[string]int64{}, Options: json.RawMessage(`{"categories":["tmp"]}`)})
			if e != nil {
				t.Fatal(e)
			}
			// Receipt completion is part of the proof, not just the 100% counter.
			var options map[string]json.RawMessage
			json.Unmarshal(p.Input.Options, &options)
			var frozen []cleanupFile
			json.Unmarshal(options["_cleanup_files"], &frozen)
			frozen[0].Material = model.StorageMaterial{ID: "instance:proof", Kind: "instance_recovery", OperationID: "proof", State: "offline_unconfirmed"}
			options["_cleanup_files"], _ = json.Marshal(frozen)
			p.Input.Options, _ = json.Marshal(options)
			input, _ := json.Marshal(p.Input)
			if _, e = db.Write.Exec("UPDATE maintenance_plans SET input_json=? WHERE id=?", string(input), p.ID); e != nil {
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
			if e = s.cleanupFrozen(ctx, *claimed, p.Input.Options); e != nil {
				t.Fatal(e)
			}
			items, e := db.Items(ctx, j.ID, 1, 1, true)
			if e != nil {
				t.Fatal(e)
			}
			if e = db.FailBackgroundItem(ctx, *claimed, items[0], "preview_expired"); e != nil {
				t.Fatal(e)
			}
			if e = db.FinishBackground(ctx, *claimed, "preview_expired"); e != nil {
				t.Fatal(e)
			}
			switch variant {
			case "incomplete":
				_, e = db.Write.Exec("UPDATE maintenance_plans SET summary_json=json_set(summary_json,'$.summary.cleanup_result.next_index',0) WHERE id=?", p.ID)
			case "skipped":
				_, e = db.Write.Exec("UPDATE maintenance_plans SET summary_json=json_set(summary_json,'$.summary.cleanup_result.skipped_files',1) WHERE id=?", p.ID)
			case "receipt":
				_, e = db.Write.Exec("UPDATE storage_material_receipts SET state='cleaning'")
			case "active":
				_, e = db.Write.Exec("UPDATE jobs SET state='running' WHERE id=?", j.ID)
			case "wrong_error":
				_, e = db.Write.Exec("UPDATE jobs SET error_code='storage_full' WHERE id=?", j.ID)
			case "rollback":
				_, e = db.Write.Exec("CREATE TRIGGER reject_reconcile BEFORE UPDATE ON jobs WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'anonymous injected failure'); END")
			}
			if e != nil {
				t.Fatal(e)
			}
			cat.Close()
			db.Close()
			lock, e := appdata.Acquire(s.c.DataDir)
			if e != nil {
				t.Fatal(e)
			}
			defer lock.Close()
			// A newly recreated pathname must remain untouched by reconciliation.
			storageWrite(t, s.c.DataDir, "tmp/old", []byte("new retained object"))
			preview, e := store.ReconcileCleanup(ctx, s.c.DataDir, j.ID, false, migrations.Files)
			if variant != "complete" && variant != "rollback" {
				if e == nil {
					t.Fatal("invalid proof accepted", preview)
				}
				return
			}
			if e != nil || preview.Applied || preview.DeletedFiles != 1 || preview.ReleasedBytes != 9 {
				t.Fatal(preview, e)
			}
			result, e := store.ReconcileCleanup(ctx, s.c.DataDir, j.ID, true, migrations.Files)
			if variant == "rollback" {
				if e == nil {
					t.Fatal("injected failure accepted")
				}
				again, e := store.ReconcileCleanup(ctx, s.c.DataDir, j.ID, false, migrations.Files)
				if e != nil || again.AlreadyApplied {
					t.Fatal(again, e)
				}
				return
			}
			if e != nil || !result.Applied {
				t.Fatal(result, e)
			}
			again, e := store.ReconcileCleanup(ctx, s.c.DataDir, j.ID, true, migrations.Files)
			if e != nil || !again.AlreadyApplied || again.ReleasedBytes != result.ReleasedBytes {
				t.Fatal(again, e)
			}
			raw, e := os.ReadFile(filepath.Join(s.c.DataDir, "tmp/old"))
			if e != nil || string(raw) != "new retained object" {
				t.Fatal(e)
			}
			if _, e = store.ReconcileCleanup(ctx, s.c.DataDir, "not-existing", true, migrations.Files); e == nil || errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
		})
	}
}
