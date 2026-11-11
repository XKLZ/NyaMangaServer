package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"os"
	"strings"
	"time"
)

const assetReferencedSQL = `EXISTS(SELECT 1 FROM cover_selections c WHERE c.asset_id=a.id) OR EXISTS(SELECT 1 FROM recommendation_materials r WHERE r.asset_id=a.id) OR EXISTS(SELECT 1 FROM tag_images t WHERE t.asset_id=a.id)`

// Use the same cache identity rules for statistics and the deletion transaction.
// Rows are closed before filesystem callbacks; no nested connection is acquired.
func storageCacheKeys(ctx context.Context, q queryer, kind string) (map[string]bool, error) {
	caches := map[string]bool{}
	read := func(query string, visit func(*sql.Rows) error) error {
		rows, e := scopeRows(ctx, q, query)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			if e = visit(rows); e != nil {
				return e
			}
		}
		return rows.Err()
	}
	var e error
	if kind == "" || kind == "covers" {
		e = read(`SELECT f.edition_id,f.content_revision,f.parsing_revision,COALESCE(c.mode,'auto'),COALESCE(c.revision,0),COALESCE(c.parsing_revision,1),f.legacy_cover_sha1 FROM files f LEFT JOIN cover_selections c ON c.edition_id=f.edition_id`, func(r *sql.Rows) error {
			var id, mode string
			var content, parsing, selection, selectedParsing int64
			var legacy *string
			if e := r.Scan(&id, &content, &parsing, &mode, &selection, &selectedParsing, &legacy); e != nil {
				return e
			}
			if mode == "auto" {
				selectedParsing = parsing
			}
			name := model.SelectedCoverCacheName(id, content, selectedParsing, selection, mode)
			caches["cache/covers/v1/"+name[:2]+"/"+name] = true
			if mode == "auto" && legacy != nil {
				sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:page=0:sha1=%s:320x480:nearest:png:v1", id, content, *legacy)))
				name = fmt.Sprintf("%x.png", sum)
				caches["cache/covers/v1/"+name[:2]+"/"+name] = true
			}
			return nil
		})
		if e != nil {
			return nil, e
		}
	}
	if kind == "" || kind == "tag_images" {
		e = read(`SELECT a.digest,t.role FROM tag_images t JOIN assets a ON a.id=t.asset_id`, func(r *sql.Rows) error {
			var digest, role string
			if e := r.Scan(&digest, &role); e != nil {
				return e
			}
			name := model.TagImageCacheName(digest, role)
			caches["cache/tag-images/v1/"+name[:2]+"/"+name] = true
			return nil
		})
		if e != nil {
			return nil, e
		}
	}
	return caches, nil
}

// A bounded number of batched queries; no per-file SQL during statistics polling.
func (s *Store) StorageFacts(ctx context.Context) (model.StorageFacts, error) {
	out := model.StorageFacts{Assets: map[string]model.StorageAsset{}, Caches: map[string]bool{}, Operations: map[string]model.StorageOperation{}, Health: map[string]string{}, HealthArtifacts: map[string]string{}, Receipts: map[string]string{}}
	if e := s.RequireAdministrator(ctx); e != nil {
		return out, e
	}

	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	read := func(query string, visit func(*sql.Rows) error) error {
		rows, e := scopeRows(ctx, tx, query)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			if e = visit(rows); e != nil {
				return e
			}
		}
		return rows.Err()
	}
	e = read("SELECT id,storage_key,digest,size,("+assetReferencedSQL+") FROM assets a", func(r *sql.Rows) error {
		var a model.StorageAsset
		if e := r.Scan(&a.ID, &a.Key, &a.Digest, &a.Size, &a.Referenced); e != nil {
			return e
		}
		out.Assets[a.Key] = a
		return nil
	})
	if e != nil {
		return out, e
	}
	out.Caches, e = storageCacheKeys(ctx, tx, "")
	if e != nil {
		return out, e
	}
	e = read("SELECT id,kind,state,revision FROM instance_operations", func(r *sql.Rows) error {
		var id string
		var v model.StorageOperation
		if e := r.Scan(&id, &v.Kind, &v.State, &v.Revision); e != nil {
			return e
		}
		out.Operations[id] = v
		return nil
	})
	if e != nil {
		return out, e
	}
	e = read("SELECT id,state FROM health_repairs", func(r *sql.Rows) error {
		var id, state string
		if e := r.Scan(&id, &state); e != nil {
			return e
		}
		out.Health[id] = state
		return nil
	})
	if e != nil {
		return out, e
	}
	e = read("SELECT key,repair_id FROM health_artifacts", func(r *sql.Rows) error {
		var key, id string
		if e := r.Scan(&key, &id); e != nil {
			return e
		}
		out.HealthArtifacts[key] = id
		return nil
	})
	if e != nil {
		return out, e
	}
	e = read("SELECT id,state FROM storage_material_receipts", func(r *sql.Rows) error {
		var id, state string
		if e := r.Scan(&id, &state); e != nil {
			return e
		}
		out.Receipts[id] = state
		return nil
	})
	if e != nil {
		return out, e
	}
	if e = scopeRow(ctx, tx, "SELECT COALESCE(owner,''),write_epoch FROM instance_state WHERE singleton=1").Scan(&out.InstanceOwner, &out.Epoch); e != nil {
		return out, e
	}
	if e = scopeRow(ctx, tx, "SELECT COALESCE(repair_id,'') FROM health_state WHERE singleton=1 AND phase!='idle'").Scan(&out.HealthOwner); e != nil && e != sql.ErrNoRows {
		return out, e
	}
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM jobs WHERE state IN('queued','running','paused')").Scan(&out.ActiveTasks); e != nil {
		return out, e
	}

	return out, tx.Commit()
}

