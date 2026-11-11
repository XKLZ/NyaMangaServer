package httpapi

import (
	"context"
	"encoding/json"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/organization"
	"nyamangaserver.local/server/internal/search"
	"nyamangaserver.local/server/internal/userstate"
	"strings"
	"testing"
)

func TestAdminHTTPContractAndValidation(t *testing.T) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	h.WithOrganization(organization.New(db), search.New(db), userstate.New(db))
	m := maintenance.New(db, h.c, h.catalog, h.metadata, h.logger)
	h.WithMaintenance(m)
	ctx := context.Background()
	doc, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers[0].URL = "http://localhost/api/v1"
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path, body string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", h.c.PublicURL)
		r.Header.Set("X-CSRF-Token", csrf)
		r.AddCookie(cookie)
		route, params, e := router.FindRoute(r)
		if e != nil {
			t.Fatal(path, e)
		}
		input := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		if e = openapi3filter.ValidateRequest(ctx, input); e != nil {
			t.Fatal(path, e)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, want)
		out := &openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: w.Code, Header: w.Header()}
		out.SetBodyBytes(w.Body.Bytes())
		if e = openapi3filter.ValidateResponse(ctx, out); e != nil {
			t.Fatal(path, e)
		}
		var result map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return result
	}
	call("POST", "/admin/books/search", `{"page":1,"page_size":50,"collapse":"none","visibility":"active","include_disabled":true}`, 200)
	call("GET", "/jobs?page=1&page_size=1&type=scan", "", 200)
	series := call("POST", "/series", `{"name":"Anonymous series","library_id":"lib","expected_revisions":{}}`, 200)
	sid := series["id"].(string)
	account, x := db.Account(ctx)
	if x != nil {
		t.Fatal(x)
	}
	g, e := h.organization.WorkContext(ctx, account.ID, []string{"book"}, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	g.Expected["series:"+sid] = 1
	raw, _ := json.Marshal(map[string]any{"edition_ids": []string{"book"}, "expected_revisions": g.Expected})
	call("POST", "/series/"+sid+"/members", string(raw), 200)
	call("GET", "/series/"+sid+"/members?page=1&page_size=50", "", 200)
	call("GET", "/series/"+sid+"?summary=true", "", 200)

	call("GET", "/books/book/cover-selection", "", 200)
	call("PUT", "/books/book/cover-selection", `{"expected_revision":0,"mode":"auto"}`, 200)
	call("GET", "/maintenance/storage", "", 200)
	plan := call("POST", "/maintenance/plans", `{"operation":"trash","book_ids":["book"],"expected_revisions":{"book:book":1}}`, 200)
	call("POST", "/maintenance/plans/"+plan["id"].(string)+"/execute", `{}`, 202)
	call("GET", "/maintenance/trash?kind=book&page=1&page_size=50", "", 200)
	for _, v := range []struct{ method, path, body string }{{"PATCH", "/books/book/user-state", `{"favorite":true}`}, {"POST", "/series", `{"library_id":"lib","name":"Missing CAS"}`}, {"POST", "/saved-searches", `{"name":"Missing CAS","query_version":1,"conditions":{}}`}, {"POST", "/maintenance/plans", `{"operation":"trash","book_ids":["book"]}`}} {
		code(t, request(t, h, v.method, "/api/v1"+v.path, v.body, cookie, csrf), 400)
	}
	code(t, request(t, h, "POST", "/api/v1/maintenance/plans", `{"operation":"trash","book_ids":["book"],"expected_revisions":{"book:book":1}}`, cookie, "wrong"), 403)
	code(t, request(t, h, "GET", "/api/v1/maintenance/storage", "", nil, ""), 401)
	code(t, request(t, h, "GET", "/api/v1/jobs?type=unknown", "", cookie, csrf), 422)
	code(t, request(t, h, "GET", "/api/v1/maintenance/roots/unknown/entries?relative_path=../bad", "", cookie, csrf), 422)
}
func TestAdminSPAAndNonce(t *testing.T) {
	h, _ := testHandler(t, "http://localhost")
	one := request(t, h, "GET", "/admin/books", "", nil, "")
	code(t, one, 200)
	two := request(t, h, "GET", "/admin/tasks", "", nil, "")
	code(t, two, 200)
	if !strings.Contains(one.Body.String(), `name="csp-nonce"`) || one.Header().Get("Content-Security-Policy") == two.Header().Get("Content-Security-Policy") {
		t.Fatal("missing per-response nonce")
	}
	if strings.Contains(one.Header().Get("Content-Security-Policy"), "script-src 'unsafe-inline'") {
		t.Fatal("script policy weakened")
	}
}
