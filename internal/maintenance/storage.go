package maintenance

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type storageRepository interface {
	StorageFacts(context.Context) (model.StorageFacts, error)
	CleanupProgress(context.Context, model.Job) (map[string]int64, error)
	ReserveStorageMaterial(context.Context, model.Job, model.StorageMaterial) error
	DeleteStorageFile(context.Context, model.Job, string, string, string, string, bool, int64, int64, model.StorageMaterial, func() (bool, error)) (bool, error)
	FinishStorageMaterial(context.Context, model.Job, string, int64) error
}
type storageEntry struct {
	ExplicitClear bool
	cleanupFile
	Group, Reason string
	Cleanable     bool
}

func terminalMaterial(state string) bool {
	return state == "completed" || state == "failed" || state == "cancelled" || state == "abandoned" || state == "rolled_back"
}

// Recognize older failed offline previews without inventing a database operation.
// Both the private manifest and the uploaded package's manifest must agree. Only
// known staging members will be eligible; unrelated files stay protected.
func offlineManifest(root string, trusted bool) (map[string]bool, error) {
	var m InstanceManifest
	if e := readInstanceJSON(filepath.Join(root, "manifest.json"), &m, instanceMaxManifest); e != nil {
		return nil, e
	}
	if e := validateInstanceMembers(m, DefaultInstanceBudget()); e != nil {
		return nil, e
	}
	path := filepath.Join(root, "upload.zip")
	if e := appdata.NoSymlinks(path); e != nil {
		return nil, e
	}
	z, e := zip.OpenReader(path)
	if errors.Is(e, os.ErrNotExist) && trusted {
		keys := map[string]bool{}
		for _, f := range m.Files {
			keys["validated/"+f.Key] = true
		}
		return keys, nil
	}
	if e != nil {
		return nil, e
	}
	defer z.Close()
	found := false
	for _, f := range z.File {
		if f.Name != "manifest.json" {
			continue
		}
		if found || f.UncompressedSize64 > instanceMaxManifest {
			return nil, model.ErrInstancePackage
		}
		found = true
		r, e := f.Open()
		if e != nil {
			return nil, e
		}
		raw, e := io.ReadAll(io.LimitReader(r, instanceMaxManifest+1))
		r.Close()
		if e != nil {
			return nil, e
		}
		var other InstanceManifest
		if json.Unmarshal(raw, &other) != nil {
			return nil, model.ErrInstancePackage
		}
		a, _ := json.Marshal(m)
		b, _ := json.Marshal(other)
		if string(a) != string(b) {
			return nil, model.ErrInstancePackage
		}
	}
	if !found {
		return nil, model.ErrInstancePackage
	}
	keys := map[string]bool{"upload.zip": true}
	for _, f := range m.Files {
		keys["validated/"+f.Key] = true
	}
	return keys, nil
}
func storageCategory(key string) (string, string) {
	for _, v := range []struct{ prefix, kind, group string }{
		{"cache/covers/", "covers", "rebuildable"}, {"cache/tag-images/", "tag_images", "rebuildable"},
		{"cache/tag-translations/", "translation_cache", "long_term"}, {"assets/", "assets", "long_term"},
		{"tmp/", "tmp", "temporary"}, {"upgrade-snapshots/", "snapshots", "recovery"},
		{"health/", "health_rollback", "recovery"}, {"instance-recovery/", "instance_recovery", "recovery"},
		{"cache/import-previews/", "import_preview_cache", "rebuildable"}, {"indexes/recommendations/", "recommendation_indexes", "rebuildable"},
	} {
		if strings.HasPrefix(key, v.prefix) {
			return v.kind, v.group
		}
	}
	if key == "app.sqlite" || strings.HasPrefix(key, "app.sqlite-") {
		return "database", "long_term"
	}
	return "other_application_data", "long_term"
}
func (s *Service) storageInventory(ctx context.Context) ([]storageEntry, error) {
	repo, ok := s.repo.(storageRepository)
	if !ok {
		return nil, model.ErrInput
	}
	facts, e := repo.StorageFacts(ctx)
	if e != nil {
		return nil, e
	}
	var activation instanceActivation
	ae := readInstanceJSON(activationPath(s.c), &activation, instanceMaxManifest)
	activationUnknown := ae != nil && !errors.Is(ae, os.ErrNotExist)
	latest, e := latestUpgradeSnapshot(s.c.DataDir)
	if e != nil {
		return nil, e
	}

	originals, originalsError := s.loadOriginalReceipt()
	offline := map[string]map[string]bool{}
	entries := []storageEntry{}
	e = filepath.WalkDir(s.c.DataDir, func(path string, d fs.DirEntry, x error) error {
		if x != nil {
			return x
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return model.ErrInput
		}
		i, x := d.Info()
		if x != nil {
			return x
		}
		rel, x := filepath.Rel(s.c.DataDir, path)
		if x != nil {
			return x
		}
		key := filepath.ToSlash(rel)
		kind, group := storageCategory(key)
		v := storageEntry{cleanupFile: cleanupFile{Kind: kind, Key: key, Size: i.Size(), Mtime: i.ModTime().UnixNano()}, Group: group, Reason: "retained_application_data"}
		switch kind {
		case "covers", "tag_images":
			v.Cleanable = !facts.Caches[key]
			v.Reason = "valid_cache"
			if v.Cleanable {
				v.Reason = "redundant_cache"
			}
		case "import_preview_cache":
			v.ExplicitClear = facts.ActiveTasks == 0
			v.Reason = "valid_cache"
		case "recommendation_indexes":
			v.ExplicitClear = facts.ActiveTasks == 0 && strings.HasPrefix(key, "indexes/recommendations/features/") && strings.HasSuffix(key, ".json")
			v.Reason = "retained_recommendation_evidence"
			if v.ExplicitClear {
				v.Reason = "valid_feature_cache"
			}

		case "assets":
			a := facts.Assets[key]
			v.AssetID = a.ID
			v.Reason = "referenced_asset"
			if !a.Referenced {
				v.Cleanable = true
				v.Reason = "unreferenced_asset"
				v.Kind = "orphan_assets"
			}
			if strings.HasPrefix(key, "assets/treec-originals/") {
				v.Kind = "migration_originals"
				v.Cleanable = false
				v.Reason = "archive_verification_required"
				if !a.Referenced && originalsError == nil && originalReceiptMatches(originals, a) == nil {
					v.Cleanable = true
					v.Reason = "verified_external_original"
				}
			}
			if time.Since(i.ModTime()) < 24*time.Hour && v.Kind == "orphan_assets" {
				v.Cleanable = false
				v.Reason = "recent_upload"
			}
		case "tmp":
			v.Cleanable = facts.ActiveTasks == 0 && time.Since(i.ModTime()) >= 24*time.Hour && !strings.HasPrefix(d.Name(), "pending-")
			v.Reason = "active_or_recent_temporary"
			if v.Cleanable {
				v.Reason = "expired_temporary"
			}
		case "snapshots":
			v.Cleanable = filepath.Ext(d.Name()) == ".sqlite" && d.Name() != latest && !strings.HasPrefix(d.Name(), "pending-")
			v.Reason = "latest_upgrade_snapshot"
			if v.Cleanable {
				v.Reason = "older_upgrade_snapshot"
			}
		case "health_rollback":
			id := facts.HealthArtifacts[key]
			state := facts.Health[id]
			if id != "" {
				v.Material = model.StorageMaterial{ID: "health:" + id, Kind: kind, OperationID: id, State: state}
			}
			if id != "" && (state == "completed" || state == "rolled_back") && facts.HealthOwner != id {
				v.Cleanable = true
				v.Reason = "finished_health_rollback"
				v.Material = model.StorageMaterial{ID: "health:" + id, Kind: kind, OperationID: id, State: state}
			}
		case "instance_recovery":
			parts := strings.SplitN(strings.TrimPrefix(key, "instance-recovery/"), "/", 2)
			if len(parts) != 2 {
				break
			}
			id, member := parts[0], parts[1]
			op, known := facts.Operations[id]
			if known {
				v.Material = model.StorageMaterial{ID: "instance:" + id, Kind: kind, OperationID: id, State: op.State, Revision: op.Revision}
			} else if activation.ID == id {
				v.Material = model.StorageMaterial{ID: "instance:" + id, Kind: kind, OperationID: id, State: activation.State, Revision: activation.Revision}
			}
			if op.Kind == "backup" && member == "archive.zip" {
				v.Kind = "user_backups"
				v.Group = "long_term"
				v.Reason = "user_backup"
				break
			}
			if activationUnknown || facts.InstanceOwner == id || activation.ID == id && !(activation.State == "completed" || activation.State == "rolled_back" && activation.RollbackFinished) {
				v.Reason = "active_instance_operation"
				break
			}
			m := model.StorageMaterial{ID: "instance:" + id, Kind: kind, OperationID: id, State: op.State, Revision: op.Revision}
			allowed := false
			if known && terminalMaterial(op.State) {
				// These names match actual backup/upload/activation writers.
				if op.Kind == "backup" {
					allowed = member == "archive.partial" || strings.HasPrefix(member, "stage/")
				}
				if op.Kind == "restore" {
					allowed = member == "upload.partial" || member == "upload.zip" || strings.HasPrefix(member, "validated/") || strings.HasPrefix(member, "prepared/") || strings.HasPrefix(member, "before-confirm/") || strings.HasPrefix(member, "before-activation/") || strings.HasPrefix(member, "before-rollback/")
				}
			} else if !known && strings.HasPrefix(id, "offline-") {
				members, seen := offline[id]
				if !seen {
					members, _ = offlineManifest(filepath.Join(s.c.DataDir, "instance-recovery", id), facts.Receipts["instance:"+id] != "")
					offline[id] = members
				}
				allowed = members[member]
				m.State = "offline_unconfirmed"
				if members != nil {
					v.Material = m
				}
				// A completed offline activation has additional prepared and before material.
				if activation.ID == id && (activation.State == "completed" || activation.State == "rolled_back" && activation.RollbackFinished) {
					for _, f := range activation.Incoming {
						if member == "prepared/"+f.Key {
							allowed = true
						}
					}
					for _, f := range activation.RollbackCurrent {
						if member == "before-rollback/"+f.Key {
							allowed = true
						}
					}
					for _, f := range activation.Before {
						if member == "before-activation/"+f.Key || member == "before-confirm/"+f.Key {
							allowed = true
						}
					}
				}
			}
			if allowed {
				v.Cleanable = true
				v.Material = m
				v.Reason = "finished_instance_material"
				if m.State == "offline_unconfirmed" {
					v.Reason = "unconfirmed_offline_staging"
				}
				if activation.ID == id && activation.State == "completed" && facts.Epoch == 0 {
					v.Reason = "removes_rollback_point"
				}
			}
		}
		if strings.HasPrefix(d.Name(), "pending-") {
			v.Cleanable = false
			v.Reason = "pending_write"
		}
		entries = append(entries, v)
		if len(entries) > 1000000 {
			return model.ErrCapacity
		}
		return nil
	})
	return entries, e
}
func (s *Service) Storage(ctx context.Context) (map[string]any, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return nil, e
	}
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	if len(s.storageCache) > 0 && time.Since(s.storageAt) < 30*time.Second {
		var out map[string]any
		e := json.Unmarshal(s.storageCache, &out)
		return out, e
	}
	entries, e := s.storageInventory(ctx)
	if e != nil {
		return nil, e
	}
	out := storageSummary(entries)
	s.storageCache, _ = json.Marshal(out)
	s.storageAt = time.Now()
	var response map[string]any
	e = json.Unmarshal(s.storageCache, &response)
	return response, e
}
func storageSummary(entries []storageEntry) map[string]any {
	categories := map[string]map[string]any{}
	groups := map[string]map[string]any{}
	var total, clean int64
	add := func(m map[string]any, v storageEntry) {
		m["bytes"] = m["bytes"].(int64) + v.Size
		m["files"] = m["files"].(int) + 1
		if v.Cleanable {
			m["cleanable_bytes"] = m["cleanable_bytes"].(int64) + v.Size
			m["cleanable_files"] = m["cleanable_files"].(int) + 1
		} else {
			m["protected_bytes"] = m["protected_bytes"].(int64) + v.Size
			m["protected_files"] = m["protected_files"].(int) + 1
		}
		r := m["reasons"].(map[string]int64)
		r[v.Reason] += v.Size
	}
	empty := func(id string) map[string]any {
		return map[string]any{"kind": id, "bytes": int64(0), "files": 0, "cleanable_bytes": int64(0), "cleanable_files": 0, "protected_bytes": int64(0), "protected_files": 0, "reasons": map[string]int64{}}
	}
	for _, id := range []string{"long_term", "rebuildable", "temporary", "recovery"} {
		groups[id] = empty(id)
	}
	operations := map[string]map[string]any{}
	for _, v := range entries {
		kind := v.Kind
		if kind == "orphan_assets" || kind == "migration_originals" {
			kind = "assets"
		}
		if categories[kind] == nil {
			categories[kind] = empty(kind)
			categories[kind]["group"] = v.Group
		}
		add(categories[kind], v)
		add(groups[v.Group], v)
		total += v.Size
		if v.Cleanable {
			clean += v.Size
		}
		if v.Material.ID != "" {
			if operations[v.Material.ID] == nil {
				m := empty(v.Material.ID)
				m["id"] = v.Material.ID
				m["kind"] = v.Material.Kind
				m["operation_id"] = v.Material.OperationID
				m["state"] = v.Material.State
				m["revision"] = v.Material.Revision
				operations[v.Material.ID] = m
			}
			add(operations[v.Material.ID], v)
		}
	}
	sorted := func(m map[string]map[string]any) []map[string]any {
		keys := []string{}
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := []map[string]any{}
		for _, k := range keys {
			out = append(out, m[k])
		}
		return out
	}
	out := map[string]any{"categories": sorted(categories), "groups": sorted(groups), "total_bytes": total, "cleanable_bytes": clean, "protected_bytes": total - clean, "statistics_at": time.Now().Unix(), "cache_seconds": 30, "operations": operations, "protected": []string{"referenced_assets", "unverified_migration_originals", "latest_upgrade_snapshot", "active_tasks", "unknown_data"}, "suggested_categories": []string{"redundant_covers", "redundant_tag_images", "instance_recovery", "health_rollback", "migration_originals", "tmp", "snapshots"}}
	return out
}
func (s *Service) invalidateStorage() { s.storageMu.Lock(); s.storageCache = nil; s.storageMu.Unlock() }

func (s *Service) RefreshStorage(ctx context.Context) (map[string]any, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return nil, e
	}
	s.invalidateStorage()
	return s.Storage(ctx)
}

func latestUpgradeSnapshot(dir string) (string, error) {
	latest := ""
	var newest time.Time
	entries, e := os.ReadDir(filepath.Join(dir, "upgrade-snapshots"))
	if errors.Is(e, os.ErrNotExist) {
		return "", nil
	}
	if e != nil {
		return "", e
	}
	for _, d := range entries {
		if !d.Type().IsRegular() || filepath.Ext(d.Name()) != ".sqlite" || strings.HasPrefix(d.Name(), "pending-") {
			continue
		}
		i, e := d.Info()
		if e != nil {
			return "", e
		}
		if latest == "" || i.ModTime().After(newest) {
			latest = d.Name()
			newest = i.ModTime()
		}
	}
	return latest, nil
}
