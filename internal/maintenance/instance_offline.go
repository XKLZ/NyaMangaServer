package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"sort"
)

type InstanceOfflineOptions struct {
	Inspect                func(context.Context, string, fs.FS) (model.InstanceInventory, error)
	PrepareDatabase        func(context.Context, string, string, string, []model.InstanceRebase, []model.InstanceStat) error
	CheckTarget            func(context.Context, string) error
	SpaceProbe             func(string) (uint64, error)
	ReplaceUnhealthyTarget bool
	CheckUnhealthyTarget   func(context.Context, string) error
}

// This stopped entry point deliberately never calls Store.Open. An empty or
// corrupt target is preserved as-is; no replacement empty database is created.
type offlineInstanceRequest struct {
	Fingerprint string                 `json:"fingerprint"`
	Preview     InstanceRestorePreview `json:"preview"`
}

func offlineTargetFingerprint(ctx context.Context, c config.Config) (string, error) {
	keys, e := instanceTargetMembers(ctx, c, true, DefaultInstanceBudget())
	if e != nil {
		return "", e
	}
	h := sha256.New()
	h.Write([]byte(instanceConfigHash(c)))
	for _, key := range keys {
		path, e := instanceTargetPath(c, key)
		if e != nil {
			return "", e
		}
		sum, n, e := instanceHash(ctx, path)
		if e != nil {
			return "", e
		}
		raw, _ := json.Marshal([]any{key, n, sum})
		h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func PrepareOfflineInstanceRestore(ctx context.Context, c config.Config, build InstanceBuild, files fs.FS, archive string, in InstanceRestoreInput, options InstanceOfflineOptions) (InstanceRestorePreview, error) {
	var out InstanceRestorePreview
	if !build.valid() || options.Inspect == nil || options.PrepareDatabase == nil || options.CheckTarget == nil {
		return out, model.ErrInstanceBuild
	}
	if !in.AcknowledgeStatOnly || len(in.RequestKey) < 8 || len(in.RequestKey) > 128 {
		return out, model.ErrInput
	}
	lock, e := appdata.Acquire(c.DataDir)
	if e != nil {
		return out, e
	}
	defer lock.Close()
	if e = appdata.CheckReady(c.DataDir); e != nil {
		return out, e
	}
	info, e := os.Lstat(archive)
	if e != nil {
		return out, e
	}
	if !info.Mode().IsRegular() || info.Size() > DefaultInstanceBudget().ArchiveBytes {
		return out, model.ErrInstancePackage
	}
	sum, n, e := instanceHash(ctx, archive)
	if e != nil {
		return out, e
	}
	mappings := append([]model.InstanceRootMapping{}, in.Mappings...)
	sort.Slice(mappings, func(i, j int) bool { return mappings[i].LibraryID < mappings[j].LibraryID })
	raw, _ := json.Marshal([]any{build, sum, n, mappings, instanceConfigHash(c), options.ReplaceUnhealthyTarget})
	fingerprint := sha256.Sum256(raw)
	requestFingerprint := hex.EncodeToString(fingerprint[:])
	identity := sha256.Sum256([]byte(c.DataDir + "\x00" + in.RequestKey))
	id := "offline-" + hex.EncodeToString(identity[:16])
	service := NewInstance(nil, c, build, files)
	service.Inspect = options.Inspect
	service.PrepareDatabase = options.PrepareDatabase
	service.SpaceProbe = options.SpaceProbe
	root, e := service.root(id)
	if e != nil {
		return out, e
	}
	var prior offlineInstanceRequest
	priorErr := readInstanceJSON(filepath.Join(root, "offline-request.json"), &prior, 8<<20)
	if priorErr == nil && prior.Fingerprint != requestFingerprint {
		return out, model.ErrConflict
	}
	if priorErr != nil && !os.IsNotExist(priorErr) {
		return out, priorErr
	}
	if p, e := loadInstanceActivation(c, build); e == nil {
		if p.ID == id && p.OfflineRequest == requestFingerprint && priorErr == nil {
			prior.Preview.Operation.State = p.State
			return prior.Preview, nil
		}
		if p.State != "completed" && p.State != "rolled_back" {
			return out, model.ErrInstanceRecovery
		}
	} else if !os.IsNotExist(e) {
		return out, e
	}
	check := options.CheckTarget
	if options.ReplaceUnhealthyTarget {
		if options.CheckUnhealthyTarget == nil {
			return out, model.ErrInput
		}
		check = options.CheckUnhealthyTarget
	}
	if e = check(ctx, c.DataDir); e != nil {
		return out, e
	}
	if e = instancePrivateDir(root); e != nil {
		return out, e
	}
	upload := filepath.Join(root, "upload.zip")
	if old, size, er := instanceHash(ctx, upload); er == nil {
		if old != sum || size != n {
			return out, model.ErrConflict
		}
	} else if os.IsNotExist(er) {
		if _, e = instanceCopy(ctx, archive, upload, service.budget.ArchiveBytes); e != nil {
			return out, e
		}
	} else {
		return out, er
	}
	v := model.InstanceOperation{ID: id, Kind: "restore", State: "validated", Revision: 1, Bytes: n, Digest: sum}
	var m InstanceManifest
	if _, e = os.Lstat(filepath.Join(root, "manifest.json")); e == nil {
		m, _, e = service.loadRestore(ctx, v)
	} else if os.IsNotExist(e) {
		m, e = service.validateRestore(ctx, v)
	}
	if e != nil {
		return out, e
	}
	_, inventory, e := service.loadRestore(ctx, v)
	if e != nil {
		return out, e
	}
	roots, stats, unlinked, e := service.mapRestore(ctx, inventory, in)
	if e != nil {
		return out, e
	}
	if e = instanceVaultTargetPreflight(c, m); e != nil {
		return out, e
	}
	space, e := service.space(ctx, m, n)
	if e != nil {
		return out, e
	}
	target, e := offlineTargetFingerprint(ctx, c)
	if e != nil {
		return out, e
	}
	out = InstanceRestorePreview{Operation: v, Counts: inventory.Counts, Roots: inventory.Roots, Mappings: in.Mappings, ArchiveBytes: n, LinkedFiles: len(stats), UnlinkedLibraries: unlinked, ReplacesWholeInstance: true, RequiresRelogin: true, CredentialsIncluded: true, Space: space, TargetFingerprint: target, ReplacesUnhealthyTarget: options.ReplaceUnhealthyTarget}
	for _, f := range m.Files {
		out.ExpandedBytes += f.Size
	}
	if in.Expected == 0 {
		if e = instanceJSON(filepath.Join(root, "offline-request.json"), offlineInstanceRequest{requestFingerprint, out}); e != nil {
			return out, e
		}
		return out, nil
	}
	if in.Expected != 1 || priorErr != nil || prior.Preview.TargetFingerprint != target {
		return out, model.ErrConflict
	}
	prepared := filepath.Join(root, "prepared")
	if e = os.RemoveAll(prepared); e != nil {
		return out, e
	}
	if e = instancePrivateDir(prepared); e != nil {
		return out, e
	}
	for _, f := range m.Files {
		if _, e = instanceCopy(ctx, filepath.Join(root, "validated", filepath.FromSlash(f.Key)), filepath.Join(prepared, filepath.FromSlash(f.Key)), service.budget.FileBytes); e != nil {
			return out, e
		}
	}
	if e = options.PrepareDatabase(ctx, filepath.Join(prepared, "app.sqlite"), id, in.RequestKey, roots, stats); e != nil {
		return out, e
	}
	for i := range m.Files {
		if m.Files[i].Key == "app.sqlite" {
			m.Files[i].SHA256, m.Files[i].Size, e = instanceHash(ctx, filepath.Join(prepared, "app.sqlite"))
			if e != nil {
				return out, e
			}
		}
	}
	before := filepath.Join(root, "before-confirm")
	if e = instancePrivateDir(before); e != nil {
		return out, e
	}
	if _, e = preserveInstanceTarget(ctx, c, before, true, service.budget); e != nil {
		return out, e
	}
	p := instanceActivation{ID: id, Build: build, Config: instanceConfigHash(c), State: "awaiting_restart", Revision: 1, Incoming: m.Files, RequestKey: in.RequestKey, OfflineRequest: requestFingerprint, Roots: roots, ReplaceUnhealthyTarget: options.ReplaceUnhealthyTarget}
	out.Operation.State = "awaiting_restart"
	if e = instanceJSON(filepath.Join(root, "offline-request.json"), offlineInstanceRequest{requestFingerprint, out}); e != nil {
		return out, e
	}
	if e = writeInstanceActivation(c, p); e != nil {
		return out, e
	}
	return out, nil
}

// Offline rollback request does not replace anything until normal server start.
func RequestOfflineInstanceRollback(ctx context.Context, c config.Config, build InstanceBuild, expected int64, epoch func(context.Context, string) (int64, error)) (InstanceActivationResult, error) {
	var result InstanceActivationResult
	lock, e := appdata.Acquire(c.DataDir)
	if e != nil {
		return result, e
	}
	defer lock.Close()
	p, e := loadInstanceActivation(c, build)
	if e != nil {
		return result, e
	}
	if p.MaterialsCleaned {
		return result, model.ErrConflict
	}
	if p.Revision != expected {
		return result, model.ErrConflict
	}
	if p.Build != build || p.Config != instanceConfigHash(c) {
		return result, model.ErrInstanceBuild
	}
	if p.State != "completed" && p.State != "activating" && p.State != "preserving" && p.State != "awaiting_restart" {
		return result, model.ErrConflict
	}
	if p.State == "completed" {
		if epoch == nil {
			return result, model.ErrInstanceRecovery
		}
		n, e := epoch(ctx, filepath.Join(c.DataDir, "app.sqlite"))
		if e != nil {
			return result, e
		}
		if n != 0 {
			return result, model.ErrConflict
		}
		p.State = "rollback_requested"
	} else if p.State == "activating" {
		p.State = "rolling_back"
		p.Cursor = 0
	} else {
		// No target replacement has happened yet. Keep the target untouched.
		p.State = "rolled_back"
	}
	p.Revision++
	if e = writeInstanceActivation(c, p); e != nil {
		return result, e
	}
	return instanceActivationStatus(p), nil
}
