package httpapi

import (
	"context"
	"errors"
	"net/http"
	"nyamangaserver.local/server/internal/enhancements"
	"nyamangaserver.local/server/internal/model"
	"runtime"
	"strings"
	"time"
)

type serviceDatabase interface {
	ServiceDatabase(context.Context) (int, string, error)
}

func (h *Handler) WithEnhancements(s *enhancements.Service, db serviceDatabase) *Handler {
	h.protected.HandleFunc("/api/v1/settings/enhancements", func(w http.ResponseWriter, r *http.Request) {
		var v model.Enhancements
		var e error
		status := 200
		switch r.Method {
		case "GET":
			v, e = s.Read(r.Context())
		case "PATCH":
			var in model.EnhancementPatch
			if !h.decode(w, r, &in) {
				return
			}
			var pending bool
			v, pending, e = s.Change(r.Context(), in)
			if pending {
				status = 202
			}
		default:
			w.Header().Set("Allow", "GET, PATCH")
			h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
			return
		}
		if e != nil {
			h.enhancementFailure(w, r, e)
			return
		}
		h.json(w, status, v)
	})
	h.protected.HandleFunc("/api/v1/settings/service", h.method("GET", func(w http.ResponseWriter, r *http.Request) {
		schema, baseline, e := db.ServiceDatabase(r.Context())
		if e != nil {
			h.error(w, r, 503, "service_state_unavailable", "服务状态暂不可用")
			return
		}
		version, commit := strings.TrimSpace(h.build.Version), strings.TrimSpace(h.build.Commit)
		if version == "" {
			version = "unknown"
		}
		if commit == "" {
			commit = "unknown"
		}
		h.json(w, 200, model.ServiceInfo{Version: version, Commit: commit, StartedAt: h.startedAt.UTC(), UptimeSeconds: max(0, int64(time.Since(h.startedAt).Seconds())), OS: runtime.GOOS, Architecture: runtime.GOARCH, Schema: schema, Baseline: baseline, State: "ready"})
	}))
	return h
}
func (h *Handler) enhancementFailure(w http.ResponseWriter, r *http.Request, e error) {
	switch {
	case errors.Is(e, model.ErrInput):
		h.error(w, r, 400, "invalid_enhancement", "功能、版本或切换参数无效")
	case errors.Is(e, model.ErrEnhancementImpact):
		h.error(w, r, 409, "enhancement_impact_changed", "任务影响已变化，请重新核对并确认暂停")
	case errors.Is(e, model.ErrEnhancementTransition):
		h.error(w, r, 409, "enhancement_transition_conflict", "功能正在切换，或重试的切换身份已变化")
	case errors.Is(e, model.ErrConflict):
		h.error(w, r, 409, "enhancement_revision_conflict", "增强设置已变化，请重新读取版本")
	default:
		h.error(w, r, 503, "enhancement_storage_unavailable", "增强设置存储暂不可用，请重新查询持久状态")
	}
}
