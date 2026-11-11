package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/galleryhighlight"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/organization"
	"nyamangaserver.local/server/internal/search"
	"nyamangaserver.local/server/internal/userstate"
	"os"
	"strings"
	"testing"
)

func TestHealthMaintenanceAuthenticationAndReadRoutes(t *testing.T) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	h.WithOrganization(organization.New(db), search.New(db), userstate.New(db)).WithMaintenance(maintenance.New(db, h.c, h.catalog, h.metadata, h.logger)).WithGalleryHighlight(galleryhighlight.New(db))
	health := maintenance.NewHealth(db, h.c, h.catalog.Pool)
	h.WithHealth(health)
	doc, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers[0].URL = "http://localhost/api/v1"
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path, body string, c *http.Cookie, token, origin string, want int) {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", token)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, want)
		if strings.Contains(path, "/health-") {
			route, params, e := router.FindRoute(r)
			if e != nil {
				t.Fatal(e)
			}
			in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
			out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: w.Code, Header: w.Header()}
			out.SetBodyBytes(w.Body.Bytes())
			if e = openapi3filter.ValidateResponse(context.Background(), out); e != nil {
				t.Fatal(path, e)
			}
		}
	}
	call("POST", "/maintenance/health-checks", `{"request_key":"anonymous-check","mode":"quick"}`, cookie, csrf, h.c.PublicURL, 202)
	if _, e := db.Write.Exec("UPDATE health_state SET phase='draining'"); e != nil {
		t.Fatal(e)
	}
	call("POST", "/series", `{}`, nil, csrf, h.c.PublicURL, 401)
	call("POST", "/series", `{}`, cookie, csrf, "https://invalid.example", 403)
	call("POST", "/series", `{}`, cookie, "wrong", h.c.PublicURL, 403)
	call("POST", "/series", `{}`, cookie, csrf, h.c.PublicURL, 503)
	for _, path := range []string{"/books/search", "/editions/search"} {
		call("POST", path, `{"library_id":"lib","page":1,"page_size":50}`, cookie, csrf, h.c.PublicURL, 200)
	}
	for _, path := range []string{"/admin/books/search", "/admin/editions/search"} {
		call("POST", path, `{"page":1,"page_size":50,"collapse":"none","visibility":"active","include_disabled":true}`, cookie, csrf, h.c.PublicURL, 200)
	}
	call("POST", "/tag-suggestions", `{"namespace":"artist"}`, cookie, csrf, h.c.PublicURL, 200)
	call("GET", "/maintenance/health-state", ``, cookie, "", "", 200)
	call("POST", "/gallery-highlight/lookup", `{"gids":[1]}`, nil, "", "", 401)
}

func TestHealthFailureDoesNotLogHostPaths(t *testing.T) {
	h, _, _ := metadataHandler(t)
	var logs bytes.Buffer
	h.logger = slog.New(slog.NewTextHandler(&logs, nil))
	request := httptest.NewRequest("POST", "http://localhost/api/v1/maintenance/health-repair-plans", nil)
	response := httptest.NewRecorder()
	h.healthResponse(response, request, 0, nil, &os.PathError{Op: "open", Path: "/private/anonymous-secret-title", Err: errors.New("anonymous cause")})
	if response.Code != 500 {
		t.Fatal(response.Code)
	}
	if strings.Contains(logs.String(), "anonymous-secret") || strings.Contains(response.Body.String(), "/private/") {
		t.Fatal("host path leaked")
	}
}

func TestHealthCheckPlanExecuteRollbackHTTPContracts(t *testing.T) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	health := maintenance.NewHealth(db, h.c, h.catalog.Pool)
	h.WithHealth(health)
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
		in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		if want < 400 {
			if e = openapi3filter.ValidateRequest(ctx, in); e != nil {
				t.Fatal(path, e)
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, want)
		out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: w.Code, Header: w.Header()}
		out.SetBodyBytes(w.Body.Bytes())
		if e = openapi3filter.ValidateResponse(ctx, out); e != nil {
			t.Fatal(path, e)
		}
		v := map[string]any{}
		if e = json.Unmarshal(w.Body.Bytes(), &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	base := "/maintenance"
	created := call("POST", base+"/health-checks", `{"request_key":"contract-check","mode":"application"}`, 202)
	id := created["id"].(string)
	again := call("POST", base+"/health-checks", `{"request_key":"contract-check","mode":"application"}`, 202)
	if again["id"] != id {
		t.Fatal("check idempotency")
	}
	call("POST", base+"/health-checks", `{"request_key":"contract-check","mode":"quick"}`, 409)
	call("GET", base+"/health-checks?page=1&page_size=1", "", 200)
	health.Tick(ctx)
	detail := call("GET", base+"/health-checks/"+id, "", 200)
	if detail["state"] != "completed" {
		t.Fatal(detail)
	}
	findings := call("GET", base+"/health-checks/"+id+"/findings?status=confirmed&category=projections&page_size=1", "", 200)
	items := findings["items"].([]any)
	if len(items) != 1 {
		t.Fatal(findings)
	}
	finding := items[0].(map[string]any)["id"].(string)
	input := fmt.Sprintf(`{"request_key":"contract-plan","check_id":%q,"finding_ids":[%q]}`, id, finding)
	plan := call("POST", base+"/health-repair-plans", input, 200)
	pid := plan["id"].(string)
	call("GET", base+"/health-repair-plans/"+pid, "", 200)
	call("POST", base+"/health-repair-plans/"+pid+"/execute", `{"request_key":"contract-execute","expected_revision":1,"confirm_maintenance":false}`, 422)
	execution := `{"request_key":"contract-execute","expected_revision":1,"confirm_maintenance":true}`
	repair := call("POST", base+"/health-repair-plans/"+pid+"/execute", execution, 202)
	rid := repair["id"].(string)
	health.Tick(ctx)
	result := call("GET", base+"/health-repairs/"+rid, "", 200)
	if result["state"] != "completed" {
		t.Fatal(result)
	}
	duplicate := call("POST", base+"/health-repair-plans/"+pid+"/execute", execution, 202)
	if duplicate["id"] != rid {
		t.Fatal("execution idempotency")
	}
	rollback := fmt.Sprintf(`{"request_key":"contract-rollback","expected_revision":%v,"confirm_maintenance":true}`, result["revision"])
	call("POST", base+"/health-repairs/"+rid+"/rollback", rollback, 202)
	health.Tick(ctx)
	result = call("GET", base+"/health-repairs/"+rid, "", 200)
	if result["state"] != "rolled_back" {
		t.Fatal(result)
	}
	state := call("GET", base+"/health-state", "", 200)
	if state["phase"] != "idle" {
		t.Fatal(state)
	}
	call("POST", base+"/health-state/recover", fmt.Sprintf(`{"request_key":"contract-recover","expected_revision":%v,"confirm_maintenance":true}`, state["revision"]), 200)
	call("GET", base+"/health-checks/absent/findings", "", 404)
	call("POST", base+"/health-repair-plans", strings.Replace(input, "contract-plan", "contract-new-plan", 1), 409)
}
