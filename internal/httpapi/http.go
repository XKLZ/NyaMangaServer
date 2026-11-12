// Package httpapi owns authentication, HTTP validation, and the public wire contract.
package httpapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"nyamangaserver.local/server/internal/auth"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/importflow"
	"nyamangaserver.local/server/internal/library"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/metadata"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	"nyamangaserver.local/server/internal/recommendation"
	"nyamangaserver.local/server/internal/search"
	"nyamangaserver.local/server/internal/tagcatalog"
	"nyamangaserver.local/server/internal/userstate"
)

const CookieName = "nyamangaserver_session"

type Build struct{ Version, Commit string }

// Capture process initialization, before database preparation and HTTP startup.
var processStartedAt = time.Now()

type sessionKey struct{}
type identity struct {
	raw     string
	session *model.Session
	device  *model.DeviceSession
}
type attempt struct {
	start time.Time
	count int
}
type Handler struct {
	downloads                         downloadRequests
	pageRequests                      imageRequests
	imageWait                         time.Duration         // deterministic test override
	beforeImageResponse               func(context.Context) // deterministic authorization race tests
	requestTimeout                    time.Duration         // test injection; zero retains ordinary 30s
	instanceTransfers                 bool
	health                            *healthHTTP
	startedAt                         time.Time
	tagCatalog                        *tagcatalog.Service
	imports                           *importflow.Service
	recommendations                   *recommendation.Service
	maintenance                       *maintenance.Service
	organization                      *organization.Service
	search                            *search.Service
	userstate                         *userstate.Service
	metadata                          *metadata.Service
	protected                         *http.ServeMux
	lifecycle                         context.Context
	streams                           int
	streamInterval, heartbeatInterval time.Duration
	catalog                           *library.Service
	c                                 config.Config
	svc                               *auth.Service
	assets                            fs.FS
	logger                            *slog.Logger
	build                             Build
	mux                               *http.ServeMux
	mu                                sync.Mutex
	attempts                          map[string]attempt
}

