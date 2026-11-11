package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"io"
	"net/http"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
	"strings"
	"testing"
	"time"
)

type instanceSlowReader struct {
	reader *bytes.Reader
	delay  time.Duration
}

func (r *instanceSlowReader) Read(p []byte) (int, error) {
	if r.delay != 0 {
		time.Sleep(r.delay)
		r.delay = 0
	}
	return r.reader.Read(p)
}

func TestInstanceAuthenticatedTransferOverridesOnlyFiniteRouteDeadline(t *testing.T) {
	h, _ := testHandler(t, "http://localhost")
	h.requestTimeout = 100 * time.Millisecond
	db := handlerDB(t, h)
	h.c.DataDir = t.TempDir()
	cookie, csrf := sessionResponse(t, request(t, h, "POST", "/api/v1/auth/setup", `{"setup_token":"anonymous-setup","password":"anonymous password"}`, nil, ""))
	s := maintenance.NewInstance(db, h.c, maintenance.InstanceBuild{Version: "dev", Commit: "deadline-fixture"}, migrations.Files)
	h.WithInstance(s)
	server := httptest.NewUnstartedServer(h)
	server.Config.ReadTimeout = 150 * time.Millisecond
	server.Config.WriteTimeout = 150 * time.Millisecond
	server.Start()
	defer server.Close()
	reader := &instanceSlowReader{reader: bytes.NewReader([]byte("anonymous upload staged for later validation")), delay: 350 * time.Millisecond}
	r, e := http.NewRequest("POST", server.URL+"/api/v1/maintenance/restores", reader)
	if e != nil {
		t.Fatal(e)
	}
	r.ContentLength = int64(reader.reader.Len())
	r.Header.Set("Content-Type", "application/zip")
	r.Header.Set("Idempotency-Key", "slow-transfer-fixture")
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("Origin", h.c.PublicURL)
	r.Header.Set("Expect", "100-continue")
	r.AddCookie(cookie)
	client := http.Client{Timeout: 5 * time.Second}
	response, e := client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	body, e := io.ReadAll(response.Body)
	if e != nil {
		t.Fatal(e)
	}
	if response.StatusCode != 201 {
		t.Fatalf("finite bulk deadline not applied: %d %s", response.StatusCode, body)
	}
	if server.Config.ReadTimeout != 150*time.Millisecond || server.Config.WriteTimeout != 150*time.Millisecond {
		t.Fatal("global server timeouts were changed")
	}
}

func TestInstanceBulkContextIsAuthenticatedFiniteAndClientCancellable(t *testing.T) {
	h, _ := testHandler(t, "http://localhost")
	db := handlerDB(t, h)
	h.c.DataDir = t.TempDir()
	cookie, csrf := sessionResponse(t, request(t, h, "POST", "/api/v1/auth/setup", `{"setup_token":"anonymous-setup","password":"anonymous password"}`, nil, ""))
	h.WithInstance(maintenance.NewInstance(db, h.c, maintenance.InstanceBuild{Version: "dev", Commit: "deadline-context"}, migrations.Files))
	probe := "/api/v1/maintenance/restores/anonymous-probe/preview"
	entered := make(chan context.Context, 2)
	h.protected.HandleFunc(probe, func(w http.ResponseWriter, r *http.Request) {
		if actor, ok := model.Actor(r.Context()); !ok || actor.ID == "" || actor.Role != "admin" {
			t.Error("bulk request lost authenticated actor")
		}
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Error("bulk deadline missing")
		}
		if remaining := time.Until(deadline); remaining < 29*time.Minute || remaining > 30*time.Minute {
			t.Errorf("bulk still bounded by ordinary parent: %v", remaining)
		}
		if r.Header.Get("X-Anonymous-Wait") == "cancel" {
			entered <- r.Context()
			<-r.Context().Done()
			w.WriteHeader(204)
			return
		}
		entered <- r.Context()
		w.WriteHeader(200)
	})
	h.mux.HandleFunc("/anonymous-ordinary-deadline", func(w http.ResponseWriter, r *http.Request) {
		deadline, ok := r.Context().Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining < 29*time.Second || remaining > 30*time.Second {
			t.Errorf("ordinary deadline changed: %v", remaining)
		}
		w.WriteHeader(200)
	})
	code(t, request(t, h, "GET", "/anonymous-ordinary-deadline", "", nil, ""), 200)
	code(t, request(t, h, "POST", probe, `{}`, nil, csrf), 401)
	select {
	case <-entered:
		t.Fatal("unauthenticated bulk route entered")
	default:
	}
	code(t, request(t, h, "POST", probe, `{}`, cookie, csrf), 200)
	<-entered
	original, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "http://localhost"+probe, strings.NewReader(`{}`)).WithContext(original)
	r.AddCookie(cookie)
	r.Header.Set("Origin", h.c.PublicURL)
	r.Header.Set("X-CSRF-Token", csrf)
	r.Header.Set("X-Anonymous-Wait", "cancel")
	done := make(chan struct{})
	w := httptest.NewRecorder()
	go func() { h.ServeHTTP(w, r); close(done) }()
	var operation context.Context
	select {
	case operation = <-entered:
	case <-time.After(time.Second):
		t.Fatal("bulk route not reached")
	}
	cancel()
	select {
	case <-operation.Done():
	case <-time.After(time.Second):
		t.Fatal("client cancellation was detached")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not leave after disconnect")
	}
	code(t, w, 204)
}

