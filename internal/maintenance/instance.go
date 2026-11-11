package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type InstanceRepository interface {
	RequireAdministrator(context.Context) error
	InstanceOperation(context.Context, string) (model.InstanceOperation, error)
	InstanceOperations(context.Context, string, int, int, ...string) ([]model.InstanceOperation, error)
	CreateInstanceOperation(context.Context, string, string, string) (model.InstanceOperation, bool, error)
	SetInstanceOperation(context.Context, string, int64, string, string, string, int64, string, string) (model.InstanceOperation, error)
	FailPendingInstanceUpload(context.Context, string, int64, int64, string) (model.InstanceOperation, error)
	InstancePrivate(context.Context, string) (string, error)
	InstanceState(context.Context) (model.InstanceState, error)
	ReleaseInstanceHold(context.Context, int64) (model.InstanceState, error)
	BeginInstanceProtection(context.Context, string, int64, ...int64) error
	EndInstanceProtection(context.Context, string) error
	HealthStorageLease(context.Context) (func(), error)
	HealthSnapshot(context.Context, string) error
}
type InstanceService struct {
	repo            InstanceRepository
	c               config.Config
	build           InstanceBuild
	files           fs.FS
	budget          InstanceBudget
	Drain           func(context.Context) error
	Inspect         func(context.Context, string, fs.FS) (model.InstanceInventory, error)
	PrepareDatabase func(context.Context, string, string, string, []model.InstanceRebase, []model.InstanceStat) error
	SpaceProbe      func(string) (uint64, error)
	mu              sync.Mutex
	active          map[string]context.CancelFunc
	workers         sync.WaitGroup
	lifecycle       context.Context
}

