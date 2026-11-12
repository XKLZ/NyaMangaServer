package httpapi

import (
	"context"
	"errors"
	"net/http"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/model"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

type healthHTTP struct {
	service *maintenance.HealthService
	active  atomic.Int64
}

func (h *Handler) WithHealth(s *maintenance.HealthService) *Handler {
	h.health = &healthHTTP{service: s}
	p := h.protected
	p.HandleFunc("/api/v1/maintenance/health-checks", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			var in model.HealthCheckInput
			if !h.decode(w, r, &in) {
				return
			}
			v, e := s.Create(r.Context(), in)
			h.healthResponse(w, r, 202, v, e)
		case "GET":
			page, size, err := pagination(r)
			if err != nil {
				h.failure(w, r, model.ErrInput)
				return
			}
			v, e := s.Checks(r.Context(), page, size)
			h.healthResponse(w, r, 200, v, e)
		default:
			h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
		}
	})
	p.HandleFunc("/api/v1/maintenance/health-checks/{check}", h.method("GET", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Check(r.Context(), r.PathValue("check"))
		h.healthResponse(w, r, 200, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/health-checks/{check}/findings", h.method("GET", func(w http.ResponseWriter, r *http.Request) {
		page, size, err := pagination(r)
		if err != nil {
			h.failure(w, r, model.ErrInput)
			return
		}
		v, e := s.Findings(r.Context(), r.PathValue("check"), r.URL.Query().Get("status"), r.URL.Query().Get("category"), page, size)
		h.healthResponse(w, r, 200, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/health-repair-plans", h.method("POST", func(w http.ResponseWriter, r *http.Request) {
		var in model.HealthPlanInput
		if !h.decode(w, r, &in) {
			return
		}
		v, e := s.Plan(r.Context(), in)
		h.healthResponse(w, r, 200, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/health-repair-plans/{plan}", h.method("GET", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.PlanDetail(r.Context(), r.PathValue("plan"))
		h.healthResponse(w, r, 200, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/health-repair-plans/{plan}/execute", h.method("POST", func(w http.ResponseWriter, r *http.Request) {
		var in model.HealthExecuteInput
		if !h.decode(w, r, &in) {
			return
		}
		v, e := s.Execute(r.Context(), r.PathValue("plan"), in)
		h.healthResponse(w, r, 202, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/health-repairs/{repair}", h.method("GET", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Repair(r.Context(), r.PathValue("repair"))
		h.healthResponse(w, r, 200, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/health-repairs/{repair}/rollback", h.method("POST", func(w http.ResponseWriter, r *http.Request) {
		var in model.HealthExecuteInput
		if !h.decode(w, r, &in) {
			return
		}
		v, e := s.Rollback(r.Context(), r.PathValue("repair"), in)
		h.healthResponse(w, r, 202, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/health-state", h.method("GET", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.State(r.Context())
		h.healthResponse(w, r, 200, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/health-state/recover", h.method("POST", func(w http.ResponseWriter, r *http.Request) {
		var in model.HealthExecuteInput
		if !h.decode(w, r, &in) {
			return
		}
		v, e := s.RecoverProtection(r.Context(), in)
		h.healthResponse(w, r, 200, v, e)
	}))
	return h
}
func (h *Handler) healthResponse(w http.ResponseWriter, r *http.Request, status int, v any, e error) {
	if e != nil {
		switch {
		case errors.Is(e, model.ErrHealthStale):
			h.error(w, r, 409, "health_result_stale", "检查或预览已过期，请重新检查")
		case healthMaintenanceError(e):
			h.error(w, r, 503, "maintenance_active", "全局维护中，请等待或查看修复状态")
		case errors.Is(e, model.ErrHealthRecovery):
			h.error(w, r, 409, "health_recovery_required", "操作已停止，请查看检查点并选择继续或回退")
		case errors.Is(e, model.ErrInput):
			h.error(w, r, 422, "invalid_health_request", "检查范围、选择或确认无效")
		case errors.Is(e, model.ErrNotFound), errors.Is(e, os.ErrNotExist):
			h.error(w, r, 404, "not_found", "检查记录或所需材料不存在")
		case errors.Is(e, model.ErrConflict), errors.Is(e, model.ErrBusy):
			h.error(w, r, 409, "revision_conflict", "记录或维护状态已变化，请刷新后重试")
		case errors.Is(e, os.ErrPermission):
			h.error(w, r, 403, "permission_denied", "没有读取或保存健康检查材料的权限")
		case errors.Is(e, syscall.ENOSPC):
			h.error(w, r, 507, "storage_full", "应用存储空间不足")
		case errors.Is(e, context.DeadlineExceeded):
			h.error(w, r, 504, "request_timeout", "操作超时，请查看持久任务状态")
		default:
			h.logger.Error("health request failed", "request_id", w.Header().Get("X-Request-ID"), "reason", "health_operation_failed")
			h.error(w, r, 500, "health_operation_failed", "操作已停止，请查看任务状态和检查点")
		}
		return
	}
	h.json(w, status, v)
}
func (h *Handler) healthAdmission(w http.ResponseWriter, r *http.Request) (func(), bool) {
	if h.health == nil || !strings.HasPrefix(r.URL.Path, "/api/v1/") || r.Method == "GET" || r.Method == "HEAD" {
		return func() {}, true
	}
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/v1/maintenance/backups") || strings.HasPrefix(path, "/api/v1/maintenance/restores") || strings.HasPrefix(path, "/api/v1/maintenance/instance-") {
		return func() {}, true
	}
	if r.Method == "POST" {
		switch path {
		case "/api/v1/books/search", "/api/v1/editions/search", "/api/v1/admin/books/search", "/api/v1/admin/editions/search", "/api/v1/tag-suggestions":
			return func() {}, true
		}
	}
	if strings.HasPrefix(path, "/api/v1/maintenance/health-") || path == "/api/v1/auth/logout" && r.Header.Get("Authorization") == "" {
		return func() {}, true
	}
	if r.Method == "POST" && strings.HasPrefix(path, "/api/v1/jobs/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/jobs/"), "/")
		if len(parts) == 2 && (parts[1] == "pause" || parts[1] == "cancel" || parts[1] == "resume" && h.health.service.Owns(r.Context(), parts[0])) {
			return func() {}, true
		}
	}

	h.health.active.Add(1)
	release := func() { h.health.active.Add(-1) }
	if e := h.health.service.CheckWriteAllowed(r.Context()); e != nil {
		release()
		h.healthResponse(w, r, 0, nil, model.ErrHealthMaintenance)
		return func() {}, false
	}
	return release, true
}
func (h *Handler) DrainHealthHTTP(ctx context.Context) error {
	for h.health != nil && h.health.active.Load() != 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	return nil
}

func healthMaintenanceError(e error) bool {
	var coded interface{ Code() int }
	return errors.Is(e, model.ErrHealthMaintenance) || errors.As(e, &coded) && coded.Code() == 1811 && strings.Contains(e.Error(), "maintenance_active")
}
