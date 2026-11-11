// Package maintenance owns explicit, durable management operations. Source archives remain read-only.
package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/tagimages"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Repository interface {
	JobContext(context.Context, string) (context.Context, error)
	BackfillRemoteFileInfo(context.Context, model.RemoteFileInfoBackfill) (model.RemoteFileInfoReport, error)
	RequireAdministrator(context.Context) error
	CreateMaintenancePlan(context.Context, model.MaintenanceInput) (model.MaintenancePlan, error)
	MaintenancePlan(context.Context, string) (model.MaintenancePlan, error)
	ExecuteMaintenance(context.Context, string) (*model.Job, error)
	PauseMaintenanceResources(context.Context, model.MaintenancePlan) error
	ApplyMaintenance(context.Context, model.Job, model.Item, model.MaintenancePlan, string, string, string) error
	SetLibraryStatus(context.Context, string, int64, bool) (*model.Library, error)
	Library(context.Context, string) (*model.Library, error)
	Edition(context.Context, string) (*model.Edition, error)
	MaintenanceJobs(context.Context) ([]model.Job, error)
	CheckExecution(context.Context, model.Job) error
	Job(context.Context, string) (*model.Job, error)
	Claim(context.Context, string) (*model.Job, error)
	Items(context.Context, string, int, int, bool) ([]model.Item, error)
	FinishBackground(context.Context, model.Job, string) error
	FailBackgroundItem(context.Context, model.Job, model.Item, string) error
	ControlJob(context.Context, string, string, string) error
	CoverSelection(context.Context, string) (model.CoverSelection, error)
	CreateCoverJob(context.Context, string, int64) (*model.Job, error)
	PruneAssets(context.Context, model.Job, []string) error
	AssetKeys(context.Context) ([]string, error)
	CleanHistory(context.Context, model.Job, []string) error
	Trash(context.Context, string, int, int) (map[string]any, error)
}
type Catalog interface {
	Drain(context.Context) error
	RebuildCover(context.Context, string, int64) error
	RemoveCoverIfUnchanged(string, int64, int64) error
}
type Metadata interface{ Drain(context.Context) error }
type Service struct {
	// BeforeExecute is a deterministic anonymous-test IO barrier.
	BeforeExecute func(context.Context, model.Job)
	// CleanupPreviewReader wraps reads only for deterministic anonymous slow-IO tests.
	CleanupPreviewReader func(io.Reader) io.Reader

	tagImages    *tagimages.Service
	repo         Repository
	c            config.Config
	catalog      Catalog
	metadata     Metadata
	logger       *slog.Logger
	cancel       context.CancelFunc
	done         chan struct{}
	mu           sync.Mutex
	storageMu    sync.Mutex
	storageCache []byte
	storageAt    time.Time
	wake         chan struct{}
	workerMu     sync.Mutex
	activeID     string
	activeCancel context.CancelFunc
}

