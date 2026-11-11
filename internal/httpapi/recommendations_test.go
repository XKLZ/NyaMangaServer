package httpapi

import (
	"context"
	"encoding/json"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"net/http/httptest"
	rec "nyamangaserver.local/server/internal/recommendation"
	"nyamangaserver.local/server/internal/testfixture"
	"strings"
	"testing"
)

type capabilityRunner struct {
	enabled bool
	calls   int
}

func (r *capabilityRunner) Enabled() bool { return r.enabled }
func (*capabilityRunner) Close() error    { return nil }
func (r *capabilityRunner) Call(ctx context.Context, operation string, input, output any) (rec.Resources, error) {
	r.calls++
	if operation != "capabilities" {
		panic("HTTP capability probe initiated inference")
	}
	raw := []byte(`{"available":true,"cpu_threads":1}`)
	return rec.Resources{}, json.Unmarshal(raw, output)
}
func TestRecommendationHTTPContractAndAuthentication(t *testing.T) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	ctx := context.Background()
	for _, id := range []string{"target", "miss"} {
		if e := testfixture.SeedEdition(ctx, db.Write, id, "lib", id+".cbz", id); e != nil {
			t.Fatal(e)
		}
	}
	runner := &capabilityRunner{}
	h.WithRecommendations(rec.New(db, h.catalog, runner, h.c.DataDir, h.logger))
	document, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	if e = document.Validate(ctx); e != nil {
		t.Fatal(e)
	}
	document.Servers[0].URL = "http://localhost/api/v1"
	router, e := legacy.NewRouter(document)
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
		if e = openapi3filter.ValidateRequest(ctx, in); e != nil {
			t.Fatal(path, e)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, want)
		out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: w.Code, Header: w.Header()}
		out.SetBodyBytes(w.Body.Bytes())
		if e = openapi3filter.ValidateResponse(ctx, out); e != nil {
			t.Fatal(path, e)
		}
		var result map[string]any
		if e = json.Unmarshal(w.Body.Bytes(), &result); e != nil {
			t.Fatal(e)
		}
		return result
	}
	code(t, request(t, h, "GET", "/api/v1/settings/work-recommendations", "", nil, ""), 401)
	disabled := call("GET", "/settings/work-recommendations", "", 200)
	if disabled["available"] != false || runner.calls != 0 {
		t.Fatal(disabled, runner.calls)
	}
	call("POST", "/work-recommendation-jobs", `{"edition_ids":["book"]}`, 503)
	runner.enabled = true
	cap := call("GET", "/settings/work-recommendations", "", 200)
	if cap["available"] != false || runner.calls != 0 {
		t.Fatal(cap)
	}
	code(t, request(t, h, "POST", "/api/v1/work-recommendation-jobs", `{"edition_ids":["book"]}`, cookie, ""), 403)
	call("POST", "/work-recommendation-jobs", `{"edition_ids":["missing"]}`, 404)
	started := call("POST", "/work-recommendation-jobs", `{"edition_ids":["book"]}`, 202)
	id := started["job_id"].(string)
	call("POST", "/work-recommendation-jobs", `{"edition_ids":["book"]}`, 409)
	call("POST", "/jobs/"+id+"/pause", `{}`, 202)
	call("POST", "/jobs/"+id+"/resume", `{}`, 202)
	j, e := db.Claim(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	items, e := db.Items(ctx, id, 1, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.PublishRecommendations(ctx, *j, items[0], rec.Result{Algorithm: rec.Algorithm, MaxResults: 20, CandidateCount: 1, Items: []rec.Candidate{{WorkID: "i:target", QueryID: "book", Tier: "strong", SupportingID: "target", Algorithm: rec.Algorithm}}}); e != nil {
		t.Fatal(e)
	}
	if e = db.FinishBackground(ctx, *j, ""); e != nil {
		t.Fatal(e)
	}
	page := call("GET", "/work-recommendations?job_id="+id, "", 200)
	rows := page["items"].([]any)
	recordID := rows[0].(map[string]any)["id"].(string)
	call("POST", "/work-recommendations/"+recordID+"/feedback", `{"relation":"same_series","expected_revision":1,"note":"匿名反馈"}`, 200)
	call("POST", "/work-recommendations/"+recordID+"/feedback", `{"relation":"unrelated","expected_revision":1}`, 409)
	call("POST", "/work-recommendation-jobs/"+id+"/misses", `{"query_edition_id":"book","target_work_id":"i:miss","note":"匿名漏报"}`, 200)
	misses := call("GET", "/work-recommendation-jobs/"+id+"/misses", "", 200)
	if misses["total"] != float64(1) {
		t.Fatal(misses)
	}
	preview := call("POST", "/work-recommendations/"+recordID+"/preview", `{"action":"edition_move","edition_id":"book","target_work_id":"i:target"}`, 200)
	organization := preview["organization"].(map[string]any)
	raw, _ := json.Marshal(map[string]any{"expected_revision": preview["revision"], "command": organization["command"]})
	call("POST", "/work-recommendations/"+recordID+"/accept", string(raw), 200)
	call("POST", "/work-recommendations/"+recordID+"/accept", string(raw), 200)
	call("POST", "/work-recommendations/"+recordID+"/feedback", `{"relation":"uncertain","expected_revision":3}`, 409)
	exported := call("GET", "/work-recommendation-jobs/"+id+"/feedback-export", "", 200)
	if exported["schema"] != "work-recommendation-feedback-v1" {
		t.Fatal(exported)
	}
	code(t, request(t, h, "GET", "/api/v1/work-recommendations?page_size=51", "", cookie, csrf), 400)
}