func NewInstance(repo InstanceRepository, c config.Config, build InstanceBuild, files fs.FS) *InstanceService {
	return &InstanceService{repo: repo, c: c, build: build, files: files, budget: DefaultInstanceBudget(), active: map[string]context.CancelFunc{}, lifecycle: context.Background()}
}
func (s *InstanceService) Start(ctx context.Context) { s.mu.Lock(); s.lifecycle = ctx; s.mu.Unlock() }
func (s *InstanceService) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	for _, cancel := range s.active {
		cancel()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.workers.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *InstanceService) root(id string) (string, error) {
	if id == "" || strings.ContainsAny(id, "/\\.\x00") || len(id) > 100 {
		return "", model.ErrInput
	}
	root := filepath.Join(s.c.DataDir, "instance-recovery", id)
	if e := appdata.NoSymlinks(root); e != nil {
		return "", e
	}
	return root, nil
}
func (s *InstanceService) Operation(ctx context.Context, id string) (model.InstanceOperation, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.InstanceOperation{}, e
	}

	return s.repo.InstanceOperation(ctx, id)
}
func (s *InstanceService) Operations(ctx context.Context, kind string, page, size int, requestKey ...string) ([]model.InstanceOperation, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return nil, e
	}

	return s.repo.InstanceOperations(ctx, kind, page, size, requestKey...)
}
func (s *InstanceService) CreateBackup(ctx context.Context, in model.InstanceRequest) (model.InstanceOperation, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.InstanceOperation{}, e
	}

	if e := instanceModelLayout(s.c); e != nil {
		return model.InstanceOperation{}, e
	}
	if !s.build.valid() {
		return model.InstanceOperation{}, model.ErrInstanceBuild
	}
	if len(in.RequestKey) < 8 || len(in.RequestKey) > 128 || in.Expected != 0 {
		return model.InstanceOperation{}, model.ErrInput
	}
	id, e := model.NewID()
	if e != nil {
		return model.InstanceOperation{}, e
	}
	v, created, e := s.repo.CreateInstanceOperation(ctx, id, "backup", in.RequestKey)
	if e != nil || !created {
		return v, e
	}
	s.mu.Lock()
	child, cancel := context.WithCancel(s.lifecycle)
	if actor, ok := model.Actor(ctx); ok {
		child = model.WithActor(child, actor)
	}
	s.active[v.ID] = cancel
	s.workers.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.workers.Done()
		defer cancel()
		defer func() { s.mu.Lock(); delete(s.active, v.ID); s.mu.Unlock() }()
		s.runBackup(child, v)
	}()
	return v, nil
}
func (s *InstanceService) runBackup(ctx context.Context, v model.InstanceOperation) {
	root, e := s.root(v.ID)
	if e == nil {
		e = instancePrivateDir(root)
	}
	protected := false
	defer func() {
		settle, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if protected {
			if er := s.repo.EndInstanceProtection(settle, v.ID); er != nil {
				e = errors.Join(e, er)
			}
		}
		if e != nil {
			state, code := "failed", instanceFailureCode(e)
			if errors.Is(e, context.Canceled) {
				state = "cancelled"
				code = "cancelled"
			}
			current, er := s.repo.InstanceOperation(settle, v.ID)
			if er == nil && current.State != "completed" {
				_, _ = s.repo.SetInstanceOperation(settle, v.ID, current.Revision, state, current.Phase, "{}", 0, "", code)
			}
		}
	}()
	if e != nil {
		return
	}
	if e = s.repo.BeginInstanceProtection(ctx, v.ID, v.Revision); e != nil {
		return
	}
	protected = true
	v, e = s.repo.SetInstanceOperation(ctx, v.ID, v.Revision, "running", "draining", "{}", 0, "", "")
	if e != nil {
		return
	}
	if s.Drain != nil {
		if e = s.Drain(ctx); e != nil {
			return
		}
	}
	release, e := s.repo.HealthStorageLease(ctx)
	if e != nil {
		return
	}
	staged := false
	defer func() {
		if !staged {
			release()
		}
	}()
	stage := filepath.Join(root, "stage")
	if e = instancePrivateDir(stage); e != nil {
		return
	}
	if e = s.repo.HealthSnapshot(ctx, filepath.Join(stage, "app.sqlite")); e != nil {
		return
	}
	if s.Inspect == nil {
		e = model.ErrInstanceRecovery
		return
	}
	inventory, er := s.Inspect(ctx, filepath.Join(stage, "app.sqlite"), s.files)
	if er != nil {
		e = er
		return
	}
	m := InstanceManifest{Format: InstanceFormat, Application: "nyamangaserver", Baseline: "foundation-v1", Build: s.build, Created: time.Now().Unix(), Inventory: inventory, Secrets: true, Files: []model.InstanceFile{}}
	keys := []string{"app.sqlite"}
	for _, sub := range []string{"assets", "cache/covers", "cache/tag-images", "cache/import-previews", "cache/tag-translations", "indexes/recommendations", "health/repairs", "health/storage-original-verification.json"} {
		base := filepath.Join(s.c.DataDir, sub)
		if e = appdata.NoSymlinks(base); e != nil {
			return
		}
		er = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if os.IsNotExist(err) && path == base {
				return nil
			}
			if err != nil {
				return err
			}
			if d.IsDir() {
				return ctx.Err()
			}
			if !d.Type().IsRegular() {
				return model.ErrInstancePackage
			}
			rel, err := filepath.Rel(s.c.DataDir, path)
			if err != nil {
				return err
			}
			key := filepath.ToSlash(rel)
			if !instanceKey(key) {
				return model.ErrInstancePackage
			}
			keys = append(keys, key)
			if len(keys) > s.budget.Files {
				return model.ErrInstancePackage
			}
			return ctx.Err()
		})
		if er != nil {
			e = er
			return
		}
	}
	var total int64
	for _, key := range keys {
		var f model.InstanceFile
		if key == "app.sqlite" {
			f.SHA256, f.Size, e = instanceHash(ctx, filepath.Join(stage, key))
		} else {
			f, e = instanceCopy(ctx, filepath.Join(s.c.DataDir, filepath.FromSlash(key)), filepath.Join(stage, filepath.FromSlash(key)), s.budget.FileBytes)
		}
		if e != nil {
			return
		}
		f.Key = key
		total += f.Size
		if total > s.budget.ExpandedBytes {
			e = model.ErrInstancePackage
			return
		}
		m.Files = append(m.Files, f)
	}
	// Vault paths are explicit deployment inputs and never appear in the package.
	if s.c.ProviderKeyFile != "" || s.c.ProviderCredentialsFile != "" {
		paired, err := provider.ValidateBackupPair(s.c.ProviderKeyFile, s.c.ProviderCredentialsFile)
		if err != nil {
			e = err
			return
		}
		if paired {
			for _, pair := range [][2]string{{s.c.ProviderKeyFile, "vault/key"}, {s.c.ProviderCredentialsFile, "vault/credentials"}} {
				f, err := instanceCopy(ctx, pair[0], filepath.Join(stage, pair[1]), 64<<10)
				if err != nil {
					e = err
					return
				}
				f.Key = pair[1]
				m.Files = append(m.Files, f)
			}
		}
	}
	if _, e = provider.ValidateBackupPair(filepath.Join(stage, "vault/key"), filepath.Join(stage, "vault/credentials")); e != nil {
		return
	}
	if e = instanceRequired(m); e != nil {
		return
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Key < m.Files[j].Key })
	if e = instanceJSON(filepath.Join(root, "manifest.json"), m); e != nil {
		return
	}
	// Compression never prolongs the global maintenance freeze.
	if e = s.repo.EndInstanceProtection(ctx, v.ID); e != nil {
		return
	}
	protected = false
	release()
	staged = true
	v, e = s.repo.SetInstanceOperation(ctx, v.ID, v.Revision, "running", "compressing", "{}", total, "", "")
	if e != nil {
		return
	}
	pending := filepath.Join(root, "archive.partial")
	if e = writeInstancePackage(ctx, stage, pending, m, s.budget); e != nil {
		return
	}
	digest, n, er := instanceHash(ctx, pending)
	if er != nil {
		e = er
		return
	}
	if e = os.Rename(pending, filepath.Join(root, "archive.zip")); e != nil {
		return
	}
	if e = instanceSyncDir(root); e != nil {
		return
	}
	v, e = s.repo.SetInstanceOperation(ctx, v.ID, v.Revision, "completed", "complete", "{}", n, digest, "")
	// Independent staging is disposable only after the completed archive receipt.
	if e == nil {
		e = os.RemoveAll(stage)
	}
}
func instanceRequired(m InstanceManifest) error {
	members := map[string]model.InstanceFile{}
	for _, f := range m.Files {
		members[f.Key] = f
	}
	for _, f := range m.Inventory.Required {
		v, ok := members[f.Key]
		if !ok || v.SHA256 != f.SHA256 || v.Size != f.Size {
			return model.ErrInstancePackage
		}
	}
	return nil
}
func (s *InstanceService) Cancel(ctx context.Context, id string, expected int64) (model.InstanceOperation, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.InstanceOperation{}, e
	}

	v, e := s.repo.InstanceOperation(ctx, id)
	if e != nil {
		return v, e
	}
	if v.Revision != expected || v.Kind != "backup" || v.State == "completed" {
		return v, model.ErrConflict
	}
	s.mu.Lock()
	cancel := s.active[id]
	if cancel != nil {
		cancel()
	}
	s.mu.Unlock()
	if cancel != nil {
		return v, nil
	}
	// Interrupted processes retain the durable barrier. Explicit cancellation
	// drains current IO and releases only this operation's protection.
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
		release, er := s.repo.HealthStorageLease(ctx)
		if er != nil {
			return v, er
		}
		defer release()
		if e = s.repo.EndInstanceProtection(ctx, id); e != nil {
			return v, e
		}
	}
	return s.repo.SetInstanceOperation(ctx, id, v.Revision, "cancelled", v.Phase, "{}", v.Bytes, "", "cancelled")
}
func (s *InstanceService) Download(ctx context.Context, id string) (*os.File, model.InstanceOperation, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return nil, model.InstanceOperation{}, e
	}

	v, e := s.repo.InstanceOperation(ctx, id)
	if e != nil {
		return nil, v, e
	}
	if v.Kind != "backup" || v.State != "completed" {
		return nil, v, model.ErrConflict
	}
	root, e := s.root(id)
	if e != nil {
		return nil, v, e
	}
	path := filepath.Join(root, "archive.zip")
	digest, n, e := instanceHash(ctx, path)
	if e != nil {
		return nil, v, e
	}
	if digest != v.Digest || n != v.Bytes {
		return nil, v, model.ErrInstancePackage
	}
	f, e := os.Open(path)
	return f, v, e
}

func instanceDecodePrivate(raw string, v any) error {
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(v)
}
