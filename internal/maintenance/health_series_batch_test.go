package maintenance

import (
	"context"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func TestHealthSeriesBatchOneSnapshotPreservesIdentity(t *testing.T) {
	legacy, db, cat, lib, _ := fixture(t)
	ctx := context.Background()
	tx, e := db.Write.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 231; i++ {
		id := fmt.Sprintf("series-batch-%03d", i)
		ed := id + "-edition"
		work := id + "-work"
		for _, v := range []struct {
			sql  string
			args []any
		}{
			{"INSERT INTO works(id,library_id,preferred_edition_id,revision) VALUES(?,?,?,1)", []any{work, lib.ID, ed}},
			{"INSERT INTO editions(id,library_id,work_id,revision,organization_revision) VALUES(?,?,?,1,1)", []any{ed, lib.ID, work}},
			{"INSERT INTO files(edition_id,library_id,relative_path,filename_title,size,mtime_ns,content_revision,representative_algorithm,page_count,file_state,indexed_at) VALUES(?,?,?,?,0,1,1,'none',0,'missing',1)", []any{ed, lib.ID, ed + ".cbz", "Anonymous"}},
			{"INSERT INTO series VALUES(?,?,?,1)", []any{id, lib.ID, id}},
			{"INSERT INTO series_members VALUES(?,?,0)", []any{id, work}},
		} {
			if _, e = tx.ExecContext(ctx, v.sql, v.args...); e != nil {
				tx.Rollback()
				t.Fatal(e)
			}
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	h := NewHealth(db, legacy.c, cat.Pool)
	check, e := h.Create(ctx, model.HealthCheckInput{RequestKey: "series-batch-check", Mode: "quick"})
	if e != nil {
		t.Fatal(e)
	}
	h.Tick(ctx)
	ids := []string{}
	for page := 1; ; page++ {
		findings, e := db.HealthFindings(ctx, check.ID, "confirmed", "organization", page, 100)
		if e != nil {
			t.Fatal(e)
		}
		for _, f := range findings.Items {
			if f.Code == "series_positions_start" {
				ids = append(ids, f.ID)
			}
		}
		if page*100 >= findings.Total {
			break
		}
	}
	if len(ids) != 231 {
		t.Fatal(len(ids))
	}
	plan, e := h.Plan(ctx, model.HealthPlanInput{RequestKey: "series-batch-plan", CheckID: check.ID, FindingIDs: ids})
	if e != nil || len(plan.Actions) != 231 {
		t.Fatal(plan, e)
	}
	snapshots := 0
	h.BeforeIO = func(phase string) error {
		if phase == "snapshot" {
			snapshots++
		}
		return nil
	}
	repair := healthExecute(t, h, plan)
	h.Tick(ctx)
	after, e := db.HealthRepair(ctx, repair.ID)
	if e != nil || after.State != "completed" || snapshots != 1 {
		t.Fatal(after.State, snapshots, e)
	}
	var bad int
	if e = db.Read.QueryRow(`SELECT count(*) FROM series_members sm JOIN works w ON w.id=sm.work_id WHERE sm.series_id LIKE 'series-batch-%' AND (sm.position<>1 OR w.id<>sm.series_id||'-work' OR w.preferred_edition_id<>sm.series_id||'-edition')`).Scan(&bad); e != nil || bad != 0 {
		t.Fatal(bad, e)
	}
	// Existing scoped rollback restores the exact original order/positions.
	rolled := healthRollback(t, h, db, repair.ID)
	if rolled.State != "rolled_back" {
		t.Fatal(rolled.State)
	}
	if e = db.Read.QueryRow("SELECT count(*) FROM series_members WHERE position<>0").Scan(&bad); e != nil || bad != 0 {
		t.Fatal(bad, e)
	}
}
