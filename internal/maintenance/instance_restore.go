package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type InstanceRestoreInput struct {
	Expected            int64                       `json:"expected_revision"`
	RequestKey          string                      `json:"request_key"`
	Mappings            []model.InstanceRootMapping `json:"root_mappings"`
	AcknowledgeStatOnly bool                        `json:"acknowledge_stat_only"`
}
type InstanceRestorePreview struct {
	Operation               model.InstanceOperation     `json:"operation"`
	Counts                  map[string]int64            `json:"object_counts"`
	Roots                   []model.InstanceRoot        `json:"source_roots"`
	Mappings                []model.InstanceRootMapping `json:"root_mappings"`
	ArchiveBytes            int64                       `json:"archive_bytes"`
	ExpandedBytes           int64                       `json:"expanded_bytes"`
	LinkedFiles             int                         `json:"linked_files"`
	UnlinkedLibraries       int                         `json:"unlinked_libraries"`
	ContentVerified         bool                        `json:"source_content_verified"`
	ReplacesWholeInstance   bool                        `json:"replaces_whole_instance"`
	RequiresRelogin         bool                        `json:"requires_relogin"`
	CredentialsIncluded     bool                        `json:"contains_credentials_and_password_hashes"`
	TargetFingerprint       string                      `json:"target_fingerprint"`
	ReplacesUnhealthyTarget bool                        `json:"replaces_unhealthy_target"`
	Space                   InstanceSpace               `json:"space"`
}
type instanceRestorePrivate struct {
	Preview     *InstanceRestorePreview     `json:"preview,omitempty"`
	Mappings    []model.InstanceRootMapping `json:"root_mappings,omitempty"`
	Acknowledge bool                        `json:"acknowledge_stat_only"`
	Target      string                      `json:"target_fingerprint"`
	Epoch       int64                       `json:"target_write_epoch"`
	Vault       string                      `json:"target_vault_sha256"`
}

