package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"nyamangaserver.local/server/internal/model"
)

const deviceSessionsPath = "/api/v1/auth/device-sessions"
const deviceRenewPath = deviceSessionsPath + "/current/renew"

// A device gets only these exact methods and registered handler patterns.
var deviceRoutes = map[string]map[string]bool{
	manifestCreatePath:                               {"POST": true},
	manifestGetPath:                                  {"GET": true},
	manifestPagesPath:                                {"GET": true},
	offlineCheckPath:                                 {"POST": true},
	downloadPreparePath:                              {"POST": true},
	downloadStatusPath:                               {"GET": true, "DELETE": true},
	downloadContentPath:                              {"GET": true, "HEAD": true},
	clientWorkSearchPath:                             {"POST": true},
	clientContextPath:                                {"GET": true},
	clientPagesPath:                                  {"GET": true},
	clientImagePath:                                  {"GET": true},
	clientCapabilitiesPath:                           {"GET": true},
	deviceSessionsPath:                               {"GET": true},
	deviceSessionsPath + "/{id}":                     {"DELETE": true},
	deviceRenewPath:                                  {"POST": true},
	"/api/v1/auth/session":                           {"GET": true},
	"/api/v1/auth/logout":                            {"POST": true},
	"/api/v1/auth/password":                          {"PUT": true},
	"/api/v1/libraries":                              {"GET": true},
	"/api/v1/editions/{id}/cover":                    {"GET": true},
	"/api/v1/editions/{id}/reading-state":            {"GET": true, "PUT": true},
	"/api/v1/editions/{id}/reading-sessions":         {"POST": true},
	"/api/v1/editions/{id}/user-state":               {"PATCH": true},
	"/api/v1/saved-searches":                         {"GET": true, "POST": true},
	"/api/v1/saved-searches/{id}":                    {"GET": true, "PATCH": true, "DELETE": true},
	"/api/v1/tags":                                   {"GET": true},
	"/api/v1/tags/{id}/follow":                       {"PUT": true, "DELETE": true},
	"/api/v1/tag-catalog":                            {"GET": true},
	"/api/v1/tag-catalog/{id}/images/{role}/content": {"GET": true},
}

func (h *Handler) registerDeviceRoutes() {
	h.mux.HandleFunc("POST "+deviceSessionsPath, h.deviceLogin)
	h.protected.HandleFunc(deviceSessionsPath, h.method("GET", h.deviceList))
	h.protected.HandleFunc(deviceRenewPath, h.method("POST", h.deviceRenew))
	h.protected.HandleFunc(deviceSessionsPath+"/{id}", h.method("DELETE", h.deviceRevoke))
}

func (h *Handler) deviceOrigin(w http.ResponseWriter, r *http.Request) bool {
	values := r.Header.Values("Origin")
	if len(values) == 0 {
		return true
	}
	if len(values) != 1 || values[0] != h.c.PublicURL {
		h.error(w, r, 403, "origin_rejected", "请求来源不允许")
		return false
	}
	return true
}

func (h *Handler) deviceLogin(w http.ResponseWriter, r *http.Request) {
	if len(r.Header.Values("Authorization")) != 0 || len(r.Header.Values("Cookie")) != 0 {
		h.error(w, r, 400, "unexpected_credentials", "设备登录仅接受请求正文中的账号密码")
		return
	}
	if !h.deviceOrigin(w, r) || !h.limited(w, r) {
		return
	}
	release, admitted := h.healthAdmission(w, r)
	if !admitted {
		return
	}
	defer release()
	var in struct {
		Username         *string `json:"username"`
		Password         *string `json:"password"`
		DeviceName       *string `json:"device_name"`
		ClientInstanceID *string `json:"client_instance_id"`
	}
	if !h.decodeJSON(w, r, &in, true) {
		return
	}
	if in.Username == nil || in.Password == nil || in.DeviceName == nil || in.ClientInstanceID == nil {
		h.error(w, r, 400, "invalid_request", "缺少必填字段")
		return
	}
	raw, v, e := h.svc.LoginDevice(r.Context(), *in.Username, *in.Password, *in.DeviceName, *in.ClientInstanceID)
	if e != nil {
		h.failure(w, r, e)
		return
	}
	h.json(w, 201, map[string]any{"device": v, "token": raw, "actor": deviceActorResponse(v.Account)})
}

func deviceActorResponse(a *model.Account) any {
	out := actorResponse(a).(map[string]any)
	out["capabilities"] = map[string]bool{"manage_accounts": false, "global_maintenance": false, "manage_global_tags": false, "personal_state": !a.MustChangePassword}
	return out
}

func (h *Handler) deviceList(w http.ResponseWriter, r *http.Request) {
	v, e := h.svc.DeviceSessions(r.Context())
	if e != nil {
		h.failure(w, r, e)
		return
	}
	h.json(w, 200, map[string]any{"items": v})
}

func (h *Handler) deviceRenew(w http.ResponseWriter, r *http.Request) {
	v, e := h.svc.RenewDeviceSession(r.Context())
	if e != nil {
		h.failure(w, r, e)
		return
	}
	h.json(w, 200, map[string]any{"device": v})
}

func (h *Handler) deviceRevoke(w http.ResponseWriter, r *http.Request) {
	if e := h.svc.RevokeDeviceSession(r.Context(), r.PathValue("id")); e != nil {
		h.failure(w, r, e)
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) authenticateDevice(w http.ResponseWriter, r *http.Request, next http.Handler, headers []string) {
	if !h.deviceOrigin(w, r) {
		return
	}
	if len(headers) != 1 {
		h.failure(w, r, model.ErrDeviceSession)
		return
	}
	scheme, raw, ok := strings.Cut(headers[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		h.failure(w, r, model.ErrDeviceSession)
		return
	}
	v, e := h.svc.DeviceSession(r.Context(), raw)
	if e != nil {
		h.failure(w, r, e)
		return
	}
	_, pattern := h.protected.Handler(r)
	if !deviceRoutes[pattern][r.Method] {
		h.failure(w, r, model.ErrForbidden)
		return
	}
	if v.Account.MustChangePassword && !passwordChangeRoute(r.URL.Path) {
		h.failure(w, r, model.ErrPasswordChangeRequired)
		return
	}
	ctx := model.WithDeviceActor(r.Context(), *v.Account, v.ID, h.svc.Now)
	r = r.WithContext(ctx)
	release, admitted := h.healthAdmission(w, r)
	if !admitted {
		return
	}
	defer release()
	if !v.Account.MustChangePassword {
		if e = h.svc.TouchDeviceSession(ctx, v); errors.Is(e, model.ErrDeviceSession) {
			h.failure(w, r, e)
			return
		} else if e != nil && !healthMaintenanceError(e) {
			h.logger.Warn("device activity update failed", "request_id", w.Header().Get("X-Request-ID"))
		}
	}
	next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, sessionKey{}, identity{device: v})))
}

func passwordChangeRoute(path string) bool {
	return path == "/api/v1/auth/session" || path == "/api/v1/auth/password" || path == "/api/v1/auth/logout"
}

type deviceLibrary struct {
	ID           string `json:"id"`
	DisplayName  string `json:"display_name"`
	Disabled     bool   `json:"disabled"`
	Availability string `json:"availability"`
}
