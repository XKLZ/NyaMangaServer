package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type instanceActivation struct {
	MaterialsCleaned       bool                   `json:"materials_cleaned,omitempty"`
	ID                     string                 `json:"id"`
	Build                  InstanceBuild          `json:"build"`
	Config                 string                 `json:"target_config_sha256"`
	State                  string                 `json:"state"`
	Revision               int64                  `json:"revision"`
	RequestKey             string                 `json:"request_key"`
	OfflineRequest         string                 `json:"offline_request_sha256,omitempty"`
	ReplaceUnhealthyTarget bool                   `json:"replace_unhealthy_target"`
	Incoming               []model.InstanceFile   `json:"incoming"`
	Before                 []model.InstanceFile   `json:"before,omitempty"`
	Keys                   []string               `json:"keys,omitempty"`
	Cursor                 int                    `json:"cursor"`
	Roots                  []model.InstanceRebase `json:"roots"`
	RollbackCurrent        []model.InstanceFile   `json:"rollback_current,omitempty"`
	RollbackFinished       bool                   `json:"rollback_finished"`
	RollbackFrozen         bool                   `json:"rollback_frozen"`
}
type InstanceActivationResult struct {
	MaterialsCleaned bool   `json:"materials_cleaned"`
	ID               string `json:"id"`
	State            string `json:"state"`
	Revision         int64  `json:"revision"`
	CompletedFiles   int    `json:"completed_files"`
	TotalFiles       int    `json:"total_files"`
	BackgroundHold   bool   `json:"background_hold"`
	RequiresRestart  bool   `json:"requires_restart"`
}
type InstanceActivationOptions struct {
	// Called only after the caller holds the real instance lock, before Store.Open.
	Epoch            func(context.Context, string) (int64, error)
	FinishRollback   func(context.Context, string, string) error
	CheckRollback    func(context.Context, string, string) error
	ValidatePrepared func(context.Context, string, []model.InstanceRebase) error
	Fault            func(string, int) error // anonymous deterministic crash-window injection
}