func (s *InstanceService) UploadRestore(ctx context.Context, key string, reader io.Reader) (result model.InstanceOperation, resultErr error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.InstanceOperation{}, e
	}

	if !s.build.valid() {
		return model.InstanceOperation{}, model.ErrInstanceBuild
	}
	if len(key) < 8 || len(key) > 128 {
		return model.InstanceOperation{}, model.ErrInput
	}
	id, e := model.NewID()
	if e != nil {
		return model.InstanceOperation{}, e
	}
	v, created, e := s.repo.CreateInstanceOperation(ctx, id, "restore", key)
	if !created {
		return v, e
	}
	original := v
	if original.ID == "" {
		original = model.InstanceOperation{ID: id, Kind: "restore", State: "pending", Revision: 1}
	}
	var written int64
	defer func() {
		if resultErr == nil {
			return
		}
		if result.ID == "" {
			result = original
		}
		settle, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		settled, settleErr := s.repo.FailPendingInstanceUpload(settle, original.ID, original.Revision, written, instanceFailureCode(resultErr))
		if settleErr == nil {
			result = settled
		} else {
			resultErr = errors.Join(resultErr, settleErr)
		}
	}()
	if e != nil {
		return original, e
	}
	root, e := s.root(v.ID)
	if e == nil {
		e = instancePrivateDir(root)
	}
	if e != nil {
		return v, e
	}
	path := filepath.Join(root, "upload.partial")
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return v, e
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), instanceReader{ctx, io.LimitReader(reader, s.budget.ArchiveBytes+1)})
	written = n
	syncErr := f.Sync()
	closeErr := f.Close()
	if e == nil {
		e = errors.Join(syncErr, closeErr)
	}
	if n > s.budget.ArchiveBytes {
		e = model.ErrInstancePackage
	}
	if e != nil {
		os.Remove(path)
		return v, e
	}
	if e = os.Rename(path, filepath.Join(root, "upload.zip")); e != nil {
		return v, e
	}
	if e = instanceSyncDir(root); e != nil {
		return v, e
	}
	return s.repo.SetInstanceOperation(ctx, id, v.Revision, "uploaded", "uploaded", "{}", n, hex.EncodeToString(h.Sum(nil)), "")
}
func (s *InstanceService) ValidateRestore(ctx context.Context, id string, expected int64) (model.InstanceOperation, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.InstanceOperation{}, e
	}

	v, e := s.repo.InstanceOperation(ctx, id)
	if e != nil {
		return v, e
	}
	if v.Kind != "restore" || v.Revision != expected || v.State != "uploaded" {
		return v, model.ErrConflict
	}
	v, e = s.repo.SetInstanceOperation(ctx, id, v.Revision, "validating", "validating", "{}", v.Bytes, v.Digest, "")
	if e != nil {
		return v, e
	}
	s.mu.Lock()
	child, cancel := context.WithCancel(s.lifecycle)
	if actor, ok := model.Actor(ctx); ok {
		child = model.WithActor(child, actor)
	}
	s.active[id] = cancel
	s.workers.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.workers.Done()
		defer cancel()
		defer func() { s.mu.Lock(); delete(s.active, id); s.mu.Unlock() }()
		_, err := s.validateRestore(child, v)
		if err == nil {
			err = model.AuthorizeAdministrator(child, s.repo)
		}
		settle, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		state, code, phase := "validated", "", "validation_complete"
		if err != nil {
			state = "failed"
			code = instanceFailureCode(err)
			phase = v.Phase
			if errors.Is(err, context.Canceled) {
				state = "cancelled"
			}
		}
		for attempt := 0; attempt < 3; attempt++ {
			current, e := s.repo.InstanceOperation(settle, id)
			if e != nil {
				return
			}
			if current.State == "abandon_requested" {
				state = "abandoned"
				code = "cancelled"
				phase = v.Phase
			} else if current.State != "validating" || current.Revision != v.Revision {
				return
			}
			writeContext := settle
			if state == "validated" {
				if actor, ok := model.Actor(child); ok {
					writeContext = model.WithActor(settle, actor)
				}
			}
			_, e = s.repo.SetInstanceOperation(writeContext, id, current.Revision, state, phase, "{}", v.Bytes, v.Digest, code)
			if e != nil && state == "validated" && !errors.Is(e, model.ErrConflict) {
				state = "failed"
				code = "authorization_revoked"
				phase = v.Phase
				continue
			}

			if !errors.Is(e, model.ErrConflict) {
				return
			}
		}
	}()
	return v, nil
}
func (s *InstanceService) validateRestore(ctx context.Context, v model.InstanceOperation) (InstanceManifest, error) {
	root, e := s.root(v.ID)
	if e != nil {
		return InstanceManifest{}, e
	}
	hash, n, e := instanceHash(ctx, filepath.Join(root, "upload.zip"))
	if e != nil {
		return InstanceManifest{}, e
	}
	if hash != v.Digest || n != v.Bytes {
		return InstanceManifest{}, model.ErrInstancePackage
	}
	stage := filepath.Join(root, "validated")
	m, e := readInstancePackage(ctx, filepath.Join(root, "upload.zip"), stage, s.build, s.budget, s.SpaceProbe)
	if e != nil {
		return m, e
	}
	if s.Inspect == nil {
		return m, model.ErrInstanceRecovery
	}
	inventory, e := s.Inspect(ctx, filepath.Join(stage, "app.sqlite"), s.files)
	if e != nil {
		return m, e
	}
	if inventory.Ledger != m.Inventory.Ledger || inventory.Schema != m.Inventory.Schema || inventory.Version != m.Inventory.Version {
		return m, model.ErrInstancePackage
	}
	// Trust references only after reading the canonical validated DB, never just
	// the upload manifest's claimed inventory.
	m.Inventory = inventory
	if e = instanceRequired(m); e != nil {
		return m, e
	}
	if _, e = provider.ValidateBackupPair(filepath.Join(stage, "vault/key"), filepath.Join(stage, "vault/credentials")); e != nil {
		return m, e
	}
	if e = instanceJSON(filepath.Join(root, "manifest.json"), m); e != nil {
		return m, e
	}
	if e = instanceJSON(filepath.Join(root, "summary.json"), instanceSummary(m)); e != nil {
		return m, e
	}
	return m, nil
}
func (s *InstanceService) loadRestore(ctx context.Context, v model.InstanceOperation) (InstanceManifest, model.InstanceInventory, error) {
	var m InstanceManifest
	root, e := s.root(v.ID)
	if e != nil {
		return m, m.Inventory, e
	}
	if e = readInstanceJSON(filepath.Join(root, "manifest.json"), &m, instanceMaxManifest); e != nil {
		return m, m.Inventory, e
	}
	if e = validateInstanceMembers(m, s.budget); e != nil {
		return m, m.Inventory, e
	}
	if m.Build != s.build {
		return m, m.Inventory, model.ErrInstanceBuild
	}
	for _, f := range m.Files {
		hash, n, e := instanceHash(ctx, filepath.Join(root, "validated", filepath.FromSlash(f.Key)))
		if e != nil {
			return m, m.Inventory, e
		}
		if hash != f.SHA256 || n != f.Size {
			return m, m.Inventory, model.ErrInstancePackage
		}
	}
	inventory, e := s.Inspect(ctx, filepath.Join(root, "validated/app.sqlite"), s.files)
	return m, inventory, e
}

