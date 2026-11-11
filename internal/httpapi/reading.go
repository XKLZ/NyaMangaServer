package httpapi

import (
	"errors"
	"net/http"
	"nyamangaserver.local/server/internal/model"
)

func (h *Handler) readingRoutes(p *http.ServeMux) {
	for _, prefix := range []string{"editions", "books"} {
		p.HandleFunc("/api/v1/"+prefix+"/{id}/reading-state", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case "GET":
				out, e := h.userstate.Reading(r.Context(), r.PathValue("id"))
				if e != nil {
					h.readingFailure(w, r, e)
					return
				}
				h.json(w, 200, out)
			case "PUT":
				var in model.ReadingWrite
				if !h.decode(w, r, &in) {
					return
				}
				out, e := h.userstate.WriteReading(r.Context(), r.PathValue("id"), in)
				if e != nil {
					h.readingFailure(w, r, e)
					return
				}
				h.json(w, 200, out)
			default:
				h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
			}
		})
		p.HandleFunc("/api/v1/"+prefix+"/{id}/reading-sessions", h.method("POST", func(w http.ResponseWriter, r *http.Request) {
			var in model.ReadingSessionStart
			if !h.decode(w, r, &in) {
				return
			}
			out, e := h.userstate.StartReading(r.Context(), r.PathValue("id"), in)
			if e != nil {
				h.readingFailure(w, r, e)
				return
			}
			h.json(w, 200, out)
		}))
	}
}

func (h *Handler) readingFailure(w http.ResponseWriter, r *http.Request, e error) {
	code, message := "", ""
	switch {
	case errors.Is(e, model.ErrReadingRevision):
		code = "reading_revision_conflict"
		message = "阅读状态已变化，请保留草稿并由用户处理冲突"
	case errors.Is(e, model.ErrReadingContent):
		code = "reading_content_changed"
		message = "文件内容已变化，请重新读取并明确确认"
	case errors.Is(e, model.ErrReadingConfirmation):
		code = "reading_content_confirmation_required"
		message = "请明确确认新内容版本；确认会清除旧页码和完成状态"
	case errors.Is(e, model.ErrReadingIdempotency):
		code = "reading_idempotency_conflict"
		message = "幂等键已用于不同请求"
	}
	if code == "" {
		h.failure(w, r, e)
		return
	}
	current, readErr := h.userstate.Reading(r.Context(), r.PathValue("id"))
	if readErr != nil {
		h.failure(w, r, readErr)
		return
	}
	h.json(w, 409, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": w.Header().Get("X-Request-ID")}, "current_state": current})
}
