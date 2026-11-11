package maintenance

import (
	"context"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/model"
	"os"
	"testing"
)

func TestHealthApplicationAndProjectionRepairRollback(t *testing.T) {
	legacy, db, cat, lib, _ := fixture(t)
	book := indexed(t, db, cat, lib)
	ctx := context.Background()
	h := NewHealth(db, legacy.c, cat.Pool)
	c, e := h.Create(ctx, model.HealthCheckInput{RequestKey: "health-baseline", Mode: "application"})
	if e != nil {
		t.Fatal(e)
	}
	h.Tick(ctx)
	v, e := db.HealthCheck(ctx, c.ID)
	if e != nil || v.State != "completed" {
		t.Fatal(v, e)
	}
	page, e := db.HealthFindings(ctx, c.ID, "confirmed", "", 1, 100)
	if e != nil {
		t.Fatal(e)
	}
	if page.Total != 0 {
		t.Fatalf("healthy fixture: %+v", page.Items)
	}
	if _, e = db.Write.Exec("UPDATE search_documents SET title='corrupt synthetic' WHERE edition_id=?", book.ID); e != nil {
		t.Fatal(e)
	}
	c, e = h.Create(ctx, model.HealthCheckInput{RequestKey: "health-corrupted", Mode: "application"})
	if e != nil {
		t.Fatal(e)
	}
	h.Tick(ctx)
	v, _ = db.HealthCheck(ctx, c.ID)
	if v.State != "completed" {
		t.Fatal(v)
	}
	page, e = db.HealthFindings(ctx, c.ID, "confirmed", "projections", 1, 100)
	if e != nil || page.Total != 1 {
		t.Fatal(page, e)
	}
	plan, e := h.Plan(ctx, model.HealthPlanInput{RequestKey: "repair-preview", CheckID: c.ID, FindingIDs: []string{page.Items[0].ID}})
	if e != nil {
		t.Fatal(e)
	}
	r, e := h.Execute(ctx, plan.ID, model.HealthExecuteInput{RequestKey: "repair-execution", ExpectedRevision: plan.Revision, ConfirmMaintenance: true})
	if e != nil {
		t.Fatal(e)
	}
	h.Tick(ctx)
	r, e = db.HealthRepair(ctx, r.ID)
	if e != nil || r.State != "completed" {
		t.Fatal(r, e)
	}
	var title string
	db.Read.QueryRow("SELECT title FROM search_documents WHERE edition_id=?", book.ID).Scan(&title)
	if title == "corrupt synthetic" {
		t.Fatal("projection was not rebuilt")
	}
	r, e = h.Rollback(ctx, r.ID, model.HealthExecuteInput{RequestKey: "repair-rollback", ExpectedRevision: r.Revision, ConfirmMaintenance: true})
	if e != nil {
		t.Fatal(e)
	}
	h.Tick(ctx)
	r, e = db.HealthRepair(ctx, r.ID)
	if e != nil || r.State != "rolled_back" {
		t.Fatal(r, e)
	}
	db.Read.QueryRow("SELECT title FROM search_documents WHERE edition_id=?", book.ID).Scan(&title)
	if title != "corrupt synthetic" {
		t.Fatal("before image not restored", title)
	}
}