func New(r Repository, c config.Config, l Catalog, m Metadata, logger *slog.Logger) *Service {
	return &Service{repo: r, c: c, catalog: l, metadata: m, logger: logger, wake: make(chan struct{}, 1)}
}
func (s *Service) WithTagImages(images *tagimages.Service) *Service { s.tagImages = images; return s }
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) root(key string) (config.Root, error) {
	for _, r := range s.c.Roots {
		if r.Key == key {
			return r, nil
		}
	}
	return config.Root{}, model.ErrInput
}
func (s *Service) bounded(key, relative string, dir bool) (string, error) {
	r, e := s.root(key)
	if e != nil {
		return "", e
	}
	if !fs.ValidPath(relative) || strings.Contains(relative, "\\") || appdata.NoSymlinks(filepath.Join(r.Path, relative)) != nil {
		return "", model.ErrInput
	}
	root, e := os.OpenRoot(r.Path)
	if e != nil {
		return "", e
	}
	defer root.Close()
	info, e := root.Stat(relative)
	if e != nil {
		return "", e
	}
	if dir && !info.IsDir() || !dir && !info.Mode().IsRegular() {
		return "", model.ErrInput
	}
	if !dir {
		ext := strings.ToLower(filepath.Ext(relative))
		if ext != ".zip" && ext != ".cbz" {
			return "", model.ErrInput
		}
	}
	return filepath.Join(r.Path, relative), nil
}
func (s *Service) Plan(ctx context.Context, in model.MaintenanceInput) (model.MaintenancePlan, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.MaintenancePlan{}, e
	}
	for !s.mu.TryLock() {
		select {
		case <-ctx.Done():
			return model.MaintenancePlan{}, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return model.MaintenancePlan{}, e
	}
	var o struct {
		Root         string   `json:"root_key"`
		Path         string   `json:"relative_path"`
		Confirm      bool     `json:"confirm_replacement"`
		Categories   []string `json:"categories"`
		OperationIDs []string `json:"operation_ids"`
	}
	if len(in.Options) > 0 && json.Unmarshal(in.Options, &o) != nil {
		return model.MaintenancePlan{}, model.ErrInput
	}
	if in.Operation == "relink" {
		if len(in.BookIDs) != 1 || !o.Confirm {
			return model.MaintenancePlan{}, model.ErrInput
		}
		if _, e := s.bounded(o.Root, o.Path, false); e != nil {
			return model.MaintenancePlan{}, e
		}
	}
	if in.Operation == "rebind" {
		if in.LibraryID == "" {
			return model.MaintenancePlan{}, model.ErrInput
		}
		if _, e := s.bounded(o.Root, ".", true); e != nil {
			return model.MaintenancePlan{}, e
		}
	}
	if in.Operation == "cleanup" {
		if len(o.Categories) == 0 {
			return model.MaintenancePlan{}, model.ErrInput
		}
		for _, c := range o.Categories {
			if !slices.Contains([]string{"covers", "tag_images", "orphan_assets", "tmp", "terminal_jobs", "snapshots", "redundant_covers", "redundant_tag_images", "instance_recovery", "health_rollback", "migration_originals", "recommendation_indexes", "import_preview_cache"}, c) {
				return model.MaintenancePlan{}, model.ErrInput
			}
		}
	}
	var private map[string]json.RawMessage
	if len(in.Options) > 0 {
		_ = json.Unmarshal(in.Options, &private)
	}
	if private == nil {
		private = map[string]json.RawMessage{}
	}
	for key := range private {
		if strings.HasPrefix(key, "_") {
			return model.MaintenancePlan{}, model.ErrInput
		}
	}
	if in.Operation == "relink" {
		fingerprint, e := s.fingerprint(ctx, o.Root, o.Path)
		if e != nil {
			return model.MaintenancePlan{}, e
		}
		private["_fingerprint"], _ = json.Marshal(fingerprint)
	}
	if in.Operation == "rebind" {
		r, e := s.root(o.Root)
		if e != nil {
			return model.MaintenancePlan{}, e
		}
		identity, e := directoryIdentity(r.Path)
		if e != nil {
			return model.MaintenancePlan{}, e
		}
		private["_root_identity"], _ = json.Marshal(identity)
	}
	if in.Operation == "cleanup" {
		entries, e := s.storageInventory(ctx)
		if e != nil {
			return model.MaintenancePlan{}, e
		}
		storage := storageSummary(entries)
		files, summary, e := s.cleanupPreviewEntries(ctx, entries, o.Categories, o.OperationIDs)
		if e != nil {
			return model.MaintenancePlan{}, e
		}
		storage["planned"] = summary
		private["_cleanup_files"], _ = json.Marshal(files)
		private["_storage"], _ = json.Marshal(storage)
	}
	in.Options, _ = json.Marshal(private)
	plan, e := s.repo.CreateMaintenancePlan(ctx, in)
	if e != nil {
		return plan, e
	}
	if in.Operation == "rebind" {
		matched, e := s.validateCorrespondence(ctx, plan, o.Root)
		if e != nil {
			return model.MaintenancePlan{}, e
		}
		plan.Summary["matched_paths"] = matched
	}
	return plan, nil
}

// GetPlan reads persisted progress and terminal history without refreshing the
// inventory or revalidating execution eligibility. Execute retains those guards.
func (s *Service) GetPlan(ctx context.Context, id string) (model.MaintenancePlan, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.MaintenancePlan{}, e
	}
	return s.repo.MaintenancePlan(ctx, id)
}

