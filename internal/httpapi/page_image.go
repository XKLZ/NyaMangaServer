package httpapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"nyamangaserver.local/server/internal/auth"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/model"
)

// Let ServeContent evaluate the exact same preconditions for a verified
// representation before asking for bytes. The probe never writes to the client.
type imageCondition struct {
	header http.Header
	status int
}

func (w *imageCondition) Header() http.Header            { return w.header }
func (w *imageCondition) WriteHeader(code int)           { w.status = code }
func (w *imageCondition) Write(data []byte) (int, error) { return len(data), nil }

func imageNotModified(r *http.Request, etag string) bool {
	if r.Header.Get("If-None-Match") == "" {
		return false
	}
	probe := imageCondition{header: http.Header{"Etag": {etag}, "Content-Type": {"application/octet-stream"}}}
	http.ServeContent(&probe, r, "image", time.Time{}, bytes.NewReader(nil))
	return probe.status == http.StatusNotModified
}

func imageConditions(r *http.Request) *http.Request {
	if len(r.Header.Values("If-None-Match")) <= 1 && len(r.Header.Values("If-Match")) <= 1 {
		return r
	}
	r = r.Clone(r.Context())
	for _, name := range []string{"If-None-Match", "If-Match"} {
		if values := r.Header.Values(name); len(values) > 1 {
			r.Header.Set(name, strings.Join(values, ","))
		}
	}
	return r
}

func serveImageBytes(w http.ResponseWriter, r *http.Request, data []byte, kind, tag string) {
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", tag)
	http.ServeContent(w, r, "image", time.Time{}, bytes.NewReader(data))
}

func (h *Handler) servePageImage(w http.ResponseWriter, r *http.Request, edition model.Edition, index int, display bool) {
	deadline, _ := r.Context().Value(httpImageDeadlineKey{}).(time.Time)
	wait, cancel := context.WithDeadline(r.Context(), deadline)
	key, err := h.catalog.PreparePage(wait, edition, index)
	if errors.Is(wait.Err(), context.DeadlineExceeded) && r.Context().Err() == nil {
		err = comic.ErrImageBusy
	}
	cancel()
	if err != nil {
		h.catalogFailure(w, r, err)
		return
	}
	r = imageConditions(r)
	out, err := h.catalog.Pool.Page(r.Context(), key, display, func(tag string) bool { return imageNotModified(r, tag) })
	if err != nil {
		h.catalogFailure(w, r, err)
		return
	}
	if out.Release != nil {
		defer out.Release()
	}
	if h.beforeImageResponse != nil {
		h.beforeImageResponse(r.Context())
	}
	if err = h.recheckImageSession(r); err == nil {
		err = h.catalog.CheckPage(r.Context(), key)
	}
	if err != nil {
		h.catalogFailure(w, r, err)
		return
	}
	serveImageBytes(w, r, out.Bytes, out.Kind, out.ETag)
}

func (h *Handler) recheckImageSession(r *http.Request) error {
	principal := r.Context().Value(sessionKey{}).(identity)
	if principal.device != nil {
		_, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		v, err := h.svc.DeviceSession(r.Context(), token)
		if err != nil {
			return err
		}
		if v.ID != principal.device.ID {
			return model.ErrDeviceSession
		}
		if v.Account.MustChangePassword {
			return model.ErrPasswordChangeRequired
		}
		return nil
	}
	v, err := h.svc.Session(r.Context(), principal.raw)
	if err != nil {
		return err
	}
	if v.Account == nil || v.AccountID != principal.session.AccountID {
		return auth.ErrSession
	}
	if v.Account.MustChangePassword {
		return model.ErrPasswordChangeRequired
	}
	return nil
}

const imagePurposeHeader = "X-Nya-Image-Purpose"
const imagesPerSession = 4
const imagesGlobal = 16

type httpImageDeadlineKey struct{}

type imageRequests struct {
	mu        sync.Mutex
	total     int
	bySession map[string]int
}

func (a *imageRequests) acquire(key string) (func(), int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.bySession[key] >= imagesPerSession {
		return nil, http.StatusTooManyRequests
	}
	if a.total >= imagesGlobal {
		return nil, http.StatusServiceUnavailable
	}
	if a.bySession == nil {
		a.bySession = make(map[string]int)
	}
	a.bySession[key]++
	a.total++
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.total--
		a.bySession[key]--
		if a.bySession[key] == 0 {
			delete(a.bySession, key)
		}
	}, 0
}

func (h *Handler) admitImage(w http.ResponseWriter, r *http.Request) (*http.Request, func(), bool) {
	identity := r.Context().Value(sessionKey{}).(identity)
	var key string
	if identity.device != nil {
		key = "device:" + identity.device.ID
	} else {
		key = "web:" + string(identity.session.Hash)
	}
	release, status := h.pageRequests.acquire(key)
	if status != 0 {
		h.imageBusy(w, r, status)
		return nil, nil, false
	}
	wait := h.imageWait
	if wait <= 0 {
		wait = comic.ImageWait
	}
	deadline := time.Now().Add(wait)
	ctx := context.WithValue(comic.WithImageDeadline(r.Context(), deadline), httpImageDeadlineKey{}, deadline)
	return r.WithContext(ctx), release, true
}

func (h *Handler) imageBusy(w http.ResponseWriter, r *http.Request, status int) {
	w.Header().Set("Retry-After", "1")
	if status == http.StatusTooManyRequests {
		h.error(w, r, status, "image_session_limit", "当前会话的并行图片请求已满")
		return
	}
	h.error(w, r, status, string(comic.ErrImageBusy), "图片处理繁忙，请稍后重试")
}
