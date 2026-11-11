package httpapi

import (
	"context"
	"encoding/json"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/organization"
	"nyamangaserver.local/server/internal/search"
	"nyamangaserver.local/server/internal/testfixture"
	"nyamangaserver.local/server/internal/userstate"
	"strings"
	"testing"
)

func TestWorkEditionHTTPRoutingAndProtection(t *testing.T)       { workEditionHTTP(t, false) }
func TestAuthorizationEditorOrganizationOperations(t *testing.T) { workEditionHTTP(t, true) }
func workEditionHTTP(t *testing.T, isEditor bool) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	h.WithOrganization(organization.New(db), search.New(db), userstate.New(db))
	if e := testfixture.SeedEdition(context.Background(), db.Write, "b2", "lib", "b2.cbz", "第2卷"); e != nil {
		t.Fatal(e)
	}
	if isEditor {
		cookie, csrf, _ = authorizedTestEditor(t, h, "lib")
	}
	doc, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers[0].URL = "http://localhost/api/v1"
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		raw := ""
		if body != nil {
			b, _ := json.Marshal(body)
			raw = string(b)
		}
		r := httptest.NewRequest(method, "http://localhost/api/v1"+path, strings.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", h.c.PublicURL)
		r.Header.Set("X-CSRF-Token", csrf)
		r.AddCookie(cookie)
		route, params, e := router.FindRoute(r)
		if e != nil {
			t.Fatal(path, e)
		}
		input := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		if e = openapi3filter.ValidateRequest(context.Background(), input); e != nil {
			t.Fatal(path, e)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, want)
		output := &openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: w.Code, Header: w.Header()}
		output.SetBodyBytes(w.Body.Bytes())
		if e = openapi3filter.ValidateResponse(context.Background(), output); e != nil {
			t.Fatal(path, e)
		}
		v := map[string]any{}
		if want != 204 {
			json.Unmarshal(w.Body.Bytes(), &v)
		}
		return v
	}
	for _, path := range []string{"/organization-context?book_ids=book", "/version-groups", "/version-groups/old", "/version-groups/old/members"} {
		v := call("GET", path, nil, 410)
		if v["error"].(map[string]any)["code"] != "organization_model_changed" {
			t.Fatal(v)
		}
	}
	call("POST", "/organization/preview", map[string]any{}, 410)
	book := call("GET", "/books/book", nil, 200)
	edition := call("GET", "/editions/book", nil, 200)
	if book["id"] != edition["id"] || edition["file"].(map[string]any)["edition_id"] != "book" || edition["work_id"] != "i:book" {
		t.Fatal(edition)
	}
	call("GET", "/works?page=1&page_size=1", nil, 200)
	call("GET", "/works/i:book/editions?page=1&page_size=1", nil, 200)
	contextFor := func(editions, works string) map[string]any {
		return call("GET", "/work-organization/context?edition_ids="+editions+"&work_ids="+works, nil, 200)
	}
	g := contextFor("", "i:book,i:b2")
	command := map[string]any{"action": "work_merge", "id": "i:book", "work_ids": []string{"i:b2"}, "expected_revisions": g["expected_revisions"]}
	call("POST", "/work-organization/preview", command, 200)
	call("POST", "/works/i:book/merge", command, 200)
	call("POST", "/works/i:book/merge", command, 409)
	g = contextFor("book", "")
	call("PUT", "/editions/book/version-labels", map[string]any{"version_labels": []string{"中文", "彩色"}, "expected_revisions": g["expected_revisions"]}, 200)
	g = contextFor("", "i:book")
	call("PUT", "/works/i:book/preferred", map[string]any{"preferred_edition_id": "b2", "expected_revisions": g["expected_revisions"]}, 200)
	series := call("POST", "/series", map[string]any{"library_id": "lib", "name": "Series", "expected_revisions": map[string]int{}}, 200)
	sid := series["id"].(string)
	g = call("GET", "/work-organization/context?work_ids=i:book&series_ids="+sid, nil, 200)
	call("PUT", "/series/"+sid+"/members", map[string]any{"item_ids": []string{"i:book"}, "expected_revisions": g["expected_revisions"]}, 200)
	call("PUT", "/series/"+sid+"/members", map[string]any{"item_ids": []string{}, "work_ids": []string{}, "expected_revisions": g["expected_revisions"]}, 400)
	members := call("GET", "/series/"+sid+"/members?page=1&page_size=1", nil, 200)
	if members["total"] != float64(1) || members["expected_revisions"] == nil {
		t.Fatal(members)
	}
	// Destructive replacement is explicit; preview preserves empty arrays for replay.
	for _, payload := range []string{`{"expected_revisions":{}}`, `{"version_labels":null,"expected_revisions":{}}`} {
		code(t, request(t, h, "PUT", "/api/v1/editions/book/version-labels", payload, cookie, csrf), 422)
	}
	for _, payload := range []string{`{"expected_revisions":{}}`, `{"work_ids":null,"expected_revisions":{}}`} {
		code(t, request(t, h, "PUT", "/api/v1/series/"+sid+"/members", payload, cookie, csrf), 422)
	}
	for _, payload := range []string{`{"work_ids":[],"item_ids":null,"expected_revisions":{}}`, `{"work_ids":null,"item_ids":[],"expected_revisions":{}}`} {
		code(t, request(t, h, "PUT", "/api/v1/series/"+sid+"/members", payload, cookie, csrf), 400)
	}
	g = contextFor("book", "")
	clear := call("POST", "/work-organization/preview", map[string]any{"action": "edition_labels", "edition_id": "book", "version_labels": []string{}, "expected_revisions": g["expected_revisions"]}, 200)
	if v, ok := clear["command"].(map[string]any)["version_labels"].([]any); !ok || len(v) != 0 {
		t.Fatal(clear)
	}
	call("PUT", "/editions/book/version-labels", clear["command"], 200)
	g = call("GET", "/work-organization/context?series_ids="+sid, nil, 200)
	clear = call("POST", "/work-organization/preview", map[string]any{"action": "series_members", "id": sid, "work_ids": []string{}, "expected_revisions": g["expected_revisions"]}, 200)
	if v, ok := clear["command"].(map[string]any)["work_ids"].([]any); !ok || len(v) != 0 {
		t.Fatal(clear)
	}
	call("PUT", "/series/"+sid+"/members", clear["command"], 200)
	g = contextFor("", "i:book")
	call("PATCH", "/works/i:book", map[string]any{"name_op": "set", "name": "Unique work name", "expected_revisions": g["expected_revisions"]}, 200)
	result := call("POST", "/editions/search", map[string]any{"query": "Unique work", "collapse": "works"}, 200)
	if result["total"] != float64(1) {
		t.Fatal(result)
	}
	result = call("POST", "/books/search", map[string]any{"query": "title:Unique", "collapse": "none"}, 200)
	if result["total"] != float64(0) {
		t.Fatal(result)
	}
	code(t, request(t, h, "POST", "/api/v1/works/i:book/split", `{}`, cookie, ""), 403)
	code(t, request(t, h, "GET", "/api/v1/works", "", nil, ""), 401)
}