func (s *Service) Execute(ctx context.Context, id string) (*model.Job, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return nil, e
	}
	j, e := s.repo.ExecuteMaintenance(ctx, id)
	if e == nil {
		s.Wake()
	}
	return j, e
}
func (s *Service) Status(ctx context.Context, id string, rev int64, disabled bool) (*model.Library, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return nil, e
	}
	if rev < 1 {
		return nil, model.ErrInput
	}
	l, e := s.repo.SetLibraryStatus(ctx, id, rev, disabled)
	if e == nil && disabled {
		e = errors.Join(s.catalog.Drain(ctx), s.metadata.Drain(ctx), s.drainInvalidated(ctx))
	}
	return l, e
}
func (s *Service) Entries(ctx context.Context, key, path string, page, size int) (map[string]any, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return nil, e
	}
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		return nil, model.ErrInput
	}
	if path == "" {
		path = "."
	}
	_, e := s.bounded(key, path, true)
	if e != nil {
		return nil, e
	}
	r, _ := s.root(key)
	root, e := os.OpenRoot(r.Path)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	f, e := root.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	entries := []map[string]string{}
	for {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		batch, x := f.ReadDir(100)
		for _, entry := range batch {
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			kind := "directory"
			if !entry.IsDir() {
				ext := strings.ToLower(filepath.Ext(entry.Name()))
				if ext != ".zip" && ext != ".cbz" {
					continue
				}
				kind = "archive"
			}
			entries = append(entries, map[string]string{"name": entry.Name(), "relative_path": filepath.ToSlash(filepath.Join(path, entry.Name())), "kind": kind})
		}
		if x != nil {
			if !errors.Is(x, fs.ErrClosed) && !errors.Is(x, io.EOF) {
				return nil, x
			}
			break
		}
		if len(entries) > 100000 {
			return nil, model.ErrCapacity
		}
	}
	slices.SortFunc(entries, func(a, b map[string]string) int { return strings.Compare(a["name"], b["name"]) })
	total := len(entries)
	start := min(total, (page-1)*size)
	return map[string]any{"items": entries[start:min(total, start+size)], "total": total, "page": page, "page_size": size}, nil
}
func (s *Service) Trash(ctx context.Context, kind string, page, size int) (map[string]any, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return nil, e
	}
	return s.repo.Trash(ctx, kind, page, size)
}
func (s *Service) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			jobs, e := s.repo.MaintenanceJobs(ctx)
			if e == nil {
				for _, j := range jobs {
					if (j.Type == "maintenance" || j.Type == "cover_rebuild") && (j.State == "queued" || j.State == "running") {
						s.run(ctx, j.ID)
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.wake:
			}
		}
	}()
}
func (s *Service) Shutdown(ctx context.Context) error {
	if s.cancel == nil {
		return nil
	}
	s.cancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) run(parent context.Context, id string) {
	if r, ok := s.repo.(interface {
		BeginHealthIO(context.Context) (func(), error)
	}); ok {
		release, e := r.BeginHealthIO(parent)
		if e != nil {
			return
		}
		defer release()
	}
	ctx, cancel := context.WithCancel(parent)
	s.workerMu.Lock()
	s.activeID = id
	s.activeCancel = cancel
	s.workerMu.Unlock()
	defer func() { cancel(); s.workerMu.Lock(); s.activeID = ""; s.activeCancel = nil; s.workerMu.Unlock() }()

	scopedContext, contextErr := s.repo.JobContext(ctx, id)
	if contextErr != nil {
		return
	}
	ctx = scopedContext
	j, e := s.repo.Claim(ctx, id)
	if e != nil {
		return
	}
	items, e := s.repo.Items(ctx, id, 1, 1, true)
	if e != nil || len(items) == 0 {
		_ = s.repo.FinishBackground(ctx, *j, "")
		return
	}
	item := items[0]
	if s.BeforeExecute != nil {
		s.BeforeExecute(ctx, *j)
	}
	if j.Type == "cover_rebuild" {
		if item.BookID == nil {
			_ = s.repo.FailBackgroundItem(ctx, *j, item, "not_found")
			_ = s.repo.FinishBackground(ctx, *j, "not_found")
			return
		}
		current, x := s.repo.CoverSelection(ctx, *item.BookID)
		if x != nil || current.Revision != item.SelectionRevision {
			_ = s.repo.FailBackgroundItem(ctx, *j, item, "revision_conflict")
			_ = s.repo.FinishBackground(ctx, *j, "revision_conflict")
			return
		}
		if item.BookID == nil {
			e = model.ErrInput
		} else {
			e = s.catalog.RebuildCover(ctx, *item.BookID, item.ContentRevision)
		}
		if e == nil {
			e = s.repo.CheckExecution(ctx, *j)
		}
		if e == nil {
			current, x := s.repo.CoverSelection(ctx, *item.BookID)
			e = x
			if e == nil && current.Revision != item.SelectionRevision {
				e = model.ErrConflict
			}
		}
		if e == nil {
			e = s.repo.FailBackgroundItem(ctx, *j, item, "")
		}
	} else {
		var in struct {
			Plan string `json:"plan_id"`
		}
		if json.Unmarshal([]byte(item.Input), &in) != nil {
			e = model.ErrInput
		} else {
			p, x := s.repo.MaintenancePlan(ctx, in.Plan)
			e = x
			if e == nil && p.Operation != "cleanup" && p.Expires < time.Now().Unix() {
				e = model.ErrPreviewExpired
			}
			if e == nil {
				e = s.repo.PauseMaintenanceResources(ctx, p)
			}
			if e == nil {
				e = errors.Join(s.catalog.Drain(ctx), s.metadata.Drain(ctx))
			}
			var options struct {
				Root        string   `json:"root_key"`
				Path        string   `json:"relative_path"`
				Categories  []string `json:"categories"`
				Fingerprint string   `json:"_fingerprint"`
				Identity    string   `json:"_root_identity"`
			}
			_ = json.Unmarshal(p.Input.Options, &options)
			identity := ""
			if e == nil && p.Operation == "relink" {
				fingerprint, x := s.fingerprint(ctx, options.Root, options.Path)
				e = x
				if e == nil && fingerprint != options.Fingerprint {
					e = model.ErrConflict
				}
			}
			if e == nil && p.Operation == "rebind" {
				var path string
				path, e = s.bounded(options.Root, ".", true)
				if e == nil {
					identity, e = directoryIdentity(path)
					if e == nil && identity != options.Identity {
						e = model.ErrConflict
					}
					if e == nil {
						_, e = s.validateCorrespondence(ctx, p, options.Root)
					}
				}
			}
			if e == nil && p.Operation == "cleanup" {
				if p.JobID != j.ID {
					e = model.ErrConflict
				} else {
					e = s.cleanupPhase(ctx, *j, "deleting")
					if e == nil {
						e = s.cleanupFrozen(ctx, *j, p.Input.Options)
					}
				}
			}
			if e == nil {
				e = s.repo.ApplyMaintenance(ctx, *j, item, p, options.Root, identity, options.Path)
			}
		}
	}
	if e != nil {
		if errors.Is(e, context.Canceled) || errors.Is(e, model.ErrStaleJob) {
			return
		}
		_ = s.repo.FailBackgroundItem(ctx, *j, item, safeCode(e))
		_ = s.repo.FinishBackground(ctx, *j, safeCode(e))
		return
	}
	_ = s.repo.FinishBackground(ctx, *j, "")
}
func safeCode(e error) string {
	if errors.Is(e, syscall.ENOSPC) {
		return "storage_full"
	}
	if errors.Is(e, os.ErrPermission) {
		return "permission_denied"
	}
	for _, v := range []error{model.ErrConflict, model.ErrInput, model.ErrBusy, model.ErrNotFound, model.ErrPreviewExpired, model.ErrMembership, model.ErrDisabled, model.ErrTrashed, model.ErrCapacity} {
		if errors.Is(e, v) {
			if v == model.ErrConflict {
				return "revision_conflict"
			}
			return v.Error()
		}
	}
	if errors.Is(e, os.ErrNotExist) {
		return "not_found"
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return "request_timeout"
	}
	for _, code := range []string{"content_changed", "root_unavailable", "root_changed", "file_unavailable", "resource_limit", "page_corrupt", "archive_corrupt"} {
		if e.Error() == code {
			return code
		}
	}
	return "maintenance_failed"
}
func (s *Service) fingerprint(ctx context.Context, key, path string) (string, error) {
	if _, e := s.bounded(key, path, false); e != nil {
		return "", e
	}
	r, _ := s.root(key)
	root, e := os.OpenRoot(r.Path)
	if e != nil {
		return "", e
	}
	defer root.Close()
	f, e := root.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	before, e := f.Stat()
	if e != nil {
		return "", e
	}
	h := sha256.New()
	buf := make([]byte, 128<<10)
	for {
		if e = ctx.Err(); e != nil {
			return "", e
		}
		n, x := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if x == io.EOF {
			break
		}
		if x != nil {
			return "", x
		}
	}
	after, e := f.Stat()
	if e != nil {
		return "", e
	}
	if before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return "", model.ErrConflict
	}
	return fmt.Sprintf("%x:%d:%d", h.Sum(nil), after.Size(), after.ModTime().UnixNano()), nil
}

