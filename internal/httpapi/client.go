package httpapi

import (
	"net/http"

	"nyamangaserver.local/server/internal/model"
)

const clientProtocol = 1
const clientServerInfoPath = "/api/v1/client/server-info"
const clientCapabilitiesPath = "/api/v1/client/capabilities"

type clientFeature struct {
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Allowed   bool   `json:"allowed"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

func (h *Handler) clientServerInfo(w http.ResponseWriter, r *http.Request) {
	v, e := h.svc.ClientIdentity(r.Context())
	if e != nil {
		h.failure(w, r, e)
		return
	}
	initialized, e := h.svc.Initialized(r.Context())
	if e != nil {
		h.failure(w, r, e)
		return
	}
	h.json(w, 200, map[string]any{"service": "NyaMangaServer", "version": h.build.Version, "protocol_version": clientProtocol, "instance_id": v.InstanceID, "initialized": initialized})
}

func (h *Handler) clientCapabilities(w http.ResponseWriter, r *http.Request) {
	v, e := h.svc.ClientConfiguration(r.Context())
	if e != nil {
		h.failure(w, r, e)
		return
	}
	a, _ := model.Actor(r.Context())
	feature := func(supported, enabled, allowed bool, writable bool) clientFeature {
		reason := "available"
		switch {
		case !supported:
			reason = "unsupported"
		case !enabled:
			reason = "instance_disabled"
		case !allowed:
			reason = "account_not_allowed"
		case writable && v.HealthMaintenance:
			reason = "maintenance_active"
		}
		return clientFeature{Supported: supported, Enabled: enabled, Allowed: allowed, Available: reason == "available", Reason: reason}
	}
	libraryAccess := len(v.Libraries) > 0
	sourceAccess := false
	for _, l := range v.Libraries {
		sourceAccess = sourceAccess || !l.Disabled
	}
	pages := feature(true, h.catalog != nil, libraryAccess, false)
	if pages.Available && !sourceAccess {
		pages.Available, pages.Reason = false, "library_disabled"
	}
	h.json(w, 200, map[string]any{
		"protocol_version": clientProtocol, "instance": v.Identity, "account_id": a.ID, "acl_revision": a.ACLRevision,
		"libraries": v.Libraries, "maintenance_active": v.HealthMaintenance, "background_hold": v.BackgroundHold,
		"features": map[string]clientFeature{
			"device_sessions": feature(true, true, true, true),
			"work_search":     feature(true, h.search != nil, libraryAccess, false),
			"reading_context": feature(true, h.catalog != nil, libraryAccess, false),
			"page_images":     pages,
			"personal_state":  feature(true, h.userstate != nil, true, true),
			"downloads":       feature(false, false, false, false),
			"offline_sync":    feature(false, false, false, false),
			"image_variants":  feature(false, false, false, false),
		},
		"limits": map[string]int{"page_index_base": 0, "list_page_base": 1, "default_page_size": 24, "max_page_size": 100,
			"active_devices": model.DeviceLimit, "device_lifetime_seconds": model.DeviceLifetimeSeconds, "device_renew_window_seconds": model.DeviceRenewWindowSeconds},
	})
}
