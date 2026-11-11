package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/library"
	"nyamangaserver.local/server/internal/model"
)

func (h *Handler) catalogRoutes(m *http.ServeMux) {
	m.HandleFunc("/api/v1/libraries", h.libraries)
	m.HandleFunc("/api/v1/libraries/{id}", h.method("PATCH", h.patchLibrary))
	m.HandleFunc("/api/v1/libraries/{id}/scans", h.method("POST", h.startScan))
	m.HandleFunc("/api/v1/jobs", h.method("GET", h.jobs))
	m.HandleFunc("/api/v1/jobs/{id}", h.method("GET", h.job))
	m.HandleFunc("/api/v1/jobs/{id}/items", h.method("GET", h.items))
	m.HandleFunc("/api/v1/jobs/{id}/cancel", h.method("POST", h.cancelJob))
	m.HandleFunc("/api/v1/editions", h.method("GET", h.books))
	m.HandleFunc("/api/v1/editions/{id}", h.method("GET", h.book))
	m.HandleFunc("/api/v1/editions/{id}/pages", h.method("GET", h.pages))
	m.HandleFunc("/api/v1/editions/{id}/cover", h.method("GET", h.image))
	m.HandleFunc("/api/v1/editions/{id}/pages/{index}/image", h.method("GET", h.image))
	m.HandleFunc("/api/v1/books", h.method("GET", h.books))
	m.HandleFunc("/api/v1/books/{id}", h.method("GET", h.book))
	m.HandleFunc("/api/v1/books/{id}/pages", h.method("GET", h.pages))
	m.HandleFunc("/api/v1/books/{id}/cover", h.method("GET", h.image))
	m.HandleFunc("/api/v1/books/{id}/pages/{index}/image", h.method("GET", h.image))
}
func (h *Handler) catalogFailure(w http.ResponseWriter, r *http.Request, e error) {
	if errors.Is(e, model.ErrParsingChanged) {
		h.error(w, r, 409, "parsing_changed", "页目录顺序已变化，请选择编码或重新检查后确认位置")
		return
	}
	switch {
	case errors.Is(e, model.ErrDisabled):
		h.error(w, r, 409, "library_disabled", "书库已停用")
	case errors.Is(e, model.ErrTrashed):
		h.error(w, r, 409, "resource_trashed", "资源已移入回收站")
	case errors.Is(e, model.ErrBusy):
		h.error(w, r, 409, "resource_busy", "资源正被其他任务使用")
	case errors.Is(e, model.ErrPreviewExpired):
		h.error(w, r, 409, "preview_expired", "预览已过期，请重新预览")
	case errors.Is(e, model.ErrInput):
		h.error(w, r, 422, "invalid_request", "输入参数无效")
	case errors.Is(e, model.ErrNotFound):
		h.error(w, r, 404, "not_found", "资源不存在")
	case errors.Is(e, model.ErrConflict):
		h.error(w, r, 409, "revision_conflict", "记录已变化，请刷新后重试")
	case errors.Is(e, model.ErrScanActive):
		h.error(w, r, 409, "scan_in_progress", "该书库已有扫描任务")
	case errors.Is(e, library.ErrInput):
		h.error(w, r, 422, "invalid_library", "书库名称、编码或扫描间隔无效")
	case errors.Is(e, library.ErrRoot), errors.Is(e, library.ErrIdentity):
		h.error(w, r, 409, e.Error(), "书库目录不可用或已变化，请检查配置")
	case errors.Is(e, comic.ErrChanged):
		h.error(w, r, 409, "content_changed", "文件已变化，请重新扫描")
	case errors.Is(e, comic.ErrLimit):
		h.imageLimitFailure(w, r, 422, "resource_limit", e)
	case errors.Is(e, comic.ErrImage):
		h.error(w, r, 422, "page_corrupt", "该页图片损坏或格式不受支持")
	case errors.Is(e, library.ErrUnavailable), errors.Is(e, comic.ErrArchive):
		h.error(w, r, 409, "file_unavailable", "源文件缺失或不可用")
	default:
		h.failure(w, r, e)
	}
}
func (h *Handler) libraries(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		v, e := h.catalog.Libraries(r.Context())
		if e != nil {
			h.catalogFailure(w, r, e)
			return
		}
		for i := range v {
			v[i].Availability = h.catalog.Availability(v[i])
		}
		h.json(w, 200, map[string]any{"items": v})
	case "POST":
		var in struct {
			RootKey  *string `json:"root_key"`
			Name     *string `json:"display_name"`
			Encoding string  `json:"legacy_zip_encoding"`
			Interval *int    `json:"scan_interval_minutes"`
		}
		if !h.decode(w, r, &in) {
			return
		}
		if in.RootKey == nil || in.Name == nil {
			h.error(w, r, 400, "invalid_request", "缺少必填字段")
			return
		}
		if in.Encoding == "" {
			in.Encoding = "utf8"
		}
		v, e := h.catalog.SaveLibrary(r.Context(), model.Library{RootKey: *in.RootKey, Name: *in.Name, Encoding: in.Encoding, Interval: in.Interval}, 0)
		if e != nil {
			h.catalogFailure(w, r, e)
			return
		}
		h.json(w, 201, v)
	default:
		w.Header().Set("Allow", "GET, POST")
		h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
	}
}