func (s *Service) Retry(ctx context.Context, id string) (*model.Job, error) {
	j, e := s.repo.Job(ctx, id)
	if e != nil {
		return nil, e
	}
	if j.State != "failed" && j.State != "cancelled" && j.State != "completed" {
		return nil, model.ErrConflict
	}
	items, e := s.repo.Items(ctx, id, 1, 1, false)
	if e != nil || len(items) != 1 {
		return nil, model.ErrInput
	}
	if items[0].State == "completed" {
		return nil, model.ErrInput
	}
	if j.Type == "cover_rebuild" {
		if items[0].BookID == nil {
			return nil, model.ErrInput
		}
		v, e := s.repo.CoverSelection(ctx, *items[0].BookID)
		if e != nil {
			return nil, e
		}
		next, e := s.repo.CreateCoverJob(ctx, *items[0].BookID, v.Revision)
		if e == nil {
			s.Wake()
		}
		return next, e
	}
	var in struct {
		Plan string `json:"plan_id"`
	}
	if json.Unmarshal([]byte(items[0].Input), &in) != nil {
		return nil, model.ErrInput
	}
	p, e := s.repo.MaintenancePlan(ctx, in.Plan)
	if e != nil {
		return nil, e
	}
	next, e := s.repo.CreateMaintenancePlan(ctx, p.Input)
	if e != nil {
		return nil, e
	}
	return s.Execute(ctx, next.ID)
}