func New(c config.Config, svc *auth.Service, assets fs.FS, logger *slog.Logger, build Build, catalog ...*library.Service) *Handler {
	h := &Handler{startedAt: processStartedAt, c: c, svc: svc, assets: assets, logger: logger, build: build, mux: http.NewServeMux(), attempts: map[string]attempt{}}
	if len(catalog) > 0 {
		h.catalog = catalog[0]
	}
	h.mux.HandleFunc("/api/v1/auth/status", h.method("GET", h.status))
	h.mux.HandleFunc("/api/v1/auth/setup", h.method("POST", h.setup))
	h.mux.HandleFunc("/api/v1/auth/login", h.method("POST", h.login))
	h.mux.HandleFunc("/api/v1/health/live", h.method("GET", func(w http.ResponseWriter, r *http.Request) { h.json(w, 200, map[string]bool{"live": true}) }))
	h.mux.HandleFunc("/api/v1/health/ready", h.method("GET", h.ready))
	protected := http.NewServeMux()
	h.protected = protected
	h.lifecycle = context.Background()
	h.streamInterval = time.Second
	h.heartbeatInterval = 15 * time.Second
	if h.catalog != nil {
		h.catalogRoutes(protected)
	}
	h.accountRoutes(protected)
	h.registerDeviceRoutes()
	h.mux.HandleFunc(clientServerInfoPath, h.method("GET", h.clientServerInfo))
	protected.HandleFunc(clientCapabilitiesPath, h.method("GET", h.clientCapabilities))
	protected.HandleFunc("/api/v1/auth/session", h.method("GET", h.session))
	protected.HandleFunc("/api/v1/auth/logout", h.method("POST", h.logout))
	protected.HandleFunc("/api/v1/auth/password", h.method("PUT", h.password))
	protected.HandleFunc("/api/v1/library-roots", h.method("GET", h.roots))
	protected.HandleFunc("/api/v1/diagnostics", h.method("GET", h.diagnostics))
	protected.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { h.error(w, r, 404, "not_found", "资源不存在") })
	h.mux.Handle("/api/v1/", h.authenticate(protected))
	h.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { h.error(w, r, 404, "not_found", "API不存在") })
	h.mux.HandleFunc("/", h.static)
	return h
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (w *recorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *recorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
		w.ResponseWriter.WriteHeader(code)
	}
}
func (w *recorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Preserve the actual request's cancellation separately from our ordinary
	// deadline. Only authenticated, explicitly registered bulk routes can use it.
	base := context.WithValue(r.Context(), requestBaseContextKey{}, r.Context())
	ctx, cancel := context.WithCancel(base)
	if r.URL.Path != "/api/v1/events" {
		cancel()
		timeout := h.requestTimeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		ctx, cancel = context.WithTimeout(base, timeout)
	}
	defer cancel()
	r = r.WithContext(ctx)
	id, e := auth.RandomToken(12)
	if e != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	w.Header().Set("X-Request-ID", id)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Cache-Control", "no-store")
	}
	rw := &recorder{ResponseWriter: w}
	start := time.Now()
	defer func() {
		h.logger.Info("request", "request_id", id, "method", r.Method, "status", rw.status, "duration_ms", time.Since(start).Milliseconds())
	}()
	defer func() {
		if failure := recover(); failure != nil {
			if failure == http.ErrAbortHandler {
				panic(failure)
			}
			h.logger.Error("request panic", "request_id", id)
			if rw.status == 0 {
				h.error(rw, r, 500, "internal_error", "服务暂时不可用")
			}
		}
	}()
	if strings.HasPrefix(r.URL.Path, "/api/v1/") && !strings.HasPrefix(r.URL.Path, "/api/v1/gallery-highlight/") &&
		len(r.Header.Values("Authorization")) != 0 && len(r.Header.Values("Cookie")) != 0 {
		h.error(rw, r, 400, "mixed_credentials", "不能同时使用Cookie和Bearer凭证")
		return
	}
	h.mux.ServeHTTP(rw, r)
}
func (h *Handler) method(method string, f http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
			return
		}
		f(w, r)
	}
}
func (h *Handler) json(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(response(v))
}
func (h *Handler) error(w http.ResponseWriter, r *http.Request, code int, key, message string) {
	h.json(w, code, map[string]any{"error": map[string]string{"code": key, "message": message, "request_id": w.Header().Get("X-Request-ID")}})
}
func (h *Handler) failure(w http.ResponseWriter, r *http.Request, e error) {
	switch {
	case healthMaintenanceError(e):
		h.error(w, r, 503, "maintenance_active", "全局维护中，请等待或查看修复状态")
	case errors.Is(e, model.ErrReadingRevision):
		h.error(w, r, 409, "reading_revision_conflict", "阅读状态已变化，请重新读取")
	case errors.Is(e, model.ErrReadingContent):
		h.error(w, r, 409, "reading_content_changed", "文件内容已变化，请重新读取并确认")
	case errors.Is(e, model.ErrReadingConfirmation):
		h.error(w, r, 409, "reading_content_confirmation_required", "请明确确认当前文件版本后再写阅读状态")
	case errors.Is(e, model.ErrReadingIdempotency):
		h.error(w, r, 409, "reading_idempotency_conflict", "幂等键已用于不同请求")
	case errors.Is(e, model.ErrReadingUnknownPages):
		h.error(w, r, 422, "reading_page_count_unknown", "本地总页数未知，不能写页码")
	case errors.Is(e, model.ErrSharedSourcePermission):
		h.error(w, r, 403, "shared_source_requires_admin", "共享来源涉及无编辑权限的书库，请由管理员处理")
	case errors.Is(e, model.ErrForbidden), strings.Contains(e.Error(), "forbidden"):
		h.error(w, r, 403, "forbidden", "没有执行此操作的权限")
	case errors.Is(e, model.ErrPasswordChangeRequired):
		h.error(w, r, 403, "password_change_required", "请先修改本人密码")
	case errors.Is(e, model.ErrLastAdministrator):
		h.error(w, r, 409, "last_administrator", "必须保留至少一个启用的管理员")
	case errors.Is(e, model.ErrAccountConflict):
		h.error(w, r, 409, "account_revision_conflict", "账号已变化，请重新读取")
	case errors.Is(e, model.ErrInput):
		h.error(w, r, 422, "invalid_input", "输入无效")
	case errors.Is(e, model.ErrNotFound):
		h.error(w, r, 404, "not_found", "资源不存在")
	case errors.Is(e, model.ErrDeviceLimit):
		h.error(w, r, 409, "device_limit_reached", "有效设备已达上限，请撤销旧设备后再登录")
	case errors.Is(e, model.ErrDeviceSession):
		w.Header().Set("WWW-Authenticate", `Bearer realm="nyamangaserver-device"`)
		h.error(w, r, 401, "unauthenticated", "设备会话无效或已过期，请重新登录")
	case errors.Is(e, auth.ErrSession):
		h.error(w, r, 401, "unauthenticated", "会话已过期，请重新登录")
	case errors.Is(e, auth.ErrCredentials):
		h.error(w, r, 401, "invalid_credentials", "凭据无效或会话已过期")
	case errors.Is(e, auth.ErrPassword):
		h.error(w, r, 422, "invalid_password", auth.ErrPassword.Error())
	case errors.Is(e, model.ErrInitialized):
		h.error(w, r, 409, "already_initialized", "管理员已完成初始化，请登录")
	case errors.Is(e, model.ErrEnhancementDisabled):
		h.error(w, r, 409, "enhancement_disabled", "此增强功能未启用，请在设置中开启后再明确恢复任务")
	case errors.Is(e, model.ErrConflict):
		h.error(w, r, 409, "revision_conflict", "记录已变化，请重新登录后操作")
	default:
		h.logger.Error("operation failed", "request_id", w.Header().Get("X-Request-ID"), "error", e)
		h.error(w, r, 500, "internal_error", "服务暂时不可用")
	}
}
func (h *Handler) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return h.decodeJSON(w, r, v, false)
}

