package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	"nyamangaserver.local/server/internal/search"
	"nyamangaserver.local/server/internal/userstate"
	"strings"
)

func accountID(r *http.Request) string {
	a, _ := model.Actor(r.Context())
	return a.ID
}
func (h *Handler) WithOrganization(o *organization.Service, s *search.Service, u *userstate.Service) *Handler {
	h.organization = o
	h.search = s
	h.userstate = u
	h.readingRoutes(h.protected)
	h.offlineOperationRoutes(h.protected)
	h.protected.HandleFunc(clientWorkSearchPath, h.method("POST", h.clientWorkSearch))
	p := h.protected
	p.HandleFunc("/api/v1/books/search", h.method("POST", h.searchBooks))
	p.HandleFunc("/api/v1/editions/search", h.method("POST", h.searchBooks))
	p.HandleFunc("/api/v1/search-results/{kind}/{id}/matches", h.method("GET", h.searchMatches))
	p.HandleFunc("/api/v1/books/{id}/user-state", h.method("PATCH", h.bookState))
	p.HandleFunc("/api/v1/editions/{id}/user-state", h.method("PATCH", h.bookState))
	for _, path := range []string{"/api/v1/organization-context", "/api/v1/organization/preview", "/api/v1/version-groups", "/api/v1/version-groups/"} {
		p.HandleFunc(path, h.retiredOrganization)
	}
	p.HandleFunc("/api/v1/work-organization/context", h.method("GET", h.workContext))
	p.HandleFunc("/api/v1/work-organization/preview", h.method("POST", h.workPreview))
	p.HandleFunc("/api/v1/works", h.method("GET", h.works))
	p.HandleFunc("/api/v1/works/{id}", h.workDetail)
	p.HandleFunc("/api/v1/works/{id}/editions", h.method("GET", h.workEditions))
	p.HandleFunc("/api/v1/works/{id}/version-labels", h.method("POST", h.generateVersionLabels))
	for _, suffix := range []string{"merge", "split", "separate", "preferred", "favorite"} {
		p.HandleFunc("/api/v1/works/{id}/"+suffix, h.workMutation)
	}
	p.HandleFunc("/api/v1/editions/{id}/move", h.method("POST", h.moveEdition))
	p.HandleFunc("/api/v1/editions/{id}/version-labels", h.method("PUT", h.editionLabels))
	p.HandleFunc("/api/v1/series", h.seriesCollection)
	p.HandleFunc("/api/v1/series/{id}", h.seriesDetail)
	for _, path := range []string{"series/{id}/members", "series/{id}/sort", "series/{id}/members/{item}", "series/{id}/members/{item}/move", "series/{id}/transfers"} {
		p.HandleFunc("/api/v1/"+path, h.seriesMutation)
	}
	p.HandleFunc("/api/v1/saved-searches", h.savedSearches)
	p.HandleFunc("/api/v1/saved-searches/{id}", h.savedSearch)
	return h
}
func (h *Handler) organizationFailure(w http.ResponseWriter, r *http.Request, e error) {
	var syntax *search.SyntaxError
	switch {
	case errors.Is(e, model.ErrOrganizationModelChanged):
		h.error(w, r, 410, "organization_model_changed", "组织模型已升级，请使用 /api/v1/works 和 /api/v1/work-organization/preview")
	case errors.Is(e, model.ErrPreferredRequired):
		h.error(w, r, 409, "preferred_edition_required", "移动首选版本前，请明确选择源作品的剩余首选版本")
	case errors.Is(e, model.ErrAliasFields):
		h.error(w, r, 400, "conflicting_alias_fields", "不能同时提交新字段与兼容字段")
	case errors.Is(e, model.ErrLabelFields):
		h.error(w, r, 400, "conflicting_version_label_fields", "不能同时提交单标签和多标签字段")
	case errors.Is(e, model.ErrMultipleVersionLabels):
		h.error(w, r, 409, "multiple_version_labels_require_array", "当前存在多个版本标签，请使用数组编辑")
	case errors.As(e, &syntax):
		h.json(w, 422, map[string]any{"error": map[string]any{"code": "invalid_query", "message": syntax.Error(), "position": syntax.Position, "request_id": w.Header().Get("X-Request-ID")}})
	case errors.Is(e, model.ErrCapacity):
		h.error(w, r, 409, "organization_capacity", "组织容量超过限制")
	case errors.Is(e, model.ErrMembership):
		h.error(w, r, 409, "membership_conflict", "成员已有组织归属")
	case errors.Is(e, model.ErrSnapshot):
		h.error(w, r, 409, "search_snapshot_expired", "结果已更新，请重新查询第一页")
	case errors.Is(e, model.ErrLibraryConflict):
		h.error(w, r, 409, "library_conflict", "系列与版本组只允许同一书库的书籍")
	case errors.Is(e, model.ErrSeriesConflict):
		h.error(w, r, 409, "series_conflict", "书籍属于不同系列，请先明确解除归属")
	case errors.Is(e, model.ErrInput):
		h.error(w, r, 422, "invalid_organization", "查询、选择、名称或组织操作无效")
	default:
		h.catalogFailure(w, r, e)
	}
}
func (h *Handler) organizationBook(w http.ResponseWriter, r *http.Request) {
	b, d, state, g, e := h.organization.EditionDetails(r.Context(), accountID(r), r.PathValue("id"))
	if e != nil {
		h.organizationFailure(w, r, e)
		return
	}
	h.json(w, 200, struct {
		bookResponse
		Metadata     model.MetadataDetails `json:"metadata"`
		State        model.UserState       `json:"user_state"`
		Organization model.WorkContext     `json:"organization"`
	}{response(*b).(bookResponse), d, state, g})
}
func (h *Handler) searchBooks(w http.ResponseWriter, r *http.Request) {
	var v model.SearchInput
	if !h.decode(w, r, &v) {
		return
	}
	out, e := h.search.Search(r.Context(), accountID(r), v)
	if e != nil {
		h.organizationFailure(w, r, e)
		return
	}
	items := []any{}
	for _, hit := range out.Items {
		items = append(items, struct {
			model.SearchHit
			Edition any `json:"representative"`
		}{hit, response(hit.Representative)})
	}
	h.json(w, 200, map[string]any{"items": items, "total": out.Total, "page": out.Page, "page_size": out.PageSize, "snapshot_revision": out.Snapshot})
}
func searchQuery(r *http.Request) (model.SearchInput, error) {
	page, size, e := pagination(r)
	q := r.URL.Query()
	v := model.SearchInput{Query: q.Get("query"), LibraryID: q.Get("library_id"), FileState: q.Get("file_state"), Scope: q.Get("scope"), Collapse: q.Get("collapse"), Sort: q.Get("sort"), Order: q.Get("order"), Page: page, PageSize: size, Snapshot: q.Get("snapshot_revision")}
	if e != nil {
		return v, e
	}
	if q.Has("properties") {
		raw := q.Get("properties")
		if len(raw) > 65536 {
			return v, model.ErrInput
		}
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if e = decoder.Decode(&v.Properties); e != nil {
			return v, model.ErrInput
		}
		if decoder.Decode(new(any)) != io.EOF {
			return v, model.ErrInput
		}
	}
	return v, nil
}
func (h *Handler) searchMatches(w http.ResponseWriter, r *http.Request) {
	v, e := searchQuery(r)
	if e != nil {
		h.organizationFailure(w, r, model.ErrInput)
		return
	}
	out, e := h.search.Matches(r.Context(), accountID(r), v, r.PathValue("kind"), r.PathValue("id"))
	if e != nil {
		h.organizationFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
func (h *Handler) bookState(w http.ResponseWriter, r *http.Request) {
	var in model.StatePatch
	if !h.decode(w, r, &in) {
		return
	}
	v, e := h.userstate.Set(r.Context(), accountID(r), r.PathValue("id"), in)
	if e != nil {
		h.organizationFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) savedSearches(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		v, e := h.search.List(r.Context(), accountID(r))
		if e != nil {
			h.organizationFailure(w, r, e)
			return
		}
		h.json(w, 200, map[string]any{"items": v})
		return
	}
	if r.Method == "POST" {
		h.saveSearch(w, r)
		return
	}
	h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
}
func (h *Handler) saveSearch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name         string            `json:"name"`
		Conditions   model.SearchInput `json:"conditions"`
		Expected     int64             `json:"expected_revision"`
		QueryVersion int               `json:"query_version"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	if in.QueryVersion != 1 {
		h.error(w, r, 422, "unsupported_query_version", "不支持的保存查询版本")
		return
	}
	v, e := h.search.Save(r.Context(), accountID(r), r.PathValue("id"), in.Name, in.Conditions, in.Expected)
	if e != nil {
		h.organizationFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) savedSearch(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		all, e := h.search.List(r.Context(), accountID(r))
		if e != nil {
			h.organizationFailure(w, r, e)
			return
		}
		for _, v := range all {
			if v.ID == r.PathValue("id") {
				if v.QueryVersion != 1 {
					h.error(w, r, 422, "unsupported_query_version", "不支持的保存查询版本")
					return
				}
				h.json(w, 200, v)
				return
			}
		}
		h.organizationFailure(w, r, model.ErrNotFound)
	case "PATCH":
		h.saveSearch(w, r)
	case "DELETE":
		var in struct {
			Expected int64 `json:"expected_revision"`
		}
		if !h.decode(w, r, &in) {
			return
		}
		if e := h.search.Delete(r.Context(), accountID(r), r.PathValue("id"), in.Expected); e != nil {
			h.organizationFailure(w, r, e)
			return
		}
		w.WriteHeader(204)
	default:
		h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
	}
}
