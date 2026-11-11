package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	rec "nyamangaserver.local/server/internal/recommendation"
	"nyamangaserver.local/server/internal/search"
	"nyamangaserver.local/server/internal/testfixture"
	"nyamangaserver.local/server/internal/userstate"
	"strings"
	"testing"
	"time"
)

func TestSeriesWorkerPauseRestartAndCancelThenStart(t *testing.T) {
	h, _, _ := metadataHandler(t)
	db := handlerDB(t, h)
	ctx := context.Background()
	for _, id := range []string{"second", "third"} {
		if e := testfixture.SeedEdition(ctx, db.Write, id, "lib", id+".cbz", id); e != nil {
			t.Fatal(e)
		}
	}
	start := func() *rec.Service {
		s := rec.New(db, h.catalog, &testfixture.RecommendationRunner{Delay: 40 * time.Millisecond}, h.c.DataDir, h.logger)
		s.Start(ctx)
		t.Cleanup(func() { c, cancel := context.WithTimeout(ctx, 3*time.Second); defer cancel(); s.Shutdown(c) })
		return s
	}
	wait := func(f func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !f() {
			if time.Now().After(deadline) {
				t.Fatal("series checkpoint timeout")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	s := start()
	j, e := s.StartSeries(ctx, "lib", "")
	if e != nil {
		t.Fatal(e)
	}
	wait(func() bool { r, _ := db.SeriesRun(ctx, j.ID, false); return r.FeaturesDone > 0 })
	if e = s.Control(ctx, j.ID, "pause"); e != nil {
		t.Fatal(e)
	}
	if e = s.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
	paused, _ := db.SeriesRun(ctx, j.ID, false)
	if len(paused.Features) != paused.FeaturesDone {
		t.Fatal("feature checkpoint lost", len(paused.Features), paused.FeaturesDone)
	}
	s = start()
	// Paused work remains paused after the worker restarts.
	time.Sleep(80 * time.Millisecond)
	current, _ := db.Job(ctx, j.ID)
	if current.State != "paused" {
		t.Fatal(current.State)
	}
	if e = s.Control(ctx, j.ID, "resume"); e != nil {
		t.Fatal(e)
	}
	wait(func() bool {
		v, _ := db.Job(ctx, j.ID)
		return v.State == "running" && v.Generation > current.Generation
	})
	old, _ := db.Job(ctx, j.ID)
	if e = s.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
	// An interrupted running task resumes from its persisted checkpoint.
	s = start()
	wait(func() bool { v, _ := db.Job(ctx, j.ID); return v.State == "completed" })
	after, _ := db.SeriesRun(ctx, j.ID, false)
	if after.FeaturesDone < paused.FeaturesDone || after.NextRow != len(after.Pool.Works) {
		t.Fatal(after.FeaturesDone, after.NextRow)
	}
	if len(after.Features) != len(after.Pool.Editions) {
		t.Fatal("resume did not retain all feature values")
	}
	if e = db.SaveSeriesFeature(ctx, *old, 0, after.Pool.Editions[0], nil); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal("old generation survived", e)
	}
	second, e := s.StartSeries(ctx, "lib", "")
	if e != nil {
		t.Fatal(e)
	}
	wait(func() bool { v, _ := db.Job(ctx, second.ID); return v.State == "running" })
	if e = s.Control(ctx, second.ID, "cancel"); e != nil {
		t.Fatal(e)
	}
	third, e := s.StartSeries(ctx, "lib", "")
	if e != nil {
		t.Fatal(e)
	}
	wait(func() bool { v, _ := db.Job(ctx, third.ID); return v.State == "completed" })
	cancelled, _ := db.Job(ctx, second.ID)
	if cancelled.State != "cancelled" {
		t.Fatal(cancelled.State)
	}
}

func TestSeriesHTTPContractRealTaskAndOrganization(t *testing.T)       { seriesHTTP(t, false) }
func TestAuthorizationEditorDeterministicSeriesDecisions(t *testing.T) { seriesHTTP(t, true) }
func seriesHTTP(t *testing.T, isEditor bool) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	h.WithOrganization(organization.New(db), search.New(db), userstate.New(db))
	ctx := context.Background()
	for _, id := range []string{"second", "third"} {
		if e := testfixture.SeedEdition(ctx, db.Write, id, "lib", id+".cbz", id); e != nil {
			t.Fatal(e)
		}
	}
	if isEditor {
		cookie, csrf, ctx = authorizedTestEditor(t, h, "lib")
	}
	svc := rec.New(db, h.catalog, &testfixture.RecommendationRunner{Delay: time.Millisecond}, h.c.DataDir, h.logger)
	h.WithRecommendations(svc)
	svc.Start(ctx)
	t.Cleanup(func() { c, cancel := context.WithTimeout(ctx, 5*time.Second); defer cancel(); svc.Shutdown(c) })
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
	code(t, request(t, h, "GET", "/api/v1/series-proposals", "", nil, ""), 401)
	code(t, request(t, h, "POST", "/api/v1/series-discovery-jobs", `{"library_id":"lib"}`, cookie, ""), 403)
	call("GET", "/series-proposals/missing", "", 404)
	start := call("POST", "/series-discovery-jobs", `{"library_id":"lib"}`, 202)
	job := start["job_id"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, e := db.Job(ctx, job)
		if e != nil {
			t.Fatal(e)
		}
		if j.State == "completed" {
			break
		}
		if j.State == "failed" || j.State == "paused" || time.Now().After(deadline) {
			t.Fatal(j)
		}
		time.Sleep(10 * time.Millisecond)
	}
	page := call("GET", "/series-proposals?library_id=lib", "", 200)
	items := page["items"].([]any)
	if len(items) != 1 {
		t.Fatal(page)
	}
	p := items[0].(map[string]any)
	cover, ok := p["cover"].(map[string]any)
	if !ok || cover["edition_id"] == "" || cover["work_id"] == "" {
		t.Fatal("missing proposal cover", p)
	}
	if total := call("GET", "/series-proposals?target_series_id=missing&alternatives=true&page_size=1", "", 200)["total"]; total != float64(0) {
		t.Fatal(total)
	}
	code(t, request(t, h, "GET", "/api/v1/series-proposals?alternatives=garbage", "", cookie, csrf), 400)
	code(t, request(t, h, "GET", "/api/v1/series-proposals?alternatives=", "", cookie, csrf), 400)
	code(t, request(t, h, "GET", "/api/v1/series-proposals?target_series_id="+strings.Repeat("x", 129), "", cookie, csrf), 400)
	id := p["id"].(string)
	call("GET", "/series-proposals/"+id, "", 200)
	call("GET", "/series-proposals/"+id+"/members?page_size=1&page=2", "", 200)
	call("GET", "/series-discovery-jobs/"+job+"/pending", "", 200)
	ids := p["new_work_ids"].([]any)
	raw, _ := json.Marshal(map[string]any{"expected_revision": p["revision"], "work_ids": ids[:2], "name": "Edited series", "order": ids[:2]})
	resolved := call("POST", "/series-proposals/"+id+"/resolve", string(raw), 200)
	id = resolved["id"].(string)
	for _, action := range []string{"dismiss", "restore"} {
		raw, _ = json.Marshal(map[string]any{"expected_revision": resolved["revision"]})
		resolved = call("POST", "/series-proposals/"+id+"/"+action, string(raw), 200)
	}
	raw, _ = json.Marshal(map[string]any{"expected_revision": resolved["revision"], "work_ids": resolved["new_work_ids"]})
	accepted := call("POST", "/series-proposals/"+id+"/accept", string(raw), 200)
	sid := accepted["target_series_id"].(string)
	if _, e = db.Write.Exec("INSERT INTO series VALUES('empty','lib','ZZ empty',1)"); e != nil {
		t.Fatal(e)
	}
	empty := call("GET", "/series/empty", "", 200)
	if cover, ok := empty["cover"]; !ok || cover != nil {
		t.Fatal("empty cover must be explicit null", empty)
	}
	series := call("GET", "/series/"+sid, "", 200)
	if series["cover"] == nil {
		t.Fatal("missing series cover", series)
	}
	ordered := call("GET", "/series/"+sid+"?summary=false", "", 200)
	if len(ordered["work_ids"].([]any)) != 2 || len(series["work_ids"].([]any)) != 0 {
		t.Fatal("explicit order and compact default", ordered, series)
	}
	code(t, request(t, h, "GET", "/api/v1/series/"+sid+"?summary=invalid", "", cookie, csrf), 400)
	seriesPage := call("GET", "/series?page_size=1", "", 200)
	if seriesPage["items"].([]any)[0].(map[string]any)["cover"] == nil {
		t.Fatal(seriesPage)
	}
	memberPage := call("GET", "/series/"+sid+"/members?page_size=1", "", 200)
	m := memberPage["items"].([]any)[0].(map[string]any)
	if _, ok := m["content_revision"]; !ok {
		t.Fatal(m)
	}
	if _, ok := m["cover_revision"]; !ok {
		t.Fatal(m)
	}
	call("POST", "/series-proposals/"+id+"/accept", string(raw), 200)
	// Unknown fields remain rejected. Custom accept requires the explicit
	// customize contract; legacy exact acceptance above is unchanged.
	code(t, request(t, h, "POST", "/api/v1/series-proposals/"+id+"/accept", `{"expected_revision":1,"work_ids":[],"unexpected":true}`, cookie, csrf), 400)
	raw, _ = json.Marshal(map[string]any{"expected_revision": accepted["revision"]})
	call("POST", "/series-proposals/"+id+"/undo", string(raw), 200)
	code(t, request(t, h, "GET", "/api/v1/series-proposals?page_size=101", "", cookie, csrf), 400)
	var works, editions int
	db.Read.QueryRow("SELECT count(*) FROM works").Scan(&works)
	db.Read.QueryRow("SELECT count(*) FROM editions").Scan(&editions)
	if works != 3 || editions != 3 {
		t.Fatal(works, editions)
	}
}
