package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/importflow"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/testfixture"
	"strings"
	"testing"
	"time"
)

func TestSourceSearchHTTPContractAndStaging(t *testing.T) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, e := db.Write.Exec(`INSERT OR IGNORE INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision) VALUES('lib','fixture','Anonymous','utf8','test',1)`)
	if e != nil {
		t.Fatal(e)
	}
	if e = testfixture.SeedEdition(ctx, db.Write, "search-edition", "lib", "anonymous.cbz", "Anonymous"); e != nil {
		t.Fatal(e)
	}
	_, e = db.Write.Exec(`INSERT INTO import_batches(id,library_id,request_key,stage,state) VALUES('search-batch','lib','search-batch','metadata','review');INSERT INTO import_members(batch_id,edition_id,change_kind,content_revision) VALUES('search-batch','search-edition','new',1)`)
	if e != nil {
		t.Fatal(e)
	}
	imports := importflow.New(db, h.catalog, h.logger, func() {}).WithCovers(&testfixture.Remote{}, h.c.DataDir)
	h.WithImports(imports)
	h.metadata.Start(ctx)
	t.Cleanup(func() { h.metadata.Shutdown(context.Background()) })
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
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		w := request(t, h, method, "/api/v1"+path, string(raw), cookie, csrf)
		code(t, w, want)
		if strings.Contains(w.Body.String(), `"Raw"`) || strings.Contains(w.Body.String(), `"result_json"`) {
			t.Fatal("raw response leaked")
		}
		r := httptest.NewRequest(method, "http://localhost/api/v1"+path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", h.c.PublicURL)
		r.Header.Set("X-CSRF-Token", csrf)
		route, params, e := router.FindRoute(r)
		if e != nil {
			t.Fatal(e)
		}
		in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		if want < 400 {
			if e = openapi3filter.ValidateRequest(ctx, in); e != nil {
				t.Fatal(path, e)
			}
		}
		out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: w.Code, Header: w.Header()}
		out.SetBodyBytes(w.Body.Bytes())
		if e = openapi3filter.ValidateResponse(ctx, out); e != nil {
			t.Fatal(path, e)
		}
		return w.Body.Bytes()
	}
	base := "/import-batches/search-batch/members/search-edition/source-searches"
	// Authentication and CSRF remain mandatory, before any remote work.
	code(t, request(t, h, "POST", "/api/v1"+base, `{}`, nil, ""), 401)
	code(t, request(t, h, "POST", "/api/v1"+base, `{}`, cookie, ""), 403)
	var v model.SourceSearch
	json.Unmarshal(call("POST", base, model.SourceSearchInput{Mode: "title", Query: "Anonymous", RequestKey: "one", ExpectedRevision: 1}, 202), &v)
	deadline := time.Now().Add(5 * time.Second)
	for {
		json.Unmarshal(call("GET", base+"/"+v.ID, nil, 200), &v)
		if v.State == "completed" {
			break
		}
		if v.State == "failed" || time.Now().After(deadline) {
			t.Fatal(v)
		}
		time.Sleep(15 * time.Millisecond)
	}
	var page model.SourceSearchPage
	json.Unmarshal(call("GET", base+"/"+v.ID+"/results?page=1&page_size=10", nil, 200), &page)
	if len(page.Items) != 2 || page.Items[0].State != "ready" {
		t.Fatal(page)
	}
	detailPath := base + "/" + v.ID + "/results/" + page.Items[0].ID
	call("GET", detailPath, nil, 200)
	call("GET", detailPath+"/cover?result_revision="+fmt.Sprint(page.Items[0].Revision), nil, 200)
	call("GET", detailPath+"/cover?result_revision=999", nil, 409)
	call("GET", strings.Replace(base, "search-edition", "not-member", 1)+"/"+v.ID, nil, 404)
	call("POST", base+"/"+v.ID+"/select", model.SearchSelect{ResultID: page.Items[0].ID, ExpectedRevision: v.Revision, ResultRevision: page.Items[0].Revision, MemberRevision: 1, EditionRevision: v.EditionRevision}, 200)
	var count int
	db.Read.QueryRow("SELECT count(*) FROM edition_sources").Scan(&count)
	if count != 0 {
		t.Fatal("HTTP selection adopted source")
	}
	var staged model.ImportCandidate
	json.Unmarshal(call("GET", "/import-batches/search-batch/members/search-edition/candidate", nil, 200), &staged)
	call("POST", "/import-batches/search-batch/reviews", map[string]any{"items": []model.ImportReview{{ID: "search-edition", ExpectedRevision: 2, EditionRevision: v.EditionRevision, CandidateRevision: staged.Revision, Decision: "accept"}}}, 200)
	db.Read.QueryRow("SELECT count(*) FROM edition_sources").Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
}
