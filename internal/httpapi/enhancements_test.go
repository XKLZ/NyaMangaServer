package httpapi

import (
	"context"
	"encoding/json"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"io"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/enhancements"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
	"strings"
	"testing"
)

type httpEnhancementRuntime struct {
	drains int
}

func (r *httpEnhancementRuntime) DrainEnhancement(context.Context, string) error {
	r.drains++
	return nil
}
func (r *httpEnhancementRuntime) RestoreDictionary(context.Context) error { return nil }
func (r *httpEnhancementRuntime) EnhancementResources(context.Context) map[string]string {
	return map[string]string{model.EnhancementWork: "configured_unverified", model.EnhancementSeries: "not_configured", model.EnhancementDictionary: "not_downloaded"}
}
func TestEnhancementHTTPContractServiceStatusAndImpact(t *testing.T) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	runtime := &httpEnhancementRuntime{}
	settings := enhancements.New(db, runtime)
	runner := &capabilityRunner{enabled: true}
	h.WithEnhancements(settings, db).WithRecommendations(rec.New(db, h.catalog, runner, h.c.DataDir, h.logger))
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
		if want != 400 {
			if e = openapi3filter.ValidateRequest(context.Background(), in); e != nil {
				t.Fatal(path, e)
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, want)
		out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: w.Code, Header: w.Header(), Body: io.NopCloser(strings.NewReader(w.Body.String())), Options: &openapi3filter.Options{IncludeResponseStatus: true}}
		if e = openapi3filter.ValidateResponse(context.Background(), out); e != nil {
			t.Fatal(path, w.Body.String(), e)
		}
		v := map[string]any{}
		json.Unmarshal(w.Body.Bytes(), &v)
		return v
	}
	for _, path := range []string{"/settings/enhancements", "/settings/service"} {
		code(t, request(t, h, "GET", "/api/v1"+path, "", nil, ""), 401)
	}
	call("GET", "/settings/enhancements", "", 200)
	h.build = Build{}
	info := call("GET", "/settings/service", "", 200)
	if info["version"] != "unknown" || info["commit"] != "unknown" || info["database_schema"] != float64(handlerDB(t, h).Version) || info["database_baseline"] != "foundation-v1" {
		t.Fatal(info)
	}
	raw, _ := json.Marshal(info)
	if strings.Contains(string(raw), h.c.DataDir) {
		t.Fatal("host path leaked")
	}
	call("GET", "/settings/work-recommendations", "", 200)
	if runner.calls != 0 || runtime.drains != 0 {
		t.Fatal("read triggered work", runner.calls, runtime.drains)
	}
	code(t, request(t, h, "PATCH", "/api/v1/settings/enhancements", `{"expected_revision":1,"feature":"work_recommendations","enabled":false}`, cookie, ""), 403)
	call("PATCH", "/settings/enhancements", `{"expected_revision":1,"feature":"bad","enabled":false}`, 400)
	db.Write.Exec("INSERT INTO jobs(id,type,state,stage,generation,created_at,updated_at) VALUES('impact','work_recommendation','queued','processing',1,1,1)")
	v := call("GET", "/settings/enhancements", "", 200)
	feature := v["features"].(map[string]any)[model.EnhancementWork].(map[string]any)
	token := feature["impact_token"].(string)
	denied := call("PATCH", "/settings/enhancements", `{"expected_revision":1,"feature":"work_recommendations","enabled":false}`, 409)
	if denied["error"].(map[string]any)["code"] != "enhancement_impact_changed" {
		t.Fatal(denied)
	}
	body, _ := json.Marshal(map[string]any{"expected_revision": 1, "feature": model.EnhancementWork, "enabled": false, "confirm_pause": true, "impact_token": token})
	pending := call("PATCH", "/settings/enhancements", string(body), 202)
	f := pending["features"].(map[string]any)[model.EnhancementWork].(map[string]any)
	if f["enabled"] != false || f["state"] != "disabling" || f["transition"] == nil {
		t.Fatal(f)
	}
	// Both old and new execution endpoints share the same store gate.
	blocked := call("POST", "/work-recommendation-jobs", `{"edition_ids":["book"]}`, 409)
	if blocked["error"].(map[string]any)["code"] != "enhancement_disabled" {
		t.Fatal(blocked)
	}
	if runner.calls != 0 {
		t.Fatal("disabled request started Python")
	}
	if e = settings.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	stable := call("GET", "/settings/enhancements", "", 200)
	f = stable["features"].(map[string]any)[model.EnhancementWork].(map[string]any)
	if f["enabled"] != false || f["state"] != "stable" {
		t.Fatal(f)
	}
	call("PATCH", "/settings/enhancements", `{"expected_revision":1,"feature":"series_recommendations","enabled":false}`, 409)
	var events int
	db.Read.QueryRow("SELECT count(*) FROM events WHERE type='enhancements' AND resource_id='global'").Scan(&events)
	if events != 2 {
		t.Fatal(events)
	}
}
