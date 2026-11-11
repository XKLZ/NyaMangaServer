package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/model"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func (h *Handler) WithMaintenance(m *maintenance.Service) *Handler {
	h.maintenance = m
	p := h.protected
	p.HandleFunc("/api/v1/maintenance/remote-file-info-backfill", h.method("POST", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			DryRun   *bool            `json:"dry_run"`
			Expected map[string]int64 `json:"expected_revisions"`
		}
		if !h.decode(w, r, &in) {
			return
		}
		if in.DryRun == nil {
			h.error(w, r, 400, "invalid_request", "必须明确dry_run")
			return
		}
		out, e := h.maintenance.BackfillRemoteFileInfo(r.Context(), model.RemoteFileInfoBackfill{DryRun: *in.DryRun, Expected: in.Expected})
		if e != nil {
			h.failure(w, r, e)
			return
		}
		h.json(w, 200, out)
	}))
	p.HandleFunc("/api/v1/admin/books/search", h.method("POST", h.adminBooks))
	p.HandleFunc("/api/v1/admin/editions/search", h.method("POST", h.adminBooks))
	p.HandleFunc("/api/v1/libraries/{id}/status", h.method("PATCH", h.libraryStatus))
	p.HandleFunc("/api/v1/maintenance/roots/{key}/entries", h.method("GET", h.rootEntries))
	p.HandleFunc("/api/v1/maintenance/plans", h.method("POST", h.maintenancePlan))
	p.HandleFunc("/api/v1/maintenance/plans/{id}", h.method("GET", h.getMaintenancePlan))
	p.HandleFunc("/api/v1/maintenance/plans/{plan}/execute", h.method("POST", h.executePlan))
	p.HandleFunc("/api/v1/maintenance/trash", h.method("GET", h.trash))
	p.HandleFunc("/api/v1/maintenance/storage", h.method("GET", h.storage))
	p.HandleFunc("/api/v1/books/{id}/cover-selection", h.coverSelection)
	p.HandleFunc("/api/v1/books/{id}/cover-assets", h.method("POST", h.coverAsset))
	p.HandleFunc("/api/v1/books/{id}/cover-rebuilds", h.method("POST", h.coverRebuild))
	p.HandleFunc("/api/v1/books/{id}/source-previews", h.method("POST", h.sourcePreview))
	p.HandleFunc("/api/v1/books/{id}/source-previews/{preview}", h.method("GET", h.getSourcePreview))
	p.HandleFunc("/api/v1/books/{id}/source-previews/{preview}/confirm", h.method("POST", h.confirmSourcePreview))
	p.HandleFunc("/api/v1/books/{id}/source", h.method("DELETE", h.removeSource))
	p.HandleFunc("/api/v1/books/{id}/manual-source", h.method("DELETE", h.removeSource))
	p.HandleFunc("/api/v1/editions/{id}/cover-selection", h.coverSelection)
	for _, prefix := range []string{"books", "editions"} {
		p.HandleFunc("/api/v1/"+prefix+"/{id}/encoding-preview", h.method("GET", func(w http.ResponseWriter, r *http.Request) {
			v, e := h.catalog.PreviewEncoding(r.Context(), r.PathValue("id"))
			if e != nil {
				h.failure(w, r, e)
				return
			}
			h.json(w, 200, v)
		}))
		p.HandleFunc("/api/v1/"+prefix+"/{id}/encoding", h.method("PUT", func(w http.ResponseWriter, r *http.Request) {
			var in model.EncodingUpdate
			if !h.decode(w, r, &in) {
				return
			}
			v, e := h.catalog.SetEncoding(r.Context(), r.PathValue("id"), in)
			if e != nil {
				h.failure(w, r, e)
				return
			}
			h.json(w, 200, response(v))
		}))
	}

	p.HandleFunc("/api/v1/editions/{id}/cover-assets", h.method("POST", h.coverAsset))
	p.HandleFunc("/api/v1/editions/{id}/cover-rebuilds", h.method("POST", h.coverRebuild))
	p.HandleFunc("/api/v1/editions/{id}/source-previews", h.method("POST", h.sourcePreview))
	p.HandleFunc("/api/v1/editions/{id}/source-previews/{preview}", h.method("GET", h.getSourcePreview))
	p.HandleFunc("/api/v1/editions/{id}/source-previews/{preview}/confirm", h.method("POST", h.confirmSourcePreview))
	p.HandleFunc("/api/v1/editions/{id}/source", h.method("DELETE", h.removeSource))
	p.HandleFunc("/api/v1/editions/{id}/manual-source", h.method("DELETE", h.removeSource))
	return h
}
func (h *Handler) adminFailure(w http.ResponseWriter, r *http.Request, e error) {
	if errors.Is(e, context.DeadlineExceeded) || errors.Is(e, context.Canceled) {
		h.error(w, r, 504, "request_timeout", "操作超时，请检查任务状态后重试")
		return
	}
	if errors.Is(e, os.ErrNotExist) {
		h.error(w, r, 404, "not_found", "指定目录或文件不存在")
		return
	}
	if errors.Is(e, os.ErrPermission) {
		h.error(w, r, 403, "permission_denied", "没有操作所需的权限")
		return
	}
	if errors.Is(e, syscall.ENOSPC) {
		h.error(w, r, 507, "storage_full", "应用存储空间不足")
		return
	}
	h.organizationFailure(w, r, e)
}
func (h *Handler) adminBooks(w http.ResponseWriter, r *http.Request) {
	var in model.SearchInput
	if !h.decode(w, r, &in) {
		return
	}
	in.Collapse = "none"
	out, e := h.search.Admin(r.Context(), accountID(r), in)
	if e != nil {
		h.adminFailure(w, r, e)
		return
	}
	items := []any{}
	for _, hit := range out.Items {
		items = append(items, struct {
			bookResponse
			State    model.UserState       `json:"user_state"`
			Metadata model.MetadataDetails `json:"metadata"`
		}{response(hit.Representative).(bookResponse), hit.State, hit.Metadata})
	}
	h.json(w, 200, map[string]any{"items": items, "total": out.Total, "page": out.Page, "page_size": out.PageSize, "snapshot_revision": out.Snapshot})
}
func (h *Handler) libraryStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision *int64 `json:"expected_revision"`
		Disabled *bool  `json:"disabled"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	if in.Revision == nil || in.Disabled == nil {
		h.error(w, r, 400, "invalid_request", "缺少版本或状态")
		return
	}
	v, e := h.maintenance.Status(r.Context(), r.PathValue("id"), *in.Revision, *in.Disabled)
	if e != nil {
		h.adminFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) rootEntries(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.adminFailure(w, r, e)
		return
	}
	v, e := h.maintenance.Entries(r.Context(), r.PathValue("key"), r.URL.Query().Get("relative_path"), page, size)
	if e != nil {
		h.adminFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) maintenancePlan(w http.ResponseWriter, r *http.Request) {
	var in model.MaintenanceInput
	if !h.decode(w, r, &in) {
		return
	}
	// Decode/authentication/CSRF stay under the ordinary deadline. Only an
	// authenticated cleanup preview gets the finite bulk validation budget.
	if in.Operation == "cleanup" {
		if r.Context().Err() != nil {
			h.adminFailure(w, r, r.Context().Err())
			return
		}
		ctx, cancel := authenticatedBulkContext(r)
		defer cancel()
		r = r.WithContext(ctx)
		ctx, transferCancel := instanceTransfer(w, r)
		defer transferCancel()
		r = r.WithContext(ctx)
	}
	p, e := h.maintenance.Plan(r.Context(), in)
	if e != nil {
		if in.Operation == "cleanup" {
			if errors.Is(e, model.ErrCapacity) {
				h.error(w, r, 422, "cleanup_preview_capacity", "清理预览超过容量限制，请缩小类别或操作选择")
				return
			}
			if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
				h.error(w, r, 504, "request_timeout", "清理预览已停止，未执行删除；请缩小选择后重试")
				return
			}
		}
		h.adminFailure(w, r, e)
		return
	}
	h.json(w, 200, p)
}
func (h *Handler) getMaintenancePlan(w http.ResponseWriter, r *http.Request) {
	p, e := h.maintenance.GetPlan(r.Context(), r.PathValue("id"))
	if e != nil {
		h.adminFailure(w, r, e)
		return
	}
	h.json(w, 200, p)
}

func (h *Handler) executePlan(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if !h.decode(w, r, &in) {
		return
	}
	j, e := h.maintenance.Execute(r.Context(), r.PathValue("plan"))
	if e != nil {
		h.adminFailure(w, r, e)
		return
	}
	h.json(w, 202, map[string]string{"job_id": j.ID})
}
func (h *Handler) trash(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.adminFailure(w, r, e)
		return
	}
	v, e := h.maintenance.Trash(r.Context(), r.URL.Query().Get("kind"), page, size)
	if e != nil {
		h.adminFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) storage(w http.ResponseWriter, r *http.Request) {
	var v map[string]any
	var e error
	if r.URL.Query().Get("refresh") == "true" {
		v, e = h.maintenance.RefreshStorage(r.Context())
	} else {
		v, e = h.maintenance.Storage(r.Context())
	}
	if e != nil {
		h.adminFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) coverSelection(w http.ResponseWriter, r *http.Request) {
	var v model.CoverSelection
	var e error
	if r.Method == "GET" {
		v, e = h.catalog.CoverSelection(r.Context(), r.PathValue("id"))
	} else if r.Method == "PUT" {
		var in struct {
			Expected *int64 `json:"expected_revision"`
			Mode     string `json:"mode"`
			Page     *int   `json:"page_index"`
			Content  int64  `json:"content_revision"`
			Parsing  int64  `json:"parsing_revision"`
		}
		if !h.decode(w, r, &in) {
			return
		}
		if in.Expected == nil {
			h.error(w, r, 400, "invalid_request", "缺少封面版本")
			return
		}
		v, e = h.catalog.SetCover(r.Context(), r.PathValue("id"), *in.Expected, model.CoverSelection{Mode: in.Mode, PageIndex: in.Page, ContentRevision: in.Content, ParsingRevision: in.Parsing})
	} else {
		h.error(w, r, 405, "method_not_allowed", "请求方法不支持")
		return
	}
	if e != nil {
		h.adminFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) coverAsset(w http.ResponseWriter, r *http.Request) {
	rev, e := strconv.ParseInt(r.Header.Get("X-Expected-Revision"), 10, 64)
	if e != nil || rev < 0 {
		h.error(w, r, 400, "invalid_request", "缺少封面版本")
		return
	}
	ct := strings.Split(r.Header.Get("Content-Type"), ";")[0]
	if ct != "image/png" && ct != "image/jpeg" && ct != "image/webp" {
		h.error(w, r, 415, "unsupported_media_type", "仅支持JPEG/PNG/WebP")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	raw, e := io.ReadAll(r.Body)
	if e != nil {
		h.error(w, r, 413, "body_too_large", "上传图片超过10MiB")
		return
	}
	v, e := h.catalog.UploadCover(r.Context(), r.PathValue("id"), rev, raw)
	if e != nil {
		h.adminFailure(w, r, e)
		return
	}
	h.json(w, 200, v)
}
func (h *Handler) coverRebuild(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Expected *int64 `json:"expected_revision"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	if in.Expected == nil {
		h.error(w, r, 400, "invalid_request", "缺少封面版本")
		return
	}
	j, e := h.catalog.CreateCoverJob(r.Context(), r.PathValue("id"), *in.Expected)
	if e != nil {
		h.adminFailure(w, r, e)
		return
	}
	h.maintenance.Wake()
	h.json(w, 202, map[string]string{"job_id": j.ID})
}
func (h *Handler) sourcePreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL       string `json:"gallery_url"`
		Force     bool   `json:"force_refresh"`
		Expected  *int64 `json:"expected_revision"`
		Selection *int64 `json:"selection_revision"`
		Content   *int64 `json:"content_revision"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	if in.Expected == nil || in.Selection == nil || in.Content == nil {
		h.error(w, r, 400, "invalid_request", "缺少来源或内容版本")
		return
	}
	j, e := h.metadata.Preview(r.Context(), r.PathValue("id"), in.URL, *in.Expected, *in.Selection, *in.Content)
	if e != nil {
		h.adminFailure(w, r, e)
		return
	}
	h.json(w, 202, map[string]string{"job_id": j.ID})
}
func (h *Handler) getSourcePreview(w http.ResponseWriter, r *http.Request) {
	p, e := h.metadata.SourcePreview(r.Context(), r.PathValue("id"), r.PathValue("preview"))
	if e != nil {
		h.adminFailure(w, r, e)
		return
	}
	h.json(w, 200, p)
}
func (h *Handler) confirmSourcePreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Expected  *int64 `json:"expected_revision"`
		Selection *int64 `json:"selection_revision"`
		Content   *int64 `json:"content_revision"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	if in.Expected == nil || in.Selection == nil || in.Content == nil {
		h.error(w, r, 400, "invalid_request", "缺少来源或内容版本")
		return
	}
	if e := h.metadata.ConfirmPreview(r.Context(), r.PathValue("id"), r.PathValue("preview"), *in.Expected, *in.Selection, *in.Content); e != nil {
		h.adminFailure(w, r, e)
		return
	}
	d, e := h.metadata.Details(r.Context(), r.PathValue("id"))
	if e != nil {
		h.adminFailure(w, r, e)
		return
	}
	h.json(w, 200, d)
}
func (h *Handler) removeSource(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Expected  *int64 `json:"expected_revision"`
		Selection *int64 `json:"selection_revision"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	hint := strings.HasSuffix(r.URL.Path, "/manual-source")
	if in.Selection == nil || !hint && in.Expected == nil {
		h.error(w, r, 400, "invalid_request", "缺少来源版本")
		return
	}
	expected := int64(0)
	if in.Expected != nil {
		expected = *in.Expected
	}
	if e := h.metadata.RemoveSource(r.Context(), r.PathValue("id"), expected, *in.Selection, hint); e != nil {
		h.adminFailure(w, r, e)
		return
	}
	d, e := h.metadata.Details(r.Context(), r.PathValue("id"))
	if e != nil {
		h.adminFailure(w, r, e)
		return
	}
	h.json(w, 200, d)
}
