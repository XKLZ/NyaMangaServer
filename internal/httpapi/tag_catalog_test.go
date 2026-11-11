package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"io"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/tagcatalog"
	"strings"
	"testing"
)

func catalogTagID(ns, name string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(ns+"\x00"+name)))
}
func TestTagCatalogHTTPContractPermissionsAndCAS(t *testing.T) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	h.WithTagCatalog(tagcatalog.New(db))
	ctx := context.Background()
	if e := db.ApplyOverrides(ctx, "book", 1, map[string]model.Override{"tags": {Op: "set", Value: json.RawMessage(`[{"namespace":"parody","name":"alpha"},{"namespace":"character","name":"hero"}]`)}}); e != nil {
		t.Fatal(e)
	}
	p, c := catalogTagID("parody", "alpha"), catalogTagID("character", "hero")
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
			t.Fatal(e)
		}
		in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		// Invalid-input cases deliberately test server validation; valid traffic must
		// satisfy the documented request schema as well as the returned response.
		if want != 400 {
			if e = openapi3filter.ValidateRequest(ctx, in); e != nil {
				t.Fatal(method, path, e)
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, want)
		validation := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: w.Code, Header: w.Header(), Body: io.NopCloser(strings.NewReader(w.Body.String())), Options: &openapi3filter.Options{IncludeResponseStatus: true}}
		if e = openapi3filter.ValidateResponse(ctx, validation); e != nil {
			t.Fatal(method, path, w.Body.String(), e)
		}
		out := map[string]any{}
		if e = json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	for _, route := range []string{"/tag-catalog?library_id=lib", "/tag-catalog/" + p + "/relations?library_id=lib"} {
		code(t, request(t, h, "GET", "/api/v1"+route, "", nil, ""), 401)
	}
	first := call("GET", "/tag-catalog?library_id=lib&page_size=1", "", 200)
	call("GET", "/tag-catalog", "", 400)
	call("GET", "/tag-catalog?library_id=lib&page=2", "", 400)
	call("GET", "/tag-catalog?library_id=lib&category=bogus", "", 400)
	call("GET", "/tag-catalog?library_id=missing", "", 404)
	call("GET", "/tag-catalog/"+p+"?library_id=lib", "", 200)
	call("GET", "/tag-catalog/"+p+"/relations?library_id=lib", "", 200)
	call("GET", "/tag-catalog/"+c+"/relations?library_id=lib", "", 200)
	call("GET", "/tag-catalog/"+p+"/editions?library_id=lib&related_tag_id="+c+"&partition=evidence", "", 200)

	relationBase := "/tag-catalog/" + p + "/relations?library_id=lib"
	display := call("GET", relationBase+"&view=display", "", 200)
	if display["total"] != float64(1) {
		t.Fatal(display)
	}
	all := call("GET", relationBase, "", 200)
	explicit := call("GET", relationBase+"&view=all", "", 200)
	if all["snapshot"] != explicit["snapshot"] {
		t.Fatal("default snapshot compatibility")
	}
	staleView := call("GET", relationBase+"&snapshot="+display["snapshot"].(string), "", 409)
	if staleView["error"].(map[string]any)["code"] != "tag_catalog_snapshot_expired" {
		t.Fatal(staleView)
	}
	for _, suffix := range []string{"&view=unknown", "&view=", "&view=all&view=display"} {
		call("GET", relationBase+suffix, "", 400)
	}
	for _, path := range []string{"/tag-catalog", "/tag-catalog/" + p, "/tag-catalog/" + p + "/editions", "/tag-catalog/" + p + "/works"} {
		for _, view := range []string{"all", "display", "unknown", ""} {
			call("GET", path+"?library_id=lib&view="+view, "", 400)
		}
	}
	for _, param := range []string{"partition=all", "partition=", "related_tag_id="} {
		call("GET", "/tag-catalog/"+p+"/works?library_id=lib&"+param, "", 400)
	}
	code(t, request(t, h, "GET", "/api/v1"+relationBase+"&view=display", "", nil, ""), 401)
	relationPath := "/parody-character-relations/" + p + "/" + c
	code(t, request(t, h, "PUT", "/api/v1"+relationPath, `{"expected_revision":0,"decision":"confirmed"}`, cookie, ""), 403)
	r := httptest.NewRequest("PUT", "http://localhost/api/v1"+relationPath, strings.NewReader(`{"expected_revision":0,"decision":"confirmed"}`))
	r.AddCookie(cookie)
	r.Header.Set("Origin", "http://hostile.invalid")
	r.Header.Set("X-CSRF-Token", csrf)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	code(t, w, 403)
	confirmed := call("PUT", relationPath, `{"expected_revision":0,"decision":"confirmed"}`, 200)
	if confirmed["scope"] != "global" || confirmed["revision"] != float64(1) {
		t.Fatal(confirmed)
	}
	call("GET", "/tag-catalog?library_id=lib&page=2&page_size=1&snapshot="+first["snapshot"].(string), "", 409)
	call("DELETE", relationPath, `{"expected_revision":1}`, 200)
	stale := call("PUT", relationPath, `{"expected_revision":0,"decision":"excluded"}`, 409)
	if stale["error"].(map[string]any)["code"] != "tag_relation_revision_conflict" {
		t.Fatal(stale)
	}
	call("PUT", relationPath, `{"decision":"confirmed"}`, 400)
	suppression := "/editions/book/character-suppressions/" + p + "/" + c
	call("PUT", suppression, `{"expected_revision":0}`, 200)
	denied := call("GET", "/tag-catalog/"+p+"/editions?library_id=lib&related_tag_id="+c+"&partition=suppressed", "", 200)
	if denied["total"] != float64(1) {
		t.Fatal(denied)
	}
	suppressedDisplay := call("GET", relationBase+"&view=display", "", 200)
	if suppressedDisplay["total"] != float64(0) {
		t.Fatal(suppressedDisplay)
	}
	diagnostic := call("GET", relationBase, "", 200)
	if diagnostic["total"] != float64(1) {
		t.Fatal(diagnostic)
	}
	call("DELETE", suppression, `{"expected_revision":1}`, 200)
	call("PUT", suppression, `{"expected_revision":0}`, 409)
	call("GET", "/tag-catalog/"+p+"/editions?library_id=lib&partition=unclassified", "", 200)
	// Existing translation endpoint remains the source of human translation CAS.
	call("PUT", "/tags/"+p+"/translations/zh-CN", `{"expected_revision":0,"text":"原作译名"}`, 200)
	detail := call("GET", "/tag-catalog/"+p+"?library_id=lib", "", 200)
	if detail["translation"] != "原作译名" || detail["translation_revision"] != float64(1) {
		t.Fatal(detail)
	}
	if _, e = db.Write.Exec("UPDATE tag_relation_projection SET ready=0"); e != nil {
		t.Fatal(e)
	}
	unavailable := call("GET", "/tag-catalog?library_id=lib", "", 503)
	if unavailable["error"].(map[string]any)["code"] != "tag_projection_unavailable" {
		t.Fatal(unavailable)
	}
}
