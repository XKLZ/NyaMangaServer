package httpapi

import (
	"net/http"
	"nyamangaserver.local/server/internal/model"
	"strconv"
)

func (h *Handler) registerSourceSearch() {
	base := "/api/v1/import-batches/{id}/members/{edition}/source-searches"
	h.protected.HandleFunc(base, h.method("POST", h.createSourceSearch))
	h.protected.HandleFunc(base+"/{search}", h.method("GET", h.sourceSearch))
	h.protected.HandleFunc(base+"/{search}/results", h.method("GET", h.sourceSearchResults))
	h.protected.HandleFunc(base+"/{search}/load-page", h.method("POST", h.loadSourceSearch))
	h.protected.HandleFunc(base+"/{search}/results/{result}", h.method("GET", h.sourceSearchResult))
	h.protected.HandleFunc(base+"/{search}/results/{result}/cover", h.method("GET", h.sourceSearchCover))
	h.protected.HandleFunc(base+"/{search}/select", h.method("POST", h.selectSourceSearch))
	h.protected.HandleFunc(base+"/{search}/control/{action}", h.method("POST", h.controlSourceSearch))
}
func (h *Handler) createSourceSearch(w http.ResponseWriter, r *http.Request) {
	var in model.SourceSearchInput
	if !h.decode(w, r, &in) {
		return
	}
	out, e := h.metadata.CreateSourceSearch(r.Context(), r.PathValue("id"), r.PathValue("edition"), in)
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.json(w, 202, out)
}
func (h *Handler) sourceSearch(w http.ResponseWriter, r *http.Request) {
	out, e := h.metadata.SourceSearch(r.Context(), r.PathValue("id"), r.PathValue("edition"), r.PathValue("search"))
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
func (h *Handler) sourceSearchResults(w http.ResponseWriter, r *http.Request) {
	page, n, e := pagination(r)
	if e != nil || (r.URL.Query().Has("page_size") && n != 10) {
		h.catalogFailure(w, r, model.ErrInput)
		return
	}
	out, e := h.metadata.SearchResults(r.Context(), r.PathValue("id"), r.PathValue("edition"), r.PathValue("search"), page)
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
func (h *Handler) loadSourceSearch(w http.ResponseWriter, r *http.Request) {
	var in model.SearchLoad
	if !h.decode(w, r, &in) {
		return
	}
	out, e := h.metadata.LoadSourceSearch(r.Context(), r.PathValue("id"), r.PathValue("edition"), r.PathValue("search"), in)
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.json(w, 202, out)
}
func (h *Handler) sourceSearchResult(w http.ResponseWriter, r *http.Request) {
	out, e := h.metadata.SearchResult(r.Context(), r.PathValue("id"), r.PathValue("edition"), r.PathValue("search"), r.PathValue("result"))
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
func (h *Handler) selectSourceSearch(w http.ResponseWriter, r *http.Request) {
	var in model.SearchSelect
	if !h.decode(w, r, &in) {
		return
	}
	if e := h.metadata.SelectSourceSearch(r.Context(), r.PathValue("id"), r.PathValue("edition"), r.PathValue("search"), in); e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.json(w, 200, map[string]bool{"selected": true})
}
func (h *Handler) controlSourceSearch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	if e := h.metadata.ControlSourceSearch(r.Context(), r.PathValue("id"), r.PathValue("edition"), r.PathValue("search"), r.PathValue("action"), in.ExpectedRevision); e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.sourceSearch(w, r)
}
func (h *Handler) sourceSearchCover(w http.ResponseWriter, r *http.Request) {
	rev, e := strconv.ParseInt(r.URL.Query().Get("result_revision"), 10, 64)
	if e != nil || rev < 1 {
		h.catalogFailure(w, r, model.ErrInput)
		return
	}
	data, e := h.imports.SourceSearchCover(r.Context(), r.PathValue("id"), r.PathValue("edition"), r.PathValue("search"), r.PathValue("result"), rev)
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(200)
	_, _ = w.Write(data)
}
