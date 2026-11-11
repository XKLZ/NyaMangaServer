package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"nyamangaserver.local/server/internal/galleryhighlight"
	"nyamangaserver.local/server/internal/model"
	"strconv"
	"strings"
	"time"
)

func (h *Handler) WithGalleryHighlight(s *galleryhighlight.Service) *Handler {
	h.protected.HandleFunc("/api/v1/settings/gallery-highlight/connections", func(w http.ResponseWriter, r *http.Request) {
		actor := *r.Context().Value(sessionKey{}).(identity).session
		switch r.Method {
		case "GET":
			q, e := url.ParseQuery(r.URL.RawQuery)
			if e != nil {
				h.galleryFailure(w, r, model.ErrInput)
				return
			}
			page, size := 1, 50
			for k, values := range q {
				if (k != "page" && k != "page_size") || len(values) != 1 {
					h.galleryFailure(w, r, model.ErrInput)
					return
				}
				v, e := strconv.Atoi(values[0])
				if e != nil || v < 1 {
					h.galleryFailure(w, r, model.ErrInput)
					return
				}
				if k == "page" {
					page = v
				} else {
					size = v
				}
			}
			out, e := s.List(r.Context(), actor, page, size)
			if e != nil {
				h.galleryFailure(w, r, e)
				return
			}
			h.json(w, 200, out)
		case "POST":
			if r.URL.RawQuery != "" || r.URL.ForceQuery {
				h.galleryFailure(w, r, model.ErrInput)
				return
			}
			var in *model.GalleryConnectionCreate
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			if !h.decode(w, r, &in) {
				return
			}
			if in == nil {
				h.galleryFailure(w, r, model.ErrInput)
				return
			}
			out, e := s.Create(r.Context(), actor, *in)
			if e != nil {
				h.galleryFailure(w, r, e)
				return
			}
			h.json(w, 201, out)
		default:
			w.Header().Set("Allow", "GET, POST")
			h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
		}
	})
	h.protected.HandleFunc("/api/v1/settings/gallery-highlight/connections/{id}", h.method("DELETE", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" || r.URL.ForceQuery {
			h.galleryFailure(w, r, model.ErrInput)
			return
		}
		var in struct {
			Expected *int64 `json:"expected_revision"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if !h.decode(w, r, &in) {
			return
		}
		if in.Expected == nil {
			h.galleryFailure(w, r, model.ErrInput)
			return
		}
		actor := *r.Context().Value(sessionKey{}).(identity).session
		out, e := s.Revoke(r.Context(), actor, r.PathValue("id"), *in.Expected)
		if e != nil {
			h.galleryFailure(w, r, e)
			return
		}
		h.json(w, 200, out)
	}))
	// Separate mux entries bypass session/CSRF auth only for these exact,
	// Bearer-only read endpoints. No CORS headers or management authorization.
	h.mux.HandleFunc("/api/v1/gallery-highlight/status", h.method("GET", func(w http.ResponseWriter, r *http.Request) {
		raw, ok := h.galleryBearer(w, r)
		if !ok {
			return
		}
		if r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 {
			h.galleryFailure(w, r, model.ErrInput)
			return
		}
		var body []byte
		e := s.Status(r.Context(), raw, func(out model.GalleryStatus) error { var e error; body, e = json.Marshal(out); return e })
		h.galleryResponse(w, r, body, e)
	}))
	h.mux.HandleFunc("/api/v1/gallery-highlight/lookup", h.method("POST", func(w http.ResponseWriter, r *http.Request) {
		raw, ok := h.galleryBearer(w, r)
		if !ok {
			return
		}
		if r.URL.RawQuery != "" || r.URL.ForceQuery {
			h.galleryFailure(w, r, model.ErrInput)
			return
		}
		var in model.GalleryLookupInput
		r.Body = http.MaxBytesReader(w, r.Body, 16384)
		if !h.decode(w, r, &in) {
			return
		}
		var body []byte
		e := s.Lookup(r.Context(), raw, in, func(out model.GalleryLookup) error { var e error; body, e = json.Marshal(out); return e })
		h.galleryResponse(w, r, body, e)
	}))
	return h
}
func (h *Handler) galleryBearer(w http.ResponseWriter, r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || len(r.Header.Values("Cookie")) > 0 {
		h.galleryFailure(w, r, model.ErrGalleryAuth)
		return "", false
	}
	scheme, raw, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		h.galleryFailure(w, r, model.ErrGalleryAuth)
		return "", false
	}
	if _, e := galleryhighlight.TokenHash(raw); e != nil {
		h.galleryFailure(w, r, e)
		return "", false
	}
	return raw, true
}
func (h *Handler) galleryResponse(w http.ResponseWriter, r *http.Request, body []byte, e error) {
	if e != nil {
		h.galleryFailure(w, r, e)
		return
	}
	// Publication already froze the authorized response. Slow transport does not
	// hold a database transaction or authorization lock, and is deadline-bounded.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(3 * time.Second))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}
func (h *Handler) galleryFailure(w http.ResponseWriter, r *http.Request, e error) {
	var rate *galleryhighlight.RateError
	switch {
	case errors.Is(e, model.ErrGalleryAuth):
		w.Header().Set("WWW-Authenticate", `Bearer realm="gallery-highlight"`)
		h.error(w, r, 401, "gallery_highlight_unauthorized", "连接授权无效、已撤销或已过期")
	case errors.Is(e, model.ErrGalleryDisabled):
		h.error(w, r, 403, "gallery_highlight_disabled", "本地画廊高亮未开启")
	case errors.Is(e, model.ErrInput):
		h.error(w, r, 400, "invalid_gallery_highlight", "连接或画廊查询参数无效")
	case errors.Is(e, model.ErrNotFound):
		h.error(w, r, 404, "gallery_connection_not_found", "连接不存在")
	case errors.Is(e, model.ErrGalleryConflict):
		h.error(w, r, 409, "gallery_connection_revision_conflict", "连接版本已变化，请重新读取并核对")
	case errors.Is(e, model.ErrGalleryCapacity):
		w.Header().Set("Retry-After", "60")
		h.error(w, r, 429, "gallery_connection_limit", "有效连接已达20条，请撤销不再使用的连接")
	case errors.As(e, &rate):
		w.Header().Set("Retry-After", strconv.Itoa(rate.RetryAfter))
		h.error(w, r, 429, "gallery_highlight_rate_limited", "查询过于频繁或正在处理，请稍后重试")
	default:
		h.error(w, r, 503, "gallery_highlight_unavailable", "只读高亮服务暂不可用")
	}
	// Never pass errors through generic failure: SQL/input errors may include
	// private values. Request logging contains only method/status/request ID.
}
