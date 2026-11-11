package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"os"
	"path/filepath"
	"slices"

	"strings"
	"sync"
	"syscall"
	"time"
)

// HealthRepository is separate from legacy cleanup/relink operations.
type HealthRepository interface {
	JobContext(context.Context, string) (context.Context, error)
	RequireAdministrator(context.Context) error
	CreateHealthCheck(context.Context, model.HealthCheckInput) (model.HealthCheck, error)
	HealthCheck(context.Context, string) (model.HealthCheck, error)
	HealthChecks(context.Context, int, int) (model.HealthPage[model.HealthCheck], error)
	HealthFindings(context.Context, string, string, string, int, int) (model.HealthPage[model.HealthFinding], error)
	HealthObjects(context.Context, string, string, model.HealthCheckInput) ([]model.HealthObject, error)
	HealthObjectStamp(context.Context, string, string) (string, int64, error)
	HealthRules(context.Context, model.HealthObject) ([]model.HealthFinding, error)
	HealthDatabase(context.Context, bool, string) ([]model.HealthFinding, error)
	HealthTaskRules(context.Context) ([]model.HealthFinding, error)
	HealthFiles(context.Context, string, string) ([]model.HealthObject, error)
	HealthCheckpoint(context.Context, model.Job, int, string, []model.HealthFinding, int64, bool, string) error
	RecoverHealthProtection(context.Context, int64) error
	HealthState(context.Context) (model.HealthState, error)
	HealthWriteAllowed(context.Context) error
	HealthJobs(context.Context) ([]model.Job, error)
	HealthRecover(context.Context) error
	Job(context.Context, string) (*model.Job, error)
	Claim(context.Context, string) (*model.Job, error)
	CheckExecution(context.Context, model.Job) error
	ControlJob(context.Context, string, string, string) error
	HealthSnapshot(context.Context, string) error
	HealthDatabaseBytes(context.Context) (int64, error)
	VerifyHealthPostState(context.Context, model.HealthAction, string) error
	HealthDictionaryValid(context.Context) error
	HealthDictionaryMatches(context.Context, map[model.TagName]string) (bool, error)
	HealthStepReceipt(context.Context, string, int) (string, string, string, error)
	HealthArtifact(context.Context, string) (string, int64, error)
	HealthStorageLease(context.Context) (func(), error)
	CreateHealthPlan(context.Context, model.HealthPlanInput, int64) (model.HealthPlan, error)
	HealthPlan(context.Context, string) (model.HealthPlan, error)
	ExecuteHealthPlan(context.Context, string, model.HealthExecuteInput) (model.HealthRepair, error)
	HealthRepairByRequest(context.Context, string) (model.HealthRepair, error)
	HealthFileWrite(context.Context, model.Job, func() error) error
	HealthRepair(context.Context, string) (model.HealthRepair, error)
	SetHealthRepair(context.Context, model.Job, string, string, int) error
	HealthRepairObject(context.Context, model.HealthAction) (model.HealthObject, error)
	ApplyHealthRepair(context.Context, model.Job, string, int, model.HealthAction, string) (string, error)
	RollbackHealthStep(context.Context, model.Job, int, model.HealthAction, string, string) error
	FinishHealthStep(context.Context, model.Job, int, string, string, string) error
	PinHealthArtifact(context.Context, string, string, string, string, int64) error
	EndHealthMaintenance(context.Context, model.Job, bool) error
}
type HealthService struct {
	repo            HealthRepository
	c               config.Config
	pool            *comic.Pool
	mu              sync.Mutex
	cancel          context.CancelFunc
	done            chan struct{}
	wake            chan struct{}
	Drain           func(context.Context) error
	BeforeIO        func(string) error // Anonymous failure injection, never configured by clients.
	BeforeObject    func(context.Context, model.HealthObject)
	CoverBytes      func(context.Context, string, bool) (string, []byte, error)
	TagBytes        func(context.Context, string, string) (string, []byte, error)
	DictionaryBytes func(context.Context) (map[model.TagName]string, error)
}

func NewHealth(r HealthRepository, c config.Config, p *comic.Pool) *HealthService {
	return &HealthService{repo: r, c: c, pool: p, wake: make(chan struct{}, 1)}
}