func (h *Handler) decodeJSON(w http.ResponseWriter, r *http.Request, v any, strictObject bool) bool {
	if media := strings.Split(r.Header.Get("Content-Type"), ";")[0]; media != "application/json" {
		h.error(w, r, 415, "unsupported_media_type", "请使用JSON请求")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		var max *http.MaxBytesError
		if errors.As(readErr, &max) {
			h.error(w, r, 413, "request_too_large", "请求内容过大")
		} else {
			h.error(w, r, 400, "invalid_request", "请求内容无效")
		}
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) == nil {
		typ := reflect.TypeOf(v)
		if typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ.Kind() == reflect.Struct {
			for i := 0; i < typ.NumField(); i++ {
				tag := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
				if (tag == "expected_revision" || tag == "expected_revisions") && !strings.HasSuffix(r.URL.Path, "/scans") {
					value, ok := fields[tag]
					if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
						h.error(w, r, 400, "invalid_request", "缺少必填记录版本")
						return false
					}
				}
			}
		}
	}
	if strictObject && !exactObjectKeys(raw, reflect.TypeOf(v).Elem()) {
		h.error(w, r, 400, "invalid_request", "请求JSON字段无效或重复")
		return false
	}

	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	e := d.Decode(v)
	if e == nil {
		if next := d.Decode(new(any)); next != io.EOF {
			if next != nil {
				e = next
			} else {
				e = errors.New("multiple JSON values")
			}
		}
	}
	if e != nil {
		var max *http.MaxBytesError
		if errors.As(e, &max) {
			h.error(w, r, 413, "request_too_large", "请求内容过大")
		} else {
			h.error(w, r, 400, "invalid_request", "请求JSON或字段无效")
		}
		return false
	}
	return true
}
func exactObjectKeys(raw []byte, target reflect.Type) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, e := d.Token(); e != nil || token != json.Delim('{') {
		return false
	}
	fields := make(map[string]bool, target.NumField())
	for f := range target.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		fields[name] = true
	}
	for d.More() {
		token, e := d.Token()
		name, ok := token.(string)
		if e != nil || !ok || !fields[name] {
			return false
		}
		fields[name] = false
		var value json.RawMessage
		if e = d.Decode(&value); e != nil {
			return false
		}
	}
	return true
}