func storageIdle(ctx context.Context, tx *sql.Tx) error {
	var busy int
	if e := scopeRow(ctx, tx, `SELECT (SELECT count(*) FROM instance_state WHERE owner IS NOT NULL)+(SELECT count(*) FROM health_state WHERE phase!='idle')`).Scan(&busy); e != nil {
		return e
	}
	if busy > 0 {
		return model.ErrBusy
	}
	return nil
}
func storageMaterialGuard(ctx context.Context, tx *sql.Tx, m model.StorageMaterial) error {
	if m.Kind == "instance_recovery" && m.State == "offline_unconfirmed" {
		var n int
		if e := scopeRow(ctx, tx, "SELECT count(*) FROM instance_operations WHERE id=?", m.OperationID).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return model.ErrConflict
		}
	}
	if m.Kind == "instance_recovery" && m.State != "offline_unconfirmed" {
		var state string
		var rev int64
		if e := scopeRow(ctx, tx, "SELECT state,revision FROM instance_operations WHERE id=?", m.OperationID).Scan(&state, &rev); e != nil {
			return e
		}
		if state != m.State || rev != m.Revision || !(state == "completed" || state == "failed" || state == "cancelled" || state == "abandoned" || state == "rolled_back") {
			return model.ErrConflict
		}
	}
	if m.Kind == "health_rollback" {
		var state string
		if e := scopeRow(ctx, tx, "SELECT state FROM health_repairs WHERE id=?", m.OperationID).Scan(&state); e != nil {
			return e
		}
		if state != m.State || !(state == "completed" || state == "rolled_back") {
			return model.ErrConflict
		}
	}
	return nil
}

// Persist rollback unavailability before removing the first byte. A crash leaves
// 'cleaning', which is deliberately as protective as 'cleaned'.
func (s *Store) ReserveStorageMaterial(ctx context.Context, j model.Job, m model.StorageMaterial) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	if e = storageIdle(ctx, tx); e != nil {
		return e
	}
	if e = storageMaterialGuard(ctx, tx, m); e != nil {
		return e
	}
	raw, _ := json.Marshal(m)
	_, e = tx.ExecContext(ctx, `INSERT INTO storage_material_receipts(id,kind,operation_id,state,manifest_json,updated_at) VALUES(?,?,?,'cleaning',?,unixepoch()) ON CONFLICT(id) DO NOTHING`, m.ID, m.Kind, m.OperationID, string(raw))
	if e != nil {
		return e
	}
	return tx.Commit()
}

// The writer transaction excludes new references and operation activation until
// the identity-checked filesystem callback and asset pruning are complete.
func (s *Store) DeleteStorageFile(ctx context.Context, j model.Job, kind, key, assetID, digest string, onlyRedundant bool, ordinal, size int64, m model.StorageMaterial, remove func() (bool, error)) (bool, error) {
	result, e := s.DeleteStorageBatch(ctx, j, []model.StorageDeletion{{Kind: kind, Key: key, AssetID: assetID, Digest: digest, OnlyRedundant: onlyRedundant, Ordinal: ordinal, Size: size, Material: m, Remove: remove}})
	return len(result) > 0 && result[0], e
}

