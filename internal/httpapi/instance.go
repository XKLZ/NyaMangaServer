package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"os"
	"strings"
	"syscall"
	"time"
)

func (h *Handler) WithInstance(s *maintenance.InstanceService) *Handler {
	h.instanceTransfers = true
	p := h.protected
	p.HandleFunc("/api/v1/maintenance/backups", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			page, size, e := pagination(r)
			if e != nil {
				h.failure(w, r, e)
				return
			}
			key, e := instanceHistoryKey(r)
			if e != nil {
				h.instanceResponse(w, r, 0, nil, e)
				return
			}
			v, e := s.Operations(r.Context(), "backup", page, size, key...)
			h.instanceResponse(w, r, 200, v, e)
		case "POST":
			var in struct {
				RequestKey string `json:"request_key"`
			}
			if !h.decode(w, r, &in) {
				return
			}
			v, e := s.CreateBackup(r.Context(), model.InstanceRequest{RequestKey: in.RequestKey})
			h.instanceResponse(w, r, 202, v, e)
		default:
			h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
		}
	})
	p.HandleFunc("/api/v1/maintenance/backups/{operation}", h.method("GET", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Operation(r.Context(), r.PathValue("operation"))
		if e == nil && v.Kind != "backup" {
			e = model.ErrNotFound
		}
		h.instanceResponse(w, r, 200, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/backups/{operation}/cancel", h.method("POST", func(w http.ResponseWriter, r *http.Request) {
		var in model.InstanceRequest
		if !h.decode(w, r, &in) {
			return
		}
		v, e := s.Cancel(r.Context(), r.PathValue("operation"), in.Expected)
		h.instanceResponse(w, r, 202, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/backups/{operation}/download", h.method("GET", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := instanceTransfer(w, r)
		defer cancel()
		f, v, e := s.Download(ctx, r.PathValue("operation"))
		if e != nil {
			h.instanceResponse(w, r, 0, nil, e)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=instance-%s.zip", v.ID))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("ETag", `"`+v.Digest+`"`)
		http.ServeContent(w, r.WithContext(ctx), "instance.zip", time.Time{}, f)
	}))
	p.HandleFunc("/api/v1/maintenance/restores", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			page, size, e := pagination(r)
			if e != nil {
				h.failure(w, r, e)
				return
			}
			key, e := instanceHistoryKey(r)
			if e != nil {
				h.instanceResponse(w, r, 0, nil, e)
				return
			}
			v, e := s.Operations(r.Context(), "restore", page, size, key...)
			h.instanceResponse(w, r, 200, v, e)
		case "POST":
			if r.Header.Get("Content-Type") != "application/zip" {
				h.error(w, r, 415, "unsupported_media_type", "请上传整实例ZIP包")
				return
			}
			ctx, cancel := instanceTransfer(w, r)
			defer cancel()
			r.Body = http.MaxBytesReader(w, r.Body, maintenance.DefaultInstanceBudget().ArchiveBytes)
			v, e := s.UploadRestore(ctx, r.Header.Get("Idempotency-Key"), r.Body)
			h.instanceResponse(w, r, 201, v, e)
		default:
			h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
		}
	})
	p.HandleFunc("/api/v1/maintenance/restores/{operation}", h.method("GET", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.RestoreDetail(r.Context(), r.PathValue("operation"))
		h.instanceResponse(w, r, 200, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/restores/{operation}/validate", h.method("POST", func(w http.ResponseWriter, r *http.Request) {
		var in model.InstanceRequest
		if !h.decode(w, r, &in) {
			return
		}
		v, e := s.ValidateRestore(r.Context(), r.PathValue("operation"), in.Expected)
		h.instanceResponse(w, r, 202, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/restores/{operation}/preview", h.method("POST", func(w http.ResponseWriter, r *http.Request) {
		var in maintenance.InstanceRestoreInput
		if !h.decode(w, r, &in) {
			return
		}
		ctx, cancel := instanceTransfer(w, r)
		defer cancel()
		v, e := s.PreviewRestore(ctx, r.PathValue("operation"), in)
		h.instanceResponse(w, r, 200, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/restores/{operation}/confirm", h.method("POST", func(w http.ResponseWriter, r *http.Request) {
		var in model.InstanceRequest
		if !h.decode(w, r, &in) {
			return
		}
		ctx, cancel := instanceTransfer(w, r)
		defer cancel()
		v, e := s.ConfirmRestore(ctx, r.PathValue("operation"), in)
		h.instanceResponse(w, r, 202, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/restores/{operation}/abandon", h.method("POST", func(w http.ResponseWriter, r *http.Request) {
		var in model.InstanceRequest
		if !h.decode(w, r, &in) {
			return
		}
		v, e := s.AbandonRestore(r.Context(), r.PathValue("operation"), in.Expected)
		h.instanceResponse(w, r, 200, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/instance-activation", h.method("GET", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Activation(r.Context())
		h.instanceResponse(w, r, 200, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/restores/{operation}/rollback", h.method("POST", func(w http.ResponseWriter, r *http.Request) {
		var in model.InstanceRequest
		if !h.decode(w, r, &in) {
			return
		}
		v, e := s.RequestRollback(r.Context(), r.PathValue("operation"), in)
		h.instanceResponse(w, r, 202, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/instance-state", h.method("GET", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.State(r.Context())
		h.instanceResponse(w, r, 200, v, e)
	}))
	p.HandleFunc("/api/v1/maintenance/instance-state/release", h.method("POST", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Expected    int64 `json:"expected_revision"`
			Acknowledge bool  `json:"allow_automatic_background"`
		}
		if !h.decode(w, r, &in) {
			return
		}
		if !in.Acknowledge {
			h.failure(w, r, model.ErrInput)
			return
		}
		v, e := s.ReleaseHold(r.Context(), in.Expected)
		h.instanceResponse(w, r, 200, v, e)
	}))
	return h
}

func instanceHistoryKey(r *http.Request) ([]string, error) {
	query, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return nil, model.ErrInput
	}
	keys, exists := query["request_key"]
	if !exists {
		return nil, nil
	}
	if len(keys) != 1 || len(keys[0]) < 8 || len(keys[0]) > 128 {
		return nil, model.ErrInput
	}
	return keys, nil
}

type requestBaseContextKey struct{}

// Retain authenticated values while replacing only our ordinary route deadline.
// The actual client context (including any shorter upstream deadline) still wins.
func authenticatedBulkContext(r *http.Request) (context.Context, context.CancelFunc) {
	base, ok := r.Context().Value(requestBaseContextKey{}).(context.Context)
	if !ok {
		return context.WithCancel(r.Context())
	}
	deadline := time.Now().Add(30 * time.Minute)
	if d, ok := base.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	ctx, cancel := context.WithDeadline(context.WithoutCancel(r.Context()), deadline)
	stop := context.AfterFunc(base, cancel)
	if base.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

func instanceBulkRoute(r *http.Request) bool {
	path := r.URL.Path
	if r.Method == "POST" && path == "/api/v1/maintenance/restores" {
		return true
	}
	if r.Method == "GET" && strings.HasPrefix(path, "/api/v1/maintenance/backups/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/maintenance/backups/"), "/")
		return len(parts) == 2 && parts[0] != "" && parts[1] == "download"
	}
	if r.Method == "POST" && strings.HasPrefix(path, "/api/v1/maintenance/restores/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/maintenance/restores/"), "/")
		return len(parts) == 2 && parts[0] != "" && (parts[1] == "preview" || parts[1] == "confirm")
	}
	return false
}

