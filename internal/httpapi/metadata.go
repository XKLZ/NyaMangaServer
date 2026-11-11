package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"nyamangaserver.local/server/internal/metadata"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"strconv"
	"strings"
	"time"
)

func (h *Handler) WithMetadata(service *metadata.Service, lifecycle context.Context) *Handler {
	h.metadata = service
	if lifecycle != nil {
		h.lifecycle = lifecycle
	}
	p := h.protected
	p.HandleFunc("/api/v1/editions/{id}/overrides", h.method("PATCH", h.overrides))
	p.HandleFunc("/api/v1/books/{id}/overrides", h.method("PATCH", h.overrides))
	p.HandleFunc("/api/v1/metadata-jobs", h.method("POST", h.startMetadata))
	p.HandleFunc("/api/v1/tag-translation-jobs", h.method("POST", h.startTranslation))
	p.HandleFunc("/api/v1/tag-suggestions", h.method("POST", h.tagSuggestions))
	p.HandleFunc("/api/v1/tags", h.method("GET", h.tags))
	p.HandleFunc("/api/v1/tags/{id}/translations/{locale}", h.translation)
	p.HandleFunc("/api/v1/jobs/{id}/pause", h.method("POST", h.controlJob))
	p.HandleFunc("/api/v1/jobs/{id}/resume", h.method("POST", h.controlJob))
	p.HandleFunc("/api/v1/jobs/{id}/retry", h.method("POST", h.retryJob))
	p.HandleFunc("/api/v1/settings/metadata-provider", h.providerSettings)
	p.HandleFunc("/api/v1/settings/provider-credentials", h.method("PUT", h.providerCredentials))
	p.HandleFunc("/api/v1/settings/metadata-provider/test", h.method("POST", h.providerTest))
	p.HandleFunc("/api/v1/settings/tag-translations", h.method("GET", h.translationSettings))
	p.HandleFunc("/api/v1/events", h.method("GET", h.events))
	return h
}
func (h *Handler) metadataFailure(w http.ResponseWriter, r *http.Request, e error) {
	var remote *model.RemoteError
	switch {
	case errors.Is(e, model.ErrLanguageFields):
		h.error(w, r, 400, "conflicting_language_fields", "不能同时提交单语言和多语言字段")
	case errors.Is(e, model.ErrMultipleLanguages):
		h.error(w, r, 409, "multiple_languages_require_array", "当前包含多个语言，请使用languages数组编辑")
	case errors.Is(e, model.ErrDisabled), errors.Is(e, model.ErrTrashed), errors.Is(e, model.ErrBusy), errors.Is(e, model.ErrPreviewExpired):
		h.catalogFailure(w, r, e)
	case errors.Is(e, model.ErrInput):
		h.error(w, r, 422, "invalid_metadata", "字段、网址、选择或设置无效")
	case errors.Is(e, model.ErrNotFound):
		h.error(w, r, 404, "not_found", "资源不存在")
	case errors.Is(e, model.ErrConflict):
		h.error(w, r, 409, "revision_conflict", "记录或任务状态已变化，请刷新后重试")
	case errors.Is(e, model.ErrJobActive), errors.Is(e, model.ErrScanActive):
		h.error(w, r, 409, "job_in_progress", "所选资源已有活动或暂停的任务")
	case errors.Is(e, provider.ErrSecrets):
		h.error(w, r, 503, "provider_secrets_unavailable", "站点凭据文件或密钥不可用，请检查部署配置")
	case errors.As(e, &remote):
		switch remote.Code {
		case "no_sha1":
			h.error(w, r, 422, remote.Code, "没有可用的图片摘要，请按标题搜索或粘贴画廊网址")
			return
		case "provider_settings_changed":
			h.error(w, r, 409, remote.Code, "检索站点已变化，请重新创建查询")
			return
		case "search_limit_reached":
			h.error(w, r, 422, remote.Code, "查询已达到资源上限，请缩小查询范围")
			return
		}
		h.error(w, r, 503, remote.Code, "站点暂不可用或需要处理认证／访问限制，请检查设置")
	default:
		h.logger.Error("metadata operation failed", "request_id", w.Header().Get("X-Request-ID"))
		h.error(w, r, 500, "internal_error", "操作失败，请稍后重试")
	}
}
func (h *Handler) metadataBook(w http.ResponseWriter, r *http.Request) {
	if h.organization != nil {
		h.organizationBook(w, r)
		return
	}
	b, details, e := h.metadata.View(r.Context(), r.PathValue("id"))
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.json(w, 200, struct {
		bookResponse
		Metadata model.MetadataDetails `json:"metadata"`
	}{response(*b).(bookResponse), details})
}
func (h *Handler) overrides(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Expected int64                     `json:"expected_revision"`
		Fields   map[string]model.Override `json:"fields"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	if e := h.metadata.ApplyOverrides(r.Context(), r.PathValue("id"), in.Expected, in.Fields); e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.metadataBook(w, r)
}
func (h *Handler) accepted(w http.ResponseWriter, j *model.Job) {
	w.Header().Set("Location", "/api/v1/jobs/"+j.ID)
	h.json(w, 202, map[string]string{"job_id": j.ID})
}
func (h *Handler) startMetadata(w http.ResponseWriter, r *http.Request) {
	var in metadata.JobInput
	if !h.decode(w, r, &in) {
		return
	}
	j, e := h.metadata.StartMetadata(r.Context(), in)
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.accepted(w, j)
}
func (h *Handler) startTranslation(w http.ResponseWriter, r *http.Request) {
	j, e := h.metadata.StartTranslation(r.Context())
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.accepted(w, j)
}
func (h *Handler) controlJob(w http.ResponseWriter, r *http.Request) {
	action := "pause"
	if strings.HasSuffix(r.URL.Path, "/resume") {
		action = "resume"
	}
	old, e := h.catalog.Job(r.Context(), r.PathValue("id"))
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	if h.health != nil && h.health.service.Owns(r.Context(), old.ID) {
		e = h.health.service.Control(r.Context(), old.ID, action)
	} else if old.Type == "work_recommendation" || old.Type == "series_recommendation" {
		if h.recommendations == nil {
			h.error(w, r, 503, "work_recommendation_unavailable", "推荐功能未配置")
			return
		}
		e = h.recommendations.Control(r.Context(), old.ID, action)
	} else if h.maintenance != nil && (old.Type == "maintenance" || old.Type == "cover_rebuild") {
		e = h.maintenance.Control(r.Context(), old.ID, action)
	} else {
		e = h.metadata.Control(r.Context(), old.ID, action)
	}
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	j, e := h.catalog.Job(r.Context(), r.PathValue("id"))
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.json(w, 202, j)
}
func (h *Handler) retryJob(w http.ResponseWriter, r *http.Request) {
	var in metadata.RetryInput
	if !h.decode(w, r, &in) {
		return
	}
	var j *model.Job
	old, e := h.catalog.Job(r.Context(), r.PathValue("id"))
	if e != nil {
		h.adminFailure(w, r, e)
		return
	}
	if old.Type == "work_recommendation" || old.Type == "series_recommendation" {
		if h.recommendations == nil || len(in.URLs) > 0 {
			h.recommendationFailure(w, r, model.ErrInput)
			return
		}
		j, e = h.recommendations.Retry(r.Context(), old.ID, in.Ordinals)
	} else if h.maintenance != nil && (old.Type == "maintenance" || old.Type == "cover_rebuild") {
		j, e = h.maintenance.Retry(r.Context(), old.ID)
	} else {
		j, e = h.metadata.Retry(r.Context(), old.ID, in)
	}
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.accepted(w, j)
}
func (h *Handler) tags(w http.ResponseWriter, r *http.Request) {
	locale := r.URL.Query().Get("locale")
	if locale == "" {
		locale = "zh-CN"
	}
	tags, e := h.metadata.Tags(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("namespace"), locale)
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.json(w, 200, map[string]any{"items": tags})
}
func (h *Handler) translation(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PUT" && r.Method != "DELETE" {
		w.Header().Set("Allow", "PUT, DELETE")
		h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
		return
	}
	var in struct {
		Expected int64   `json:"expected_revision"`
		Text     *string `json:"text,omitempty"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	if r.Method == "PUT" && in.Text == nil || r.Method == "DELETE" && in.Text != nil {
		h.metadataFailure(w, r, model.ErrInput)
		return
	}
	if e := h.metadata.Translate(r.Context(), r.PathValue("id"), r.PathValue("locale"), in.Expected, in.Text); e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.json(w, 200, map[string]bool{"updated": true})
}
func (h *Handler) providerSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		v, e := h.metadata.Settings(r.Context())
		if e != nil {
			h.metadataFailure(w, r, e)
			return
		}
		h.json(w, 200, v)
	case "PATCH":
		var in provider.SettingPatch
		if !h.decode(w, r, &in) {
			return
		}
		v, e := h.metadata.Configure(r.Context(), in)
		if e != nil {
			h.metadataFailure(w, r, e)
			return
		}
		h.json(w, 200, v)
	default:
		w.Header().Set("Allow", "GET, PATCH")
		h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
	}
}
func (h *Handler) providerCredentials(w http.ResponseWriter, r *http.Request) {
	var in provider.CredentialPatch
	if !h.decode(w, r, &in) {
		return
	}
	v, e := h.metadata.Credentials(r.Context(), in)
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) providerTest(w http.ResponseWriter, r *http.Request) {
	if e := h.metadata.Test(r.Context()); e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.json(w, 200, map[string]bool{"reachable": true})
}
func (h *Handler) translationSettings(w http.ResponseWriter, r *http.Request) {
	v, e := h.metadata.TranslationVersion(r.Context())
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.json(w, 200, map[string]any{"active": v, "source": "https://github.com/EhTagTranslation/Database", "locale": "zh-CN"})
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("account_id") {
		h.error(w, r, 400, "invalid_request", "事件身份仅来自当前会话")
		return
	}
	after := int64(0)
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		var e error
		after, e = strconv.ParseInt(raw, 10, 64)
		if e != nil || after < 0 {
			h.error(w, r, 400, "invalid_request", "事件游标无效")
			return
		}
	}
	h.mu.Lock()
	if h.streams >= 16 {
		h.mu.Unlock()
		h.error(w, r, 429, "stream_limit", "通知连接过多")
		return
	}
	h.streams++
	h.mu.Unlock()
	defer func() { h.mu.Lock(); h.streams--; h.mu.Unlock() }()
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	write := func(name string, event model.Event) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
		data, e := json.Marshal(event)
		if e != nil {
			return false
		}
		if _, e = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, name, data); e != nil {
			return false
		}
		return rc.Flush() == nil
	}
	events, high, gap, e := h.metadata.EventsForAccount(r.Context(), r.Context().Value(sessionKey{}).(identity).session.AccountID, after)
	if e != nil {
		return
	}
	if after == 0 || gap {
		after = high
		if !write("resync_required", model.Event{ID: high, Type: "resync_required"}) {
			return
		}
	} else {
		for _, event := range events {
			if !write("change", event) {
				return
			}
			after = event.ID
		}
		// A partial visible batch exhausted this read snapshot, including hidden rows.
		if len(events) < 200 {
			after = high
		}
	}
	ticker := time.NewTicker(h.streamInterval)
	defer ticker.Stop()
	heartbeat := time.NewTicker(h.heartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.lifecycle.Done():
			return
		case <-heartbeat.C:
			_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, e = fmt.Fprint(w, ": heartbeat\n\n"); e != nil || rc.Flush() != nil {
				return
			}
		case <-ticker.C:
			who := r.Context().Value(sessionKey{}).(identity)
			currentSession, sessionErr := h.svc.Session(r.Context(), who.raw)
			if sessionErr != nil || currentSession.Account == nil || currentSession.Account.MustChangePassword {
				return
			}
			r = r.WithContext(model.WithActor(r.Context(), *currentSession.Account))
			events, high, gap, e = h.metadata.EventsForAccount(r.Context(), currentSession.AccountID, after)
			if e != nil {
				return
			}
			if gap {
				after = high
				if !write("resync_required", model.Event{ID: high, Type: "resync_required"}) {
					return
				}
				continue
			}
			for _, event := range events {
				if !write("change", event) {
					return
				}
				after = event.ID
			}
			if len(events) < 200 {
				after = high
			}
		}
	}
}

func (h *Handler) tagSuggestions(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Has("account_id") {
		h.error(w, r, 400, "invalid_tag_suggestions", "标签身份仅来自当前会话")
		return
	}
	var in model.TagSuggestionInput
	if !h.decode(w, r, &in) {
		return
	}
	out, e := h.metadata.TagSuggestions(tagReadContext(r), in)
	if errors.Is(e, model.ErrSnapshot) {
		h.error(w, r, 409, "tag_suggestions_snapshot_expired", "标签候选已更新，请重新查询第一页")
		return
	}
	if errors.Is(e, model.ErrInput) {
		h.error(w, r, 400, "invalid_tag_suggestions", "标签查询、排除或分页参数无效")
		return
	}
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