func instanceConfigHash(c config.Config) string {
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func activationPath(c config.Config) string {
	return filepath.Join(c.DataDir, "instance-recovery/activation.json")
}
func writeInstanceActivation(c config.Config, plan instanceActivation) error {
	return instanceJSON(activationPath(c), plan)
}
func loadInstanceActivation(c config.Config, build InstanceBuild) (instanceActivation, error) {
	var p instanceActivation
	e := readInstanceJSON(activationPath(c), &p, instanceMaxManifest)
	if e != nil {
		return p, e
	}
	if p.ID == "" || strings.ContainsAny(p.ID, "/\\.\x00") || len(p.ID) > 100 || p.Cursor < 0 || p.Cursor > len(p.Keys) {
		return p, model.ErrInstanceRecovery
	}
	// Terminal receipts are history, not a permanent prohibition on later app
	// upgrades or deployment configuration changes. Rollback checks exact build
	// and target config separately before acquiring any protection.
	validKey := func(key string) bool {
		return instanceKey(key) || key == "app.sqlite-wal" || key == "app.sqlite-shm" || key == "app.sqlite-journal" || key == "health-write-unsafe"
	}
	for _, key := range p.Keys {
		if !validKey(key) {
			return p, model.ErrInstanceRecovery
		}
	}
	for _, group := range [][]model.InstanceFile{p.Incoming, p.Before, p.RollbackCurrent} {
		for _, f := range group {
			digest, err := hex.DecodeString(f.SHA256)
			if !validKey(f.Key) || f.Size < 0 || err != nil || len(digest) != 32 {
				return p, model.ErrInstanceRecovery
			}
		}
	}
	if p.State == "completed" || p.State == "rolled_back" && p.RollbackFinished {
		return p, nil
	}
	if p.Build != build || p.Config != instanceConfigHash(c) {
		return p, model.ErrInstanceRecovery
	}
	switch p.State {
	case "awaiting_restart", "preserving", "activating", "completed", "rollback_requested", "rolling_back", "rolled_back":
	default:
		return p, model.ErrInstanceRecovery
	}
	seen := map[string]bool{}
	for _, key := range p.Keys {
		if seen[key] {
			return p, model.ErrInstanceRecovery
		}
		seen[key] = true
		if _, e = instanceTargetPath(c, key); e != nil {
			return p, e
		}
	}
	for _, group := range [][]model.InstanceFile{p.Incoming, p.Before, p.RollbackCurrent} {
		seen = map[string]bool{}
		for _, f := range group {
			if seen[f.Key] || f.Size < 0 || len(f.SHA256) != 64 {
				return p, model.ErrInstanceRecovery
			}
			seen[f.Key] = true
			if _, e = instanceTargetPath(c, f.Key); e != nil {
				return p, e
			}
		}
	}
	return p, nil
}
func instanceTargetPath(c config.Config, key string) (string, error) {
	var path string
	switch key {
	case "vault/key":
		path = c.ProviderKeyFile
	case "vault/credentials":
		path = c.ProviderCredentialsFile
	case "app.sqlite-wal", "app.sqlite-shm", "app.sqlite-journal", "health-write-unsafe":
		path = filepath.Join(c.DataDir, key)
	default:
		if !instanceKey(key) {
			return "", model.ErrInstanceRecovery
		}
		path = filepath.Join(c.DataDir, filepath.FromSlash(key))
	}
	if path == "" {
		return "", model.ErrInstanceRecovery
	}
	if e := appdata.NoSymlinks(path); e != nil {
		return "", e
	}
	return path, nil
}
func instanceVaultTargetPreflight(c config.Config, m InstanceManifest) error {
	if e := instanceModelLayout(c); e != nil {
		return e
	}
	vault := false
	for _, f := range m.Files {
		if strings.HasPrefix(f.Key, "vault/") {
			vault = true
		}
	}
	if !vault {
		present := false
		for _, path := range []string{c.ProviderKeyFile, c.ProviderCredentialsFile} {
			if path != "" {
				if _, e := os.Lstat(path); e == nil {
					present = true
				} else if !os.IsNotExist(e) {
					return e
				}
			}
		}
		if !present {
			return nil
		}
	}
	if c.ProviderKeyFile == "" || c.ProviderCredentialsFile == "" || c.ProviderKeyFile == c.ProviderCredentialsFile {
		return model.ErrInstanceRecovery
	}
	for _, path := range []string{c.ProviderKeyFile, c.ProviderCredentialsFile} {
		if e := appdata.NoSymlinks(path); e != nil {
			return e
		}
		if i, e := os.Lstat(path); e == nil {
			if !i.Mode().IsRegular() || i.Mode().Perm()&0222 == 0 {
				return os.ErrPermission
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		parent := filepath.Dir(path)
		if i, e := os.Stat(parent); e != nil || !i.IsDir() || i.Mode().Perm()&0222 == 0 {
			return os.ErrPermission
		}
		f, e := os.CreateTemp(parent, "restore-preflight-")
		if e != nil {
			return e
		}
		name := f.Name()
		e = f.Close()
		remove := os.Remove(name)
		if e != nil || remove != nil {
			return errors.Join(e, remove)
		}
	}
	return nil
}

// Managed cache/asset roots are the backup boundary, not a model store. Refuse
// ambiguous deployment layouts before walking either source or target weights.
func instanceModelLayout(c config.Config) error {
	contains := func(root, path string) bool {
		rel, e := filepath.Rel(root, path)
		return e == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
	}
	for _, root := range c.Roots {
		if root.Path != "" && (contains(root.Path, c.DataDir) || contains(c.DataDir, root.Path)) {
			return model.ErrInstanceRecovery
		}
	}
	models := c.WorkRecommendations.ModelsDir
	if models != "" && (contains(models, c.DataDir) || contains(c.DataDir, models)) {
		return model.ErrInstanceRecovery
	}
	for _, program := range []string{c.WorkRecommendations.Python, c.WorkRecommendations.Worker} {
		if program != "" && contains(c.DataDir, program) {
			return model.ErrInstanceRecovery
		}
	}
	python := c.WorkRecommendations.Python
	if python != "" && filepath.Base(filepath.Dir(python)) == "bin" {
		environment := filepath.Dir(filepath.Dir(python))
		if contains(environment, c.DataDir) || contains(c.DataDir, environment) {
			return model.ErrInstanceRecovery
		}
	}
	return nil
}
func instanceTargetMembers(ctx context.Context, c config.Config, rawDB bool, b InstanceBudget) ([]string, error) {
	keys := []string{}
	if rawDB {
		for _, key := range []string{"app.sqlite", "app.sqlite-wal", "app.sqlite-shm", "app.sqlite-journal", "health-write-unsafe"} {
			if _, e := os.Lstat(filepath.Join(c.DataDir, key)); e == nil {
				keys = append(keys, key)
			} else if !os.IsNotExist(e) {
				return nil, e
			}
		}
	}
	for _, sub := range []string{"assets", "cache/covers", "cache/tag-images", "cache/import-previews", "cache/tag-translations", "indexes/recommendations", "health"} {
		base := filepath.Join(c.DataDir, sub)
		if e := appdata.NoSymlinks(base); e != nil {
			return nil, e
		}
		e := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if os.IsNotExist(err) && path == base {
				return nil
			}
			if err != nil {
				return err
			}
			if e := ctx.Err(); e != nil {
				return e
			}
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return model.ErrInstanceRecovery
			}
			rel, e := filepath.Rel(c.DataDir, path)
			if e != nil {
				return e
			}
			key := filepath.ToSlash(rel)
			if !instanceKey(key) {
				return model.ErrInstanceRecovery
			}
			keys = append(keys, key)
			if len(keys) > b.Files {
				return model.ErrInstanceRecovery
			}
			return nil
		})
		if e != nil {
			return nil, e
		}
	}
	for _, key := range []string{"vault/key", "vault/credentials"} {
		path, e := instanceTargetPath(c, key)
		if e != nil {
			if c.ProviderKeyFile == "" && c.ProviderCredentialsFile == "" {
				continue
			}
			return nil, e
		}
		if _, e = os.Lstat(path); e == nil {
			keys = append(keys, key)
		} else if !os.IsNotExist(e) {
			return nil, e
		}
	}
	sort.Strings(keys)
	return keys, nil
}
func preserveInstanceTarget(ctx context.Context, c config.Config, to string, rawDB bool, b InstanceBudget) ([]model.InstanceFile, error) {
	keys, e := instanceTargetMembers(ctx, c, rawDB, b)
	if e != nil {
		return nil, e
	}
	out := []model.InstanceFile{}
	var total int64
	for _, key := range keys {
		from, e := instanceTargetPath(c, key)
		if e != nil {
			return nil, e
		}
		target := filepath.Join(to, filepath.FromSlash(key))
		// A pre-preservation interruption is replayed against unchanged stopped files.
		if _, e = os.Lstat(target); e == nil {
			a, n, e := instanceHash(ctx, from)
			if e != nil {
				return nil, e
			}
			old, m, e := instanceHash(ctx, target)
			if e != nil || a != old || n != m {
				return nil, model.ErrInstanceRecovery
			}
			out = append(out, model.InstanceFile{Key: key, Size: n, SHA256: a})
			total += n
		} else if os.IsNotExist(e) {
			f, e := instanceCopy(ctx, from, target, b.FileBytes)
			if e != nil {
				return nil, e
			}
			f.Key = key
			out = append(out, f)
			total += f.Size
		} else {
			return nil, e
		}
		if total > b.ExpandedBytes {
			return nil, model.ErrInstanceRecovery
		}
	}
	return out, instanceSyncDir(to)
}
func instanceActivationStatus(p instanceActivation) InstanceActivationResult {
	return InstanceActivationResult{MaterialsCleaned: p.MaterialsCleaned, ID: p.ID, State: p.State, Revision: p.Revision, CompletedFiles: p.Cursor, TotalFiles: len(p.Keys), BackgroundHold: p.State == "completed", RequiresRestart: p.State != "completed" && p.State != "rolled_back"}
}
func ReadInstanceActivation(c config.Config, build InstanceBuild) (InstanceActivationResult, error) {
	p, e := loadInstanceActivation(c, build)
	return instanceActivationStatus(p), e
}

