package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupAcceptedExpiryAndFinalization(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "first_confirmation_expired", true: "accepted_before_expiry"}[accepted], func(t *testing.T) {
			s, db, _, _, _ := fixture(t)
			ctx := context.Background()
			storageWrite(t, s.c.DataDir, "tmp/old", []byte("anonymous"))
			old := time.Now().Add(-48 * time.Hour)
			os.Chtimes(filepath.Join(s.c.DataDir, "tmp/old"), old, old)
			p, e := s.Plan(ctx, model.MaintenanceInput{Operation: "cleanup", Options: json.RawMessage(`{"categories":["tmp"]}`), Expected: map[string]int64{}})
			if e != nil {
				t.Fatal(e)
			}
			var j *model.Job
			if accepted {
				j, e = s.Execute(ctx, p.ID)
				if e != nil {
					t.Fatal(e)
				}
			}
			if _, e = db.Write.Exec("UPDATE maintenance_plans SET expires_at=1,summary_json=json_set(summary_json,'$.expires_at',1) WHERE id=?", p.ID); e != nil {
				t.Fatal(e)
			}
			if !accepted {
				if _, e = s.Execute(ctx, p.ID); !errors.Is(e, model.ErrPreviewExpired) {
					t.Fatal(e)
				}
				return
			}
			s.Start(ctx)
			if done := wait(t, db, j.ID); done.State != "completed" {
				t.Fatal(done)
			}
			result, e := db.MaintenancePlan(ctx, p.ID)
			if e != nil {
				t.Fatal(e)
			}
			r := result.Summary["cleanup_result"].(map[string]any)
			if r["status"] != "completed" || r["phase"] != "finished" || r["deleted_files"] != float64(1) {
				t.Fatal(r)
			}
		})
	}
}
