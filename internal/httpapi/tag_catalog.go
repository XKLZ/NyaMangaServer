package httpapi

import (
	"errors"
	"net/http"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/tagcatalog"
	"strconv"
)

func (h *Handler) WithTagCatalog(s *tagcatalog.Service) *Handler {
	h.tagCatalog = s
	h.protected.HandleFunc("/api/v1/tags/{id}/follow", h.tagFollow)
	h.protected.HandleFunc("/api/v1/tag-catalog", h.method("GET", h.tagCatalogList))
	h.protected.HandleFunc("/api/v1/tag-catalog/{id}", h.method("GET", h.tagCatalogDetail))
	h.protected.HandleFunc("/api/v1/tag-catalog/{id}/works", h.method("GET", h.tagCatalogWorks))
	h.protected.HandleFunc("/api/v1/tag-catalog/{id}/editions", h.method("GET", h.tagCatalogEditions))
	h.protected.HandleFunc("/api/v1/tag-catalog/{id}/relations", h.method("GET", h.tagCatalogRelations))
	h.protected.HandleFunc("/api/v1/parody-character-relations/{parody}/{character}", h.tagRelationWrite)
	h.protected.HandleFunc("/api/v1/editions/{id}/character-suppressions/{parody}/{character}", h.tagSuppressionWrite)
	return h
}
func (h *Handler) tagCatalogFailure(w http.ResponseWriter, r *http.Request, e error) {
	switch {
	case errors.Is(e, model.ErrDisabled), errors.Is(e, model.ErrTrashed), errors.Is(e, model.ErrBusy):
		h.catalogFailure(w, r, e)
	case errors.Is(e, model.ErrInput):
		h.error(w, r, 400, "invalid_tag_catalog", "标签管理需要明确书库，分类、关系目标或分页参数无效")
	case errors.Is(e, model.ErrSnapshot):
		h.error(w, r, 409, "tag_catalog_snapshot_expired", "标签目录或关系已更新，请重新查询第一页")
	case errors.Is(e, model.ErrConflict):
		h.error(w, r, 409, "tag_relation_revision_conflict", "标签关系已变化，请保留草稿并重新核对版本")
	case errors.Is(e, model.ErrTagProjection):
		h.error(w, r, 503, "tag_projection_unavailable", "标签关系索引尚未就绪，当前不能读取或提交")
	case errors.Is(e, model.ErrNotFound):
		h.error(w, r, 404, "not_found", "标签、书库或版本不存在，或已进入回收站")
	default:
		h.failure(w, r, e)
	}
}
func (h *Handler) tagInput(w http.ResponseWriter, r *http.Request, relations bool) (model.TagCatalogInput, bool) {
	q := r.URL.Query()
	if q.Has("account_id") || q.Has("followed_only") && r.URL.Path != "/api/v1/tag-catalog" {
		h.tagCatalogFailure(w, r, model.ErrInput)
		return model.TagCatalogInput{}, false
	}
	in := model.TagCatalogInput{LibraryID: q.Get("library_id"), Category: q.Get("category"), Namespace: q.Get("namespace"), Query: q.Get("q"), Sort: q.Get("sort"), Order: q.Get("order"), Snapshot: q.Get("snapshot"), RelatedTagID: q.Get("related_tag_id"), Partition: q.Get("partition")}
	if q.Has("view") {
		views := q["view"]
		if !relations || len(views) != 1 || (views[0] != "all" && views[0] != "display") {
			h.tagCatalogFailure(w, r, model.ErrInput)
			return in, false
		}
		in.View = views[0]
	}
	var e error
	if q.Has("followed_only") {
		values := q["followed_only"]
		if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
			h.tagCatalogFailure(w, r, model.ErrInput)
			return in, false
		}
		in.FollowedOnly = values[0] == "true"
	}
	for key, dest := range map[string]*int{"page": &in.Page, "page_size": &in.PageSize} {
		if q.Has(key) {
			*dest, e = strconv.Atoi(q.Get(key))
			if e != nil || *dest < 1 {
				h.tagCatalogFailure(w, r, model.ErrInput)
				return in, false
			}
		}
	}
	if q.Has("include_unused") {
		in.IncludeUnused, e = strconv.ParseBool(q.Get("include_unused"))
		if e != nil {
			h.tagCatalogFailure(w, r, model.ErrInput)
			return in, false
		}
	}
	in, e = tagcatalog.Validate(in)
	if e != nil {
		h.tagCatalogFailure(w, r, e)
		return in, false
	}
	return in, true
}
func (h *Handler) tagCatalogList(w http.ResponseWriter, r *http.Request) {
	in, ok := h.tagInput(w, r, false)
	if !ok {
		return
	}
	out, e := h.tagCatalog.Catalog(tagReadContext(r), in)
	if e != nil {
		h.tagCatalogFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
func (h *Handler) tagCatalogDetail(w http.ResponseWriter, r *http.Request) {
	in, ok := h.tagInput(w, r, false)
	if !ok {
		return
	}
	out, e := h.tagCatalog.Detail(tagReadContext(r), r.PathValue("id"), in)
	if e != nil {
		h.tagCatalogFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
func (h *Handler) tagCatalogEditions(w http.ResponseWriter, r *http.Request) {
	in, ok := h.tagInput(w, r, false)
	if !ok {
		return
	}
	out, e := h.tagCatalog.Editions(tagReadContext(r), r.PathValue("id"), in)
	if e != nil {
		h.tagCatalogFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
func (h *Handler) tagCatalogRelations(w http.ResponseWriter, r *http.Request) {
	in, ok := h.tagInput(w, r, true)
	if !ok {
		return
	}
	out, e := h.tagCatalog.Relations(tagReadContext(r), r.PathValue("id"), in)
	if e != nil {
		h.tagCatalogFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
func (h *Handler) tagWriteInput(w http.ResponseWriter, r *http.Request) (model.TagRelationWrite, bool) {
	var out model.TagRelationWrite
	if r.Method != "PUT" && r.Method != "DELETE" {
		w.Header().Set("Allow", "PUT, DELETE")
		h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
		return out, false
	}
	var in struct {
		Expected *int64 `json:"expected_revision"`
		Decision string `json:"decision,omitempty"`
	}
	if !h.decode(w, r, &in) {
		return out, false
	}
	if in.Expected == nil || *in.Expected < 0 {
		h.tagCatalogFailure(w, r, model.ErrInput)
		return out, false
	}
	out.ExpectedRevision = *in.Expected
	out.Decision = in.Decision
	return out, true
}
func (h *Handler) tagRelationWrite(w http.ResponseWriter, r *http.Request) {
	in, ok := h.tagWriteInput(w, r)
	if !ok {
		return
	}
	if r.Method == "DELETE" {
		if in.Decision != "" {
			h.tagCatalogFailure(w, r, model.ErrInput)
			return
		}
		in.Decision = "auto"
	} else if in.Decision != "confirmed" && in.Decision != "excluded" {
		h.tagCatalogFailure(w, r, model.ErrInput)
		return
	}
	out, e := h.tagCatalog.Decide(r.Context(), r.PathValue("parody"), r.PathValue("character"), in.ExpectedRevision, in.Decision)
	if e != nil {
		h.tagCatalogFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
func (h *Handler) tagSuppressionWrite(w http.ResponseWriter, r *http.Request) {
	in, ok := h.tagWriteInput(w, r)
	if !ok {
		return
	}
	if in.Decision != "" {
		h.tagCatalogFailure(w, r, model.ErrInput)
		return
	}
	out, e := h.tagCatalog.Suppress(r.Context(), r.PathValue("id"), r.PathValue("parody"), r.PathValue("character"), in.ExpectedRevision, r.Method == "PUT")
	if e != nil {
		h.tagCatalogFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}

func (h *Handler) tagCatalogWorks(w http.ResponseWriter, r *http.Request) {
	if q := r.URL.Query(); q.Has("partition") || q.Has("related_tag_id") {
		h.tagCatalogFailure(w, r, model.ErrInput)
		return
	}
	in, ok := h.tagInput(w, r, false)
	if !ok {
		return
	}
	out, e := h.tagCatalog.Works(tagReadContext(r), r.PathValue("id"), in)
	if e != nil {
		h.tagCatalogFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
