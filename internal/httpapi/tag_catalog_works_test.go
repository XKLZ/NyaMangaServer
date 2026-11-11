package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	"nyamangaserver.local/server/internal/search"
	"nyamangaserver.local/server/internal/tagcatalog"
	"nyamangaserver.local/server/internal/testfixture"
	"nyamangaserver.local/server/internal/userstate"
)

func TestTagCatalogWorksHTTPContractAndOverviewReuse(t *testing.T) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	ctx := context.Background()
	h.WithTagCatalog(tagcatalog.New(db)).WithOrganization(organization.New(db), search.New(db), userstate.New(db))
	for _, id := range []string{"variant", "a-spare", "separate"} {
		if e := testfixture.SeedEdition(ctx, db.Write, id, "lib", id+".cbz", id+" heading"); e != nil {
			t.Fatal(e)
		}
	}
	for _, id := range []string{"variant", "separate"} {
		if e := db.ApplyOverrides(ctx, id, 1, map[string]model.Override{"tags": {Op: "set", Value: json.RawMessage(`[{"namespace":"artist","name":"author"}]`)}}); e != nil {
			t.Fatal(e)
		}
	}
	book, _ := db.Edition(ctx, "book")
	if e := db.ApplyOverrides(ctx, "book", book.Revision, map[string]model.Override{"title_original": {Op: "set", Value: json.RawMessage(`"首選原文"`)}}); e != nil {
		t.Fatal(e)
	}
	tx, e := db.Write.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`UPDATE editions SET work_id='i:book' WHERE id IN('variant','a-spare');DELETE FROM works WHERE id IN('i:variant','i:a-spare');
 UPDATE works SET name_override='Chosen Work',revision=5 WHERE id='i:book';
 UPDATE files SET content_revision=3,file_state='missing' WHERE edition_id='book';
 UPDATE files SET content_revision=9,file_state='corrupt' WHERE edition_id='a-spare';
 INSERT INTO cover_selections(edition_id,mode,content_revision,revision) VALUES('book','auto',3,6),('a-spare','auto',9,12);
 INSERT INTO series VALUES('series','lib','Anonymous Series',4);INSERT INTO series_members VALUES('series','i:book',1);`); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	id := catalogTagID("artist", "author")
	base := "/tag-catalog/" + id + "/works"
	doc, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers[0].URL = "http://localhost/api/v1"
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	call := func(path string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest("GET", "http://localhost/api/v1"+path, nil)
		r.AddCookie(cookie)
		route, params, e := router.FindRoute(r)
		if e != nil {
			t.Fatal(e)
		}
		in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		if want != 400 {
			if e = openapi3filter.ValidateRequest(ctx, in); e != nil {
				t.Fatal(path, e)
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, want)
		out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: w.Code, Header: w.Header(), Body: io.NopCloser(strings.NewReader(w.Body.String())), Options: &openapi3filter.Options{IncludeResponseStatus: true}}
		if e = openapi3filter.ValidateResponse(ctx, out); e != nil {
			t.Fatal(path, e)
		}
		v := map[string]any{}
		if e = json.Unmarshal(w.Body.Bytes(), &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	code(t, request(t, h, "GET", "/api/v1"+base+"?library_id=lib", "", nil, ""), 401)
	code(t, request(t, h, "POST", "/api/v1"+base+"?library_id=lib", `{}`, cookie, csrf), 405)
	first := call(base+"?library_id=lib&page_size=1", 200)
	if first["total"] != float64(2) {
		t.Fatal(first)
	}
	item := first["items"].([]any)[0].(map[string]any)
	cover := item["cover"].(map[string]any)
	series := item["series"].(map[string]any)
	if item["id"] != "i:book" || item["name"] != "Chosen Work" || item["title_original"] != "首選原文" || cover["edition_id"] != "book" || cover["content_revision"] != float64(3) || cover["cover_revision"] != float64(6) || series["name"] != "Anonymous Series" || series["position"] != float64(1) {
		t.Fatal(item)
	}
	for _, field := range []string{"relations", "edition_count", "decision", "inferred_edition_count", "state"} {
		if _, ok := item[field]; ok {
			t.Fatal("Work card exposed Edition diagnosis", field)
		}
	}
	second := call(base+"?library_id=lib&page_size=1&page=2&snapshot="+first["snapshot"].(string), 200)
	if second["items"].([]any)[0].(map[string]any)["id"] == item["id"] {
		t.Fatal("Work repeated across pages")
	}
	found := call(base+"?library_id=lib&q=variant", 200)
	if found["total"] != float64(1) {
		t.Fatal("nonpreferred tagged title missed", found)
	}
	found = call(base+"?library_id=lib&q=Chosen", 200)
	if found["total"] != float64(1) {
		t.Fatal(found)
	}
	found = call(base+"?library_id=lib&q=a-spare", 200)
	if found["total"] != float64(0) {
		t.Fatal("untagged member title matched", found)
	}
	call(base, 400)
	call(base+"?library_id=lib&page=2", 400)
	call(base+"?library_id=lib&page_size=101", 400)
	call(base+"?library_id=lib&page=0", 400)
	call(base+"?library_id=lib&sort=usage", 400)
	call(base+"?library_id=lib&partition=unclassified", 400)
	call(base+"?library_id=lib&related_tag_id="+id, 400)
	call(base+"?library_id=unknown", 404)
	call("/tag-catalog/unknown/works?library_id=lib", 404)
	old := call("/tag-catalog/"+id+"/editions?library_id=lib&page_size=1", 200)
	call(base+"?library_id=lib&page_size=1&snapshot="+old["snapshot"].(string), 409)
	if _, e = db.Write.Exec("UPDATE cover_selections SET revision=revision+1 WHERE edition_id='book'"); e != nil {
		t.Fatal(e)
	}
	stale := call(base+"?library_id=lib&page_size=1&page=2&snapshot="+first["snapshot"].(string), 409)
	if stale["error"].(map[string]any)["code"] != "tag_catalog_snapshot_expired" {
		t.Fatal(stale)
	}
	overview := call("/works/i:book", 200)
	if overview["name"] != "Chosen Work" || overview["series_id"] != "series" {
		t.Fatal("existing overview cannot be reused", overview)
	}
	members := call("/works/i:book/editions?page=1&page_size=1", 200)
	if members["total"] != float64(3) || len(members["items"].([]any)) != 1 {
		t.Fatal("existing member paging unavailable", members)
	}
	call("/works/i:book/editions?page=2&page_size=1", 200)
	if _, e = db.Write.Exec("UPDATE editions SET trashed_at=1 WHERE id='book';UPDATE libraries SET disabled=1 WHERE id='lib'"); e != nil {
		t.Fatal(e)
	}
	replaced := call(base+"?library_id=lib&page_size=1", 200)
	replacement := replaced["items"].([]any)[0].(map[string]any)["cover"].(map[string]any)
	if replacement["edition_id"] != "a-spare" || replacement["content_revision"] != float64(9) || replacement["cover_revision"] != float64(12) {
		t.Fatal(replacement)
	}
	if _, e = db.Write.Exec("UPDATE libraries SET trashed_at=1 WHERE id='lib'"); e != nil {
		t.Fatal(e)
	}
	call(base+"?library_id=lib", 404)
}