func readInstanceJSON(path string, v any, limit int64) error {
	if e := appdata.NoSymlinks(path); e != nil {
		return e
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return model.ErrInstanceRecovery
	}
	d := json.NewDecoder(io.LimitReader(f, limit+1))
	d.DisallowUnknownFields()
	if e = d.Decode(v); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return model.ErrInstanceRecovery
	}
	return nil
}
func (s *InstanceService) mapRestore(ctx context.Context, inventory model.InstanceInventory, in InstanceRestoreInput) ([]model.InstanceRebase, []model.InstanceStat, int, error) {
	if !in.AcknowledgeStatOnly || len(in.Mappings) != len(inventory.Roots) {
		return nil, nil, 0, model.ErrInstanceMapping
	}
	mappings := map[string]*string{}
	targets := map[string]bool{}
	for _, r := range in.Mappings {
		if _, ok := mappings[r.LibraryID]; ok {
			return nil, nil, 0, model.ErrInstanceMapping
		}
		mappings[r.LibraryID] = r.TargetRootKey
		if r.TargetRootKey != nil {
			if targets[*r.TargetRootKey] {
				return nil, nil, 0, model.ErrInstanceMapping
			}
			targets[*r.TargetRootKey] = true
		}
	}
	rebases := []model.InstanceRebase{}
	stats := []model.InstanceStat{}
	allowed := map[string]string{}
	unlinked := 0
	for _, root := range s.c.Roots {
		allowed[root.Key] = root.Path
	}
	paths := map[string]string{}
	for _, root := range inventory.Roots {
		target, ok := mappings[root.LibraryID]
		if !ok {
			return nil, nil, 0, model.ErrInstanceMapping
		}
		rebase := model.InstanceRebase{LibraryID: root.LibraryID, SourceRootKey: root.RootKey, TargetRootKey: target, Identity: root.Identity}
		if target == nil {
			unlinked++
		} else {
			path, ok := allowed[*target]
			if !ok {
				return nil, nil, 0, model.ErrInstanceMapping
			}
			if e := appdata.NoSymlinks(path); e != nil {
				return nil, nil, 0, model.ErrInstanceMapping
			}
			i, e := os.Stat(path)
			if e != nil || !i.IsDir() {
				return nil, nil, 0, model.ErrInstanceMapping
			}
			identity, e := directoryIdentity(path)
			if e != nil {
				return nil, nil, 0, e
			}
			rebase.Identity = identity
			paths[root.LibraryID] = path
		}
		rebases = append(rebases, rebase)
	}
	for _, source := range inventory.Sources {
		if e := ctx.Err(); e != nil {
			return nil, nil, 0, e
		}
		root, ok := paths[source.LibraryID]
		if !ok {
			continue
		}
		if !validInstanceSourcePath(source.Path) {
			return nil, nil, 0, model.ErrInstanceMapping
		}
		filename := filepath.Join(root, filepath.FromSlash(source.Path))
		if e := appdata.NoSymlinks(filename); e != nil {
			return nil, nil, 0, model.ErrInstanceMapping
		}
		i, e := os.Lstat(filename)
		if e != nil {
			if os.IsNotExist(e) && source.State != "available" {
				continue
			}
			return nil, nil, 0, model.ErrInstanceMapping
		}
		if !i.Mode().IsRegular() || i.Size() != source.Size {
			return nil, nil, 0, model.ErrInstanceMapping
		}
		stats = append(stats, model.InstanceStat{EditionID: source.EditionID, Mtime: i.ModTime().UnixNano()})
	}
	return rebases, stats, unlinked, nil
}
func validInstanceSourcePath(key string) bool {
	// These are source filenames, not portable package member keys. A colon in
	// a POSIX filename (for example "Re:Title.cbz") is ordinary data. Retain
	// explicit cross-platform drive rejection: filepath.VolumeName alone does
	// not recognize Windows drives on a POSIX restore host.
	if key == "." || !fs.ValidPath(key) || strings.ContainsAny(key, "\\\x00") || !filepath.IsLocal(key) {
		return false
	}
	for _, part := range strings.Split(key, "/") {
		if len(part) >= 2 && part[1] == ':' && (part[0] >= 'A' && part[0] <= 'Z' || part[0] >= 'a' && part[0] <= 'z') {
			return false
		}
	}
	// On Windows a colon can address an alternate data stream, not a file.
	return filepath.Separator != '\\' || !strings.Contains(key, ":")
}