// Caller MUST hold appdata.Acquire(c.DataDir). Running before normal store open
// also supports empty and corrupt targets without migrating or recovering them.
func ActivateInstance(ctx context.Context, c config.Config, build InstanceBuild, options InstanceActivationOptions) error {
	p, e := loadInstanceActivation(c, build)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if p.State != "completed" && !(p.State == "rolled_back" && p.RollbackFinished) {
		if e = instanceModelLayout(c); e != nil {
			return e
		}
	}
	if p.State == "completed" {
		return nil
	}
	if p.State == "rolled_back" {
		if !p.RollbackFinished && options.FinishRollback != nil {
			if e = options.FinishRollback(ctx, c.DataDir, p.ID); e != nil {
				return e
			}
			p.RollbackFinished = true
			p.Revision++
			return writeInstanceActivation(c, p)
		}
		return nil
	}
	root := filepath.Join(c.DataDir, "instance-recovery", p.ID)
	if e = appdata.NoSymlinks(root); e != nil {
		return e
	}
	if p.State == "rollback_requested" {
		if options.Epoch == nil {
			return model.ErrInstanceRecovery
		}
		epoch, e := options.Epoch(ctx, filepath.Join(c.DataDir, "app.sqlite"))
		if e != nil {
			return e
		}
		if epoch != 0 {
			return model.ErrConflict
		}
		if options.CheckRollback == nil {
			return model.ErrInstanceRecovery
		}
		if e = options.CheckRollback(ctx, filepath.Join(c.DataDir, "app.sqlite"), p.ID); e != nil {
			return e
		}
		p.State = "rolling_back"
		p.Cursor = 0
		p.Revision++
		if e = writeInstanceActivation(c, p); e != nil {
			return e
		}
	}
	if p.State == "awaiting_restart" || p.State == "preserving" {
		for _, f := range p.Incoming {
			hash, n, e := instanceHash(ctx, filepath.Join(root, "prepared", filepath.FromSlash(f.Key)))
			if e != nil {
				return e
			}
			if hash != f.SHA256 || n != f.Size {
				return model.ErrInstanceRecovery
			}
		}
		if len(p.Roots) > 0 {
			if options.ValidatePrepared == nil {
				return model.ErrInstanceRecovery
			}
			if e = options.ValidatePrepared(ctx, filepath.Join(root, "prepared/app.sqlite"), p.Roots); e != nil {
				return e
			}
		}
		if _, e = os.Lstat(filepath.Join(c.DataDir, "health-write-unsafe")); e == nil && !p.ReplaceUnhealthyTarget {
			return model.ErrInstanceRecovery
		} else if e != nil && !os.IsNotExist(e) {
			return e
		}
		p.State = "preserving"
		p.Revision++
		if e = writeInstanceActivation(c, p); e != nil {
			return e
		}
		// Every current target file is durably preserved before the first replace.
		before := filepath.Join(root, "before-activation")
		if e = instancePrivateDir(before); e != nil {
			return e
		}
		p.Before, e = preserveInstanceTarget(ctx, c, before, true, DefaultInstanceBudget())
		if e != nil {
			return e
		}
		union := map[string]bool{}
		for _, f := range p.Before {
			union[f.Key] = true
		}
		for _, f := range p.Incoming {
			union[f.Key] = true
		}
		p.Keys = []string{}
		for key := range union {
			p.Keys = append(p.Keys, key)
		}
		sort.Strings(p.Keys)
		// Main DB is installed last, after old WAL/SHM/journal removal and materials.
		for i, key := range p.Keys {
			if key == "app.sqlite" {
				p.Keys = append(append(p.Keys[:i], p.Keys[i+1:]...), key)
				break
			}
		}
		p.State = "activating"
		p.Cursor = 0
		p.Revision++
		if e = writeInstanceActivation(c, p); e != nil {
			return e
		}
	}
	rollback := p.State == "rolling_back"
	if rollback && !p.RollbackFrozen {
		// Session writes, SQLite checkpointing and ordinary cache reads change bytes
		// without business edits. Preserve the actual current stopped file set after
		// the restore identity/epoch check, before the first rollback replacement.
		current := filepath.Join(root, "before-rollback")
		if e = instancePrivateDir(current); e != nil {
			return e
		}
		p.RollbackCurrent, e = preserveInstanceTarget(ctx, c, current, true, DefaultInstanceBudget())
		if e != nil {
			return e
		}
		union := map[string]bool{}
		for _, f := range p.Before {
			union[f.Key] = true
		}
		for _, f := range p.RollbackCurrent {
			union[f.Key] = true
		}
		p.Keys = []string{}
		for key := range union {
			p.Keys = append(p.Keys, key)
		}
		sort.Strings(p.Keys)
		for i, key := range p.Keys {
			if key == "app.sqlite" {
				p.Keys = append(append(p.Keys[:i], p.Keys[i+1:]...), key)
				break
			}
		}
		p.Cursor = 0
		p.RollbackFrozen = true
		p.Revision++
		if e = writeInstanceActivation(c, p); e != nil {
			return e
		}
	}
	wanted, previous := p.Incoming, p.Before
	source := filepath.Join(root, "prepared")
	if rollback {
		wanted, previous = p.Before, p.RollbackCurrent
		source = filepath.Join(root, "before-activation")
	}
	want := map[string]model.InstanceFile{}
	old := map[string]model.InstanceFile{}
	for _, f := range wanted {
		want[f.Key] = f
	}
	for _, f := range previous {
		old[f.Key] = f
	}
	for p.Cursor < len(p.Keys) {
		if e = ctx.Err(); e != nil {
			return e
		}
		key := p.Keys[p.Cursor]
		target, e := instanceTargetPath(c, key)
		if e != nil {
			return e
		}
		desired, exists := want[key]
		prior, had := old[key]
		current, n, readErr := instanceHash(ctx, target)
		already := exists && readErr == nil && current == desired.SHA256 && n == desired.Size || !exists && os.IsNotExist(readErr)
		if !already {
			if readErr != nil && !os.IsNotExist(readErr) {
				return readErr
			}
			if had {
				if readErr != nil || current != prior.SHA256 || n != prior.Size {
					return model.ErrInstanceRecovery
				}
			} else if readErr == nil {
				return model.ErrInstanceRecovery
			}
			if options.Fault != nil {
				if e = options.Fault("before_replace", p.Cursor); e != nil {
					return e
				}
			}
			if exists {
				hash, size, e := instanceHash(ctx, filepath.Join(source, filepath.FromSlash(key)))
				if e != nil {
					return e
				}
				if hash != desired.SHA256 || size != desired.Size {
					return model.ErrInstanceRecovery
				}
				if !strings.HasPrefix(key, "vault/") {
					if e = instancePrivateDir(filepath.Dir(target)); e != nil {
						return e
					}
				}
				pending, e := os.CreateTemp(filepath.Dir(target), "restore-install-")
				if e != nil {
					return e
				}
				name := pending.Name()
				if e = pending.Close(); e != nil {
					return e
				}
				if e = os.Remove(name); e != nil {
					return e
				}
				if _, e = instanceCopy(ctx, filepath.Join(source, filepath.FromSlash(key)), name, DefaultInstanceBudget().FileBytes); e != nil {
					return e
				}
				if e = os.Rename(name, target); e != nil {
					os.Remove(name)
					return e
				}
			} else {
				if e = os.Remove(target); e != nil && !os.IsNotExist(e) {
					return e
				}
			}
			if e = instanceSyncDir(filepath.Dir(target)); e != nil {
				return e
			}
			if options.Fault != nil {
				if e = options.Fault("after_replace", p.Cursor); e != nil {
					return e
				}
			}
		}
		p.Cursor++
		p.Revision++
		if e = writeInstanceActivation(c, p); e != nil {
			return e
		}
	}
	if rollback {
		if options.FinishRollback != nil {
			if e = options.FinishRollback(ctx, c.DataDir, p.ID); e != nil {
				return e
			}
		}
		p.State = "rolled_back"
		p.RollbackFinished = true
	} else {
		p.State = "completed"
	}
	p.Revision++
	return writeInstanceActivation(c, p)
}