// CheckWriteAllowed is an internal admission check, not a maintenance detail API.
// It preserves the caller's actor and returns no global state.
func (h *HealthService) CheckWriteAllowed(ctx context.Context) error {
	return h.repo.HealthWriteAllowed(ctx)
}
func (h *HealthService) State(ctx context.Context) (model.HealthState, error) {
	if e := model.AuthorizeAdministrator(ctx, h.repo); e != nil {
		return model.HealthState{}, e
	}

	return h.repo.HealthState(ctx)
}
func (h *HealthService) Create(ctx context.Context, in model.HealthCheckInput) (model.HealthCheck, error) {
	if e := model.AuthorizeAdministrator(ctx, h.repo); e != nil {
		return model.HealthCheck{}, e
	}

	if in.Mode == "" {
		in.Mode = "application"
	}
	if !slices.Contains([]string{"quick", "application", "sources"}, in.Mode) || len(in.RequestKey) < 8 || len(in.RequestKey) > 128 || len(in.LibraryIDs) > 1000 {
		return model.HealthCheck{}, model.ErrInput
	}
	if in.Mode == "sources" && len(in.LibraryIDs) == 0 || in.Mode != "sources" && len(in.LibraryIDs) > 0 {
		return model.HealthCheck{}, model.ErrInput
	}
	if in.MaxBytes == 0 {
		in.MaxBytes = 8 << 30
	}
	if in.MaxBytes < 1 || in.MaxBytes > 1<<40 {
		return model.HealthCheck{}, model.ErrInput
	}
	seen := map[string]bool{}
	for _, id := range in.LibraryIDs {
		if id == "" || len(id) > 128 || seen[id] {
			return model.HealthCheck{}, model.ErrInput
		}
		seen[id] = true
	}
	out, e := h.repo.CreateHealthCheck(ctx, in)
	if e == nil {
		h.Wake()
	}
	return out, e
}
func (h *HealthService) Wake() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}
func (h *HealthService) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	h.cancel = cancel
	h.done = make(chan struct{})
	go func() {
		defer close(h.done)
		if model.AutomaticWorkAllowed(ctx, h.repo) && h.repo.HealthRecover(ctx) != nil {
			return
		}
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			h.Tick(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-h.wake:
			}
		}
	}()
}
func (h *HealthService) Shutdown(ctx context.Context) error {
	if h.cancel == nil {
		return nil
	}
	h.cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-h.done:
		return nil
	}
}
func (h *HealthService) Tick(ctx context.Context) {
	if !model.AutomaticWorkAllowed(ctx, h.repo) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	jobs, e := h.repo.HealthJobs(ctx)
	if e != nil {
		return
	}
	for _, j := range jobs {
		if ctx.Err() != nil {
			return
		}
		h.run(ctx, j)
	}
}

