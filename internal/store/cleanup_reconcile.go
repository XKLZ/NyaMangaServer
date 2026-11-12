package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/model"
	"path/filepath"
	"time"
)

type CleanupReconciliation struct {
	JobID          string `json:"job_id"`
	PlanID         string `json:"plan_id"`
	Applied        bool   `json:"applied"`
	AlreadyApplied bool   `json:"already_applied"`
	DeletedFiles   int64  `json:"deleted_files"`
	ReleasedBytes  int64  `json:"released_bytes"`
	EvidenceSHA256 string `json:"evidence_sha256"`
}

// Caller MUST hold appdata's stopped-instance lock. This entry point never opens
// the ordinary Store: no migrations, task recovery, backfills or file deletions.
func ReconcileCleanup(ctx context.Context, dir, id string, apply bool, files fs.FS) (CleanupReconciliation, error) {
	out := CleanupReconciliation{JobID: id}
	if id == "" {
		return out, model.ErrInput
	}
	path := filepath.Join(dir, "app.sqlite")
	if e := appdata.NoSymlinks(path); e != nil {
		return out, e
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	if apply {
		q.Set("mode", "rw")
		q.Set("_txlock", "immediate")
	}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return out, e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var app, baseline string
	if e = tx.QueryRowContext(ctx, "SELECT application,baseline FROM application_identity WHERE singleton=1").Scan(&app, &baseline); e != nil {
		return out, e
	}
	if app != ApplicationID || baseline != BaselineID {
		return out, model.ErrInput
	}
	if e = validateOfflineMaintenanceLedger(ctx, tx, files); e != nil {
		return out, e
	}
	var busy int
	if e = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM jobs WHERE state IN('running','queued'))+(SELECT count(*) FROM health_state WHERE phase!='idle')+(SELECT count(*) FROM instance_state WHERE owner IS NOT NULL)+(SELECT count(*) FROM job_resources WHERE job_id=?)`, id).Scan(&busy); e != nil {
		return out, e
	}
	if busy != 0 {
		return out, model.ErrBusy
	}
	var state, stage, code, kind string
	var generation, done, failed, total, updated int64
	if e = tx.QueryRowContext(ctx, "SELECT type,state,stage,COALESCE(error_code,''),generation,done,failed,total,updated_at FROM jobs WHERE id=?", id).Scan(&kind, &state, &stage, &code, &generation, &done, &failed, &total, &updated); e != nil {
		return out, e
	}
	var planCount int
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM maintenance_plans WHERE job_id=?", id).Scan(&planCount); e != nil {
		return out, e
	}
	if planCount != 1 {
		return out, model.ErrConflict
	}
	var input, summary string
	if e = tx.QueryRowContext(ctx, "SELECT id,input_json,summary_json FROM maintenance_plans WHERE job_id=? AND operation='cleanup'", id).Scan(&out.PlanID, &input, &summary); e != nil {
		return out, e
	}
	var envelope struct {
		Summary map[string]json.RawMessage `json:"summary"`
	}
	if e = json.Unmarshal([]byte(summary), &envelope); e != nil {
		return out, e
	}
	var in model.MaintenanceInput
	if e = json.Unmarshal([]byte(input), &in); e != nil {
		return out, e
	}
	if in.Operation != "cleanup" {
		return out, model.ErrConflict
	}
	var frozen struct {
		Files []struct {
			Size     int64                 `json:"size"`
			Key      string                `json:"key"`
			Material model.StorageMaterial `json:"material"`
		} `json:"_cleanup_files"`
	}
	if e = json.Unmarshal(in.Options, &frozen); e != nil {
		return out, e
	}
	if len(frozen.Files) == 0 || len(frozen.Files) > 100000 {
		return out, model.ErrConflict
	}
	counts, e := decodeCleanupCounts(string(envelope.Summary["cleanup_result"]))
	if e != nil {
		return out, e
	}
	materials := map[string]int64{}
	manifests := map[string]model.StorageMaterial{}
	seen := map[string]bool{}
	var bytes int64
	for _, f := range frozen.Files {
		if f.Size < 0 || f.Size > 1<<40 || seen[f.Key] || !fs.ValidPath(f.Key) {
			return out, model.ErrConflict
		}
		seen[f.Key] = true
		bytes += f.Size
		if bytes > 1<<40 {
			return out, model.ErrConflict
		}
		if f.Material.ID != "" {
			if old, ok := manifests[f.Material.ID]; ok && old != f.Material {
				return out, model.ErrConflict
			}
			manifests[f.Material.ID] = f.Material
			materials[f.Material.ID] += f.Size
		}
	}
	if counts["next_index"] != int64(len(frozen.Files)) || counts["deleted_files"] != int64(len(frozen.Files)) || counts["released_bytes"] != bytes || counts["failed_files"] != 0 || counts["skipped_files"] != 0 {
		return out, model.ErrConflict
	}
	for material, n := range materials {
		var st, manifest string
		var released int64
		if e = tx.QueryRowContext(ctx, "SELECT state,released_bytes,manifest_json FROM storage_material_receipts WHERE id=?", material).Scan(&st, &released, &manifest); e != nil {
			return out, e
		}
		var recorded model.StorageMaterial
		if json.Unmarshal([]byte(manifest), &recorded) != nil || recorded != manifests[material] || st != "cleaned" || released != n {
			return out, model.ErrConflict
		}
	}
	out.DeletedFiles = counts["deleted_files"]
	out.ReleasedBytes = counts["released_bytes"]
	out.EvidenceSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(input+string(envelope.Summary["cleanup_result"]))))
	var itemState, itemCode, itemPlan string
	var itemCount int
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM job_items WHERE job_id=?", id).Scan(&itemCount); e != nil {
		return out, e
	}
	if itemCount != 1 {
		return out, model.ErrConflict
	}
	if e = tx.QueryRowContext(ctx, "SELECT state,COALESCE(error_code,''),json_extract(input_json,'$.plan_id') FROM job_items WHERE job_id=? AND ordinal=1", id).Scan(&itemState, &itemCode, &itemPlan); e != nil {
		return out, e
	}
	if itemPlan != out.PlanID || kind != "maintenance" || stage != "finished" || done != 1 || total != 1 {
		return out, model.ErrConflict
	}
	if audit, ok := envelope.Summary["cleanup_reconciliation"]; ok {
		var prior struct {
			Evidence string `json:"evidence_sha256"`
		}
		if json.Unmarshal(audit, &prior) != nil || prior.Evidence == "" || state != "completed" || code != "" || failed != 0 || itemState != "completed" || itemCode != "" {
			return out, model.ErrConflict
		}
		out.AlreadyApplied = true
		out.EvidenceSHA256 = prior.Evidence
		return out, nil
	}
	if state != "failed" || code != "preview_expired" || failed != 1 || itemState != "failed" || itemCode != "preview_expired" {
		return out, model.ErrConflict
	}
	if !apply {
		return out, nil
	}
	audit, _ := json.Marshal(map[string]any{"original_state": state, "original_error": code, "original_item_state": itemState, "original_item_error": itemCode, "original_generation": generation, "original_updated_at": updated, "corrected_at": time.Now().Unix(), "evidence_sha256": out.EvidenceSHA256, "basis": "all_frozen_entries_deleted_and_material_receipts_completed", "version": 1})
	if _, e = tx.ExecContext(ctx, `UPDATE maintenance_plans SET summary_json=json_set(summary_json,'$.summary.cleanup_reconciliation',json(?),'$.summary.cleanup_result.phase','finished','$.summary.cleanup_result.status','completed','$.summary.cleanup_result.error_code','') WHERE id=?`, string(audit), out.PlanID); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE job_items SET state='completed',error_code='' WHERE job_id=? AND ordinal=1", id); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE jobs SET state='completed',error_code='',failed=0,generation=generation+1,updated_at=? WHERE id=?", time.Now().Unix(), id); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE instance_state SET write_epoch=write_epoch+1 WHERE singleton=1"); e != nil {
		return out, e
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	out.Applied = true
	return out, nil
}
