package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

type cleanupFile struct {
	AssetID  string                `json:"asset_id,omitempty"`
	Kind     string                `json:"kind"`
	Key      string                `json:"key"`
	Size     int64                 `json:"size"`
	Mtime    int64                 `json:"mtime_ns"`
	Digest   string                `json:"sha256"`
	Device   uint64                `json:"device"`
	Inode    uint64                `json:"inode"`
	Material model.StorageMaterial `json:"material"`
}

func fileIdentity(i fs.FileInfo) (uint64, uint64) {
	if st, ok := i.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev), st.Ino
	}
	return 0, 0
}
func cleanupSelected(v storageEntry, kinds []string) bool {
	if v.Kind == "covers" {
		return slices.Contains(kinds, "covers") || v.Cleanable && slices.Contains(kinds, "redundant_covers")
	}
	if v.Kind == "tag_images" {
		return slices.Contains(kinds, "tag_images") || v.Cleanable && slices.Contains(kinds, "redundant_tag_images")
	}
	return (v.Cleanable || v.ExplicitClear) && slices.Contains(kinds, v.Kind)
}
func (s *Service) cleanupPreview(ctx context.Context, kinds []string, operationIDs ...[]string) ([]cleanupFile, map[string]any, error) {
	entries, e := s.storageInventory(ctx)
	if e != nil {
		return nil, nil, e
	}
	return s.cleanupPreviewEntries(ctx, entries, kinds, operationIDs...)
}
func (s *Service) cleanupPreviewEntries(ctx context.Context, entries []storageEntry, kinds []string, operationIDs ...[]string) ([]cleanupFile, map[string]any, error) {
	var e error
	selected := map[string]bool{}
	if len(operationIDs) > 1 {
		return nil, nil, model.ErrInput
	}
	if len(operationIDs) == 1 {
		if len(operationIDs[0]) > 1000 {
			return nil, nil, model.ErrCapacity
		}
		for _, id := range operationIDs[0] {
			parts := strings.SplitN(id, ":", 2)
			if len(parts) != 2 || (parts[0] != "instance" && parts[0] != "health") || len(parts[1]) < 1 || len(parts[1]) > 100 || strings.ContainsAny(parts[1], "/\\.\x00") {
				return nil, nil, model.ErrInput
			}
			selected[id] = true
		}
	}

	out := []cleanupFile{}
	// Check the entire selection before hashing any byte. Walk is separately
	// capped at one million files; preview is capped at 100k files / 1 TiB.
	var selectedBytes int64
	selectedFiles := 0
	for _, v := range entries {
		if cleanupSelected(v, kinds) && !(len(selected) > 0 && v.Material.ID != "" && !selected[v.Material.ID]) {
			selectedFiles++
			if v.Size < 0 || v.Size > (1<<40)-selectedBytes || selectedFiles > 100000 {
				return nil, nil, model.ErrCapacity
			}
			selectedBytes += v.Size
		}
	}
	var bytes, protectedBytes int64
	retained := 0
	impacts := map[string]string{}
	for _, v := range entries {
		if !cleanupSelected(v, kinds) || len(selected) > 0 && v.Material.ID != "" && !selected[v.Material.ID] {
			protectedBytes += v.Size
			retained++
			continue
		}
		f := v.cleanupFile
		path := filepath.Join(s.c.DataDir, f.Key)
		if e = appdata.NoSymlinks(path); e != nil {
			return nil, nil, e
		}
		before, e := os.Stat(path)
		if e != nil {
			return nil, nil, e
		}
		if !before.Mode().IsRegular() || before.Size() != f.Size || before.ModTime().UnixNano() != f.Mtime {
			return nil, nil, model.ErrConflict
		}
		f.Device, f.Inode = fileIdentity(before)
		digest, n, e := instanceHashWithReader(ctx, path, func(r io.Reader) io.Reader {
			r = io.LimitReader(r, f.Size+1)
			if s.CleanupPreviewReader != nil {
				r = s.CleanupPreviewReader(r)
			}
			return r
		})
		if e != nil {
			return nil, nil, e
		}
		after, e := os.Stat(path)
		if e != nil {
			return nil, nil, e
		}
		device, inode := fileIdentity(after)
		if n != f.Size || after.Size() != f.Size || after.ModTime().UnixNano() != f.Mtime || device != f.Device || inode != f.Inode {
			return nil, nil, model.ErrConflict
		}
		f.Digest = digest
		out = append(out, f)
		bytes += f.Size
		if v.Material.ID != "" {
			impacts[v.Material.ID] = v.Reason
		}
		if len(out) > 100000 {
			return nil, nil, model.ErrCapacity
		}
	}
	summary := map[string]any{"files": len(out), "bytes": bytes, "protected_files": retained, "protected_bytes": protectedBytes, "impacts": impacts, "clears_valid_caches": slices.Contains(kinds, "covers") || slices.Contains(kinds, "tag_images") || slices.Contains(kinds, "recommendation_indexes") || slices.Contains(kinds, "import_preview_cache")}
	if slices.Contains(kinds, "terminal_jobs") {
		if r, ok := s.repo.(interface {
			HealthCleanupPreview(context.Context) (map[string]int, error)
		}); ok {
			counts, e := r.HealthCleanupPreview(ctx)
			if e != nil {
				return nil, nil, e
			}
			for k, n := range counts {
				summary[k] = n
			}
		}
	}
	return out, summary, nil
}
func (s *Service) cleanupFrozen(ctx context.Context, j model.Job, options json.RawMessage) (resultErr error) {
	defer s.invalidateStorage()
	var in struct {
		Files      []cleanupFile `json:"_cleanup_files"`
		Categories []string      `json:"categories"`
	}
	if json.Unmarshal(options, &in) != nil {
		return model.ErrInput
	}
	repo, ok := s.repo.(storageRepository)
	if !ok {
		return model.ErrInput
	}
	root, e := os.OpenRoot(s.c.DataDir)
	if e != nil {
		return e
	}
	defer root.Close()
	reserved := map[string]model.StorageMaterial{}
	ready := map[string]bool{}

	counts, e := repo.CleanupProgress(ctx, j)
	if e != nil {
		return e
	}
	finalizing := false
	defer func() {
		if finalizing || resultErr == nil || errors.Is(resultErr, context.Canceled) || errors.Is(resultErr, model.ErrStaleJob) {
			return
		}
		counts, err := repo.CleanupProgress(context.WithoutCancel(ctx), j)
		if err != nil {
			return
		}
		counts["failed_files"]++
		if r, ok := s.repo.(interface {
			RecordCleanupResult(context.Context, model.Job, map[string]int64) error
		}); ok {
			_ = r.RecordCleanupResult(context.WithoutCancel(ctx), j, counts)
		}
	}()

	for _, file := range in.Files {
		if file.Material.ID != "" {
			reserved[file.Material.ID] = file.Material
		}
	}
	for index := int(counts["next_index"]); index < len(in.Files); {
		end := min(index+64, len(in.Files))
		// Reserve once before preparing this batch; parsing/hashing never holds SQL.
		for _, file := range in.Files[index:end] {
			if file.Material.ID != "" && !ready[file.Material.ID] {
				if e = s.validateMaterialActivation(file.Material); e != nil {
					return e
				}
				if e = repo.ReserveStorageMaterial(ctx, j, file.Material); e != nil {
					return e
				}
				if e = s.markActivationMaterialCleaning(file.Material); e != nil {
					return e
				}
				ready[file.Material.ID] = true
			}
		}
		prepared, err := s.prepareCleanupBatch(ctx, j, root, in.Files[index:end], index, in.Categories)
		if err != nil {
			return err
		}
		// Lock order: existing health lease -> tag storage -> writer -> cache.
		unlock := func() {}
		for _, f := range prepared {
			if f.Kind == "tag_images" && s.tagImages != nil {
				unlock = s.tagImages.LockStorage()
				break
			}
		}
		processed := 0
		if batch, ok := s.repo.(interface {
			DeleteStorageBatch(context.Context, model.Job, []model.StorageDeletion) ([]bool, error)
		}); ok {
			results, x := batch.DeleteStorageBatch(ctx, j, prepared)
			e = x
			processed = len(results)
		} else {
			// Compatibility for repository decorators; the production Store is batched.
			for _, f := range prepared {
				_, e = repo.DeleteStorageFile(ctx, j, f.Kind, f.Key, f.AssetID, f.Digest, f.OnlyRedundant, f.Ordinal, f.Size, f.Material, f.Remove)
				if e != nil {
					break
				}
				processed++
			}
		}
		unlock()
		if e != nil {
			return e
		}
		if processed == 0 {
			return model.ErrConflict
		}
		index += processed
	}

	finalizing = true
	if e = s.cleanupPhase(ctx, j, "finalizing"); e != nil {
		return e
	}
	for id := range reserved {
		if e = repo.FinishStorageMaterial(ctx, j, id, 0); e != nil {
			return e
		}
	}
	return s.repo.CleanHistory(ctx, j, in.Categories)
}
func (s *Service) validateMaterialActivation(m model.StorageMaterial) error {
	if m.Kind != "instance_recovery" {
		return nil
	}
	var p instanceActivation
	e := readInstanceJSON(activationPath(s.c), &p, instanceMaxManifest)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if p.ID == m.OperationID && !(p.State == "completed" || p.State == "rolled_back" && p.RollbackFinished) {
		return model.ErrConflict
	}
	return nil
}
func (s *Service) markActivationMaterialCleaning(m model.StorageMaterial) error {
	if m.Kind != "instance_recovery" {
		return nil
	}
	var p instanceActivation
	e := readInstanceJSON(activationPath(s.c), &p, instanceMaxManifest)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if p.ID != m.OperationID || p.MaterialsCleaned {
		return nil
	}
	if p.State != "completed" && !(p.State == "rolled_back" && p.RollbackFinished) {
		return model.ErrConflict
	}
	p.MaterialsCleaned = true
	p.Revision++
	return writeInstanceActivation(s.c, p)
}