// Hashing and manifest parsing happen before admission. The writer lock is held
// only for reference/identity checks and unlink. Progress is committed once per
// bounded batch; interrupted, uncommitted unlinks never claim released bytes.
func (s *Store) DeleteStorageBatch(ctx context.Context, j model.Job, files []model.StorageDeletion) ([]bool, error) {
	if len(files) == 0 || len(files) > 64 {
		return nil, model.ErrInput
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return nil, e
	}
	if e = storageIdle(ctx, tx); e != nil {
		return nil, e
	}
	var raw string
	if e = scopeRow(ctx, tx, "SELECT COALESCE(json_extract(summary_json,'$.summary.cleanup_result'),'{}') FROM maintenance_plans WHERE job_id=?", j.ID).Scan(&raw); e != nil {
		return nil, e
	}
	counts, e := decodeCleanupCounts(raw)
	if e != nil {
		return nil, e
	}
	started := time.Now()
	caches, e := storageCandidateCacheKeys(ctx, tx, files)
	if e != nil {
		return nil, e
	}
	result := []bool{}
	released := map[string]int64{}
	for _, f := range files {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		if e = guard(ctx, tx, j); e != nil {
			return nil, e
		}
		if f.Ordinal != counts["next_index"] {
			return nil, model.ErrConflict
		}
		removed, absent, err := deleteStorageFile(ctx, tx, j, f, caches)
		if err != nil {
			return nil, err
		}
		result = append(result, removed)
		counts["next_index"]++
		if removed {
			counts["deleted_files"]++
			counts["released_bytes"] += f.Size
			released[f.Material.ID] += f.Size
		} else {
			counts["skipped_files"]++
			if absent {
				counts["unconfirmed_files"]++
			}
		}
		if time.Since(started) >= 250*time.Millisecond {
			break
		}
	}
	counts["committed_batches"]++
	if caches != nil {
		counts["cache_reference_queries"]++
	}
	if e = writeCleanupCounts(ctx, tx, j.ID, counts); e != nil {
		return nil, e
	}
	for id, n := range released {
		if id != "" {
			if _, e = tx.ExecContext(ctx, "UPDATE storage_material_receipts SET released_bytes=released_bytes+?,updated_at=unixepoch() WHERE id=?", n, id); e != nil {
				return nil, e
			}
		}
	}
	return result, tx.Commit()
}
func deleteStorageFile(ctx context.Context, tx *sql.Tx, j model.Job, f model.StorageDeletion, caches map[string]bool) (bool, bool, error) {
	kind, key, assetID, digest, onlyRedundant, size, m, remove := f.Kind, f.Key, f.AssetID, f.Digest, f.OnlyRedundant, f.Size, f.Material, f.Remove
	var e error
	finish := func(removed bool) (bool, bool, error) { return removed, false, nil }

	if onlyRedundant && (kind == "covers" || kind == "tag_images") {
		if caches[key] {
			return finish(false)
		}
	}
	if m.ID != "" {
		if e = storageMaterialGuard(ctx, tx, m); e != nil {
			return false, false, e
		}
	}
	if m.Kind == "health_rollback" {
		var matches int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM health_artifacts WHERE key=? AND repair_id=? AND digest=? AND size=?", key, m.OperationID, digest, size).Scan(&matches); e != nil {
			return false, false, e
		}
		if matches != 1 {
			return finish(false)
		}
	}
	if kind == "tmp" || kind == "recommendation_indexes" || kind == "import_preview_cache" {
		var active int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM jobs WHERE id<>? AND state IN('queued','running','paused')", j.ID).Scan(&active); e != nil {
			return false, false, e
		}
		if active > 0 {
			return finish(false)
		}
	}

	if strings.HasPrefix(key, "assets/") {
		var currentID, currentDigest string
		err := scopeRow(ctx, tx, "SELECT id,digest FROM assets WHERE storage_key=?", key).Scan(&currentID, &currentDigest)
		if err != nil && err != sql.ErrNoRows {
			return false, false, err
		}
		if currentID != assetID || currentID != "" && currentDigest != digest {
			return finish(false)
		}

		var referenced int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM assets a WHERE storage_key=? AND ("+assetReferencedSQL+")", key).Scan(&referenced); e != nil {
			return false, false, e
		}
		if referenced > 0 {
			return finish(false)
		}
		if strings.HasPrefix(key, "assets/treec-originals/") && kind != "migration_originals" {
			return finish(false)
		}
	}
	removed, e := remove()
	// Missing is distinct from a protected/replaced object. An interrupted unlink
	// must retire its old registration even though released bytes are unconfirmed.
	absent := errors.Is(e, os.ErrNotExist)
	if e != nil && !absent {
		return false, false, e
	}
	retired := removed || absent
	if retired && m.Kind == "health_rollback" {
		if _, e = tx.ExecContext(ctx, "UPDATE health_artifacts SET pinned=0 WHERE repair_id=? AND key=? AND digest=? AND size=?", m.OperationID, key, digest, size); e != nil {
			return false, false, e
		}
	}
	if retired && strings.HasPrefix(key, "assets/") {
		if _, e = tx.ExecContext(ctx, "DELETE FROM assets WHERE storage_key=?", key); e != nil {
			return false, false, e
		}
	}
	if retired && kind == "recommendation_indexes" {
		if _, e = tx.ExecContext(ctx, "DELETE FROM recommendation_features WHERE 'indexes/recommendations/features/'||algorithm||'/'||feature_fingerprint||'.json'=?", key); e != nil {
			return false, false, e
		}
	}

	return removed, absent, nil
}
func (s *Store) FinishStorageMaterial(ctx context.Context, j model.Job, id string, released int64) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, "UPDATE storage_material_receipts SET state='cleaned',released_bytes=released_bytes+?,updated_at=unixepoch() WHERE id=?", released, id)
	if e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) RecordCleanupResult(ctx context.Context, j model.Job, counts map[string]int64) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	if e = writeCleanupCounts(ctx, tx, j.ID, counts); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) CleanupProgress(ctx context.Context, j model.Job) (map[string]int64, error) {
	var raw string
	e := scopeRow(ctx, s.Read, "SELECT COALESCE(json_extract(summary_json,'$.summary.cleanup_result'),'{}') FROM maintenance_plans WHERE job_id=?", j.ID).Scan(&raw)
	if e != nil {
		return nil, e
	}
	return decodeCleanupCounts(raw)
}