func (h *Handler) origin(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Origin") != h.c.PublicURL {
		h.error(w, r, 403, "origin_rejected", "请求来源不允许")
		return false
	}
	return true
}
func (h *Handler) limited(w http.ResponseWriter, r *http.Request) bool {
	ip, _, e := net.SplitHostPort(r.RemoteAddr)
	if e != nil {
		ip = r.RemoteAddr
	}
	now := h.svc.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	a, exists := h.attempts[ip]
	if !exists && len(h.attempts) >= 1024 {
		for k, v := range h.attempts {
			if now.Sub(v.start) >= time.Minute {
				delete(h.attempts, k)
			}
		}
		if len(h.attempts) >= 1024 {
			w.Header().Set("Retry-After", "60")
			h.error(w, r, 429, "rate_limited", "请求过于频繁，请稍后重试")
			return false
		}
	}
	if a.start.IsZero() || now.Sub(a.start) >= time.Minute {
		a = attempt{start: now}
	}
	if a.count >= 5 {
		w.Header().Set("Retry-After", "60")
		h.error(w, r, 429, "rate_limited", "请求过于频繁，请稍后重试")
		return false
	}
	a.count++
	h.attempts[ip] = a
	return true
}
func (h *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if headers := r.Header.Values("Authorization"); len(headers) != 0 {
			h.authenticateDevice(w, r, next, headers)
			return
		}
		cookie, e := r.Cookie(h.cookieName())
		if e != nil {
			h.error(w, r, 401, "unauthenticated", "请先登录")
			return
		}
		v, e := h.svc.Session(r.Context(), cookie.Value)
		if e != nil {
			galleryManagement := r.URL.Path == "/api/v1/settings/gallery-highlight/connections" || strings.HasPrefix(r.URL.Path, "/api/v1/settings/gallery-highlight/connections/")
			if galleryManagement && !errors.Is(e, auth.ErrSession) {
				h.galleryFailure(w, r, e)
			} else {
				h.failure(w, r, e)
			}
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if !h.origin(w, r) {
				return
			}
			csrf := auth.CSRF(cookie.Value)
			if subtle.ConstantTimeCompare([]byte(csrf), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
				h.error(w, r, 403, "csrf_rejected", "请求验证失效，请刷新页面")
				return
			}
		}
		if v.Account == nil {
			h.failure(w, r, auth.ErrSession)
			return
		}
		r = r.WithContext(model.WithActor(r.Context(), *v.Account))
		if v.Account.MustChangePassword && !passwordChangeRoute(r.URL.Path) {
			h.failure(w, r, model.ErrPasswordChangeRequired)
			return
		}
		if !h.authorizeRoute(w, r, v.Account) {
			return
		}
		release, admitted := h.healthAdmission(w, r)
		if !admitted {
			return
		}
		defer release()
		if h.instanceTransfers && instanceBulkRoute(r) {
			ctx, cancel := authenticatedBulkContext(r)
			defer cancel()
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, identity{raw: cookie.Value, session: v})))
	})
}
func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	v, e := h.svc.Initialized(r.Context())
	if e != nil {
		h.failure(w, r, e)
		return
	}
	h.json(w, 200, map[string]bool{"initialized": v})
}
func (h *Handler) cookie(w http.ResponseWriter, token string, v model.Session) {
	http.SetCookie(w, &http.Cookie{Name: h.cookieName(), Value: token, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(h.c.PublicURL, "https://"), SameSite: http.SameSiteLaxMode, Expires: time.Unix(v.Expires, 0), MaxAge: 7 * 24 * 3600})
}
func (h *Handler) cookieName() string {
	if h.c.SessionCookieName != "" {
		return h.c.SessionCookieName
	}
	return CookieName
}
func (h *Handler) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: h.cookieName(), Value: "", Path: "/", HttpOnly: true, Secure: strings.HasPrefix(h.c.PublicURL, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
}
func (h *Handler) sessionJSON(w http.ResponseWriter, raw string, v model.Session) {
	h.json(w, 200, map[string]any{"account_id": v.AccountID, "actor": actorResponse(v.Account), "csrf_token": auth.CSRF(raw), "expires_at": time.Unix(v.Expires, 0).UTC().Format(time.RFC3339)})
}
func (h *Handler) setup(w http.ResponseWriter, r *http.Request) {
	if !h.origin(w, r) || !h.limited(w, r) {
		return
	}
	var in struct {
		Token    *string `json:"setup_token"`
		Password *string `json:"password"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	if in.Token == nil || in.Password == nil {
		h.error(w, r, 400, "invalid_request", "缺少必填字段")
		return
	}
	raw, v, e := h.svc.Setup(r.Context(), *in.Token, *in.Password)
	if e != nil {
		h.failure(w, r, e)
		return
	}
	h.cookie(w, raw, v)
	h.sessionJSON(w, raw, v)
}
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if !h.origin(w, r) || !h.limited(w, r) {
		return
	}
	var in struct {
		Username string  `json:"username"`
		Password *string `json:"password"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	if in.Password == nil {
		h.error(w, r, 400, "invalid_request", "缺少必填字段")
		return
	}
	raw, v, e := h.svc.LoginUsername(r.Context(), in.Username, *in.Password)
	if e != nil {
		h.failure(w, r, e)
		return
	}
	h.cookie(w, raw, v)
	h.sessionJSON(w, raw, v)
}
func (h *Handler) session(w http.ResponseWriter, r *http.Request) {
	v := r.Context().Value(sessionKey{}).(identity)
	if v.device != nil {
		h.json(w, 200, map[string]any{"device": v.device, "actor": deviceActorResponse(v.device.Account)})
		return
	}
	h.sessionJSON(w, v.raw, *v.session)
}
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	v := r.Context().Value(sessionKey{}).(identity)
	if v.device != nil {
		if e := h.svc.RevokeDeviceSession(r.Context(), v.device.ID); e != nil {
			h.failure(w, r, e)
			return
		}
		w.WriteHeader(204)
		return
	}
	if e := h.svc.Logout(r.Context(), v.session.Hash); e != nil {
		h.failure(w, r, e)
		return
	}
	h.clearCookie(w)
	w.WriteHeader(204)
}
func (h *Handler) password(w http.ResponseWriter, r *http.Request) {
	if !h.limited(w, r) {
		return
	}
	var in struct {
		Current *string `json:"current_password"`
		Next    *string `json:"new_password"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	v := r.Context().Value(sessionKey{}).(identity)
	if in.Current == nil || in.Next == nil {
		h.error(w, r, 400, "invalid_request", "缺少必填字段")
		return
	}
	a, _ := model.Actor(r.Context())
	if e := h.svc.ChangePassword(r.Context(), a.ID, *in.Current, *in.Next); e != nil {
		h.failure(w, r, e)
		return
	}
	if v.device == nil {
		h.clearCookie(w)
	}
	h.json(w, 200, map[string]bool{"reauthenticate": true})
}
func (h *Handler) roots(w http.ResponseWriter, r *http.Request) {
	out := make([]map[string]string, 0, len(h.c.Roots))
	for _, v := range h.c.Roots {
		out = append(out, map[string]string{"root_key": v.Key, "display_name": v.Name})
	}
	h.json(w, 200, map[string]any{"items": out})
}
func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if e := h.svc.Ready(ctx); e != nil {
		h.json(w, 503, map[string]bool{"ready": false})
		return
	}
	h.json(w, 200, map[string]bool{"ready": true})
}
func (h *Handler) diagnostics(w http.ResponseWriter, r *http.Request) {
	h.json(w, 200, map[string]any{"version": h.build.Version, "commit": h.build.Commit, "schema_version": h.svc.SchemaVersion(), "database_baseline": h.svc.Baseline()})
}
func (h *Handler) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" || p == "admin" || strings.HasPrefix(p, "admin/") || p == "setup" || p == "login" || p == "settings/security" || p == "settings/libraries" || p == "settings/metadata" || p == "series" || p == "version-groups" || strings.HasPrefix(p, "series/") || strings.HasPrefix(p, "version-groups/") || p == "jobs" || strings.HasPrefix(p, "jobs/") || strings.HasPrefix(p, "books/") || p == "works" || strings.HasPrefix(p, "works/") || strings.HasPrefix(p, "editions/") {
		p = "index.html"
	}
	if !fs.ValidPath(p) {
		h.error(w, r, 404, "not_found", "页面不存在")
		return
	}
	if _, e := fs.Stat(h.assets, p); e != nil {
		h.error(w, r, 404, "not_found", "页面不存在")
		return
	}
	if p == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
		data, err := fs.ReadFile(h.assets, p)
		if err != nil {
			h.failure(w, r, err)
			return
		}
		nonce, e := auth.RandomToken(24)
		if e != nil {
			h.failure(w, r, e)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'nonce-"+nonce+"'; style-src-elem 'self' 'nonce-"+nonce+"'; style-src-attr 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		meta := []byte("<meta name=\"csp-nonce\" content=\"" + nonce + "\">")
		if bytes.Contains(data, []byte("<head>")) {
			data = bytes.Replace(data, []byte("<head>"), append([]byte("<head>"), meta...), 1)
		} else {
			data = append(meta, data...)
		}
		http.ServeContent(w, r, p, time.Time{}, bytes.NewReader(data))
		return
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/" + p
	http.FileServer(http.FS(h.assets)).ServeHTTP(w, r2)
}
