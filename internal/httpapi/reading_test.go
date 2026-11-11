package httpapi

import (
	"context"
	"encoding/json"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	"nyamangaserver.local/server/internal/search"
	"nyamangaserver.local/server/internal/userstate"
	"strings"
	"testing"
)

func TestReadingHTTPRequiredCASIdempotencyAndBatchHydration(t *testing.T) {
	h, _, _ := metadataHandler(t)
	db := handlerDB(t, h)
	h.WithOrganization(organization.New(db), search.New(db), userstate.New(db))
	h.WithMaintenance(maintenance.New(db, h.c, h.catalog, h.metadata, h.logger))
	cookie, csrf, _ := authorizedTestEditor(t, h, "lib")
	if _, e := db.Write.Exec("UPDATE account_library_grants SET role='viewer'"); e != nil {
		t.Fatal(e)
	}
	doc, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	if e = doc.Validate(context.Background()); e != nil {
		t.Fatal(e)
	}
	doc.Servers[0].URL = "http://localhost/api/v1"
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		w := request(t, h, method, "/api/v1"+path, body, cookie, csrf)
		code(t, w, want)
		if strings.Contains(path, "/reading-") {
			r := httptest.NewRequest(method, "http://localhost/api/v1"+path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			route, params, err := router.FindRoute(r)
			if err != nil {
				t.Fatal(err)
			}
			input := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
			if want < 400 {
				if err = openapi3filter.ValidateRequest(context.Background(), input); err != nil {
					t.Fatal(err)
				}
			}
			output := &openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: w.Code, Header: w.Header()}
			output.SetBodyBytes(w.Body.Bytes())
			if err = openapi3filter.ValidateResponse(context.Background(), output); err != nil {
				t.Fatal(err)
			}
		}

		return w.Body.Bytes()
	}
	call("POST", "/editions/book/reading-sessions", `{"content_revision":1,"idempotency_key":"missing-version"}`, 400)
	call("POST", "/editions/book/reading-sessions", `{"expected_revision":null,"content_revision":1,"idempotency_key":"null-version"}`, 400)
	body := `{"expected_revision":0,"content_revision":1,"idempotency_key":"http-session"}`
	var session model.ReadingSession
	json.Unmarshal(call("POST", "/editions/book/reading-sessions", body, 200), &session)
	if session.State.ReadCount != 1 {
		t.Fatal(session)
	}
	repeat := call("POST", "/books/book/reading-sessions", body, 200)
	var again model.ReadingSession
	json.Unmarshal(repeat, &again)
	if again.ID != session.ID || again.State.ReadCount != 1 {
		t.Fatal("duplicate start", again)
	}
	call("PUT", "/editions/book/reading-state", `{"content_revision":1,"idempotency_key":"missing-cas","action":"complete"}`, 400)
	conflict := call("PUT", "/editions/book/reading-state", `{"expected_revision":0,"content_revision":1,"idempotency_key":"stale-cas-key","action":"complete"}`, 409)
	var current struct {
		State model.ReadingState `json:"current_state"`
	}
	if e := json.Unmarshal(conflict, &current); e != nil || current.State.Revision != 1 || current.State.ReadCount != 1 {
		t.Fatal("conflict missing current state", string(conflict), e)
	}
	call("PUT", "/editions/book/reading-state", `{"expected_revision":1,"content_revision":1,"idempotency_key":"position-key","action":"progress","page_index":1}`, 200)
	call("PUT", "/editions/book/reading-state", `{"expected_revision":1,"content_revision":1,"idempotency_key":"position-key","action":"progress","page_index":0}`, 409)
	details := call("GET", "/editions/book", "", 200)
	var book struct {
		Reading *model.ReadingState `json:"reading_state"`
	}
	json.Unmarshal(details, &book)
	if book.Reading == nil || book.Reading.ReadCount != 1 || book.Reading.PageIndex == nil || *book.Reading.PageIndex != 1 {
		t.Fatal("detail did not hydrate", string(details))
	}
	listing := call("POST", "/admin/books/search", `{"query":"","library_id":"lib","scope":"all","collapse":"none","page":1,"page_size":20,"sort":"title","order":"asc"}`, 200)
	var result struct {
		Items []struct {
			Reading *model.ReadingState `json:"reading_state"`
		} `json:"items"`
	}
	if e := json.Unmarshal(listing, &result); e != nil || len(result.Items) != 1 || result.Items[0].Reading == nil || result.Items[0].Reading.Revision != 2 {
		t.Fatal("batch reading hydration missing", string(listing), e)
	}
	state := call("GET", "/books/book/reading-state", "", 200)
	var after model.ReadingState
	json.Unmarshal(state, &after)
	if after.ReadCount != 1 || after.Revision != 2 {
		t.Fatal("GET counted reading", after)
	}
}
