package httpapi

import (
	"fmt"
	"net/http"
	"net/url"

	"nyamangaserver.local/server/internal/model"
)

const manifestCreatePath = "/api/v1/client/editions/{id}/offline-manifests"
const manifestGetPath = "/api/v1/client/offline-manifests/{id}"
const manifestPagesPath = manifestGetPath + "/pages"
const offlineCheckPath = "/api/v1/client/offline/check"

func (h *Handler) manifestRoutes(m *http.ServeMux) {
	m.HandleFunc(manifestCreatePath, h.method("POST", h.createOfflineManifest))
	m.HandleFunc(manifestGetPath, h.method("GET", h.getOfflineManifest))
	m.HandleFunc(manifestPagesPath, h.method("GET", h.offlineManifestPages))
	m.HandleFunc(offlineCheckPath, h.method("POST", h.checkOffline))
}

func (h *Handler) createOfflineManifest(w http.ResponseWriter, r *http.Request) {
	var in model.OfflineManifestRequest
	if !h.decode(w, r, &in) {
		return
	}
	v, e := h.catalog.CreateOfflineManifest(r.Context(), r.PathValue("id"), in)
	if e != nil {
		h.downloadFailure(w, r, e)
		return
	}
	w.Header().Set("Location", "/api/v1/client/offline-manifests/"+v.ID)
	h.json(w, 200, v)
}

func (h *Handler) getOfflineManifest(w http.ResponseWriter, r *http.Request) {
	v, e := h.catalog.OfflineManifest(r.Context(), r.PathValue("id"))
	if e != nil {
		h.downloadFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}

func (h *Handler) offlineManifestPages(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.failure(w, r, model.ErrInput)
		return
	}
	id := r.PathValue("id")
	// The page directory and identity come from the same validated manifest.
	v, e := h.catalog.OfflinePages(r.Context(), id, page, size)
	if e != nil {
		h.downloadFailure(w, r, e)
		return
	}
	for i := range v.Items {
		v.Items[i].ImageURL = fmt.Sprintf("/api/v1/client/editions/%s/pages/%d/image?content_revision=%d&parsing_revision=%d&offline_manifest_id=%s",
			url.PathEscape(v.EditionID), v.Items[i].Index, v.ContentRevision, v.ParsingRevision, url.QueryEscape(id))
	}
	h.json(w, 200, v)
}

func (h *Handler) checkOffline(w http.ResponseWriter, r *http.Request) {
	var in model.OfflineCheckRequest
	if !h.decode(w, r, &in) {
		return
	}
	items, e := h.catalog.CheckOffline(r.Context(), in)
	if e != nil {
		h.downloadFailure(w, r, e)
		return
	}
	h.json(w, 200, map[string]any{"items": items})
}