func decodeCleanupCounts(raw string) (map[string]int64, error) {
	fields := map[string]json.RawMessage{}
	if e := json.Unmarshal([]byte(raw), &fields); e != nil {
		return nil, e
	}
	out := map[string]int64{}
	for _, key := range []string{"deleted_files", "released_bytes", "skipped_files", "failed_files", "next_index", "unconfirmed_files", "committed_batches", "cache_reference_queries"} {
		out[key] = 0
		if v, ok := fields[key]; ok {
			var n int64
			if e := json.Unmarshal(v, &n); e != nil || n < 0 {
				return nil, model.ErrInput
			}
			out[key] = n
		}
	}
	return out, nil
}
func writeCleanupCounts(ctx context.Context, tx *sql.Tx, id string, counts map[string]int64) error {
	raw, _ := json.Marshal(counts)
	_, e := tx.ExecContext(ctx, `UPDATE maintenance_plans SET summary_json=json_set(summary_json,'$.summary.cleanup_result',json_patch(COALESCE(json_extract(summary_json,'$.summary.cleanup_result'),'{}'),json(?))) WHERE job_id=?`, string(raw), id)
	return e
}

func (s *Store) CleanupPhase(ctx context.Context, j model.Job, phase string) error {
	if phase != "deleting" && phase != "finalizing" {
		return model.ErrInput
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE maintenance_plans SET summary_json=json_set(summary_json,'$.summary.cleanup_result.phase',?,'$.summary.cleanup_result.status','running','$.summary.cleanup_result.error_code','','$.summary.cleanup_result.failed_files',0) WHERE job_id=? AND operation='cleanup'`, phase, j.ID)
	if e != nil {
		return e
	}
	return tx.Commit()
}

// Final status and job state commit together, including a failure after all
// unlinks. Receipt/history failures are finalization failures, never file errors.
func finishCleanup(ctx context.Context, tx *sql.Tx, j model.Job, code string) error {
	var raw string
	e := scopeRow(ctx, tx, "SELECT COALESCE(json_extract(summary_json,'$.summary.cleanup_result'),'{}') FROM maintenance_plans WHERE job_id=? AND operation='cleanup'", j.ID).Scan(&raw)
	if e == sql.ErrNoRows {
		return nil
	}
	if e != nil {
		return e
	}
	counts, e := decodeCleanupCounts(raw)
	if e != nil {
		return e
	}
	var status struct {
		Phase string `json:"phase"`
	}
	if e = json.Unmarshal([]byte(raw), &status); e != nil {
		return e
	}
	state := "completed"
	phase := status.Phase
	if code != "" {
		state = "failed"
		if phase == "finalizing" {
			state = "finalization_failed"
		}
	} else {
		phase = "finished"
		if counts["skipped_files"] > 0 || counts["failed_files"] > 0 {
			state = "partial"
		}
	}
	_, e = tx.ExecContext(ctx, `UPDATE maintenance_plans SET summary_json=json_set(summary_json,'$.summary.cleanup_result.phase',?,'$.summary.cleanup_result.status',?,'$.summary.cleanup_result.error_code',?) WHERE job_id=?`, phase, state, code, j.ID)
	return e
}