// Control invalidates durable ownership before cancelling local IO. The single
// worker loop cannot resume or start a replacement until the old IO returns.
func (s *Service) Control(ctx context.Context, id, action string) error {
	job, e := s.repo.Job(ctx, id)
	if e != nil {
		return e
	}
	if job.Type != "maintenance" && job.Type != "cover_rebuild" {
		return model.ErrInput
	}
	if e = s.repo.ControlJob(ctx, id, action, ""); e != nil {
		return e
	}
	if action != "resume" {
		s.workerMu.Lock()
		if s.activeID == id && s.activeCancel != nil {
			s.activeCancel()
		}
		s.workerMu.Unlock()
	}
	s.Wake()
	return nil
}

// Rebinding changes only the configured root. Existing non-missing archive paths
// must exist beneath the new root; changed contents are deliberately reindexed.
func (s *Service) validateCorrespondence(ctx context.Context, p model.MaintenancePlan, key string) (int, error) {
	matched := 0
	for _, id := range p.Affected {
		if e := ctx.Err(); e != nil {
			return 0, e
		}
		book, e := s.repo.Edition(ctx, id)
		if e != nil {
			return 0, e
		}
		if _, e = s.bounded(key, book.Path, false); e != nil {
			if book.State == "missing" && errors.Is(e, os.ErrNotExist) {
				continue
			}
			return 0, e
		}
		matched++
	}
	return matched, nil
}

func (s *Service) drainInvalidated(ctx context.Context) error {
	for {
		s.workerMu.Lock()
		id, stop := s.activeID, s.activeCancel
		s.workerMu.Unlock()
		if id == "" {
			return nil
		}
		job, e := s.repo.Job(ctx, id)
		if e == nil && job.State != "paused" && job.State != "cancelled" {
			return nil
		}
		if stop != nil {
			stop()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *Service) Drain(ctx context.Context) error {
	for {
		s.workerMu.Lock()
		id, stop := s.activeID, s.activeCancel
		s.workerMu.Unlock()
		if id == "" {
			return nil
		}
		if stop != nil {
			stop()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *Service) cleanupPhase(ctx context.Context, j model.Job, phase string) error {
	if r, ok := s.repo.(interface {
		CleanupPhase(context.Context, model.Job, string) error
	}); ok {
		return r.CleanupPhase(ctx, j, phase)
	}
	return nil
}