// Instance protection has already invalidated/paused health jobs. Wait until
// their bounded IO and checkpoints leave Tick before freezing target files.
func (h *HealthService) DrainChecks(ctx context.Context) error {
	for !h.mu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	h.mu.Unlock()
	return nil
}
func (h *HealthService) run(parent context.Context, j model.Job) {
	scopedContext, contextErr := h.repo.JobContext(parent, j.ID)
	if contextErr != nil {
		return
	}
	parent = scopedContext
	job, e := h.repo.Claim(parent, j.ID)
	if e != nil {
		return
	}
	j = *job
	if _, e := h.repo.HealthRepair(parent, j.ID); e == nil {
		h.runRepair(parent, j)
		return
	}
	v, e := h.repo.HealthCheck(parent, j.ID)
	if e != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if h.repo.CheckExecution(ctx, j) != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-done }()
	work := func() error {
		root := filepath.Join(h.c.DataDir, "health", "checks", j.ID)
		if e := os.MkdirAll(root, 0700); e != nil {
			return e
		}
		if e := appdata.NoSymlinks(root); e != nil {
			return e
		}
		phases := []string{"database", "work", "series", "edition", "tasks", "asset", "files", "source"}
		for v.Phase < len(phases) {
			if e := h.repo.CheckExecution(ctx, j); e != nil {
				return e
			}
			kind := phases[v.Phase]
			if kind == "database" {
				limited, stop := context.WithTimeout(ctx, 30*time.Second)
				findings, e := h.repo.HealthDatabase(limited, v.Mode != "quick", filepath.Join(root, "inspection.sqlite"))
				stop()
				if v.Mode != "quick" {
					_ = os.Remove(filepath.Join(root, "inspection.sqlite"))
				}
				if e != nil {
					return e
				}
				if e = h.repo.HealthCheckpoint(ctx, j, v.Phase+1, "", findings, 0, false, ""); e != nil {
					return e
				}
				v.Phase++
				v.Cursor = ""
				continue
			}
			if kind == "tasks" {
				f, e := h.repo.HealthTaskRules(ctx)
				if e != nil {
					return e
				}
				if e = h.repo.HealthCheckpoint(ctx, j, v.Phase+1, "", f, 0, false, ""); e != nil {
					return e
				}
				v.Phase++
				continue
			}
			if kind == "files" {
				if v.Mode == "quick" {
					f := model.HealthFinding{Category: "indexes", ObjectKind: "scope", ObjectID: "application", Status: "not_checked", Code: "quick_scope"}
					if e = h.repo.HealthCheckpoint(ctx, j, v.Phase+1, "", []model.HealthFinding{f}, 0, false, ""); e != nil {
						return e
					}
				} else {
					for {
						objects, e := h.repo.HealthFiles(ctx, v.ID, v.Cursor)
						if e != nil {
							return e
						}
						if len(objects) == 0 {
							break
						}
						for _, o := range objects {
							cursor := o.Kind + ":" + o.ID
							if cursor <= v.Cursor {
								continue
							}
							current, _, err := h.repo.HealthObjectStamp(ctx, o.Kind, o.ID)
							if err != nil {
								return err
							}
							var f model.HealthFinding
							var n int64
							if current == o.Fingerprint {
								f, n = h.inspectManaged(ctx, o)
							} else {
								f = h.stamp(ctx, o, healthManagedCategory(o))
							}
							if current != o.Fingerprint {
								f.Fingerprint = o.Fingerprint
								f.Status = "stale"
								f.Code = "target_changed_since_start"
								f.Repair = ""
							}
							if e = h.repo.HealthCheckpoint(ctx, j, v.Phase, cursor, []model.HealthFinding{f}, n, false, ""); e != nil {
								return e
							}
							v.Cursor = cursor
						}
					}
					if e = h.repo.HealthCheckpoint(ctx, j, v.Phase+1, "", nil, 0, false, ""); e != nil {
						return e
					}
				}
				v.Phase++
				v.Cursor = ""
				continue
			}
			skip := v.Mode == "quick" && kind == "asset" || kind == "source" && v.Mode != "sources"
			if skip {
				category := kind
				if kind == "asset" {
					category = "assets"
				}
				if kind == "edition" {
					category = "projections"
				}
				if kind == "source" {
					category = "sources"
				}
				if e = h.repo.HealthCheckpoint(ctx, j, v.Phase+1, "", []model.HealthFinding{{Category: category, ObjectKind: "scope", ObjectID: kind, Status: "not_checked", Code: "outside_requested_mode"}}, 0, false, ""); e != nil {
					return e
				}
				v.Phase++
				v.Cursor = ""
				continue
			}
			objects, e := h.repo.HealthObjects(ctx, kind, v.Cursor, v.Scope)
			if e != nil {
				return e
			}
			if len(objects) == 0 {
				v.Phase++
				v.Cursor = ""
				if e = h.repo.HealthCheckpoint(ctx, j, v.Phase, "", nil, 0, false, ""); e != nil {
					return e
				}
				continue
			}
			for _, o := range objects {
				if h.BeforeObject != nil {
					h.BeforeObject(ctx, o)
				}
				var f []model.HealthFinding
				var n int64
				current, _, stampErr := h.repo.HealthObjectStamp(ctx, o.Kind, o.ID)
				if stampErr != nil {
					return stampErr
				}
				if current != o.Fingerprint {
					category := "organization"
					if kind == "source" {
						category = "sources"
					} else if kind == "asset" {
						category = "assets"
					}
					f = []model.HealthFinding{{Category: category, ObjectKind: o.Kind, ObjectID: o.ID, Fingerprint: o.Fingerprint, ObjectRevision: o.Revision, Status: "stale", Code: "target_changed_since_start"}}
				} else if kind == "source" {
					one, bytes := h.inspectSource(ctx, o, max(0, v.Scope.MaxBytes-v.SourceBytesRead))
					f = []model.HealthFinding{one}
					n = bytes
				} else if kind == "asset" {
					one, bytes := h.inspectManaged(ctx, o)
					f = []model.HealthFinding{one}
					n = bytes
				} else {
					if v.Mode == "quick" {
						o.Role = "quick_core"
					}
					f, e = h.repo.HealthRules(ctx, o)
					if e != nil {
						return e
					}
				}
				if e = h.repo.HealthCheckpoint(ctx, j, v.Phase, o.ID, f, n, false, ""); e != nil {
					return e
				}
				v.Cursor = o.ID
				v.BytesRead += n
				if kind == "source" {
					v.SourceBytesRead += n
				}
			}
		}
		return h.repo.HealthCheckpoint(ctx, j, v.Phase, "", nil, 0, true, "")
	}
	e = work()
	if e != nil && ctx.Err() == nil {
		_ = h.repo.HealthCheckpoint(ctx, j, v.Phase, v.Cursor, nil, 0, true, healthCode(e))
	}
}
func healthCode(e error) string {
	switch {
	case errors.Is(e, os.ErrPermission):
		return "permission_denied"
	case errors.Is(e, syscall.ENOSPC):
		return "storage_full"
	case errors.Is(e, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(e, context.Canceled):
		return "cancelled"
	case errors.Is(e, model.ErrHealthStale), errors.Is(e, model.ErrConflict), errors.Is(e, comic.ErrChanged):
		return "revision_conflict"
	case errors.Is(e, comic.ErrLimit):
		return "resource_limit"
	case errors.Is(e, model.ErrHealthRecovery):
		return "health_recovery_required"
	case errors.Is(e, os.ErrNotExist):
		return "not_found"
	default:
		return "health_operation_failed"
	}
}
func (h *HealthService) stamp(ctx context.Context, o model.HealthObject, category string) model.HealthFinding {
	hash, rev, _ := h.repo.HealthObjectStamp(ctx, o.Kind, o.ID)
	if o.Fingerprint != "" {
		hash = o.Fingerprint
		rev = o.Revision
	}
	raw, _ := json.Marshal(o)
	return model.HealthFinding{Category: category, ObjectKind: o.Kind, ObjectID: o.ID, ObjectRevision: rev, Fingerprint: hash, Status: "normal", Code: "verified", Private: raw}
}
func (h *HealthService) inspectSource(ctx context.Context, o model.HealthObject, budget int64) (model.HealthFinding, int64) {
	f := h.stamp(ctx, o, "sources")
	if o.Disabled {
		f.Status = "not_checked"
		f.Code = "library_disabled"
		return f, 0
	}
	if budget <= 0 {
		f.Status = "not_checked"
		f.Code = "resource_limit"
		return f, 0
	}
	root := ""
	for _, r := range h.c.Roots {
		if r.Key == o.RootKey {
			root = r.Path
			break
		}
	}
	if root == "" {
		f.Status = "inaccessible"
		f.Code = "root_unconfigured"
		return f, 0
	}
	if !fs.ValidPath(o.Relative) {
		f.Status = "manual"
		f.Code = "source_path_unsafe"
		return f, 0
	}
	if err := appdata.NoSymlinks(filepath.Join(root, o.Relative)); err != nil {
		f.Status = "manual"
		f.Code = "source_path_unsafe"
		if errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrNotExist) {
			f.Status = "inaccessible"
			f.Code = "source_unavailable"
		}
		return f, 0
	}

	identity, e := directoryIdentity(root)
	if e != nil {
		f.Status = "inaccessible"
		f.Code = "root_unavailable"
		return f, 0
	}
	if identity != o.RootIdentity {
		f.Status = "inaccessible"
		f.Code = "root_identity_changed"
		return f, 0
	}
	if h.BeforeIO != nil {
		e = h.BeforeIO("source")
		if e != nil {
			f.Status = "inaccessible"
			f.Code = healthCode(e)
			return f, 0
		}
	}
	probe, e := os.OpenRoot(root)
	if e != nil {
		f.Status = "inaccessible"
		f.Code = "root_unavailable"
		return f, 0
	}
	source, err := probe.Open(o.Relative)
	probe.Close()
	if err != nil {
		f.Status = "inaccessible"
		f.Code = "source_unavailable"
		return f, 0
	}
	info, err := source.Stat()
	source.Close()
	if err != nil {
		f.Status = "inaccessible"
		f.Code = "source_unavailable"
		return f, 0
	}
	if !info.Mode().IsRegular() {
		f.Status = "manual"
		f.Code = "source_not_regular"
		return f, 0
	}
	if info.Size() != o.Size || info.ModTime().UnixNano() != o.Mtime {
		f.Status = "stale"
		f.Code = "source_changed"
		return f, 0
	}
	pages, n, e := h.pool.VerifyArchive(ctx, root, o.Relative, o.Encoding, o.Size, o.Mtime, budget)
	f.Details, _ = json.Marshal(map[string]any{"pages_verified": pages, "image_payload_bytes": n})
	f.Status, f.Code = sourceHealthState(e)
	return f, n
}
func (h *HealthService) managed(key string) (string, error) {
	if !fs.ValidPath(key) || strings.Contains(key, "\\") {
		return "", model.ErrInput
	}
	path := filepath.Join(h.c.DataDir, filepath.FromSlash(key))
	if e := appdata.NoSymlinks(path); e != nil {
		return "", e
	}
	return path, nil
}
func (h *HealthService) inspectManaged(ctx context.Context, o model.HealthObject) (model.HealthFinding, int64) {
	category := healthManagedCategory(o)
	f := h.stamp(ctx, o, category)
	if o.Kind == "dictionary" && o.ID == "not_installed" {
		f.Code = "dictionary_not_installed"
		return f, 0
	}
	path, e := h.managed(o.Key)
	if e != nil || !healthManagedPrefix(o.Kind, o.Key) {
		f.Status = "manual"
		f.Code = "managed_path_unsafe"
		return f, 0
	}
	info, e := os.Stat(path)
	if e != nil {
		if errors.Is(e, os.ErrNotExist) && o.Kind == "series_block" {
			f.Status = "confirmed"
			f.Code = "checkpoint_file_missing"
			return f, 0
		}
		if errors.Is(e, os.ErrNotExist) && o.Kind == "dictionary" {
			var extra struct {
				Ready bool `json:"index_ready"`
			}
			_ = json.Unmarshal(o.Extra, &extra)
			if !extra.Ready {
				f.Status = "manual"
				f.Code = "dictionary_input_missing"
				return f, 0
			}
		}
		if errors.Is(e, os.ErrNotExist) && o.Kind != "asset" {
			f.Code = "derived_evicted"
			return f, 0
		}
		f.Status = "inaccessible"
		if o.Kind == "asset" && errors.Is(e, os.ErrNotExist) {
			f.Status = "confirmed"
		}
		f.Code = "managed_file_unavailable"
		return f, 0
	}
	if !info.Mode().IsRegular() {
		f.Status = "manual"
		f.Code = "managed_not_regular"
		return f, 0
	}
	limit := int64(comic.MaxPage)
	switch o.Kind {
	case "series_block":
		limit = 8 << 20
	case "recommendation_index":
		limit = 65536
	case "dictionary":
		limit = 32 << 20
	}
	if info.Size() > limit {
		f.Status = "not_checked"
		f.Code = "resource_limit"
		return f, 0
	}
	file, e := os.Open(path)
	if e != nil {
		f.Status = "inaccessible"
		f.Code = healthCode(e)
		return f, 0
	}
	defer file.Close()
	raw, e := io.ReadAll(io.LimitReader(file, limit+1))
	if e != nil {
		f.Status = "inaccessible"
		f.Code = healthCode(e)
		return f, 0
	}
	if int64(len(raw)) > limit {
		f.Status = "not_checked"
		f.Code = "resource_limit"
		return f, int64(len(raw))
	}
	if e = ctx.Err(); e != nil {
		f.Status = "not_checked"
		f.Code = "interrupted"
		return f, int64(len(raw))
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	o.ObservedDigest = hash
	o.ObservedSize = int64(len(raw))
	o.ObservedMtime = info.ModTime().UnixNano()
	f.Private, _ = json.Marshal(o)
	bad := o.Digest != "" && hash != o.Digest || o.Size > 0 && o.Size != int64(len(raw))
	if o.Kind == "asset" || o.Kind == "series_block" {
		bad = bad || o.Digest == "" || o.Size <= 0 || o.Size != int64(len(raw))
	}
	if o.Kind == "asset" || o.Kind == "cover_cache" || o.Kind == "tag_cache" {
		var width, height int
		var format string
		if _, width, height, format, e = h.pool.TagImage(ctx, raw, "foreground"); e != nil {
			if errors.Is(e, comic.ErrLimit) {
				f.Status = "not_checked"
				f.Code = "resource_limit"
				return f, int64(len(raw))
			}
			bad = true
		}
		if o.Kind == "asset" && len(o.Extra) > 0 {
			var refs []struct {
				Width  int    `json:"width"`
				Height int    `json:"height"`
				Format string `json:"format"`
			}
			if json.Unmarshal(o.Extra, &refs) != nil {
				bad = true
			} else {
				for _, ref := range refs {
					if ref.Width != width || ref.Height != height || ref.Format != format {
						bad = true
					}
				}
			}
		}
		if o.Width > 0 && (o.Width != int64(width) || o.Height != int64(height)) {
			bad = true
		}
	}
	if o.Kind == "dictionary" {
		var extra struct {
			Ready bool `json:"index_ready"`
		}
		_ = json.Unmarshal(o.Extra, &extra)
		data, err := provider.ParseDictionary(raw)
		if err != nil {
			bad = true
		} else if !bad {
			matches, err := h.repo.HealthDictionaryMatches(ctx, data)
			if err != nil {
				f.Status = "not_checked"
				f.Code = "dictionary_check_unavailable"
				return f, int64(len(raw))
			}
			extra.Ready = extra.Ready && matches
		}
		if !bad && !extra.Ready {
			f.Status = "confirmed"
			f.Code = "dictionary_index_invalid"
			f.Repair = "dictionary"
		}
	}
	if o.Kind == "recommendation_index" {
		var expected struct {
			Version string `json:"algorithm_version"`
		}
		_ = json.Unmarshal(o.Extra, &expected)
		if expected.Version != healthFeatureVersion(filepath.Base(filepath.Dir(o.Key))) {
			f.Status = "not_checked"
			f.Code = "feature_version_unsupported"
			return f, int64(len(raw))
		}
	}
	if o.Kind == "recommendation_index" && !validHealthFeature(raw, o.Extra, filepath.Base(filepath.Dir(o.Key))) {
		bad = true
	}
	if o.Kind == "series_block" {
		status, code := checkHealthSeriesBlock(raw, o.Extra)
		if status != "normal" {
			f.Status = status
			f.Code = code
			if status == "not_checked" {
				return f, int64(len(raw))
			}
			bad = true
		}
	}
	if bad {
		f.Status = "confirmed"
		f.Code = "managed_content_invalid"
		if o.Kind != "asset" && o.Kind != "dictionary" {
			f.Repair = o.Role
		}
	}
	return f, int64(len(raw))
}

func (h *HealthService) Checks(ctx context.Context, page, size int) (model.HealthPage[model.HealthCheck], error) {
	if e := model.AuthorizeAdministrator(ctx, h.repo); e != nil {
		return model.HealthPage[model.HealthCheck]{}, e
	}

	return h.repo.HealthChecks(ctx, page, size)
}
func (h *HealthService) Check(ctx context.Context, id string) (model.HealthCheck, error) {
	if e := model.AuthorizeAdministrator(ctx, h.repo); e != nil {
		return model.HealthCheck{}, e
	}

	return h.repo.HealthCheck(ctx, id)
}
func (h *HealthService) Findings(ctx context.Context, id, status, category string, page, size int) (model.HealthPage[model.HealthFinding], error) {
	if e := model.AuthorizeAdministrator(ctx, h.repo); e != nil {
		return model.HealthPage[model.HealthFinding]{}, e
	}

	if status != "" && !slices.Contains([]string{"normal", "confirmed", "inaccessible", "manual", "not_checked", "stale"}, status) {
		return model.HealthPage[model.HealthFinding]{}, model.ErrInput
	}
	if category != "" && !slices.Contains([]string{"database", "organization", "projections", "assets", "caches", "indexes", "tasks", "sources"}, category) {
		return model.HealthPage[model.HealthFinding]{}, model.ErrInput
	}
	return h.repo.HealthFindings(ctx, id, status, category, page, size)
}
func (h *HealthService) PlanDetail(ctx context.Context, id string) (model.HealthPlan, error) {
	if e := model.AuthorizeAdministrator(ctx, h.repo); e != nil {
		return model.HealthPlan{}, e
	}

	return h.repo.HealthPlan(ctx, id)
}
func (h *HealthService) Repair(ctx context.Context, id string) (model.HealthRepair, error) {
	if e := model.AuthorizeAdministrator(ctx, h.repo); e != nil {
		return model.HealthRepair{}, e
	}

	v, e := h.repo.HealthRepair(ctx, id)
	if e != nil {
		return v, e
	}
	journal, _, err := h.loadJournal(id)
	if os.IsNotExist(err) {
		v.JournalState = "not_started"
		return v, nil
	}
	if err != nil || len(journal.Steps) != len(v.Steps) || h.validateJournal(ctx, v, journal) != nil {
		v.JournalState = "unavailable"
		return v, nil
	}
	v.JournalState = journal.Phase
	for i, step := range journal.Steps {
		v.Steps[i].FileState = step.State
		if v.Steps[i].State == "rolled_back" && step.State != "rolled_back" {
			v.Steps[i].State = "rollback_pending"
		}
	}
	return v, nil
}

func (h *HealthService) Control(ctx context.Context, id, action string) error {
	if e := model.AuthorizeAdministrator(ctx, h.repo); e != nil {
		return e
	}

	e := h.repo.ControlJob(ctx, id, action, "")
	if e == nil {
		h.Wake()
	}
	return e
}

func (h *HealthService) Owns(ctx context.Context, id string) bool {
	if _, e := h.repo.HealthCheck(ctx, id); e == nil {
		return true
	}
	_, e := h.repo.HealthRepair(ctx, id)
	return e == nil
}

func (h *HealthService) RecoverProtection(ctx context.Context, in model.HealthExecuteInput) (model.HealthState, error) {
	if e := model.AuthorizeAdministrator(ctx, h.repo); e != nil {
		return model.HealthState{}, e
	}

	if !in.ConfirmMaintenance || in.ExpectedRevision < 1 || len(in.RequestKey) < 8 || len(in.RequestKey) > 128 {
		return model.HealthState{}, model.ErrInput
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	state, e := h.repo.HealthState(ctx)
	if e != nil {
		return model.HealthState{}, e
	}
	if state.Phase == "idle" {
		return state, nil
	}
	if h.Drain != nil {
		if e = h.Drain(ctx); e != nil {
			return model.HealthState{}, e
		}
	}
	release, e := h.repo.HealthStorageLease(ctx)
	if e != nil {
		return model.HealthState{}, e
	}
	defer release()
	if e := h.repo.RecoverHealthProtection(ctx, in.ExpectedRevision); e != nil {
		return model.HealthState{}, e
	}
	return h.repo.HealthState(ctx)
}

func healthManagedPrefix(kind, key string) bool {
	switch kind {
	case "asset":
		return strings.HasPrefix(key, "assets/")
	case "cover_cache":
		return strings.HasPrefix(key, "cache/covers/v1/")
	case "tag_cache":
		return strings.HasPrefix(key, "cache/tag-images/v1/")
	case "dictionary":
		return strings.HasPrefix(key, "cache/tag-translations/")
	case "recommendation_index":
		return strings.HasPrefix(key, "indexes/recommendations/features/")
	case "series_block":
		return strings.HasPrefix(key, "indexes/recommendations/series/")
	case "recommendation":
		return strings.HasPrefix(key, "indexes/recommendations/")
	}
	return false
}

func sourceHealthState(e error) (string, string) {
	switch {
	case e == nil:
		return "normal", "archive_verified"
	case errors.Is(e, comic.ErrLimit):
		return "not_checked", "resource_limit"
	case errors.Is(e, comic.ErrChanged):
		return "stale", "source_changed"
	case errors.Is(e, comic.ErrEncoding):
		return "manual", "archive_encoding_unrecognized"
	case errors.Is(e, comic.ErrName):
		return "manual", "archive_name_unsafe"
	case errors.Is(e, os.ErrNotExist), errors.Is(e, os.ErrPermission), errors.Is(e, syscall.EIO):
		return "inaccessible", "source_unavailable"
	case errors.Is(e, context.Canceled), errors.Is(e, context.DeadlineExceeded):
		return "not_checked", "interrupted"
	case errors.Is(e, comic.ErrArchive), errors.Is(e, comic.ErrImage):
		return "confirmed", "archive_damaged"
	default:
		return "manual", "archive_verification_unavailable"
	}
}

func healthManagedCategory(o model.HealthObject) string {
	switch o.Kind {
	case "asset":
		return "assets"
	case "cover_cache", "tag_cache":
		return "caches"
	default:
		return "indexes"
	}
}