func ValidatePreparedInstanceRoots(ctx context.Context, c config.Config, inventory model.InstanceInventory, roots []model.InstanceRebase) error {
	s := NewInstance(nil, c, InstanceBuild{}, nil)
	input := InstanceRestoreInput{AcknowledgeStatOnly: true, Mappings: []model.InstanceRootMapping{}}
	expected := map[string]model.InstanceRebase{}
	for _, root := range roots {
		input.Mappings = append(input.Mappings, model.InstanceRootMapping{LibraryID: root.LibraryID, TargetRootKey: root.TargetRootKey})
		expected[root.LibraryID] = root
	}
	mapped, stats, _, e := s.mapRestore(ctx, inventory, input)
	if e != nil {
		return e
	}
	for _, root := range mapped {
		if root.Identity != expected[root.LibraryID].Identity {
			return model.ErrInstanceMapping
		}
	}
	mtimes := map[string]int64{}
	for _, source := range inventory.Sources {
		mtimes[source.EditionID] = source.Mtime
	}
	for _, stat := range stats {
		if mtimes[stat.EditionID] != stat.Mtime {
			return model.ErrInstanceMapping
		}
	}
	return nil
}
func instanceVaultFingerprint(ctx context.Context, c config.Config) (string, error) {
	h := sha256.New()
	for _, path := range []string{c.ProviderKeyFile, c.ProviderCredentialsFile} {
		if path == "" {
			h.Write([]byte("unconfigured"))
			continue
		}
		info, e := os.Lstat(path)
		if os.IsNotExist(e) {
			h.Write([]byte("absent"))
			continue
		}
		if e != nil {
			return "", e
		}
		if !info.Mode().IsRegular() || info.Size() > 64<<10 {
			return "", model.ErrInstanceRecovery
		}
		sum, n, e := instanceHash(ctx, path)
		if e != nil {
			return "", e
		}
		raw, _ := json.Marshal([]any{sum, n})
		h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (s *InstanceService) targetFingerprint(ctx context.Context) (string, model.InstanceState, string, error) {
	state, e := s.repo.InstanceState(ctx)
	if e != nil {
		return "", state, "", e
	}
	vault, e := instanceVaultFingerprint(ctx, s.c)
	if e != nil {
		return "", state, "", e
	}
	b, e := json.Marshal(struct {
		State         model.InstanceState
		Config, Vault string
	}{state, instanceConfigHash(s.c), vault})
	if e != nil {
		return "", state, "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), state, vault, nil
}
func (s *InstanceService) PreviewRestore(ctx context.Context, id string, in InstanceRestoreInput) (InstanceRestorePreview, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return InstanceRestorePreview{}, e
	}

	var out InstanceRestorePreview
	v, e := s.repo.InstanceOperation(ctx, id)
	if e != nil {
		return out, e
	}
	if v.Kind != "restore" || v.Revision != in.Expected || (v.State != "validated" && v.State != "previewed") {
		return out, model.ErrConflict
	}
	m, inventory, e := s.loadRestore(ctx, v)
	if e != nil {
		return out, e
	}
	_, stats, unlinked, e := s.mapRestore(ctx, inventory, in)
	if e != nil {
		return out, e
	}
	if e = instanceVaultTargetPreflight(s.c, m); e != nil {
		return out, e
	}
	target, targetState, vault, e := s.targetFingerprint(ctx)
	if e != nil {
		return out, e
	}
	space, e := s.space(ctx, m, v.Bytes)
	if e != nil {
		return out, e
	}
	out = InstanceRestorePreview{Operation: v, Counts: inventory.Counts, Roots: inventory.Roots, Mappings: in.Mappings, ArchiveBytes: v.Bytes, LinkedFiles: len(stats), UnlinkedLibraries: unlinked, ReplacesWholeInstance: true, RequiresRelogin: true, CredentialsIncluded: true, TargetFingerprint: target, Space: space}
	for _, f := range m.Files {
		out.ExpandedBytes += f.Size
	}
	out.Operation.State = "previewed"
	out.Operation.Phase = "previewed"
	out.Operation.Revision++
	private, e := json.Marshal(instanceRestorePrivate{Mappings: in.Mappings, Acknowledge: in.AcknowledgeStatOnly, Target: target, Epoch: targetState.WriteEpoch, Vault: vault, Preview: &out})
	if e != nil {
		return out, e
	}
	v, e = s.repo.SetInstanceOperation(ctx, id, v.Revision, "previewed", "previewed", string(private), v.Bytes, v.Digest, "")
	if e != nil {
		return out, e
	}
	out.Operation = v
	return out, nil
}
func (s *InstanceService) ConfirmRestore(ctx context.Context, id string, in model.InstanceRequest) (model.InstanceOperation, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.InstanceOperation{}, e
	}

	v, e := s.repo.InstanceOperation(ctx, id)
	if e != nil {
		return v, e
	}
	if p, er := loadInstanceActivation(s.c, s.build); er == nil && p.ID == id && p.RequestKey == in.RequestKey && p.State == "awaiting_restart" {
		if v.State != "awaiting_restart" {
			raw, e := s.repo.InstancePrivate(ctx, id)
			if e != nil {
				return v, e
			}
			return s.repo.SetInstanceOperation(ctx, id, v.Revision, "awaiting_restart", "awaiting_restart", raw, v.Bytes, v.Digest, "")
		}
		return v, nil
	} else if er != nil && !os.IsNotExist(er) {
		return v, er
	}

	if v.Kind != "restore" || v.Revision != in.Expected || v.State != "previewed" || len(in.RequestKey) < 8 || len(in.RequestKey) > 128 {
		return v, model.ErrConflict
	}
	s.mu.Lock()
	if s.active[id] != nil {
		s.mu.Unlock()
		return v, model.ErrBusy
	}
	child, cancel := context.WithCancel(ctx)
	s.active[id] = cancel
	s.workers.Add(1)
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); delete(s.active, id); s.mu.Unlock(); s.workers.Done() }()
	ctx = child
	raw, e := s.repo.InstancePrivate(ctx, id)
	if e != nil {
		return v, e
	}
	var private instanceRestorePrivate
	if e = instanceDecodePrivate(raw, &private); e != nil {
		return v, e
	}
	target, _, _, e := s.targetFingerprint(ctx)
	if e != nil {
		return v, e
	}
	if target != private.Target {
		return v, model.ErrConflict
	}
	m, inventory, e := s.loadRestore(ctx, v)
	if e != nil {
		return v, e
	}
	roots, stats, _, e := s.mapRestore(ctx, inventory, InstanceRestoreInput{Mappings: private.Mappings, AcknowledgeStatOnly: private.Acknowledge})
	if e != nil {
		return v, e
	}
	if e = instanceVaultTargetPreflight(s.c, m); e != nil {
		return v, e
	}
	if _, e = s.space(ctx, m, v.Bytes); e != nil {
		return v, e
	}
	if e = s.repo.BeginInstanceProtection(ctx, id, v.Revision, private.Epoch); e != nil {
		return v, e
	}
	v, e = s.repo.SetInstanceOperation(ctx, id, v.Revision, "preparing", "draining", raw, v.Bytes, v.Digest, "")
	if e != nil {
		_ = s.repo.EndInstanceProtection(ctx, id)
		return v, e
	}
	protected := true
	defer func() {
		if protected {
			settle, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if s.Drain == nil || s.Drain(settle) == nil {
				_ = s.repo.EndInstanceProtection(settle, id)
			}
		}
	}()
	if s.Drain != nil {
		if e = s.Drain(ctx); e != nil {
			return v, e
		}
	}
	release, e := s.repo.HealthStorageLease(ctx)
	if e != nil {
		return v, e
	}
	defer release()
	// Recheck the independent vault after authenticated writers have drained.
	vault, e := instanceVaultFingerprint(ctx, s.c)
	if e != nil {
		return v, e
	}
	if vault != private.Vault {
		return v, model.ErrConflict
	}
	// Recheck source correspondence and destination permissions after IO drains.
	roots, stats, _, e = s.mapRestore(ctx, inventory, InstanceRestoreInput{Mappings: private.Mappings, AcknowledgeStatOnly: private.Acknowledge})
	if e != nil {
		return v, e
	}
	root, e := s.root(id)
	if e != nil {
		return v, e
	}
	prepared := filepath.Join(root, "prepared")
	if e = instancePrivateDir(prepared); e != nil {
		return v, e
	}
	for _, f := range m.Files {
		if _, e = instanceCopy(ctx, filepath.Join(root, "validated", filepath.FromSlash(f.Key)), filepath.Join(prepared, filepath.FromSlash(f.Key)), s.budget.FileBytes); e != nil {
			return v, e
		}
	}
	if s.PrepareDatabase == nil {
		return v, model.ErrInstanceRecovery
	}
	if e = s.PrepareDatabase(ctx, filepath.Join(prepared, "app.sqlite"), id, in.RequestKey, roots, stats); e != nil {
		return v, e
	}
	for i := range m.Files {
		if m.Files[i].Key == "app.sqlite" {
			m.Files[i].SHA256, m.Files[i].Size, e = instanceHash(ctx, filepath.Join(prepared, "app.sqlite"))
			if e != nil {
				return v, e
			}
		}
	}
	// Preserve a consistent target snapshot and immutable materials before the
	// durable restart request. Activation also preserves the final stopped WAL set.
	before := filepath.Join(root, "before-confirm")
	if e = instancePrivateDir(before); e != nil {
		return v, e
	}
	if e = s.repo.HealthSnapshot(ctx, filepath.Join(before, "app.sqlite")); e != nil {
		return v, e
	}
	if _, e = preserveInstanceTarget(ctx, s.c, before, false, s.budget); e != nil {
		return v, e
	}
	plan := instanceActivation{ID: id, Build: s.build, Config: instanceConfigHash(s.c), State: "awaiting_restart", Revision: 1, Incoming: m.Files, RequestKey: in.RequestKey, Roots: roots}
	if e = writeInstanceActivation(s.c, plan); e != nil {
		return v, e
	}
	protected = false // durable journal now owns the barrier until stopped activation.
	return s.repo.SetInstanceOperation(ctx, id, v.Revision, "awaiting_restart", "awaiting_restart", raw, v.Bytes, v.Digest, "")
}
func (s *InstanceService) AbandonRestore(ctx context.Context, id string, expected int64) (model.InstanceOperation, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.InstanceOperation{}, e
	}

	v, e := s.repo.InstanceOperation(ctx, id)
	if e != nil {
		return v, e
	}
	if v.Kind != "restore" || v.Revision != expected || v.State == "completed" {
		return v, model.ErrConflict
	}
	s.mu.Lock()
	cancel := s.active[id]
	s.mu.Unlock()
	if cancel != nil && v.State == "validating" {
		requested, e := s.repo.SetInstanceOperation(ctx, id, v.Revision, "abandon_requested", v.Phase, "{}", v.Bytes, v.Digest, "")
		if e != nil {
			return v, e
		}
		cancel()
		return requested, nil
	}
	if cancel != nil {
		return v, model.ErrBusy
	}
	plan, planErr := loadInstanceActivation(s.c, s.build)
	if planErr != nil && !os.IsNotExist(planErr) {
		return v, planErr
	}
	if planErr == nil && plan.ID == id && plan.State != "awaiting_restart" && plan.State != "rolled_back" {
		return v, model.ErrInstanceRecovery
	}
	state, e := s.repo.InstanceState(ctx)
	if e != nil {
		return v, e
	}
	if state.Owner != nil && *state.Owner == id {
		if s.Drain != nil {
			if e = s.Drain(ctx); e != nil {
				return v, e
			}
		}
		release, e := s.repo.HealthStorageLease(ctx)
		if e != nil {
			return v, e
		}
		defer release()
		if planErr == nil && plan.ID == id && plan.State == "awaiting_restart" {
			plan.State = "rolled_back"
			plan.Revision++
			if e = writeInstanceActivation(s.c, plan); e != nil {
				return v, e
			}
		}
		if e = s.repo.EndInstanceProtection(ctx, id); e != nil {
			return v, e
		}
	}
	return s.repo.SetInstanceOperation(ctx, id, v.Revision, "abandoned", "abandoned", "{}", v.Bytes, v.Digest, "")
}