func TestInstanceHTTPContractAuthenticationTransferAndRestoreSummary(t *testing.T) {
	h, _ := testHandler(t, "http://localhost")
	db := handlerDB(t, h)
	h.c.DataDir = t.TempDir()
	cookie, csrf := sessionResponse(t, request(t, h, "POST", "/api/v1/auth/setup", `{"setup_token":"anonymous-setup","password":"anonymous password"}`, nil, ""))
	pool := comic.NewPool()
	defer pool.Close()
	health := maintenance.NewHealth(db, h.c, pool)
	h.WithHealth(health)
	s := maintenance.NewInstance(db, h.c, maintenance.InstanceBuild{Version: "anonymous-dev", Commit: "anonymous-http"}, migrations.Files)
	s.Inspect = store.ValidateInstanceSchema
	s.PrepareDatabase = store.PrepareInstanceDatabase
	s.Drain = h.DrainHealthHTTP
	h.WithInstance(s)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	doc, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers[0].URL = "http://localhost/api/v1"
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path string, body []byte, authn bool, csrfToken, origin, media, key string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost/api/v1"+path, bytes.NewReader(body))
		r.Header.Set("Content-Type", media)
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", csrfToken)
		r.Header.Set("Idempotency-Key", key)
		if authn {
			r.AddCookie(cookie)
		}
		route, params, e := router.FindRoute(r)
		if e != nil {
			t.Fatal(path, e)
		}
		input := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		if want < 400 && media != "application/zip" {
			if e = openapi3filter.ValidateRequest(context.Background(), input); e != nil {
				t.Fatal(path, e)
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, want)
		if !strings.HasSuffix(path, "/download") {
			out := &openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: w.Code, Header: w.Header()}
			out.SetBodyBytes(w.Body.Bytes())
			if e = openapi3filter.ValidateResponse(context.Background(), out); e != nil {
				t.Fatal(path, e)
			}
		}
		return w
	}
	body := []byte(`{"request_key":"anonymous-http-backup"}`)
	call("POST", "/maintenance/backups", body, false, csrf, h.c.PublicURL, "application/json", "", 401)
	call("POST", "/maintenance/backups", body, true, csrf, "https://invalid.example", "application/json", "", 403)
	call("POST", "/maintenance/backups", body, true, "wrong", h.c.PublicURL, "application/json", "", 403)
	w := call("POST", "/maintenance/backups", body, true, csrf, h.c.PublicURL, "application/json", "", 202)
	var operation model.InstanceOperation
	if e = json.Unmarshal(w.Body.Bytes(), &operation); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		v, e := s.Operation(context.Background(), operation.ID)
		if e != nil {
			t.Fatal(e)
		}
		if v.State == "completed" {
			break
		}
		if v.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("backup %+v", v)
		}
		time.Sleep(10 * time.Millisecond)
	}
	call("GET", "/maintenance/backups/"+operation.ID+"/download", nil, false, "", "", "", "", 401)
	downloaded := call("GET", "/maintenance/backups/"+operation.ID+"/download", nil, true, "", "", "", "", 200)
	if downloaded.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private download cache policy missing")
	}
	uploaded := call("POST", "/maintenance/restores", downloaded.Body.Bytes(), true, csrf, h.c.PublicURL, "application/zip", "anonymous-http-upload", 201)
	if e = json.Unmarshal(uploaded.Body.Bytes(), &operation); e != nil {
		t.Fatal(e)
	}
	call("POST", "/maintenance/restores/"+operation.ID+"/validate", []byte(fmt.Sprintf(`{"expected_revision":%d}`, operation.Revision)), true, csrf, h.c.PublicURL, "application/json", "", 202)
	deadline = time.Now().Add(15 * time.Second)
	for {
		v, e := s.Operation(context.Background(), operation.ID)
		if e != nil {
			t.Fatal(e)
		}
		if v.State == "validated" {
			operation = v
			break
		}
		if v.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("restore %+v", v)
		}
		time.Sleep(10 * time.Millisecond)
	}
	detail := call("GET", "/maintenance/restores/"+operation.ID, nil, true, "", "", "", "", 200)
	var parsed maintenance.InstanceRestoreDetail
	if e = json.Unmarshal(detail.Body.Bytes(), &parsed); e != nil || parsed.Summary == nil || parsed.Summary.Schema != db.Version {
		t.Fatalf("missing source summary %v %s", e, detail.Body.String())
	}
	preview := call("POST", "/maintenance/restores/"+operation.ID+"/preview", []byte(fmt.Sprintf(`{"expected_revision":%d,"root_mappings":[],"acknowledge_stat_only":true}`, operation.Revision)), true, csrf, h.c.PublicURL, "application/json", "", 200)
	var p maintenance.InstanceRestorePreview
	if e = json.Unmarshal(preview.Body.Bytes(), &p); e != nil {
		t.Fatal(e)
	}
	if p.Space.RequiredAdditionalBytes <= 0 {
		t.Fatal("no space estimate")
	}
	call("GET", "/maintenance/restores/"+operation.ID, nil, true, "", "", "", "", 200)
	call("POST", "/maintenance/restores/"+operation.ID+"/confirm", []byte(fmt.Sprintf(`{"expected_revision":%d,"request_key":"anonymous-confirm"}`, p.Operation.Revision)), true, csrf, h.c.PublicURL, "application/json", "", 202)
	call("GET", "/maintenance/instance-activation", nil, true, "", "", "", "", 200)
	call("POST", "/maintenance/backups", []byte(`{"request_key":"anonymous-conflicting-backup"}`), true, csrf, h.c.PublicURL, "application/json", "", 202)
	// Creation is durable; the worker rejects the incompatible active barrier.
}
