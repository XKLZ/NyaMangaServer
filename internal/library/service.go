// Package library owns scan lifecycle, source-root boundaries, and derived images.
package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"nyamangaserver.local/server/internal/auth"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/imagecache"
	"nyamangaserver.local/server/internal/model"
)

type Error string

func (e Error) Error() string { return string(e) }

const ErrInput Error = "invalid_library"
const ErrRoot Error = "root_unavailable"
const ErrIdentity Error = "root_changed"
const ErrUnavailable Error = "file_unavailable"

type activeScan struct {
	id     string
	cancel context.CancelFunc
}
type Service struct {
	downloadWake   chan struct{}
	downloadMu     sync.Mutex
	downloadID     string
	downloadCancel context.CancelFunc
	closed         bool
	store          Repository
	Config         config.Config
	Pool           *comic.Pool
	Logger         *slog.Logger
	mu             sync.Mutex
	active         map[string]activeScan
	wake           chan struct{}
	slots          chan struct{}
	cache          *imagecache.Cache
	cacheDir       string
	cancel         context.CancelFunc
	done           chan struct{}
	// BeforePublish is only a deterministic test synchronization hook.
	BeforePublish       func(context.Context, model.Job, model.Item)
	beforeDownloadBlock func(context.Context) // deterministic tests
}

func New(s Repository, c config.Config, logger *slog.Logger) *Service {
	maxBytes, maxFiles := c.CoverCache.Limits()
	return &Service{downloadWake: make(chan struct{}, 1), store: s, Config: c, Pool: comic.NewPoolWithBudget(comic.NewBudget(c.ImageMemoryMiB << 20)), Logger: logger, active: map[string]activeScan{}, wake: make(chan struct{}, 1), slots: make(chan struct{}, 2), cacheDir: filepath.Join(c.DataDir, "cache", "covers", "v1"), cache: imagecache.New(filepath.Join(c.DataDir, "cache", "covers", "v1"), maxBytes, maxFiles)}
}
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.run(ctx)
}
func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	already := s.closed
	s.closed = true
	s.mu.Unlock()
	if already {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
		select {
		case <-s.done:
		case <-ctx.Done():
			s.Pool.Close()
			return ctx.Err()
		}
	}
	s.Pool.Close()
	return nil
}
func (s *Service) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = s.Shutdown(ctx)
}
func (s *Service) run(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var workers sync.WaitGroup
	defer workers.Wait()
	workers.Go(func() { s.runDownloads(ctx) })
	for {
		if ctx.Err() != nil {
			return
		}
		if !model.AutomaticWorkAllowed(ctx, s.store) {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.wake:
			}
			continue
		}
		jobs, e := s.store.ActiveJobs(ctx)
		if e == nil {
			for _, j := range jobs {
				s.mu.Lock()
				_, busy := s.active[j.LibraryID]
				if !busy {
					child, cancel := context.WithCancel(ctx)
					s.active[j.LibraryID] = activeScan{j.ID, cancel}
					workers.Add(1)
					go func(j model.Job) {
						defer workers.Done()
						defer func() { s.mu.Lock(); delete(s.active, j.LibraryID); s.mu.Unlock(); cancel(); s.Wake() }()
						s.scan(child, j.ID)
					}(j)
				}
				s.mu.Unlock()
			}
		}
		due, e := s.store.DueLibraries(ctx, time.Now().Unix())
		if e == nil {
			for _, id := range due {
				if _, err := s.StartScan(ctx, id); err != nil && !errors.Is(err, model.ErrScanActive) {
					_ = s.store.DeferPeriodic(ctx, id, code(err))
				}
			}
		}
		s.Pool.Sweep(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
	}
}
func (s *Service) Root(key string) (config.Root, error) {
	for _, r := range s.Config.Roots {
		if r.Key == key {
			return r, nil
		}
	}
	return config.Root{}, ErrRoot
}
func rootInfo(path string) (fs.FileInfo, string, error) {
	info, e := os.Lstat(path)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, "", ErrRoot
	}
	v, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, "", ErrRoot
	}
	return info, fmt.Sprintf("%v:%v", v.Dev, v.Ino), nil
}
func (s *Service) checkedRoot(l model.Library) (config.Root, error) {
	if l.TrashedAt != nil {
		return config.Root{}, model.ErrTrashed
	}
	if l.Disabled {
		return config.Root{}, model.ErrDisabled
	}
	r, e := s.Root(l.RootKey)
	if e != nil {
		return r, e
	}
	_, identity, e := rootInfo(r.Path)
	if e != nil {
		return r, e
	}
	if identity != l.Identity {
		return r, ErrIdentity
	}
	return r, nil
}
func (s *Service) SaveLibrary(ctx context.Context, v model.Library, expected int64) (*model.Library, error) {
	if strings.TrimSpace(v.Name) == "" || !utf8.ValidString(v.Name) || utf8.RuneCountInString(v.Name) > 128 || len(v.Name) > 512 || (v.Encoding != "utf8" && v.Encoding != "gb18030" && v.Encoding != "shift_jis") || (v.Interval != nil && (*v.Interval < 15 || *v.Interval > 525600)) {
		return nil, ErrInput
	}
	r, e := s.Root(v.RootKey)
	if e != nil {
		return nil, e
	}
	_, v.Identity, e = rootInfo(r.Path)
	if e != nil {
		return nil, e
	}
	if expected == 0 {
		v.ID, e = auth.RandomToken(16)
		if e != nil {
			return nil, e
		}
	} else {
		old, e := s.store.Library(ctx, v.ID)
		if e != nil {
			return nil, e
		}
		if old.RootKey != v.RootKey || old.Identity != v.Identity {
			return nil, ErrIdentity
		}
	}
	if e = s.store.SaveLibrary(ctx, v, expected); e != nil {
		return nil, e
	}
	saved, e := s.store.Library(ctx, v.ID)
	if e == nil {
		saved.Availability = s.Availability(*saved)
	}
	return saved, e
}
func (s *Service) StartScan(ctx context.Context, library string) (*model.Job, error) {
	l, e := s.store.Library(ctx, library)
	if e != nil {
		return nil, e
	}
	if _, e = s.checkedRoot(*l); e != nil {
		return nil, e
	}
	id, e := auth.RandomToken(16)
	if e != nil {
		return nil, e
	}
	j, e := s.store.StartScan(ctx, id, library)
	if e == nil {
		s.Wake()
	}
	return j, e
}
func (s *Service) Cancel(ctx context.Context, id string) error {
	if e := s.store.Cancel(ctx, id); e != nil {
		return e
	}
	s.mu.Lock()
	for _, v := range s.active {
		if v.id == id {
			v.cancel()
		}
	}
	s.mu.Unlock()
	s.Wake()
	return nil
}
func code(e error) string {
	var c comic.Error
	if errors.As(e, &c) {
		return string(c)
	}
	var l Error
	if errors.As(e, &l) {
		return string(l)
	}
	if errors.Is(e, context.Canceled) {
		return "cancelled"
	}
	return "file_unavailable"
}
func (s *Service) Availability(l model.Library) string {
	_, e := s.checkedRoot(l)
	if e == nil {
		return "available"
	}
	if errors.Is(e, ErrIdentity) {
		return "changed"
	}
	return "unavailable"
}

// ValidateScan performs the same root identity check for import-owned scans.
func (s *Service) ValidateScan(ctx context.Context, id string) error {
	l, e := s.store.Library(ctx, id)
	if e != nil {
		return e
	}
	_, e = s.checkedRoot(*l)
	return e
}