// Override only authenticated bulk-transfer/validation requests, with a finite
// total deadline and independent archive/expanded/file/count budgets. Ordinary
// server timeouts remain 15s/30s; unauthenticated clients never reach this code.
func instanceTransfer(w http.ResponseWriter, r *http.Request) (context.Context, context.CancelFunc) {
	deadline := time.Now().Add(30 * time.Minute)
	if inherited, ok := r.Context().Deadline(); ok && inherited.Before(deadline) {
		deadline = inherited
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(deadline)
	_ = controller.SetWriteDeadline(deadline)
	return context.WithDeadline(r.Context(), deadline)
}
func (h *Handler) instanceResponse(w http.ResponseWriter, r *http.Request, status int, v any, e error) {
	if e == nil {
		h.json(w, status, v)
		return
	}
	switch {
	case errors.Is(e, model.ErrInstancePackage):
		h.error(w, r, 422, "invalid_instance_package", "备份包结构、摘要、引用或预算无效")
	case errors.Is(e, model.ErrInstanceBuild):
		h.error(w, r, 409, "instance_build_mismatch", "需要完全相同的应用构建、格式和迁移账本")
	case errors.Is(e, model.ErrInstanceMapping):
		h.error(w, r, 422, "instance_mapping_invalid", "根目录映射或文件对应检查失败；可选择暂不关联")
	case errors.Is(e, model.ErrInstanceRecovery), errors.Is(e, provider.ErrSecrets):
		h.error(w, r, 409, "instance_recovery_required", "恢复保护或凭据配置需要检查，请查看离线状态")
	case healthMaintenanceError(e):
		h.error(w, r, 503, "maintenance_active", "全局维护保护中")
	case errors.Is(e, model.ErrConflict), errors.Is(e, model.ErrBusy):
		h.error(w, r, 409, "revision_conflict", "目标或操作已变化，请重新预览")
	case errors.Is(e, model.ErrNotFound), errors.Is(e, os.ErrNotExist):
		h.error(w, r, 404, "not_found", "操作或所需材料不存在")
	case errors.Is(e, model.ErrInput):
		h.error(w, r, 422, "invalid_instance_request", "请求或确认无效")
	case errors.Is(e, syscall.ENOSPC):
		h.error(w, r, 507, "storage_full", "保存空间不足，目标未完成替换")
	case errors.Is(e, os.ErrPermission):
		h.error(w, r, 403, "permission_denied", "目标数据或明确配置的秘密位置不可写")
	case errors.Is(e, context.Canceled), errors.Is(e, context.DeadlineExceeded):
		h.error(w, r, 504, "request_timeout", "传输或验证已停止，请查看持久操作状态")
	default:
		h.logger.Error("instance operation failed", "reason", "instance_operation_failed")
		h.error(w, r, 500, "instance_operation_failed", "操作停止，请查看持久状态及检查点")
	}
}