type optionalInterval struct {
	Set   bool
	Value *int
}

func (v *optionalInterval) UnmarshalJSON(b []byte) error {
	v.Set = true
	return json.Unmarshal(b, &v.Value)
}
func (h *Handler) patchLibrary(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Expected *int64           `json:"expected_revision"`
		RootKey  *string          `json:"root_key"`
		Name     *string          `json:"display_name"`
		Encoding *string          `json:"legacy_zip_encoding"`
		Interval optionalInterval `json:"scan_interval_minutes"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	if in.Expected == nil || *in.Expected < 1 {
		h.error(w, r, 400, "invalid_request", "需要有效记录版本")
		return
	}
	v, e := h.catalog.Library(r.Context(), r.PathValue("id"))
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	if in.RootKey != nil {
		v.RootKey = *in.RootKey
	}
	if in.Name != nil {
		v.Name = *in.Name
	}
	if in.Encoding != nil {
		v.Encoding = *in.Encoding
	}
	if in.Interval.Set {
		v.Interval = in.Interval.Value
	}
	out, e := h.catalog.SaveLibrary(r.Context(), *v, *in.Expected)
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
func (h *Handler) startScan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Expected *int64 `json:"expected_revision"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if in.Expected != nil {
		if *in.Expected < 1 {
			h.error(w, r, 400, "invalid_request", "记录版本无效")
			return
		}
		l, e := h.catalog.Library(r.Context(), id)
		if e != nil {
			h.catalogFailure(w, r, e)
			return
		}
		if l.Revision != *in.Expected {
			h.catalogFailure(w, r, model.ErrConflict)
			return
		}
	}
	j, e := h.catalog.StartScan(r.Context(), id)
	if errors.Is(e, model.ErrScanActive) {
		h.json(w, 409, map[string]any{"error": map[string]any{"code": "scan_in_progress", "message": "该书库已有扫描任务", "request_id": w.Header().Get("X-Request-ID"), "details": map[string]string{"job_id": j.ID}}})
		return
	}
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	w.Header().Set("Location", "/api/v1/jobs/"+j.ID)
	h.json(w, 202, map[string]string{"job_id": j.ID})
}
func pagination(r *http.Request) (int, int, error) {
	page, size := 1, 50
	var e error
	if v := r.URL.Query().Get("page"); v != "" {
		page, e = strconv.Atoi(v)
		if e != nil {
			return 0, 0, library.ErrInput
		}
	}
	if v := r.URL.Query().Get("page_size"); v != "" {
		size, e = strconv.Atoi(v)
		if e != nil {
			return 0, 0, library.ErrInput
		}
	}
	if page < 1 || page > 1_000_000 || size < 1 || size > 100 {
		return 0, 0, library.ErrInput
	}
	return page, size, nil
}
func (h *Handler) jobs(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.error(w, r, 400, "invalid_request", "分页无效")
		return
	}
	q := r.URL.Query()
	v, total, e := h.catalog.JobPage(r.Context(), q.Get("type"), q.Get("state"), q.Get("library_id"), page, size)
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 200, map[string]any{"items": response(v), "total": total, "page": page, "page_size": size})
}
func (h *Handler) job(w http.ResponseWriter, r *http.Request) {
	v, e := h.catalog.Job(r.Context(), r.PathValue("id"))
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) items(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.error(w, r, 400, "invalid_request", "分页参数无效")
		return
	}
	j, e := h.catalog.Job(r.Context(), r.PathValue("id"))
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	v, e := h.catalog.Items(r.Context(), j.ID, page, size, false)
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	total := 0
	if j.Total != nil {
		total = *j.Total
	}
	h.json(w, 200, map[string]any{"items": v, "total": total, "page": page, "page_size": size})
}
func (h *Handler) cancelJob(w http.ResponseWriter, r *http.Request) {
	if h.health != nil && h.health.service.Owns(r.Context(), r.PathValue("id")) {
		e := h.health.service.Control(r.Context(), r.PathValue("id"), "cancel")
		h.healthResponse(w, r, 202, map[string]bool{"cancelled": true}, e)
		return
	}

	if h.recommendations != nil {
		j, e := h.catalog.Job(r.Context(), r.PathValue("id"))
		if e != nil {
			h.catalogFailure(w, r, e)
			return
		}
		if j.Type == "work_recommendation" || j.Type == "series_recommendation" {
			if e = h.recommendations.Control(r.Context(), j.ID, "cancel"); e != nil {
				h.recommendationFailure(w, r, e)
				return
			}
			h.json(w, 202, map[string]bool{"cancelled": true})
			return
		}
	}

	if h.maintenance != nil {
		j, e := h.catalog.Job(r.Context(), r.PathValue("id"))
		if e != nil {
			h.catalogFailure(w, r, e)
			return
		}
		if j.Type == "maintenance" || j.Type == "cover_rebuild" {
			if e = h.maintenance.Control(r.Context(), j.ID, "cancel"); e != nil {
				h.catalogFailure(w, r, e)
				return
			}
			h.json(w, 202, map[string]bool{"cancelled": true})
			return
		}
	}

	if h.metadata != nil {
		if e := h.metadata.Control(r.Context(), r.PathValue("id"), "cancel"); e != nil {
			h.metadataFailure(w, r, e)
			return
		}
		h.json(w, 202, map[string]bool{"cancelled": true})
		return
	}
	if e := h.catalog.Cancel(r.Context(), r.PathValue("id")); e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 202, map[string]bool{"cancelled": true})
}
func (h *Handler) books(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.error(w, r, 400, "invalid_request", "分页参数无效")
		return
	}
	state := r.URL.Query().Get("state")
	switch state {
	case "", "available", "missing", "corrupt", "needs_review":
	default:
		h.error(w, r, 400, "invalid_request", "文件状态无效")
		return
	}
	v, total, e := h.catalog.Editions(r.Context(), r.URL.Query().Get("library_id"), state, page, size)
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 200, map[string]any{"items": v, "total": total, "page": page, "page_size": size})
}
func (h *Handler) book(w http.ResponseWriter, r *http.Request) {
	if h.metadata != nil {
		h.metadataBook(w, r)
		return
	}
	v, e := h.catalog.Edition(r.Context(), r.PathValue("id"))
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) pages(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.error(w, r, 400, "invalid_request", "分页参数无效")
		return
	}
	b, e := h.catalog.Edition(r.Context(), r.PathValue("id"))
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	if v := r.URL.Query().Get("content_revision"); v != "" && v != strconv.FormatInt(b.ContentRevision, 10) {
		h.catalogFailure(w, r, comic.ErrChanged)
		return
	}
	if e = h.catalog.SourceGuard(r.Context(), b.ID); e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	if b.TrashedAt != nil {
		h.catalogFailure(w, r, model.ErrTrashed)
		return
	}
	if b.LibraryDisabled {
		h.catalogFailure(w, r, model.ErrDisabled)
		return
	}
	if !h.parsingRequest(w, r, b) {
		return
	}

	v, e := h.catalog.Pages(r.Context(), b.ID, b.ContentRevision, page, size)
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	current, err := h.catalog.Edition(r.Context(), b.ID)
	if err != nil {
		h.catalogFailure(w, r, err)
		return
	}
	if current.FileParsing.Fingerprint() != b.FileParsing.Fingerprint() {
		h.error(w, r, 409, "parsing_changed", "页目录已变化，请刷新后重新确认位置")
		return
	}
	h.json(w, 200, map[string]any{"items": v, "total": b.PageCount, "parsing_revision": b.ParsingRevision, "directory_version": comic.DirectoryVersion, "content_revision": b.ContentRevision, "page": page, "page_size": size})
}
func (h *Handler) image(w http.ResponseWriter, r *http.Request) {
	revision, e := strconv.ParseInt(r.URL.Query().Get("content_revision"), 10, 64)
	if e != nil || revision < 1 {
		h.error(w, r, 400, "invalid_request", "需要内容版本")
		return
	}
	cover := r.PathValue("index") == ""
	index := 1
	if !cover {
		index, e = strconv.Atoi(r.PathValue("index"))
		if e != nil || index < 1 {
			h.error(w, r, 400, "invalid_request", "页码无效")
			return
		}
	}
	var before *model.Edition
	if !cover {
		before, e = h.catalog.Edition(r.Context(), r.PathValue("id"))
		if e != nil {
			h.catalogFailure(w, r, e)
			return
		}
		if !h.parsingRequest(w, r, before) {
			return
		}
	}

	data, kind, e := h.catalog.Image(r.Context(), r.PathValue("id"), revision, index, cover)
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	if !cover {
		current, err := h.catalog.Edition(r.Context(), before.ID)
		if err != nil {
			h.catalogFailure(w, r, err)
			return
		}
		if current.FileParsing.Fingerprint() != before.FileParsing.Fingerprint() {
			h.error(w, r, 409, "parsing_changed", "页目录已变化，请刷新后重新确认位置")
			return
		}
	}

	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", fmt.Sprintf("\"%x\"", sha256.Sum256(data)))
	http.ServeContent(w, r, "image", time.Time{}, bytes.NewReader(data))
}

func (h *Handler) imageLimitFailure(w http.ResponseWriter, r *http.Request, status int, code string, e error) {
	var detail *comic.LimitError
	if errors.As(e, &detail) {
		h.json(w, status, map[string]any{"error": map[string]any{"code": code, "message": "图片或归档超过资源限制", "request_id": w.Header().Get("X-Request-ID"), "details": detail}})
		return
	}
	h.error(w, r, status, code, "图片或归档超过资源限制")
}

func (h *Handler) parsingRequest(w http.ResponseWriter, r *http.Request, b *model.Edition) bool {
	raw := r.URL.Query().Get("parsing_revision")
	if raw == "" && b.ParsingRevision <= 1 {
		return true
	}
	if raw != strconv.FormatInt(b.ParsingRevision, 10) {
		h.error(w, r, 409, "parsing_changed", "页目录已变化，请刷新后重新确认位置")
		return false
	}
	return true
}
