package httpapi

import (
	"context"
	"encoding/json"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/organization"
	rec "nyamangaserver.local/server/internal/recommendation"
	"nyamangaserver.local/server/internal/search"
	"nyamangaserver.local/server/internal/testfixture"
	"nyamangaserver.local/server/internal/userstate"
	"strings"
	"testing"
)

func TestWorkDiscoveryHTTPContractAndSelectionProtection(t *testing.T) { workDiscoveryHTTP(t, false) }
func TestAuthorizationEditorDeterministicWorkDecisions(t *testing.T)   { workDiscoveryHTTP(t, true) }
func workDiscoveryHTTP(t *testing.T, isEditor bool) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	h.WithOrganization(organization.New(db), search.New(db), userstate.New(db))
	ctx := context.Background()
	if e := testfixture.SeedEdition(ctx, db.Write, "second", "lib", "second.cbz", "Anonymous second"); e != nil {
		t.Fatal(e)
	}
	if isEditor {
		cookie, csrf, ctx = authorizedTestEditor(t, h, "lib")
	}
	runner := &capabilityRunner{enabled: true}
	h.WithRecommendations(rec.New(db, h.catalog, runner, h.c.DataDir, h.logger))
	doc, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	if e = doc.Validate(ctx); e != nil {
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
		if e = openapi3filter.ValidateRequest(ctx, in); e != nil {
			t.Fatal(e)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, want)
		out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: w.Code, Header: w.Header()}
		out.SetBodyBytes(w.Body.Bytes())
		if e = openapi3filter.ValidateResponse(ctx, out); e != nil {
			t.Fatal(e)
		}
		var v map[string]any
		if e = json.Unmarshal(w.Body.Bytes(), &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	code(t, request(t, h, "GET", "/api/v1/work-proposals", "", nil, ""), 401)
	call("GET", "/work-proposals/absent", "", 404)
	call("POST", "/work-proposals/absent/accept", `{"expected_revision":1,"member_ids":["a","b"]}`, 404)
	cap := call("GET", "/settings/work-recommendations", "", 200)
	if cap["discovery"] != true || cap["proposals"] != true || cap["available"] != false || runner.calls != 0 {
		t.Fatal(cap, runner.calls)
	}
	code(t, request(t, h, "POST", "/api/v1/work-discovery-jobs", `{"library_id":"lib"}`, cookie, ""), 403)
	start := call("POST", "/work-discovery-jobs", `{"library_id":"lib","full_recompute":true}`, 202)
	id := start["job_id"].(string)
	stats := call("GET", "/work-discovery-jobs/"+id, "", 200)
	if stats["execution_version"] != rec.DiscoveryExecutionVersion || stats["baseline_state"] != "full_recompute_requested" || stats["resources"] == nil {
		t.Fatal(stats)
	}
	call("GET", "/works?min_editions=2&page_size=1", "", 200)
	code(t, request(t, h, "GET", "/api/v1/works?min_editions=invalid", "", cookie, csrf), 422)
	call("POST", "/work-discovery-jobs", `{"library_id":"lib"}`, 409)
	j, e := db.Claim(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.SaveDiscoveryDiagnostic(ctx, *j, "pairs", "discovery_worker_unavailable", 0); e != nil {
		t.Fatal(e)
	}
	stats = call("GET", "/work-discovery-jobs/"+id, "", 200)
	if stats["failure_reason"] != "discovery_worker_unavailable" || stats["failure_stage"] != "pairs" {
		t.Fatal(stats)
	}
	if e = db.SaveDiscoveryDiagnostic(ctx, *j, "", "", 0); e != nil {
		t.Fatal(e)
	}

	score := .9
	if e = db.SaveDiscoveryBlock(ctx, *j, rec.PairBlock{Row: 0, NextRow: 1, Compared: 1, HardBits: []byte{0}, Edges: []rec.PairEvidence{{A: 0, B: 1, Tier: "strong", Score: &score, Signal: json.RawMessage(`{"risks":[]}`)}}}); e != nil {
		t.Fatal(e)
	}
	if e = db.SaveDiscoveryBlock(ctx, *j, rec.PairBlock{Row: 1, NextRow: 2, Compared: 0, HardBits: []byte{}}); e != nil {
		t.Fatal(e)
	}
	if e = db.PublishDiscoveryProposals(ctx, *j); e != nil {
		t.Fatal(e)
	}
	if e = db.FinishBackground(ctx, *j, ""); e != nil {
		t.Fatal(e)
	}
	page := call("GET", "/work-proposals?library_id=lib", "", 200)
	rows := page["items"].([]any)
	if len(rows) != 1 {
		t.Fatal(page)
	}
	p := rows[0].(map[string]any)
	pid := p["id"].(string)
	call("GET", "/work-proposals/"+pid, "", 200)
	call("GET", "/work-proposals/"+pid+"/alternatives?page_size=1", "", 200)
	for _, body := range []string{`{"expected_revision":1}`, `{"expected_revision":1,"member_ids":[]}`, `{"expected_revision":1,"member_ids":["book","book"]}`, `{"expected_revision":1,"member_ids":["book","foreign"]}`} {
		for _, action := range []string{"resolve", "accept"} {
			code(t, request(t, h, "POST", "/api/v1/work-proposals/"+pid+"/"+action, body, cookie, csrf), 400)
		}
	}
	raw, _ := json.Marshal(map[string]any{"expected_revision": p["revision"], "member_ids": p["new_edition_ids"]})
	accepted := call("POST", "/work-proposals/"+pid+"/accept", string(raw), 200)
	call("POST", "/work-proposals/"+pid+"/accept", string(raw), 200)
	target := accepted["target_work_id"].(string)
	call("GET", "/work-proposals?target_work_id="+target+"&alternatives=true", "", 200)
	members := call("GET", "/works/"+target+"/editions", "", 200)
	if len(members["items"].([]any)) != 2 {
		t.Fatal(members)
	}
	context := call("GET", "/work-organization/context?work_ids="+target, "", 200)
	labels, _ := json.Marshal(map[string]any{"expected_revisions": context["expected_revisions"]})
	call("POST", "/works/"+target+"/version-labels", string(labels), 200)
	raw, _ = json.Marshal(map[string]any{"expected_revision": accepted["revision"]})
	call("POST", "/work-proposals/"+pid+"/undo", string(raw), 200)
	code(t, request(t, h, "GET", "/api/v1/work-proposals?page_size=101", "", cookie, csrf), 400)
}
