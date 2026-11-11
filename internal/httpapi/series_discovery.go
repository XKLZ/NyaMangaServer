package httpapi

import (
	"errors"
	"net/http"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
)

func (h *Handler) seriesFailure(w http.ResponseWriter, r *http.Request, e error) {
	switch {
	case errors.Is(e, rec.ErrStale):
		h.error(w, r, 410, "series_proposal_stale", "系列输入已变化，请重新查找或加载方案")
	case errors.Is(e, rec.ErrSelection):
		h.error(w, r, 409, "series_selection_requires_resolve", "成员、名称或顺序需要重新验证，请先更新方案")
	case errors.Is(e, rec.ErrUnavailable), errors.Is(e, rec.ErrWorker):
		h.error(w, r, 503, "series_recommendation_unavailable", "系列推荐运行组件不可用，其他管理功能仍可使用")
	case errors.Is(e, model.ErrInput):
		h.error(w, r, 400, "invalid_series_proposal", "系列目标、选择、名称、顺序或分页参数无效")
	default:
		h.recommendationFailure(w, r, e)
	}
}

func (h *Handler) startSeriesDiscovery(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Library string `json:"library_id"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	j, e := h.recommendations.StartSeries(r.Context(), in.Library, "")
	if e != nil {
		h.seriesFailure(w, r, e)
		return
	}
	h.accepted(w, j)
}
func (h *Handler) seriesProposals(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.seriesFailure(w, r, model.ErrInput)
		return
	}
	if r.URL.Query().Get("page_size") == "" {
		size = 25
	}
	q := r.URL.Query()
	if q.Has("alternatives") && q.Get("alternatives") != "true" && q.Get("alternatives") != "false" {
		h.seriesFailure(w, r, model.ErrInput)
		return
	}
	v, e := h.recommendations.SeriesPage(r.Context(), q.Get("job_id"), q.Get("library_id"), q.Get("kind"), q.Get("state"), page, size, rec.SeriesProposalFilter{TargetSeriesID: q.Get("target_series_id"), Alternatives: q.Get("alternatives") == "true"})
	if e != nil {
		h.seriesFailure(w, r, e)
		return
	}
	h.json(w, 200, seriesProposalPageView(v))
}
func (h *Handler) seriesProposal(w http.ResponseWriter, r *http.Request) {
	v, e := h.recommendations.GetSeriesProposal(r.Context(), r.PathValue("id"))
	if e != nil {
		h.seriesFailure(w, r, e)
		return
	}
	h.json(w, 200, seriesProposalView(v))
}
func (h *Handler) seriesMembers(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.seriesFailure(w, r, model.ErrInput)
		return
	}
	v, e := h.recommendations.SeriesMemberPage(r.Context(), r.PathValue("id"), page, size)
	if e != nil {
		h.seriesFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) seriesPending(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.seriesFailure(w, r, model.ErrInput)
		return
	}
	v, e := h.recommendations.SeriesPendingPage(r.Context(), r.PathValue("id"), page, size)
	if e != nil {
		h.seriesFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) seriesResolve(w http.ResponseWriter, r *http.Request) {
	var in rec.SeriesSelection
	if !h.decode(w, r, &in) {
		return
	}
	v, e := h.recommendations.ResolveSeries(r.Context(), r.PathValue("id"), in)
	if e != nil {
		h.seriesFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) seriesDecision(w http.ResponseWriter, r *http.Request) {
	var in rec.SeriesSelection
	if !h.decode(w, r, &in) {
		return
	}
	v, e := h.recommendations.DecideSeries(r.Context(), accountID(r), r.PathValue("id"), r.PathValue("action"), in)
	if e != nil {
		h.seriesFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}

func (h *Handler) seriesStatus(w http.ResponseWriter, r *http.Request) {
	out, e := h.recommendations.SeriesStatus(r.Context(), r.PathValue("id"))
	if e != nil {
		h.seriesFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
