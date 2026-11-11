package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"net/http"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/model"
	"strings"
	"testing"
	"time"
)

func TestScanEncodingStorageHTTPContractsAndAuthorization(t *testing.T) {
	h, admin, csrf := catalogHandler(t)
	db := handlerDB(t, h)
	h.WithMaintenance(maintenance.New(db, h.c, h.catalog, nil, h.logger))
	ctx := context.Background()
	lib, e := h.catalog.SaveLibrary(ctx, model.Library{RootKey: "fixture", Name: "anonymous", Encoding: "utf8"}, 0)
	if e != nil {
		t.Fatal(e)
	}
	job, e := h.catalog.StartScan(ctx, lib.ID)
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, e := h.catalog.Job(ctx, job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if j.State == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(j)
		}
		time.Sleep(time.Millisecond)
	}
	books, _, e := h.catalog.Editions(ctx, lib.ID, "available", 1, 10)
	if e != nil {
		t.Fatal(e)
	}
	b := books[0]
	doc, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers[0].URL = "http://localhost/api/v1"
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path, body string, want int, cookie *http.Cookie, token string) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", h.c.PublicURL)
		r.Header.Set("X-CSRF-Token", token)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		route, params, e := router.FindRoute(r)
		if e != nil {
			t.Fatal(path, e)
		}
		input := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		if want < 400 {
			if e = openapi3filter.ValidateRequest(ctx, input); e != nil {
				t.Fatal(path, e)
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, want)
		response := &openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: w.Code, Header: w.Header()}
		response.SetBodyBytes(w.Body.Bytes())
		if e = openapi3filter.ValidateResponse(ctx, response); e != nil {
			t.Fatal(path, e)
		}
		if strings.Contains(w.Body.String(), h.c.Roots[0].Path) || strings.Contains(w.Body.String(), h.c.DataDir) {
			t.Fatal("host path leaked")
		}
		out := map[string]any{}
		if e = json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	editor, editorCSRF, editorCtx := authorizedTestEditor(t, h, lib.ID)
	preview := call("GET", "/editions/"+b.ID+"/encoding-preview", "", 200, editor, editorCSRF)
	if preview["requires_choice"] != true {
		t.Fatal(preview)
	}
	candidate := preview["candidates"].([]any)[1].(map[string]any)
	body := fmt.Sprintf(`{"expected_revision":%d,"content_revision":%d,"parsing_revision":%d,"encoding":"gb18030","directory_digest":%q}`, b.Revision, b.ContentRevision, b.ParsingRevision, candidate["directory_digest"])
	call("PUT", "/editions/"+b.ID+"/encoding", body, 200, editor, editorCSRF)
	call("PUT", "/books/"+b.ID+"/encoding", body, 409, editor, editorCSRF)
	call("GET", "/books/"+b.ID+"/encoding-preview", "", 200, editor, editorCSRF)
	call("GET", "/maintenance/storage?refresh=true", "", 200, admin, csrf)
	call("GET", "/maintenance/storage", "", 403, editor, editorCSRF)
	actor, _ := model.Actor(editorCtx)
	if _, e = db.Write.Exec("UPDATE account_library_grants SET role='viewer' WHERE account_id=?", actor.ID); e != nil {
		t.Fatal(e)
	}
	call("GET", "/editions/"+b.ID+"/encoding-preview", "", 403, editor, editorCSRF)
	call("PUT", "/editions/"+b.ID+"/encoding", body, 403, editor, editorCSRF)
	call("GET", "/editions/"+b.ID+"/encoding-preview", "", 401, nil, "")
}

func TestPageImageRequiresCurrentParsingIdentity(t *testing.T) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	if _, e := db.Write.Exec("UPDATE files SET parsing_revision=2 WHERE edition_id='book'"); e != nil {
		t.Fatal(e)
	}
	// This fails before source access; content revision alone cannot select a page
	// after its physical ordering has changed.
	w := request(t, h, "GET", "/api/v1/editions/book/pages/1/image?content_revision=1", "", cookie, csrf)
	code(t, w, 409)
	if !strings.Contains(w.Body.String(), "parsing_changed") {
		t.Fatal(w.Body.String())
	}
}

func TestImageLimitStructuredHTTPDiagnostics(t *testing.T) {
	h, _, _ := catalogHandler(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/anonymous", nil)
	h.catalogFailure(w, r, &comic.LimitError{Stage: "decode", Resource: "estimated_memory_bytes", Actual: 600 << 20, Limit: 512 << 20})
	code(t, w, 422)
	var body struct {
		Error struct {
			Code    string           `json:"code"`
			Details comic.LimitError `json:"details"`
		} `json:"error"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil || body.Error.Code != "resource_limit" || body.Error.Details.Actual != 600<<20 || body.Error.Details.Limit != 512<<20 || body.Error.Details.Resource != "estimated_memory_bytes" {
		t.Fatal(w.Body.String(), e)
	}
}
