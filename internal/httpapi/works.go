package httpapi

import (
	"net/http"
	"nyamangaserver.local/server/internal/model"
	"strconv"
	"strings"
)

func (h *Handler) retiredOrganization(w http.ResponseWriter, r *http.Request) {
	h.organizationFailure(w, r, model.ErrOrganizationModelChanged)
}
func csvIDs(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}
func (h *Handler) workContext(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	g, e := h.organization.WorkContext(r.Context(), accountID(r), csvIDs(q.Get("edition_ids")), csvIDs(q.Get("work_ids")), csvIDs(q.Get("series_ids")))
	if e != nil {
		h.organizationFailure(w, r, e)
		return
	}
	h.json(w, 200, g)
}
func (h *Handler) workPreview(w http.ResponseWriter, r *http.Request) {
	var c model.WorkCommand
	if !h.decode(w, r, &c) {
		return
	}
	h.changeWork(w, r, c, true)
}
func (h *Handler) changeWork(w http.ResponseWriter, r *http.Request, c model.WorkCommand, preview bool) {
	v, e := h.organization.ChangeWork(r.Context(), accountID(r), c, preview)
	if e != nil {
		h.organizationFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) works(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.organizationFailure(w, r, model.ErrInput)
		return
	}
	q := r.URL.Query()
	minimum := 1
	if q.Has("min_editions") {
		minimum, e = strconv.Atoi(q.Get("min_editions"))
		if e != nil {
			h.organizationFailure(w, r, model.ErrInput)
			return
		}
	}
	v, e := h.organization.Works(r.Context(), q.Get("library_id"), q.Get("query"), page, size, minimum)
	if e != nil {
		h.organizationFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) workDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		v, e := h.organization.Work(r.Context(), r.PathValue("id"))
		if e != nil {
			h.organizationFailure(w, r, e)
			return
		}
		h.json(w, 200, v)
		return
	}
	if r.Method != "PATCH" {
		h.error(w, r, 405, "method_not_allowed", "作品由导入建立；使用合并、拆分或版本移归管理")
		return
	}
	var c model.WorkCommand
	if !h.decode(w, r, &c) {
		return
	}
	c.ID = r.PathValue("id")
	c.Action = "work_name"
	h.changeWork(w, r, c, false)
}
func (h *Handler) workEditions(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.organizationFailure(w, r, model.ErrInput)
		return
	}
	v, e := h.organization.Editions(r.Context(), accountID(r), r.PathValue("id"), r.URL.Query().Get("query"), page, size)
	if e != nil {
		h.organizationFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) workMutation(w http.ResponseWriter, r *http.Request) {
	suffix := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	method := "POST"
	if suffix == "preferred" || suffix == "favorite" {
		method = "PUT"
	}
	if r.Method != method {
		h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
		return
	}
	var c model.WorkCommand
	if !h.decode(w, r, &c) {
		return
	}
	c.ID = r.PathValue("id")
	c.Action = "work_" + suffix
	h.changeWork(w, r, c, false)
}
func (h *Handler) moveEdition(w http.ResponseWriter, r *http.Request) {
	var c model.WorkCommand
	if !h.decode(w, r, &c) {
		return
	}
	c.EditionID = r.PathValue("id")
	c.Action = "edition_move"
	h.changeWork(w, r, c, false)
}
func (h *Handler) editionLabels(w http.ResponseWriter, r *http.Request) {
	var c model.WorkCommand
	if !h.decode(w, r, &c) {
		return
	}
	c.EditionID = r.PathValue("id")
	c.Action = "edition_labels"
	h.changeWork(w, r, c, false)
}
func (h *Handler) seriesCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		page, size, e := pagination(r)
		if e != nil {
			h.organizationFailure(w, r, model.ErrInput)
			return
		}
		q := r.URL.Query()
		v, e := h.organization.SeriesPage(r.Context(), q.Get("library_id"), q.Get("query"), page, size)
		if e != nil {
			h.organizationFailure(w, r, e)
			return
		}
		h.json(w, 200, seriesPageView(v))
		return
	}
	if r.Method != "POST" {
		h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
		return
	}
	var c model.WorkCommand
	if !h.decode(w, r, &c) {
		return
	}
	c.Action = "series_create"
	h.changeWork(w, r, c, false)
}
func (h *Handler) seriesDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		summary := true
		if r.URL.Query().Has("summary") {
			var e error
			summary, e = strconv.ParseBool(r.URL.Query().Get("summary"))
			if e != nil {
				h.error(w, r, 400, "invalid_organization", "summary必须为布尔值")
				return
			}
		}
		v, e := h.organization.Series(r.Context(), r.PathValue("id"), summary)
		if e != nil {
			h.organizationFailure(w, r, e)
			return
		}
		h.json(w, 200, seriesView(v))
		return
	}
	if r.Method != "PATCH" && r.Method != "DELETE" {
		h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
		return
	}
	var c model.WorkCommand
	if !h.decode(w, r, &c) {
		return
	}
	c.ID = r.PathValue("id")
	c.Action = "series_rename"
	if r.Method == "DELETE" {
		c.Action = "series_delete"
	}
	h.changeWork(w, r, c, false)
}
func (h *Handler) seriesMutation(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if r.Method == "GET" && strings.HasSuffix(path, "/members") {
		page, size, e := pagination(r)
		if e != nil {
			h.organizationFailure(w, r, model.ErrInput)
			return
		}
		v, e := h.organization.SeriesWorks(r.Context(), r.PathValue("id"), r.URL.Query().Get("query"), page, size)
		if e != nil {
			h.organizationFailure(w, r, e)
			return
		}
		h.json(w, 200, v)
		return
	}
	action := ""
	switch {
	case strings.HasSuffix(path, "/members") && r.Method == "POST":
		action = "series_add"
	case strings.HasSuffix(path, "/members") && r.Method == "PUT":
		action = "series_members"
	case strings.HasSuffix(path, "/sort") && r.Method == "POST":
		action = "series_sort"
	case strings.HasSuffix(path, "/transfers") && r.Method == "POST":
		action = "series_transfer"
	case strings.HasSuffix(path, "/move") && r.Method == "POST":
		action = "series_move"
	case r.PathValue("item") != "" && r.Method == "DELETE":
		action = "series_remove"
	default:
		h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
		return
	}
	var c model.WorkCommand
	if !h.decode(w, r, &c) {
		return
	}
	c.ID = r.PathValue("id")
	c.Action = action
	if item := r.PathValue("item"); item != "" {
		if c.WorkIDs != nil || c.LegacyItemIDs != nil {
			h.organizationFailure(w, r, model.ErrAliasFields)
			return
		}
		c.WorkIDs = []string{item}
	}
	h.changeWork(w, r, c, false)
}

func (h *Handler) generateVersionLabels(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Expected map[string]int64 `json:"expected_revisions"`
		Restore  []string         `json:"restore_edition_ids"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	v, e := h.organization.GenerateVersionLabels(r.Context(), accountID(r), r.PathValue("id"), in.Expected, in.Restore)
	if e != nil {
		h.organizationFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
