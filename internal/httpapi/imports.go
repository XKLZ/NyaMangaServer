package httpapi

import (
	"net/http"
	"nyamangaserver.local/server/internal/importflow"
	"nyamangaserver.local/server/internal/model"
	"strconv"
)

func (h *Handler) WithImports(s *importflow.Service) *Handler {
	h.imports = s
	h.registerSourceSearch()
	h.protected.HandleFunc("/api/v1/import-batches/{batch}/work-proposals/{id}", h.method("GET", h.importWorkProposal))
	h.protected.HandleFunc("/api/v1/import-batches/{batch}/series-proposals/{id}", h.method("GET", h.importSeriesProposal))
	h.protected.HandleFunc("/api/v1/import-batches/{batch}/series-proposals/{id}/members", h.method("GET", h.importSeriesMembers))
	h.protected.HandleFunc("/api/v1/import-batches/{id}/organization-review", h.method("GET", h.importOrganizationReview))
	h.protected.HandleFunc("/api/v1/import-batches/{id}/organization-review/complete", h.method("POST", h.completeImportOrganizationReview))
	h.protected.HandleFunc("/api/v1/import-batches/{id}/members/{edition}/candidate-cover", h.method("GET", h.importCandidateCover))
	h.protected.HandleFunc("/api/v1/import-batches/{id}/review-selection", h.method("GET", h.importSelection))
	h.protected.HandleFunc("/api/v1/import-batches/{id}/members/{edition}/editor", h.method("GET", h.importEditor))
	h.protected.HandleFunc("/api/v1/import-batches/{id}/members/{edition}/manual-review", h.method("POST", h.importManualReview))
	h.protected.HandleFunc("/api/v1/import-batches/{id}/retry-files", h.method("POST", h.importRetryFiles))
	h.protected.HandleFunc("/api/v1/import-batches/{id}/members/{edition}/candidate", h.method("GET", h.importCandidate))
	h.protected.HandleFunc("/api/v1/jobs/{id}/import-batch", h.method("GET", h.importJobBatch))
	h.protected.HandleFunc("/api/v1/import-batches", h.importBatches)
	h.protected.HandleFunc("/api/v1/import-batches/{id}", h.method("GET", h.importBatch))
	h.protected.HandleFunc("/api/v1/import-batches/{id}/members", h.method("GET", h.importMembers))
	h.protected.HandleFunc("/api/v1/import-batches/{id}/reviews", h.method("POST", h.importReviews))
	h.protected.HandleFunc("/api/v1/import-batches/{id}/members/{edition}/refetch", h.method("POST", h.importRefetch))
	h.protected.HandleFunc("/api/v1/import-batches/{id}/control/{action}", h.method("POST", h.importControl))

	h.protected.HandleFunc("/api/v1/import-batches/{id}/work-proposals", h.method("GET", h.importWorkProposals))
	h.protected.HandleFunc("/api/v1/import-batches/{id}/series-proposals", h.method("GET", h.importSeriesProposals))
	h.protected.HandleFunc("/api/v1/import-batches/{batch}/work-proposals/{id}/{action}", h.method("POST", h.importWorkDecision))
	h.protected.HandleFunc("/api/v1/import-batches/{batch}/series-proposals/{id}/{action}", h.method("POST", h.importSeriesDecision))
	return h
}
func (h *Handler) importBatches(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		p, n, e := pagination(r)
		if e != nil {
			h.catalogFailure(w, r, model.ErrInput)
			return
		}
		out, e := h.imports.List(r.Context(), p, n, model.ImportFilter{LibraryID: r.URL.Query().Get("library_id"), State: r.URL.Query().Get("state")})
		if e != nil {
			h.catalogFailure(w, r, e)
			return
		}
		h.json(w, 200, out)
	case "POST":
		var in struct {
			LibraryID  string `json:"library_id"`
			RequestKey string `json:"request_key"`
			Network    bool   `json:"allow_metadata_network"`
		}
		if !h.decode(w, r, &in) {
			return
		}
		if !in.Network {
			h.error(w, r, 422, "metadata_network_consent_required", "完整导入包含联网获取，请明确确认")
			return
		}
		out, e := h.imports.Create(r.Context(), in.LibraryID, in.RequestKey)
		if e != nil {
			h.catalogFailure(w, r, e)
			return
		}
		h.json(w, 202, out)
	default:
		w.Header().Set("Allow", "GET, POST")
		h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
	}
}
func (h *Handler) importBatch(w http.ResponseWriter, r *http.Request) {
	out, e := h.imports.Batch(r.Context(), r.PathValue("id"))
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
func (h *Handler) importMembers(w http.ResponseWriter, r *http.Request) {
	p, n, e := pagination(r)
	if e != nil {
		h.catalogFailure(w, r, model.ErrInput)
		return
	}
	out, e := h.imports.Members(r.Context(), r.PathValue("id"), r.URL.Query().Get("filter"), p, n, r.URL.Query().Get("stage"))
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
func (h *Handler) importReviews(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Items []model.ImportReview `json:"items"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	if e := h.imports.Review(r.Context(), r.PathValue("id"), in.Items); e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 200, map[string]bool{"reviewed": true})
}
func (h *Handler) importRefetch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ExpectedRevision int64  `json:"expected_revision"`
		URL              string `json:"gallery_url"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	id, e := h.imports.Refetch(r.Context(), r.PathValue("id"), r.PathValue("edition"), in.URL, in.ExpectedRevision)
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 202, map[string]string{"job_id": id})
}
func (h *Handler) importControl(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	if e := h.imports.Control(r.Context(), r.PathValue("id"), r.PathValue("action"), in.ExpectedRevision); e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.importBatch(w, r)
}

func (h *Handler) importWorkProposals(w http.ResponseWriter, r *http.Request) {
	p, n, e := pagination(r)
	if e != nil {
		h.catalogFailure(w, r, model.ErrInput)
		return
	}
	alternatives, e := importAlternativeQuery(r)
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	out, e := h.imports.WorkProposals(r.Context(), r.PathValue("id"), r.URL.Query().Get("state"), p, n, alternatives...)
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
func (h *Handler) importSeriesProposals(w http.ResponseWriter, r *http.Request) {
	p, n, e := pagination(r)
	if e != nil {
		h.catalogFailure(w, r, model.ErrInput)
		return
	}
	alternatives, e := importAlternativeQuery(r)
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	out, e := h.imports.SeriesProposals(r.Context(), r.PathValue("id"), r.URL.Query().Get("state"), p, n, alternatives...)
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 200, seriesProposalPageView(out))
}
func (h *Handler) importWorkDecision(w http.ResponseWriter, r *http.Request) {
	if e := h.imports.CheckProposal(r.Context(), r.PathValue("batch"), "versions", r.PathValue("id"), r.PathValue("action")); e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	if r.PathValue("action") == "resolve" {
		h.workProposalResolve(w, r)
	} else {
		h.workProposalDecision(w, r)
	}
}
func (h *Handler) importSeriesDecision(w http.ResponseWriter, r *http.Request) {
	if e := h.imports.CheckProposal(r.Context(), r.PathValue("batch"), "series", r.PathValue("id"), r.PathValue("action")); e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	if r.PathValue("action") == "resolve" {
		h.seriesResolve(w, r)
	} else {
		h.seriesDecision(w, r)
	}
}

func (h *Handler) importJobBatch(w http.ResponseWriter, r *http.Request) {
	id, e := h.imports.JobBatch(r.Context(), r.PathValue("id"))
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 200, map[string]string{"batch_id": id})
}

