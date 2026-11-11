// Package galleryhighlight owns bounded read-only gallery matching and scoped connections.
package galleryhighlight

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"nyamangaserver.local/server/internal/model"
	"strconv"
	"strings"
	"sync"
	"time"
)

const ProtocolVersion = 2
const MaxBatchSize = 200
const tokenPrefix = "ngh1_"

type Repository interface {
	RequireAdministrator(context.Context) error
	CreateGalleryConnection(context.Context, model.Session, string, string, []byte, int64) (model.GalleryConnection, error)
	GalleryConnections(context.Context, model.Session, int, int, int64) (model.GalleryConnections, error)
	RevokeGalleryConnection(context.Context, model.Session, string, int64, int64) (model.GalleryConnection, error)
	GalleryGrant(context.Context, []byte, int64) (model.GalleryGrant, error)
	GalleryLookup(context.Context, model.GalleryGrant, []string, int64) ([]model.GalleryMatch, error)
	GalleryPublish(context.Context, model.GalleryGrant, func() int64, func() error) error
}
type RateError struct{ RetryAfter int }

func (e *RateError) Error() string { return "gallery_highlight_rate_limited" }

type window struct {
	start                   time.Time
	requests, items, active int
}
type Service struct {
	repo    Repository
	Now     func() time.Time
	slots   chan struct{}
	mu      sync.Mutex
	windows map[string]*window
}

func New(r Repository) *Service {
	return &Service{repo: r, Now: time.Now, slots: make(chan struct{}, 4), windows: map[string]*window{}}
}
func TokenHash(raw string) ([]byte, error) {
	if !strings.HasPrefix(raw, tokenPrefix) || len(raw) != len(tokenPrefix)+43 {
		return nil, model.ErrGalleryAuth
	}
	encoded := strings.TrimPrefix(raw, tokenPrefix)
	b, e := base64.RawURLEncoding.DecodeString(encoded)
	if e != nil || len(b) != 32 || base64.RawURLEncoding.EncodeToString(b) != encoded {
		return nil, model.ErrGalleryAuth
	}
	h := sha256.Sum256([]byte("nyamangaserver-gallery-highlight-v1:" + raw))
	return h[:], nil
}
func (s *Service) Create(ctx context.Context, actor model.Session, in model.GalleryConnectionCreate) (model.GalleryConnectionSecret, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.GalleryConnectionSecret{}, e
	}
	out := model.GalleryConnectionSecret{}
	raw := make([]byte, 32)
	if _, e := rand.Read(raw); e != nil {
		return out, e
	}
	token := tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	hash, _ := TokenHash(token)
	id, e := model.NewID()
	if e != nil {
		return out, e
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	v, e := s.repo.CreateGalleryConnection(ctx, actor, id, "授权-"+id[:8], hash, s.Now().Unix())
	if e != nil {
		return out, e
	}
	return model.GalleryConnectionSecret{ProtocolVersion: ProtocolVersion, Connection: v, Token: token}, nil
}
func (s *Service) List(ctx context.Context, actor model.Session, page, size int) (model.GalleryConnections, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.GalleryConnections{}, e
	}
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 50
	}
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		return model.GalleryConnections{}, model.ErrInput
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return s.repo.GalleryConnections(ctx, actor, page, size, s.Now().Unix())
}
func (s *Service) Revoke(ctx context.Context, actor model.Session, id string, rev int64) (model.GalleryConnection, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return model.GalleryConnection{}, e
	}
	if id == "" || len(id) > 128 || strings.ContainsRune(id, 0) || rev < 1 || rev == math.MaxInt64 {
		return model.GalleryConnection{}, model.ErrInput
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return s.repo.RevokeGalleryConnection(ctx, actor, id, rev, s.Now().Unix())
}
func Normalize(in model.GalleryLookupInput) ([]string, error) {
	if in.Provider != "ehentai" || len(in.GalleryIDs) < 1 || len(in.GalleryIDs) > MaxBatchSize {
		return nil, model.ErrInput
	}
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range in.GalleryIDs {
		if len(raw) < 1 || len(raw) > 19 {
			return nil, model.ErrInput
		}
		for _, r := range raw {
			if r < '0' || r > '9' {
				return nil, model.ErrInput
			}
		}
		n, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || n < 1 {
			return nil, model.ErrInput
		}
		id := strconv.FormatInt(n, 10)
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}
func (s *Service) admit(id string, cost int) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	for key, w := range s.windows {
		if w.active == 0 && now.Sub(w.start) > 2*time.Minute {
			delete(s.windows, key)
		}
	}
	w := s.windows[id]
	if w == nil {
		if len(s.windows) >= 1024 {
			return nil, &RateError{1}
		}
		w = &window{start: now}
		s.windows[id] = w
	}
	if now.Sub(w.start) >= time.Minute {
		w.start = now
		w.requests = 0
		w.items = 0
	}
	if w.active >= 2 {
		return nil, &RateError{1}
	}
	if w.requests >= 60 || w.items+cost > 1000 {
		return nil, &RateError{max(1, int(w.start.Add(time.Minute).Sub(now).Seconds())+1)}
	}
	w.requests++
	w.items += cost
	w.active++
	return func() { s.mu.Lock(); w.active--; s.mu.Unlock() }, nil
}
func (s *Service) use(ctx context.Context, raw string, cost int, fn func(context.Context, model.GalleryGrant) error) error {
	hash, e := TokenHash(raw)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return &RateError{1}
	}
	grant, e := s.repo.GalleryGrant(ctx, hash, s.Now().Unix())
	if e != nil {
		return e
	}
	release, e := s.admit(grant.ID, cost)
	if e != nil {
		return e
	}
	defer release()
	return fn(ctx, grant)
}

// freeze must only prepare the bounded response in memory, never write HTTP or
// perform IO. Final auth + freeze linearizes publication with revoke/disable.
func (s *Service) Lookup(ctx context.Context, raw string, in model.GalleryLookupInput, freeze func(model.GalleryLookup) error) error {
	ids, e := Normalize(in)
	if e != nil {
		return e
	}
	return s.use(ctx, raw, len(ids), func(ctx context.Context, grant model.GalleryGrant) error {
		items, e := s.repo.GalleryLookup(ctx, grant, ids, s.Now().Unix())
		if e != nil {
			return e
		}
		out := model.GalleryLookup{ProtocolVersion: ProtocolVersion, Provider: "ehentai", Items: items}
		// Bind the version to the actual complete response semantics, even when a
		// source changes gid without changing any effective title/tag/catalog epoch.
		raw, _ := json.Marshal(out)
		out.QueryVersion = fmt.Sprintf("%x", sha256.Sum256(raw))
		return s.repo.GalleryPublish(ctx, grant, func() int64 { return s.Now().Unix() }, func() error { return freeze(out) })
	})
}
func (s *Service) Status(ctx context.Context, raw string, freeze func(model.GalleryStatus) error) error {
	return s.use(ctx, raw, 0, func(ctx context.Context, grant model.GalleryGrant) error {
		return s.repo.GalleryPublish(ctx, grant, func() int64 { return s.Now().Unix() }, func() error {
			return freeze(model.GalleryStatus{ProtocolVersion: ProtocolVersion, Provider: "ehentai", MaxBatchSize: MaxBatchSize, ConnectionExpiresAt: grant.ExpiresAt})
		})
	})
}
