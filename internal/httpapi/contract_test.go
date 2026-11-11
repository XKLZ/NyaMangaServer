package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/model"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
)

func TestImplementedOpenAPIContract(t *testing.T) {
	ctx := context.Background()
	doc, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	if e = doc.Validate(ctx); e != nil {
		t.Fatal(e)
	}
	// The legacy router matches absolute URLs literally; resolve the contract's
	// same-origin server against this test's origin before routing requests.
	if len(doc.Servers) != 1 || doc.Servers[0].URL != "/api/v1" {
		t.Fatal("expected same-origin API server")
	}
	doc.Servers[0].URL = "http://localhost/api/v1"
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	h, _ := testHandler(t, "http://localhost")
	now := time.Now()
	h.svc.Now = func() time.Time { return now }
	var cookie, csrf string
	call := func(method, path, body string, want int) {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", h.c.PublicURL)
		if cookie != "" {
			r.Header.Set("Cookie", cookie)
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		route, params, e := router.FindRoute(r)
		if e != nil {
			t.Fatal(e)
		}
		input := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		// Validate valid requests before handlers consume their bodies.
		if e = openapi3filter.ValidateRequest(ctx, input); e != nil {
			t.Fatal(method, path, e)
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		code(t, out, want)
		response := &openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: out.Code, Header: out.Header()}
		response.SetBodyBytes(out.Body.Bytes())
		if e = openapi3filter.ValidateResponse(ctx, response); e != nil {
			t.Fatal(method, path, e)
		}
		if path == "/auth/setup" || path == "/auth/login" {
			if out.Code == 200 {
				c, s := sessionResponse(t, out)
				cookie = c.String()
				csrf = s
			}
		}
	}
	call("GET", "/auth/status", "", 200)
	call("GET", "/health/live", "", 200)
	call("GET", "/health/ready", "", 200)
	call("POST", "/auth/login", `{"password":"not initialized"}`, 401)
	call("POST", "/auth/setup", `{"setup_token":"anonymous-setup","password":"anonymous password"}`, 200)
	call("GET", "/auth/session", "", 200)
	call("GET", "/library-roots", "", 200)
	call("GET", "/diagnostics", "", 200)
	now = now.Add(time.Minute)
	call("PUT", "/auth/password", `{"current_password":"anonymous password","new_password":"another password"}`, 200)
	cookie = ""
	csrf = ""
	call("POST", "/auth/login", `{"password":"another password"}`, 200)
	call("POST", "/auth/logout", "", 204)
}

func TestCatalogOpenAPIContract(t *testing.T) {
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
	h, cookie, csrf := catalogHandler(t)
	call := func(method, path, body string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", h.c.PublicURL)
		r.AddCookie(cookie)
		r.Header.Set("X-CSRF-Token", csrf)
		route, params, e := router.FindRoute(r)
		if e != nil {
			t.Fatal(path, e)
		}
		input := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		if e = openapi3filter.ValidateRequest(ctx, input); e != nil {
			t.Fatal(path, e)
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		code(t, out, want)
		response := &openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: out.Code, Header: out.Header()}
		response.SetBodyBytes(out.Body.Bytes())
		if strings.HasPrefix(out.Header().Get("Content-Type"), "image/") {
			input.Options.ExcludeResponseBody = true
		}
		if e = openapi3filter.ValidateResponse(ctx, response); e != nil {
			t.Fatal(path, e)
		}
		var result map[string]any
		if !strings.HasPrefix(out.Header().Get("Content-Type"), "image/") {
			if e = json.Unmarshal(out.Body.Bytes(), &result); e != nil {
				t.Fatal(e)
			}
		}
		return result
	}
	l := call("POST", "/libraries", `{"root_key":"fixture","display_name":"匿名书库"}`, 201)
	id := l["id"].(string)
	call("GET", "/libraries", "", 200)
	call("PATCH", "/libraries/"+id, `{"expected_revision":1,"scan_interval_minutes":null}`, 200)
	j := call("POST", "/libraries/"+id+"/scans", `{}`, 202)
	jobID := j["job_id"].(string)
	deadline := time.Now().Add(60 * time.Second)
	for {
		v, e := h.catalog.Job(ctx, jobID)
		if e != nil {
			t.Fatal(e)
		}
		if v.State == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scan timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
	call("GET", "/jobs", "", 200)
	call("GET", "/jobs/"+jobID, "", 200)
	call("GET", "/jobs/"+jobID+"/items?page=1&page_size=50", "", 200)
	call("GET", "/books?page=1&page_size=50", "", 200)
	books, _, e := h.catalog.Editions(ctx, id, "available", 1, 50)
	if e != nil {
		t.Fatal(e)
	}
	base := "/books/" + books[0].ID
	call("GET", base, "", 200)
	call("GET", base+"/pages?content_revision=1", "", 200)
	call("GET", base+"/cover?content_revision=1", "", 200)
	call("GET", base+"/pages/1/image?content_revision=1", "", 200)
	blocked := make(chan struct{}, 1)
	h.catalog.BeforePublish = func(ctx context.Context, _ model.Job, _ model.Item) {
		select {
		case blocked <- struct{}{}:
		default:
		}
		<-ctx.Done()
	}
	next := call("POST", "/libraries/"+id+"/scans", `{}`, 202)
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("scan not running")
	}
	call("POST", "/jobs/"+next["job_id"].(string)+"/cancel", "", 202)
}