func TestHealthFrozenTargetsPauseAndStaleness(t *testing.T) {
	legacy, db, cat, lib, _ := fixture(t)
	book := indexed(t, db, cat, lib)
	ctx := context.Background()
	h := NewHealth(db, legacy.c, cat.Pool)
	c, e := h.Create(ctx, model.HealthCheckInput{RequestKey: "frozen-health", Mode: "application"})
	if e != nil {
		t.Fatal(e)
	}
	paused := false
	h.BeforeObject = func(_ context.Context, o model.HealthObject) {
		if !paused && o.Kind == "edition" {
			paused = true
			if e := h.Control(ctx, c.ID, "pause"); e != nil {
				t.Error(e)
			}
		}
	}
	h.Tick(ctx)
	v, _ := db.HealthCheck(ctx, c.ID)
	if v.State != "paused" {
		t.Fatal(v)
	}
	if _, e = db.Write.Exec("UPDATE editions SET revision=revision+1 WHERE id=?", book.ID); e != nil {
		t.Fatal(e)
	}
	h.BeforeObject = nil
	if e = h.Control(ctx, c.ID, "resume"); e != nil {
		t.Fatal(e)
	}
	h.Tick(ctx)
	v, _ = db.HealthCheck(ctx, c.ID)
	if v.State != "completed" || v.Counts["stale"] == 0 {
		t.Fatal(v)
	}
	page, e := db.HealthFindings(ctx, c.ID, "stale", "", 1, 1)
	if e != nil || page.Total < 1 || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	if _, e = db.Write.Exec("UPDATE editions SET revision=revision+1 WHERE id<>?", book.ID); e != nil {
		t.Fatal(e)
	}
	after, _ := db.HealthCheck(ctx, c.ID)
	if after.Counts["stale"] < v.Counts["stale"] {
		t.Fatal(after)
	}
}
func TestHealthDeepSourcesAndBudget(t *testing.T) {
	legacy, db, cat, lib, _ := fixture(t)
	indexed(t, db, cat, lib)
	ctx := context.Background()
	h := NewHealth(db, legacy.c, cat.Pool)
	for _, test := range []struct {
		key    string
		budget int64
		status string
	}{{"source-verification", 8 << 30, "normal"}, {"source-limited", 1, "not_checked"}} {
		c, e := h.Create(ctx, model.HealthCheckInput{RequestKey: test.key, Mode: "sources", LibraryIDs: []string{lib.ID}, MaxBytes: test.budget})
		if e != nil {
			t.Fatal(e)
		}
		h.Tick(ctx)
		v, _ := db.HealthCheck(ctx, c.ID)
		if v.State != "completed" {
			t.Fatal(v)
		}
		page, e := db.HealthFindings(ctx, c.ID, test.status, "sources", 1, 100)
		if e != nil || page.Total == 0 {
			t.Fatal(page, e)
		}
	}
}

func TestHealthSourceRootMutationAndDisabledDoNotRead(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprint(disabled), func(t *testing.T) {
			legacy, db, cat, lib, _ := fixture(t)
			indexed(t, db, cat, lib)
			ctx := context.Background()
			h := NewHealth(db, legacy.c, cat.Pool)
			if disabled {
				if _, e := db.Write.Exec("UPDATE libraries SET disabled=1 WHERE id=?", lib.ID); e != nil {
					t.Fatal(e)
				}
			}
			c, e := h.Create(ctx, model.HealthCheckInput{RequestKey: "source-boundary", Mode: "sources", LibraryIDs: []string{lib.ID}})
			if e != nil {
				t.Fatal(e)
			}
			calls := 0
			h.BeforeIO = func(phase string) error {
				if phase == "source" {
					calls++
				}
				return nil
			}
			changed := false
			h.BeforeObject = func(_ context.Context, o model.HealthObject) {
				if !disabled && !changed && o.Kind == "source" {
					changed = true
					if _, e := db.Write.Exec("UPDATE libraries SET root_identity='new-root',revision=revision+1 WHERE id=?", lib.ID); e != nil {
						t.Error(e)
					}
				}
			}
			h.Tick(ctx)
			if calls != 0 {
				t.Fatal("read invalid/disabled root", calls)
			}
			status := "stale"
			if disabled {
				status = "not_checked"
			}
			page, e := db.HealthFindings(ctx, c.ID, status, "sources", 1, 100)
			if e != nil || page.Total == 0 {
				t.Fatal(page, e)
			}
		})
	}
}

func TestHealthSourceEncodingAndUnknownAreNotDamage(t *testing.T) {
	for _, test := range []struct {
		err    error
		status string
	}{{comic.ErrEncoding, "manual"}, {comic.ErrName, "manual"}, {errors.New("anonymous unknown IO"), "manual"}, {comic.ErrArchive, "confirmed"}, {comic.ErrImage, "confirmed"}, {os.ErrPermission, "inaccessible"}} {
		status, _ := sourceHealthState(test.err)
		if status != test.status {
			t.Fatal(test.err, status, test.status)
		}
	}
}