func (h *Handler) importCandidate(w http.ResponseWriter, r *http.Request) {
	out, e := h.imports.Candidate(r.Context(), r.PathValue("id"), r.PathValue("edition"))
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
func (h *Handler) importRetryFiles(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ExpectedRevision int64    `json:"expected_revision"`
		IDs              []string `json:"edition_ids"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	job, e := h.imports.RetryFiles(r.Context(), r.PathValue("id"), in.ExpectedRevision, in.IDs)
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 202, map[string]string{"job_id": job})
}

func (h *Handler) importSelection(w http.ResponseWriter, r *http.Request) {
	p, n, e := pagination(r)
	if e != nil {
		h.catalogFailure(w, r, model.ErrInput)
		return
	}
	out, e := h.imports.Selections(r.Context(), r.PathValue("id"), r.URL.Query().Get("filter"), p, n)
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
func (h *Handler) importEditor(w http.ResponseWriter, r *http.Request) {
	out, e := h.imports.Editor(r.Context(), r.PathValue("id"), r.PathValue("edition"))
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 200, struct {
		Book              any                   `json:"book"`
		Metadata          model.MetadataDetails `json:"metadata"`
		MemberRevision    int64                 `json:"member_revision"`
		CandidateRevision int64                 `json:"candidate_revision"`
	}{response(out.Edition), out.Metadata, out.MemberRevision, out.CandidateRevision})
}
func (h *Handler) importManualReview(w http.ResponseWriter, r *http.Request) {
	var in model.ImportManualReview
	if !h.decode(w, r, &in) {
		return
	}
	if e := h.imports.ReviewManual(r.Context(), r.PathValue("id"), r.PathValue("edition"), in); e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	r.SetPathValue("id", r.PathValue("edition"))
	h.metadataBook(w, r)
}

func (h *Handler) importCandidateCover(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if len(query) != 1 || len(query["candidate_revision"]) != 1 {
		h.error(w, r, 400, "invalid_candidate_cover", "请指定候选版本")
		return
	}
	revision, e := strconv.ParseInt(query.Get("candidate_revision"), 10, 64)
	if e != nil || revision < 1 {
		h.error(w, r, 400, "invalid_candidate_cover", "候选版本无效")
		return
	}
	data, e := h.imports.CandidateCover(r.Context(), r.PathValue("id"), r.PathValue("edition"), revision)
	if e != nil {
		h.metadataFailure(w, r, e)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(200)
	_, _ = w.Write(data)
}

func importAlternativeQuery(r *http.Request) ([]bool, error) {
	alts, only := true, false
	var e error
	if r.URL.Query().Has("alternatives") {
		alts, e = strconv.ParseBool(r.URL.Query().Get("alternatives"))
		if e != nil {
			return nil, model.ErrInput
		}
	}
	if r.URL.Query().Has("only_alternatives") {
		only, e = strconv.ParseBool(r.URL.Query().Get("only_alternatives"))
		if e != nil {
			return nil, model.ErrInput
		}
	}
	if only && !alts {
		return nil, model.ErrInput
	}
	return []bool{alts, only}, nil
}
func (h *Handler) importOrganizationReview(w http.ResponseWriter, r *http.Request) {
	out, e := h.imports.OrganizationReview(r.Context(), r.PathValue("id"))
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.json(w, 200, out)
}
func (h *Handler) completeImportOrganizationReview(w http.ResponseWriter, r *http.Request) {
	var in model.CompleteOrganizationReview
	if !h.decode(w, r, &in) {
		return
	}
	if e := h.imports.CompleteOrganizationReview(r.Context(), r.PathValue("id"), in); e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.importBatch(w, r)
}

func (h *Handler) importWorkProposal(w http.ResponseWriter, r *http.Request) {
	if e := h.imports.CheckProposal(r.Context(), r.PathValue("batch"), "versions", r.PathValue("id"), "read"); e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.workProposal(w, r)
}
func (h *Handler) importSeriesProposal(w http.ResponseWriter, r *http.Request) {
	if e := h.imports.CheckProposal(r.Context(), r.PathValue("batch"), "series", r.PathValue("id"), "read"); e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.seriesProposal(w, r)
}
func (h *Handler) importSeriesMembers(w http.ResponseWriter, r *http.Request) {
	if e := h.imports.CheckProposal(r.Context(), r.PathValue("batch"), "series", r.PathValue("id"), "read"); e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	h.seriesMembers(w, r)
}
