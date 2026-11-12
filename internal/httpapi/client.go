package httpapi

import (
	"net/http"
	"time"

	"nyamangaserver.local/server/internal/comic"

	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/userstate"
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
	sourceAccess, mappedSource := false, false
	for _, l := range v.Libraries {
		if !l.Disabled && h.catalog != nil {
			sourceAccess = true
			_, err := h.catalog.Root(l.RootKey)
			mappedSource = mappedSource || err == nil
		}
	}
	pages := feature(true, h.catalog != nil, libraryAccess, false)
	if pages.Available {
		switch {
		case !sourceAccess:
			pages.Available, pages.Reason = false, "library_disabled"
		case !mappedSource:
			pages.Available, pages.Reason = false, "root_unavailable"
		}
	}
	offlineContent := pages
	if offlineContent.Available && v.HealthMaintenance {
		offlineContent.Available, offlineContent.Reason = false, "maintenance_active"
	}

	h.json(w, 200, map[string]any{
		"protocol_version": clientProtocol, "instance": v.Identity, "account_id": a.ID, "acl_revision": v.ACLRevision,
		"libraries": v.Libraries, "maintenance_active": v.HealthMaintenance, "background_hold": v.BackgroundHold,
		"features": map[string]clientFeature{
			"device_sessions":   feature(true, true, true, true),
			"work_search":       feature(true, h.search != nil, libraryAccess, false),
			"reading_context":   feature(true, h.catalog != nil, libraryAccess, false),
			"page_images":       pages,
			"image_prefetch":    pages,
			"personal_state":    feature(true, h.userstate != nil, true, true),
			"downloads":         offlineContent,
			"offline_sync":      feature(true, h.userstate != nil, true, true),
			"offline_manifests": offlineContent,
			"image_variants":    feature(false, false, false, false),
		},
		"limits": map[string]int{"page_index_base": 0, "list_page_base": 1, "default_page_size": 24, "max_page_size": 100,
			"active_devices": model.DeviceLimit, "device_lifetime_seconds": model.DeviceLifetimeSeconds, "device_renew_window_seconds": model.DeviceRenewWindowSeconds,
			"image_online_per_session": imagesPerSession, "image_online_global": imagesGlobal,
			"image_processing_parallel": comic.ImageParallel, "image_prefetch_parallel": comic.ImagePrefetchParallel,
			"image_wait_seconds": int(comic.ImageWait / time.Second), "prefetch_before": 1, "prefetch_after": 2,
			"download_segment_bytes": model.DownloadSegment, "download_max_range_bytes": model.DownloadMaxRange,
			"download_per_session": 1, "download_global": downloadGlobal, "download_prepare_parallel": 1,
			"offline_batch_limit": userstate.OfflineBatchLimit},
	})
}
