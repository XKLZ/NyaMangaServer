package httpapi

import (
	"net/http"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
)

func (h *Handler) startWorkDiscovery(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Library string `json:"library_id"`
		Full    bool   `json:"full_recompute"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	j, e := h.recommendations.StartDiscovery(r.Context(), in.Library, in.Full)
	if e != nil {
		h.recommendationFailure(w, r, e)
		return
	}
	h.accepted(w, j)
}
func (h *Handler) workProposals(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.recommendationFailure(w, r, model.ErrInput)
		return
	}
	if r.URL.Query().Get("page_size") == "" {
		size = 25
	}
	q := r.URL.Query()
	v, e := h.recommendations.Proposals(r.Context(), q.Get("job_id"), q.Get("library_id"), q.Get("kind"), q.Get("state"), q.Get("alternatives") == "true", page, size, q.Get("target_work_id"))
	if e != nil {
		h.recommendationFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) workProposal(w http.ResponseWriter, r *http.Request) {
	v, e := h.recommendations.Proposal(r.Context(), r.PathValue("id"))
	if e != nil {
		h.recommendationFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) workProposalResolve(w http.ResponseWriter, r *http.Request) {
	var in rec.ProposalSelection
	if !h.decode(w, r, &in) {
		return
	}
	v, e := h.recommendations.ResolveProposal(r.Context(), r.PathValue("id"), in)
	if e != nil {
		h.recommendationFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) workProposalDecision(w http.ResponseWriter, r *http.Request) {
	var in rec.ProposalSelection
	if !h.decode(w, r, &in) {
		return
	}
	v, e := h.recommendations.DecideProposal(r.Context(), accountID(r), r.PathValue("id"), r.PathValue("action"), in)
	if e != nil {
		h.recommendationFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) workProposalAlternatives(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.recommendationFailure(w, r, model.ErrInput)
		return
	}
	v, e := h.recommendations.ProposalAlternatives(r.Context(), r.PathValue("id"), page, size)
	if e != nil {
		h.recommendationFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}

func (h *Handler) workDiscoveryStatus(w http.ResponseWriter, r *http.Request) {
	v, e := h.recommendations.DiscoveryStatus(r.Context(), r.PathValue("id"))
	if e != nil {
		h.recommendationFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
