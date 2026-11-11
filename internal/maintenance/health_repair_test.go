package maintenance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func healthPlanFor(t *testing.T, h *HealthService, db *store.Store, kind string) model.HealthPlan {
	t.Helper()
	ctx := context.Background()
	c, e := h.Create(ctx, model.HealthCheckInput{RequestKey: "check-" + kind, Mode: "application"})
	if e != nil {
		t.Fatal(e)
	}
	h.Tick(ctx)
	v, _ := db.HealthCheck(ctx, c.ID)
	if v.State != "completed" {
		t.Fatal(v)
	}
	page, e := db.HealthFindings(ctx, c.ID, "confirmed", "", 1, 100)
	if e != nil {
		t.Fatal(e)
	}
	ids := []string{}
	for _, f := range page.Items {
		if f.Repair != "" && (kind == "all" || f.Repair == kind) {
			ids = append(ids, f.ID)
		}
	}
	if len(ids) == 0 {
		t.Fatalf("no %s repair in %+v", kind, page.Items)
	}
	p, e := h.Plan(ctx, model.HealthPlanInput{RequestKey: "plan-" + kind, CheckID: c.ID, FindingIDs: ids, AllowSources: true})
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func healthExecute(t *testing.T, h *HealthService, p model.HealthPlan) model.HealthRepair {
	t.Helper()
	r, e := h.Execute(context.Background(), p.ID, model.HealthExecuteInput{RequestKey: "execute-" + p.ID, ExpectedRevision: p.Revision, ConfirmMaintenance: true})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func healthRollback(t *testing.T, h *HealthService, db *store.Store, id string) model.HealthRepair {
	t.Helper()
	r, e := db.HealthRepair(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	r, e = h.Rollback(context.Background(), r.ID, model.HealthExecuteInput{RequestKey: "rollback-" + id, ExpectedRevision: r.Revision, ConfirmMaintenance: true})
	if e != nil {
		t.Fatal(e)
	}
	h.Tick(context.Background())
	r, e = db.HealthRepair(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestHealthMultiTargetUsesOneSnapshotAndScopedRollback(t *testing.T) {
	legacy, db, cat, lib, _ := fixture(t)
	indexed(t, db, cat, lib)
	ctx := context.Background()
	h := NewHealth(db, legacy.c, cat.Pool)
	books, _, _ := db.Editions(ctx, lib.ID, "", 1, 100)
	if len(books) < 3 {
		t.Fatal(books)
	}
	for _, b := range books[:2] {
		if _, e := db.Write.Exec("UPDATE search_documents SET title='synthetic-before' WHERE edition_id=?", b.ID); e != nil {
			t.Fatal(e)
		}
	}
	p := healthPlanFor(t, h, db, "projection")
	if len(p.Actions) != 2 {
		t.Fatal(p)
	}
	snapshots := 0
	h.BeforeIO = func(phase string) error {
		if phase == "snapshot" {
			snapshots++
		}
		return nil
	}
	r := healthExecute(t, h, p)
	h.Tick(ctx)
	r, _ = db.HealthRepair(ctx, r.ID)
	if r.State != "completed" || snapshots != 1 {
		t.Fatal(r, snapshots)
	}
	if _, e := db.Write.Exec("UPDATE search_documents SET title='later-unrelated' WHERE edition_id=?", books[2].ID); e != nil {
		t.Fatal(e)
	}
	r = healthRollback(t, h, db, r.ID)
	if r.State != "rolled_back" {
		t.Fatal(r)
	}
	for i, b := range books {
		var value string
		db.Read.QueryRow("SELECT title FROM search_documents WHERE edition_id=?", b.ID).Scan(&value)
		want := "synthetic-before"
		if i == 2 {
			want = "later-unrelated"
		}
		if value != want {
			t.Fatal(i, value, want)
		}
	}
}
func TestHealthSnapshotFaultsProtectBeforeChangesAndExplicitResume(t *testing.T) {
	for _, fault := range []error{syscall.ENOSPC, os.ErrPermission, errors.New("anonymous unknown")} {
		t.Run(fmt.Sprint(fault), func(t *testing.T) {
			legacy, db, cat, lib, _ := fixture(t)
			b := indexed(t, db, cat, lib)
			ctx := context.Background()
			h := NewHealth(db, legacy.c, cat.Pool)
			db.Write.Exec("UPDATE search_documents SET title='before' WHERE edition_id=?", b.ID)
			p := healthPlanFor(t, h, db, "projection")
			h.BeforeIO = func(phase string) error {
				if phase == "snapshot" {
					return fault
				}
				return nil
			}
			r := healthExecute(t, h, p)
			h.Tick(ctx)
			r, _ = db.HealthRepair(ctx, r.ID)
			if r.State != "paused" {
				t.Fatal(r)
			}
			state, _ := db.HealthState(ctx)
			if state.Phase == "idle" {
				t.Fatal(state)
			}
			var title string
			db.Read.QueryRow("SELECT title FROM search_documents WHERE edition_id=?", b.ID).Scan(&title)
			if title != "before" {
				t.Fatal(title)
			}
			h.BeforeIO = nil
			if e := h.Control(ctx, r.ID, "resume"); e != nil {
				t.Fatal(e)
			}
			h.Tick(ctx)
			r, _ = db.HealthRepair(ctx, r.ID)
			if r.State != "completed" {
				t.Fatal(r)
			}
		})
	}
}
func TestHealthIOBarrierPausesTasksAndRejectsOldPublish(t *testing.T) {
	legacy, db, cat, lib, _ := fixture(t)
	b := indexed(t, db, cat, lib)
	ctx := context.Background()
	h := NewHealth(db, legacy.c, cat.Pool)
	db.Write.Exec("UPDATE search_documents SET title='before' WHERE edition_id=?", b.ID)
	for _, v := range []string{"queued", "running", "paused"} {
		if _, e := db.Write.Exec("INSERT INTO jobs(id,type,state,stage,generation,created_at,updated_at) VALUES(?,'maintenance',?,'processing',7,1,1)", "old-"+v, v); e != nil {
			t.Fatal(e)
		}
	}
	p := healthPlanFor(t, h, db, "projection")
	release, e := db.BeginHealthIO(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var snapshots atomic.Int64
	h.BeforeIO = func(phase string) error {
		if phase == "snapshot" {
			snapshots.Add(1)
		}
		return nil
	}
	r := healthExecute(t, h, p)
	done := make(chan struct{})
	go func() { h.Tick(ctx); close(done) }()
	select {
	case <-done:
		t.Fatal("IO not drained")
	case <-time.After(30 * time.Millisecond):
	}
	if snapshots.Load() != 0 {
		t.Fatal("snapshot before IO drained")
	}
	if _, e = db.Write.Exec("UPDATE libraries SET display_name='blocked'"); e == nil {
		t.Fatal("direct business SQL bypass")
	}
	if e = db.CheckExecution(ctx, model.Job{ID: "old-running", Generation: 7}); e == nil {
		t.Fatal("old worker accepted")
	}
	release()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("drain timeout")
	}
	r, _ = db.HealthRepair(ctx, r.ID)
	if r.State != "completed" || len(r.PausedJobs) != 2 {
		t.Fatal(r)
	}
	for _, state := range []string{"queued", "running", "paused"} {
		j, _ := db.Job(ctx, "old-"+state)
		want := int64(8)
		if state == "paused" {
			want = 7
		}
		if j.State != "paused" || j.Generation != want {
			t.Fatal(j)
		}
	}
	if e = db.CheckExecution(ctx, model.Job{ID: "old-running", Generation: 7}); e == nil {
		t.Fatal("stale generation revived")
	}
}
func TestHealthRollbackRejectsLaterTargetEdit(t *testing.T) {
	legacy, db, cat, lib, _ := fixture(t)
	b := indexed(t, db, cat, lib)
	ctx := context.Background()
	h := NewHealth(db, legacy.c, cat.Pool)
	db.Write.Exec("UPDATE search_documents SET title='before' WHERE edition_id=?", b.ID)
	p := healthPlanFor(t, h, db, "projection")
	r := healthExecute(t, h, p)
	h.Tick(ctx)
	if _, e := db.Write.Exec("UPDATE search_documents SET title='later-lawful' WHERE edition_id=?", b.ID); e != nil {
		t.Fatal(e)
	}
	r = healthRollback(t, h, db, r.ID)
	if r.State != "paused" || r.ErrorCode != "revision_conflict" {
		t.Fatal(r)
	}
	var got string
	db.Read.QueryRow("SELECT title FROM search_documents WHERE edition_id=?", b.ID).Scan(&got)
	if got != "later-lawful" {
		t.Fatal(got)
	}
}
func TestHealthCoverFileFailureAndRollback(t *testing.T) {
	legacy, db, cat, lib, _ := fixture(t)
	b := indexed(t, db, cat, lib)
	ctx := context.Background()
	h := NewHealth(db, legacy.c, cat.Pool)
	h.CoverBytes = cat.HealthCoverBytes
	if _, _, e := cat.Image(ctx, b.ID, b.ContentRevision, 1, true); e != nil {
		t.Fatal(e)
	}
	key, _, e := cat.HealthCoverBytes(ctx, b.ID, true)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(legacy.c.DataDir, key)
	bad := []byte("synthetic broken png")
	if e = os.WriteFile(path, bad, 0600); e != nil {
		t.Fatal(e)
	}
	p := healthPlanFor(t, h, db, "cover_cache")
	r := healthExecute(t, h, p)
	h.BeforeIO = func(phase string) error {
		if phase == "commit" {
			return syscall.EIO
		}
		return nil
	}
	h.Tick(ctx)
	r, _ = db.HealthRepair(ctx, r.ID)
	if r.State != "paused" || r.Steps[0].State != "failed" {
		t.Fatal(r)
	}
	changed, _ := os.ReadFile(path)
	if bytes.Equal(changed, bad) {
		t.Fatal("file stage did not execute")
	}
	h.BeforeIO = nil
	r = healthRollback(t, h, db, r.ID)
	if r.State != "rolled_back" {
		t.Fatal(r)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, bad) {
		t.Fatal("file before-image not restored")
	}
	storage, e := legacy.Storage(ctx)
	if e != nil || !strings.Contains(fmt.Sprint(storage), "health_rollback") {
		t.Fatal(storage, e)
	}
}

func TestHealthCommittedReceiptResumesWithoutRepeatingSQL(t *testing.T) {
	legacy, db, cat, lib, _ := fixture(t)
	b := indexed(t, db, cat, lib)
	ctx := context.Background()
	h := NewHealth(db, legacy.c, cat.Pool)
	db.Write.Exec("UPDATE search_documents SET title='before' WHERE edition_id=?", b.ID)
	p := healthPlanFor(t, h, db, "projection")
	r := healthExecute(t, h, p)
	committed := false
	failed := false
	h.BeforeIO = func(phase string) error {
		if phase == "commit" {
			committed = true
			return nil
		}
		if committed && !failed && phase == "stage" {
			failed = true
			return syscall.ENOSPC
		}
		return nil
	}
	h.Tick(ctx)
	r, _ = db.HealthRepair(ctx, r.ID)
	if r.State != "paused" || r.Steps[0].State != "failed" {
		t.Fatal(r)
	}
	var before int
	db.Read.QueryRow("SELECT count(*) FROM events WHERE type='tag_relations'").Scan(&before)
	h.BeforeIO = nil
	if e := h.Control(ctx, r.ID, "resume"); e != nil {
		t.Fatal(e)
	}
	h.Tick(ctx)
	r, _ = db.HealthRepair(ctx, r.ID)
	if r.State != "completed" {
		t.Fatal(r)
	}
	var after int
	db.Read.QueryRow("SELECT count(*) FROM events WHERE type='tag_relations'").Scan(&after)
	if after != before {
		t.Fatal("committed repair was repeated", before, after)
	}
}
func TestHealthCancelledRepairReopenKeepsProtectionUntilExplicitResume(t *testing.T) {
	legacy, db, cat, lib, _ := fixture(t)
	b := indexed(t, db, cat, lib)
	ctx := context.Background()
	h := NewHealth(db, legacy.c, cat.Pool)
	db.Write.Exec("UPDATE search_documents SET title='before' WHERE edition_id=?", b.ID)
	p := healthPlanFor(t, h, db, "projection")
	r := healthExecute(t, h, p)
	var old model.Job
	h.BeforeIO = func(phase string) error {
		if phase == "commit" {
			v, _ := db.Job(ctx, r.ID)
			old = *v
			return os.ErrPermission
		}
		return nil
	}
	h.Tick(ctx)
	if e := h.Control(ctx, r.ID, "cancel"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	again, e := store.Open(ctx, legacy.c.DataDir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer again.Close()
	state, e := again.HealthState(ctx)
	if e != nil || state.Phase == "idle" {
		t.Fatal(state, e)
	}
	if _, e = again.Write.Exec("UPDATE libraries SET display_name='blocked'"); e == nil {
		t.Fatal("reopen unlocked business writes")
	}
	next := NewHealth(again, legacy.c, cat.Pool)
	next.Tick(ctx)
	still, _ := again.HealthRepair(ctx, r.ID)
	if still.State == "completed" {
		t.Fatal("cancelled repair auto resumed")
	}
	if e = next.Control(ctx, r.ID, "resume"); e != nil {
		t.Fatal(e)
	}
	next.Tick(ctx)
	done, _ := again.HealthRepair(ctx, r.ID)
	if done.State != "completed" {
		t.Fatal(done)
	}
	if e = again.EndHealthMaintenance(ctx, old, false); e == nil {
		t.Fatal("old worker may unlock maintenance")
	}
}

func TestHealthRollbackFileFaultCanContinueInverse(t *testing.T) {
	legacy, db, cat, lib, _ := fixture(t)
	b := indexed(t, db, cat, lib)
	ctx := context.Background()
	h := NewHealth(db, legacy.c, cat.Pool)
	h.CoverBytes = cat.HealthCoverBytes
	cat.Image(ctx, b.ID, b.ContentRevision, 1, true)
	key, _, e := cat.HealthCoverBytes(ctx, b.ID, true)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(legacy.c.DataDir, key)
	bad := []byte("old damaged cover")
	os.WriteFile(path, bad, 0600)
	p := healthPlanFor(t, h, db, "cover_cache")
	r := healthExecute(t, h, p)
	h.Tick(ctx)
	r, _ = db.HealthRepair(ctx, r.ID)
	if _, e = h.Rollback(ctx, r.ID, model.HealthExecuteInput{RequestKey: "file-inverse", ExpectedRevision: r.Revision, ConfirmMaintenance: true}); e != nil {
		t.Fatal(e)
	}
	failed := false
	h.BeforeIO = func(phase string) error {
		if !failed && phase == "stage" {
			failed = true
			return os.ErrPermission
		}
		return nil
	}
	h.Tick(ctx)
	detail, e := h.Repair(ctx, r.ID)
	if e != nil || detail.State != "paused" || detail.Steps[0].State != "rollback_pending" {
		t.Fatal(detail, e)
	}
	h.BeforeIO = nil
	if e = h.Control(ctx, r.ID, "resume"); e != nil {
		t.Fatal(e)
	}
	h.Tick(ctx)
	r, _ = db.HealthRepair(ctx, r.ID)
	if r.State != "rolled_back" {
		t.Fatal(r)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, bad) {
		t.Fatal("file inverse failed")
	}
}
