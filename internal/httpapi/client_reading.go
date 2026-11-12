package httpapi

import (
	"net/http"
	"net/url"
	"strconv"

	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/library"
	"nyamangaserver.local/server/internal/model"
)

const clientContextPath = "/api/v1/client/works/{id}/reading-context"
const clientPagesPath = "/api/v1/client/editions/{id}/pages"
const clientImagePath = clientPagesPath + "/{page_index}/image"
const clientWorkSearchPath = "/api/v1/client/works/search"

func (h *Handler) clientReadingContext(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	intent, edition := q.Get("intent"), q.Get("edition_id")
	if intent == "" {
		intent = "open"
	}
	if intent != "open" && intent != "continue" || q.Has("edition_id") && edition == "" || len(q["intent"]) > 1 || len(q["edition_id"]) > 1 {
		h.failure(w, r, model.ErrInput)
		return
	}
	v, e := h.catalog.ClientReadingContext(r.Context(), r.PathValue("id"), edition, intent)
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	addClientCoverURL(&v.Edition)
	for i := range v.Editions {
		addClientCoverURL(&v.Editions[i])
	}
	version := url.Values{
		"content_revision": {strconv.FormatInt(v.Edition.ContentRevision, 10)},
		"parsing_revision": {strconv.FormatInt(v.Edition.ParsingRevision, 10)},
	}
	v.PagesURL = "/api/v1/client/editions/" + url.PathEscape(v.Edition.ID) + "/pages?" + version.Encode()
	h.json(w, 200, v)
}

func addClientCoverURL(v *model.ClientEdition) {
	v.CoverURL = "/api/v1/editions/" + url.PathEscape(v.ID) + "/cover?content_revision=" + strconv.FormatInt(v.ContentRevision, 10)
}

func (h *Handler) clientWorkSearch(w http.ResponseWriter, r *http.Request) {
	var in model.ClientSearchInput
	if !h.decode(w, r, &in) {
		return
	}
	v, e := h.search.ClientWorkSearch(r.Context(), in)
	if e != nil {
		h.organizationFailure(w, r, e)
		return
	}
	if h.catalog != nil {
		reason := h.catalog.ClientRootReason(v.SourceLibrary)
		for i := range v.Items {
			item := &v.Items[i].Edition
			addClientCoverURL(item)
			item.Readability = library.ClientReadability(*item, reason)
		}
	}
	h.json(w, 200, v)
}

func (h *Handler) clientEditionVersion(w http.ResponseWriter, r *http.Request) *model.Edition {
	q := r.URL.Query()
	content, contentErr := strconv.ParseInt(q.Get("content_revision"), 10, 64)
	parsing, parsingErr := strconv.ParseInt(q.Get("parsing_revision"), 10, 64)
	if len(q["content_revision"]) != 1 || len(q["parsing_revision"]) != 1 || contentErr != nil || parsingErr != nil || content < 1 || parsing < 1 {
		h.error(w, r, 400, "invalid_request", "需要有效的内容版本和页序版本")
		return nil
	}
	edition, e := h.catalog.Edition(r.Context(), r.PathValue("id"))
	if e != nil {
		h.catalogFailure(w, r, e)
		return nil
	}
	if edition.ContentRevision != content {
		h.catalogFailure(w, r, comic.ErrChanged)
		return nil
	}
	if edition.ParsingRevision != parsing {
		h.catalogFailure(w, r, model.ErrParsingChanged)
		return nil
	}
	return edition
}

func (h *Handler) clientEditionStillCurrent(w http.ResponseWriter, r *http.Request, before *model.Edition) bool {
	after := h.clientEditionVersion(w, r)
	if after == nil {
		return false
	}
	if after.FileParsing.Fingerprint() != before.FileParsing.Fingerprint() {
		h.catalogFailure(w, r, model.ErrParsingChanged)
		return false
	}
	if e := h.catalog.SourceGuard(r.Context(), before.ID); e != nil {
		h.catalogFailure(w, r, e)
		return false
	}
	return true
}

func (h *Handler) clientPages(w http.ResponseWriter, r *http.Request) {
	page, size, e := pagination(r)
	if e != nil {
		h.error(w, r, 400, "invalid_request", "分页参数无效")
		return
	}
	if !r.URL.Query().Has("page_size") {
		size = 24
	}
	b := h.clientEditionVersion(w, r)
	if b == nil {
		return
	}
	v, e := h.catalog.Pages(r.Context(), b.ID, b.ContentRevision, page, size)
	if e != nil {
		h.catalogFailure(w, r, e)
		return
	}
	if !h.clientEditionStillCurrent(w, r, b) {
		return
	}
	for i := range v {
		v[i].Index--
	}
	h.json(w, 200, map[string]any{"items": v, "total": b.PageCount, "parsing_revision": b.ParsingRevision, "directory_version": comic.DirectoryVersion, "content_revision": b.ContentRevision, "page": page, "page_size": size})
}

func (h *Handler) clientPageImage(w http.ResponseWriter, r *http.Request) {
	index, e := strconv.ParseUint(r.PathValue("page_index"), 10, 63)
	if e != nil {
		h.error(w, r, 400, "invalid_request", "页码无效")
		return
	}
	b := h.clientEditionVersion(w, r)
	if b == nil {
		return
	}
	if index >= uint64(b.PageCount) && b.PageCount > 0 || index >= uint64(^uint(0)>>1) {
		h.catalogFailure(w, r, model.ErrNotFound)
		return
	}
	h.servePageImage(w, r, *b, int(index)+1)
}