func sortedInstanceFiles(files map[string]model.InstanceFile) []model.InstanceFile {
	out := make([]model.InstanceFile, 0, len(files))
	for _, v := range files {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func (s *InstanceService) State(ctx context.Context) (model.InstanceState, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.InstanceState{}, e
	}

	return s.repo.InstanceState(ctx)
}
func (s *InstanceService) ReleaseHold(ctx context.Context, expected int64) (model.InstanceState, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.InstanceState{}, e
	}

	return s.repo.ReleaseInstanceHold(ctx, expected)
}
func (s *InstanceService) Activation(ctx context.Context) (InstanceActivationResult, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return InstanceActivationResult{}, e
	}
	return ReadInstanceActivation(s.c, s.build)
}
func (s *InstanceService) RequestRollback(ctx context.Context, id string, in model.InstanceRequest) (InstanceActivationResult, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return InstanceActivationResult{}, e
	}

	var out InstanceActivationResult
	p, e := loadInstanceActivation(s.c, s.build)
	if e != nil {
		return out, e
	}
	if p.MaterialsCleaned {
		return out, model.ErrConflict
	}
	if p.ID != id || p.State != "completed" || p.Revision != in.Expected || len(in.RequestKey) < 8 || len(in.RequestKey) > 128 {
		return out, model.ErrConflict
	}
	if p.Build != s.build || p.Config != instanceConfigHash(s.c) {
		return out, model.ErrInstanceBuild
	}
	state, e := s.repo.InstanceState(ctx)
	if e != nil {
		return out, e
	}
	if state.WriteEpoch != 0 {
		return out, model.ErrConflict
	}
	v, e := s.repo.InstanceOperation(ctx, id)
	if e != nil {
		return out, e
	}
	// Preservation material is checked before accepting another durable barrier.
	root, e := s.root(id)
	if e != nil {
		return out, e
	}
	for _, f := range p.Before {
		hash, n, e := instanceHash(ctx, filepath.Join(root, "before-activation", filepath.FromSlash(f.Key)))
		if e != nil {
			return out, e
		}
		if hash != f.SHA256 || n != f.Size {
			return out, model.ErrInstanceRecovery
		}
	}
	if e = s.repo.BeginInstanceProtection(ctx, id, v.Revision, 0); e != nil {
		return out, e
	}
	p.State = "rollback_requested"
	p.Cursor = 0
	p.Revision++
	if e = writeInstanceActivation(s.c, p); e != nil {
		_ = s.repo.EndInstanceProtection(ctx, id)
		return out, e
	}
	return instanceActivationStatus(p), nil
}
