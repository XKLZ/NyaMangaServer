package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/migrations"
)

type instanceLookupBody struct {
	io.Reader
	entered, release chan struct{}
	once             sync.Once
}

func (b *instanceLookupBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered); <-b.release })
	return b.Reader.Read(p)
}

func TestInstanceHistoryRequestKeyFindsLostAndLateUploadResponses(t *testing.T) {
	h, _ := testHandler(t, "http://localhost")
	db := handlerDB(t, h)
	h.c.DataDir = t.TempDir()
	cookie, csrf := sessionResponse(t, request(t, h, "POST", "/api/v1/auth/setup", `{"setup_token":"anonymous-setup","password":"anonymous password"}`, nil, ""))
	s := maintenance.NewInstance(db, h.c, maintenance.InstanceBuild{Version: "anonymous", Commit: "lookup-test"}, migrations.Files)
	h.WithInstance(s)
	doc, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers[0].URL = "http://localhost/api/v1"
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	lookup := func(kind, query string, authenticated bool, want int) []model.InstanceOperation {
		t.Helper()
		r := httptest.NewRequest("GET", "http://localhost/api/v1/maintenance/"+kind+query, nil)
		if authenticated {
			r.AddCookie(cookie)
		}
		route, params, e := router.FindRoute(r)
		if e != nil {
			t.Fatal(e)
		}
		input := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		if want == 200 {
			if e = openapi3filter.ValidateRequest(context.Background(), input); e != nil {
				t.Fatal(e)
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, want)
		out := &openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: w.Code, Header: w.Header()}
		out.SetBodyBytes(w.Body.Bytes())
		if e = openapi3filter.ValidateResponse(context.Background(), out); e != nil {
			t.Fatal(e)
		}
		if want != 200 {
			return nil
		}
		if strings.Contains(w.Body.String(), `"request_key"`) {
			t.Fatal("history exposed keys")
		}
		var items []model.InstanceOperation
		if e = json.Unmarshal(w.Body.Bytes(), &items); e != nil || items == nil {
			t.Fatalf("array contract: %s %v", w.Body, e)
		}
		return items
	}
	for _, kind := range []string{"backups", "restores"} {
		lookup(kind, "?request_key=anonymous-missing", false, 401)
		for _, query := range []string{"?request_key=", "?request_key=short", "?request_key=" + strings.Repeat("x", 129), "?request_key=anonymous-key-1&request_key=anonymous-key-2", "?request_key=%zz"} {
			lookup(kind, query, true, 422)
		}
	}
	before, e := db.InstanceState(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if got := lookup("restores", "?request_key=anonymous-lost-upload", true, 200); len(got) != 0 {
		t.Fatal(got)
	}
	var count int
	if e = db.Read.QueryRow("SELECT count(*) FROM instance_operations").Scan(&count); e != nil || count != 0 {
		t.Fatalf("missing lookup wrote data: %d %v", count, e)
	}
	after, e := db.InstanceState(context.Background())
	if e != nil || before != after {
		t.Fatalf("missing lookup changed state %+v %v", after, e)
	}
	upload := func(key string, body io.Reader) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost/api/v1/maintenance/restores", body)
		r.AddCookie(cookie)
		r.Header.Set("Origin", h.c.PublicURL)
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Content-Type", "application/zip")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	// Discard the successful upload response; recover exclusively by its key.
	code(t, upload("anonymous-lost-upload", strings.NewReader("anonymous staged ZIP bytes")), 201)
	code(t, upload("anonymous-lost-upload-extra", strings.NewReader("different staged upload")), 201)
	if _, _, e = db.CreateInstanceOperation(context.Background(), "anonymous-backup", "backup", "anonymous-backup-key"); e != nil {
		t.Fatal(e)
	}
	found := lookup("restores", "?request_key=anonymous-lost-upload&page_size=1", true, 200)
	if len(found) != 1 || found[0].State != "uploaded" {
		t.Fatalf("lost response not recovered: %+v", found)
	}
	for _, tc := range []struct{ kind, key string }{{"backups", "anonymous-lost-upload"}, {"restores", "anonymous-backup-key"}, {"restores", "anonymous-lost"}, {"restores", "' OR 1=1 --"}} {
		if got := lookup(tc.kind, "?request_key="+url.QueryEscape(tc.key), true, 200); len(got) != 0 {
			t.Fatal(got)
		}
	}
	if got := lookup("backups", "?request_key=anonymous-backup-key", true, 200); len(got) != 1 || got[0].ID != "anonymous-backup" {
		t.Fatal(got)
	}
	if got := lookup("restores", "", true, 200); len(got) != 2 {
		t.Fatal(got)
	}
	// The identity exists before a slow upload finishes. A late response must
	// resolve to that same identity, then permit explicit orphan abandonment.
	body := &instanceLookupBody{Reader: strings.NewReader("late anonymous upload"), entered: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-body.release:
		default:
			close(body.release)
		}
	}()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- upload("anonymous-late-upload", body) }()
	select {
	case <-body.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("upload did not enter IO")
	}
	pending := lookup("restores", "?request_key=anonymous-late-upload", true, 200)
	if len(pending) != 1 || pending[0].State != "pending" {
		close(body.release)
		t.Fatalf("pending identity missing: %+v", pending)
	}
	close(body.release)
	select {
	case w := <-done:
		code(t, w, 201)
	case <-time.After(3 * time.Second):
		t.Fatal("late upload did not finish")
	}
	late := lookup("restores", "?request_key=anonymous-late-upload", true, 200)
	if len(late) != 1 || late[0].ID != pending[0].ID || late[0].State != "uploaded" {
		t.Fatalf("late identity changed: %+v", late)
	}
	input, _ := json.Marshal(model.InstanceRequest{Expected: late[0].Revision})
	code(t, request(t, h, "POST", "/api/v1/maintenance/restores/"+late[0].ID+"/abandon", string(input), cookie, csrf), 200)
	recovered := lookup("restores", "?request_key=anonymous-late-upload", true, 200)
	if len(recovered) != 1 || recovered[0].ID != pending[0].ID || recovered[0].State != "abandoned" {
		t.Fatalf("late result not reclaimed: %+v", recovered)
	}
	// A client that already left the flow may abandon the recovered pending ID
	// before its old upload returns. The late writer's CAS must not revive it.
	inflight := &instanceLookupBody{Reader: strings.NewReader("abandoned in-flight upload"), entered: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-inflight.release:
		default:
			close(inflight.release)
		}
	}()
	lateDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { lateDone <- upload("anonymous-abandon-inflight", inflight) }()
	select {
	case <-inflight.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("inflight upload did not enter IO")
	}
	orphan := lookup("restores", "?request_key=anonymous-abandon-inflight", true, 200)
	if len(orphan) != 1 || orphan[0].State != "pending" {
		t.Fatalf("missing inflight identity: %+v", orphan)
	}
	input, _ = json.Marshal(model.InstanceRequest{Expected: orphan[0].Revision})
	code(t, request(t, h, "POST", "/api/v1/maintenance/restores/"+orphan[0].ID+"/abandon", string(input), cookie, csrf), 200)
	close(inflight.release)
	select {
	case w := <-lateDone:
		code(t, w, 409)
	case <-time.After(3 * time.Second):
		t.Fatal("inflight writer did not finish")
	}
	settled := lookup("restores", "?request_key=anonymous-abandon-inflight", true, 200)
	if len(settled) != 1 || settled[0].ID != orphan[0].ID || settled[0].State != "abandoned" {
		t.Fatalf("late writer revived abandoned identity: %+v", settled)
	}
}
