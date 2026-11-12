package httpapi

import (
	"context"
	"errors"
	"net/http"
	"nyamangaserver.local/server/internal/model"
)

func tagReadContext(r *http.Request) context.Context {
	return model.WithTagAccount(r.Context(), accountID(r))
}
func (h *Handler) tagFollow(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PUT" && r.Method != "DELETE" {
		w.Header().Set("Allow", "PUT, DELETE")
		h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
		return
	}
	if len(r.URL.Query()) != 0 {
		h.error(w, r, 400, "invalid_tag_follow", "关注接口不接受查询参数")
		return
	}
	var in struct {
		Expected *int64 `json:"expected_revision"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	if in.Expected == nil {
		h.error(w, r, 400, "invalid_tag_follow", "关注版本无效")
		return
	}
	out, e := h.tagCatalog.Follow(r.Context(), accountID(r), r.PathValue("id"), *in.Expected, r.Method == "PUT")
	switch {
	case errors.Is(e, model.ErrInput):
		h.error(w, r, 400, "invalid_tag_follow", "关注版本或标签参数无效")
	case errors.Is(e, model.ErrTagFollowConflict):
		h.error(w, r, 409, "tag_follow_revision_conflict", "关注已变化，请重新读取并核对，不要自动提升版本")
	case errors.Is(e, model.ErrNotFound):
		h.error(w, r, 404, "not_found", "标签不存在")
	case e != nil:
		h.failure(w, r, e)
	default:
		h.json(w, 200, out)
	}
}
