package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
)

func (h *Handler) WithRecommendations(service *rec.Service) *Handler {
	h.recommendations = service
	p := h.protected
	p.HandleFunc("/api/v1/series-discovery-jobs", h.method("POST", h.startSeriesDiscovery))
	p.HandleFunc("/api/v1/series-discovery-jobs/{id}", h.method("GET", h.seriesStatus))
	p.HandleFunc("/api/v1/series-discovery-jobs/{id}/pending", h.method("GET", h.seriesPending))
	p.HandleFunc("/api/v1/series-proposals", h.method("GET", h.seriesProposals))
	p.HandleFunc("/api/v1/series-proposals/{id}", h.method("GET", h.seriesProposal))
	p.HandleFunc("/api/v1/series-proposals/{id}/members", h.method("GET", h.seriesMembers))
	p.HandleFunc("/api/v1/series-proposals/{id}/resolve", h.method("POST", h.seriesResolve))
	p.HandleFunc("/api/v1/series-proposals/{id}/{action}", h.method("POST", h.seriesDecision))
	p.HandleFunc("/api/v1/work-discovery-jobs", h.method("POST", h.startWorkDiscovery))
	p.HandleFunc("/api/v1/work-discovery-jobs/{id}", h.method("GET", h.workDiscoveryStatus))
	p.HandleFunc("/api/v1/work-proposals", h.method("GET", h.workProposals))
	p.HandleFunc("/api/v1/work-proposals/{id}", h.method("GET", h.workProposal))
	p.HandleFunc("/api/v1/work-proposals/{id}/alternatives", h.method("GET", h.workProposalAlternatives))
	p.HandleFunc("/api/v1/work-proposals/{id}/resolve", h.method("POST", h.workProposalResolve))
	p.HandleFunc("/api/v1/work-proposals/{id}/{action}", h.method("POST", h.workProposalDecision))

	p.HandleFunc("/api/v1/settings/work-recommendations", h.method("GET", h.recommendationCapabilities))
	p.HandleFunc("/api/v1/work-recommendation-jobs", h.method("POST", h.startWorkRecommendations))
	p.HandleFunc("/api/v1/work-recommendations", h.method("GET", h.workRecommendations))
	p.HandleFunc("/api/v1/work-recommendations/{id}/feedback", h.method("POST", h.recommendationFeedback))
	p.HandleFunc("/api/v1/work-recommendations/{id}/preview", h.method("POST", h.recommendationPreview))
	p.HandleFunc("/api/v1/work-recommendations/{id}/accept", h.method("POST", h.acceptRecommendation))
	p.HandleFunc("/api/v1/work-recommendation-jobs/{id}/misses", h.recommendationMissRoute)
	p.HandleFunc("/api/v1/work-recommendation-jobs/{id}/feedback-export", h.method("GET", h.exportRecommendationFeedback))
	return h
}
func (h *Handler) recommendationFailure(w http.ResponseWriter, r *http.Request, e error) {
	switch {
	case errors.Is(e, rec.ErrUnavailable), errors.Is(e, rec.ErrWorker):
		h.error(w, r, 503, "work_recommendation_unavailable", "同作品推荐运行环境不可用；其他管理功能仍可使用")
	case errors.Is(e, model.ErrCapacity):
		h.error(w, r, 409, "recommendation_pool_capacity", "本次操作超出推荐资源预算或作品成员上限，请减少成员或查看任务诊断；不会静默截断")
	case errors.Is(e, model.ErrJobActive):
		h.error(w, r, 409, "job_in_progress", "所选版本已有活动或暂停的推荐任务")
	case errors.Is(e, rec.ErrSelection):
		h.error(w, r, 409, "proposal_selection_requires_resolve", "成员选择已变化，请先加载更新后的组成方案和保留作品")
	case errors.Is(e, rec.ErrStale):
		h.error(w, r, 410, "work_recommendation_stale", "版本输入或目标作品已变化，请重新生成推荐")
	case errors.Is(e, rec.ErrAlreadyAccepted):
		h.error(w, r, 409, "recommendation_already_accepted", "该建议已经采纳；后续整理请使用作品管理")
	case errors.Is(e, model.ErrInput):
		h.error(w, r, 400, "invalid_work_recommendation", "推荐目标、反馈或分页参数无效")
	default:
		h.organizationFailure(w, r, e)
	}
}
func (h *Handler) recommendationCapabilities(w http.ResponseWriter, r *http.Request) {
	h.json(w, 200, h.recommendations.Capabilities(r.Context()))
}
func (h *Handler) startWorkRecommendations(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []string `json:"edition_ids"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	j, e := h.recommendations.StartJob(r.Context(), in.IDs)
	if e != nil {
		h.recommendationFailure(w, r, e)
		return
	}
	h.accepted(w, j)
}
func (h *Handler) workRecommendations(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.recommendationFailure(w, r, model.ErrInput)
		return
	}
	if r.URL.Query().Get("page_size") == "" {
		size = 20
	}
	q := r.URL.Query()
	v, e := h.recommendations.Page(r.Context(), q.Get("job_id"), q.Get("edition_id"), q.Get("tier"), page, size)
	if e != nil {
		h.recommendationFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) recommendationFeedback(w http.ResponseWriter, r *http.Request) {
	var in rec.Feedback
	if !h.decode(w, r, &in) {
		return
	}
	if e := h.recommendations.Feedback(r.Context(), accountID(r), r.PathValue("id"), in); e != nil {
		h.recommendationFailure(w, r, e)
		return
	}
	h.json(w, 200, map[string]bool{"saved": true})
}
func (h *Handler) recommendationPreview(w http.ResponseWriter, r *http.Request) {
	// Initial preview may obtain current aggregate revisions. Existing organization
	// mutations keep their required revision decoding; this endpoint has its own DTO.
	var fields map[string]json.RawMessage
	if !h.decode(w, r, &fields) {
		return
	}
	if raw, ok := fields["expected_revisions"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		h.recommendationFailure(w, r, model.ErrInput)
		return
	}
	raw, _ := json.Marshal(fields)
	var in model.WorkCommand
	if json.Unmarshal(raw, &in) != nil {
		h.recommendationFailure(w, r, model.ErrInput)
		return
	}
	v, e := h.recommendations.Preview(r.Context(), accountID(r), r.PathValue("id"), in)
	if e != nil {
		h.recommendationFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) acceptRecommendation(w http.ResponseWriter, r *http.Request) {
	var in rec.AcceptInput
	if !h.decode(w, r, &in) {
		return
	}
	v, e := h.recommendations.Accept(r.Context(), accountID(r), r.PathValue("id"), in)
	if e != nil {
		h.recommendationFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) recommendationMiss(w http.ResponseWriter, r *http.Request) {
	var in rec.MissInput
	if !h.decode(w, r, &in) {
		return
	}
	if e := h.recommendations.Miss(r.Context(), accountID(r), r.PathValue("id"), in); e != nil {
		h.recommendationFailure(w, r, e)
		return
	}
	h.json(w, 200, map[string]bool{"saved": true})
}

func (h *Handler) recommendationMisses(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.recommendationFailure(w, r, model.ErrInput)
		return
	}
	if r.URL.Query().Get("page_size") == "" {
		size = 20
	}
	v, e := h.recommendations.Misses(r.Context(), r.PathValue("id"), page, size)
	if e != nil {
		h.recommendationFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) exportRecommendationFeedback(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.recommendationFailure(w, r, model.ErrInput)
		return
	}
	if r.URL.Query().Get("page_size") == "" {
		size = 20
	}
	v, e := h.recommendations.Export(r.Context(), r.PathValue("id"), page, size)
	if e != nil {
		h.recommendationFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}

func (h *Handler) recommendationMissRoute(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		h.recommendationMisses(w, r)
	case "POST":
		h.recommendationMiss(w, r)
	default:
		h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
	}
}
