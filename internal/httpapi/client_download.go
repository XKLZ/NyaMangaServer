package httpapi

import (
	"errors"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"nyamangaserver.local/server/internal/model"
)

const downloadPreparePath = "/api/v1/client/editions/{id}/downloads"
const downloadStatusPath = "/api/v1/client/downloads/{id}"
const downloadContentPath = downloadStatusPath + "/content"

const downloadGlobal = 2

type downloadRequests struct {
	mu     sync.Mutex
	active map[string]bool
}

func (d *downloadRequests) acquire(key string) (func(), int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.active[key] {
		return nil, 429
	}
	if len(d.active) >= downloadGlobal {
		return nil, 503
	}
	if d.active == nil {
		d.active = make(map[string]bool)
	}
	d.active[key] = true
	return func() { d.mu.Lock(); defer d.mu.Unlock(); delete(d.active, key) }, 0
}

func (h *Handler) downloadRoutes(m *http.ServeMux) {
	m.HandleFunc(downloadPreparePath, h.method("POST", h.prepareDownload))
	m.HandleFunc(downloadStatusPath, h.downloadStatus)
	m.HandleFunc(downloadContentPath, h.downloadContent)
}

func (h *Handler) downloadFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, model.ErrDownloadBusy):
		w.Header().Set("Retry-After", "1")
		h.error(w, r, 503, "download_busy", "下载准备或传输暂忙")
	case errors.Is(err, model.ErrDownloadPending):
		h.error(w, r, 409, "download_not_ready", "下载记录尚未就绪")
	case errors.Is(err, model.ErrClientKeyConflict):
		h.error(w, r, 409, "client_idempotency_conflict", "同一幂等键已绑定另一请求")
	case errors.Is(err, model.ErrClientEpoch):
		h.error(w, r, 409, "cache_epoch_changed", "实例缓存世代已变化")
	default:
		h.catalogFailure(w, r, err)
	}
}

func (h *Handler) prepareDownload(w http.ResponseWriter, r *http.Request) {
	var in model.DownloadRequest
	if !h.decode(w, r, &in) {
		return
	}
	d, err := h.catalog.PrepareDownload(r.Context(), r.PathValue("id"), in)
	if err != nil {
		h.downloadFailure(w, r, err)
		return
	}
	status := 200
	if d.State == "preparing" {
		status = 202
	}
	w.Header().Set("Location", "/api/v1/client/downloads/"+d.ID)
	h.json(w, status, d)
}

func (h *Handler) downloadStatus(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		d, err := h.catalog.Download(r.Context(), r.PathValue("id"))
		if err != nil {
			h.downloadFailure(w, r, err)
			return
		}
		h.json(w, 200, d)
	case http.MethodDelete:
		if err := h.catalog.AbandonDownload(r.Context(), r.PathValue("id")); err != nil {
			h.downloadFailure(w, r, err)
			return
		}
		w.WriteHeader(204)
	default:
		w.Header().Set("Allow", "GET, DELETE")
		h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
	}
}

func (h *Handler) downloadContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
		return
	}
	release, status := h.downloads.acquire(imageSessionKey(r))
	if status != 0 {
		w.Header().Set("Retry-After", "1")
		code := "download_busy"
		if status == 429 {
			code = "download_session_limit"
		}
		h.error(w, r, status, code, "当前下载并发已满")
		return
	}
	defer release()
	f, err := h.catalog.OpenDownload(r.Context(), r.PathValue("id"), func() error { return h.recheckImageSession(r) })
	if err != nil {
		h.downloadFailure(w, r, err)
		return
	}
	defer f.Close()
	r = imageConditions(r)
	tag := "\"" + f.Download.SHA256 + "\""
	ranges := r.Header.Values("Range")
	if cond := r.Header.Get("If-Range"); cond != "" && cond != tag {
		r = r.Clone(r.Context())
		r.Header.Del("Range")
		ranges = nil
	}
	end, valid := downloadRangeEnd(ranges, f.Download.Length)
	if !valid {
		h.error(w, r, 400, "invalid_download_range", "只支持单个不超过 16 MiB 的字节范围")
		return
	}
	f.SetEnd(end)
	kind := "application/zip"
	if strings.EqualFold(path.Ext(f.Filename), ".cbz") {
		kind = "application/vnd.comicbook+zip"
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Filename}))
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", tag)
	w.Header().Set("Accept-Ranges", "bytes")
	http.ServeContent(w, r, f.Filename, time.Time{}, f)
	if f.ReadError != nil {
		panic(http.ErrAbortHandler)
	}
}

// ServeContent handles the actual preconditions and range response. Reject
// malformed/multiple/oversized ranges before it can select multipart output.
func downloadRangeEnd(values []string, size int64) (int64, bool) {
	if len(values) == 0 {
		return size, true
	}
	if len(values) != 1 {
		return 0, false
	}
	raw, ok := strings.CutPrefix(values[0], "bytes=")
	if !ok || strings.Contains(raw, ",") {
		return 0, false
	}
	first, last, ok := strings.Cut(strings.TrimSpace(raw), "-")
	if !ok {
		return 0, false
	}
	if first == "" {
		n, err := rangeNumber(last)
		if err != nil || n == 0 {
			return 0, false
		}
		return size, min(n, size) <= model.DownloadMaxRange
	}
	begin, err := rangeNumber(first)
	if err != nil {
		return 0, false
	}
	end := size
	if last != "" {
		n, err := rangeNumber(last)
		if err != nil || n < begin {
			return 0, false
		}
		if n < size {
			end = n + 1
		}
	}
	return end, begin >= size || end-begin <= model.DownloadMaxRange
}

func rangeNumber(s string) (int64, error) {
	if s == "" || strings.ContainsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		return 0, model.ErrInput
	}
	n, err := strconv.ParseUint(s, 10, 63)
	return int64(n), err
}
