package httpapi

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/tagimages"
	"strconv"
)

func (h *Handler) tagImageFailure(w http.ResponseWriter, r *http.Request, e error) {
	switch {
	case errors.Is(e, model.ErrInput):
		h.error(w, r, 400, "invalid_tag_image", "标签类别、图片用途或版本无效")
	case errors.Is(e, model.ErrNotFound):
		h.error(w, r, 404, "tag_image_not_found", "标签或图片不存在")
	case errors.Is(e, model.ErrConflict):
		h.error(w, r, 409, "tag_image_revision_conflict", "图片已变化，请保留待上传文件并重新核对版本")
	case errors.Is(e, comic.ErrLimit):
		h.imageLimitFailure(w, r, 413, "tag_image_limit", e)
	case errors.Is(e, comic.ErrImage), errors.Is(e, model.ErrTagImageType):
		h.error(w, r, 415, "invalid_tag_image_type", "图片损坏或格式不符，仅支持JPEG、PNG、WebP、BMP")
	default:
		h.error(w, r, 503, "tag_image_storage_unavailable", "图片存储暂不可用，原有图片保持")
	}
}
func (h *Handler) WithTagImages(s *tagimages.Service) *Handler {
	base := "/api/v1/tag-catalog/{id}/images"
	h.protected.HandleFunc(base, h.method("GET", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.State(r.Context(), r.PathValue("id"))
		if e != nil {
			h.tagImageFailure(w, r, e)
			return
		}
		h.json(w, 200, v)
	}))
	h.protected.HandleFunc(base+"/{role}", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" && r.Method != "DELETE" {
			w.Header().Set("Allow", "PUT, DELETE")
			h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
			return
		}
		rev, e := strconv.ParseInt(r.Header.Get("X-Expected-Revision"), 10, 64)
		if e != nil || rev < 0 {
			h.tagImageFailure(w, r, model.ErrInput)
			return
		}
		var v model.TagImage
		if r.Method == "DELETE" {
			v, e = s.Remove(r.Context(), r.PathValue("id"), r.PathValue("role"), rev)
		} else {
			media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || (media != "image/png" && media != "image/jpeg" && media != "image/webp" && media != "image/bmp") {
				h.tagImageFailure(w, r, model.ErrTagImageType)
				return
			}
			raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10<<20))
			if err != nil {
				h.tagImageFailure(w, r, comic.ErrLimit)
				return
			}
			v, e = s.Upload(r.Context(), r.PathValue("id"), r.PathValue("role"), rev, media, raw)
		}
		if e != nil {
			h.tagImageFailure(w, r, e)
			return
		}
		h.json(w, 200, v)
	})
	h.protected.HandleFunc(base+"/{role}/content", h.method("GET", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if len(q["revision"]) != 1 {
			h.tagImageFailure(w, r, model.ErrInput)
			return
		}
		rev, e := strconv.ParseInt(q.Get("revision"), 10, 64)
		if e != nil || rev < 1 {
			h.tagImageFailure(w, r, model.ErrInput)
			return
		}
		data, etag, e := s.Content(r.Context(), r.PathValue("id"), r.PathValue("role"), rev)
		if e != nil {
			h.tagImageFailure(w, r, e)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "private, no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(200)
		_, _ = w.Write(data)
	}))
	return h
}
