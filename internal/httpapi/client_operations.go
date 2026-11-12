package httpapi

import (
	"net/http"

	"nyamangaserver.local/server/internal/model"
)

const offlineOperationsPath = "/api/v1/client/offline/operations"
const offlineOperationStatusPath = "/api/v1/client/offline/operation-status"

func (h *Handler) offlineOperationRoutes(m *http.ServeMux) {
	m.HandleFunc(offlineOperationsPath, h.method("POST", h.replayOfflineOperations))
	m.HandleFunc(offlineOperationStatusPath, h.method("POST", h.offlineOperationStatus))
}

func (h *Handler) replayOfflineOperations(w http.ResponseWriter, r *http.Request) {
	var in model.OfflineOperationsRequest
	if !h.decode(w, r, &in) {
		return
	}
	items, e := h.userstate.ReplayOffline(r.Context(), in, h.svc.Now().Unix())
	if e != nil {
		h.downloadFailure(w, r, e)
		return
	}
	h.json(w, 200, map[string]any{"items": items})
}

func (h *Handler) offlineOperationStatus(w http.ResponseWriter, r *http.Request) {
	var in model.OfflineStatusRequest
	if !h.decode(w, r, &in) {
		return
	}
	items, e := h.userstate.OfflineStatus(r.Context(), in)
	if e != nil {
		h.downloadFailure(w, r, e)
		return
	}
	h.json(w, 200, map[string]any{"items": items})
}
