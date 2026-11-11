package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/enhancements"
	"nyamangaserver.local/server/internal/galleryhighlight"
	"nyamangaserver.local/server/internal/model"
	"strings"
	"testing"
	"time"
)

func TestGalleryHTTPScopedProtocolContractAndRedaction(t *testing.T) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	runtime := &httpEnhancementRuntime{}
	settings := enhancements.New(db, runtime)
	svc := galleryhighlight.New(db)
	h.WithEnhancements(settings, db).WithGalleryHighlight(svc)
	logs := &bytes.Buffer{}
	h.logger = slog.New(slog.NewJSONHandler(logs, nil))
	doc, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers[0].URL = "http://localhost/api/v1"
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path, body string, session *http.Cookie, bearer string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", h.c.PublicURL)
		r.Header.Set("X-CSRF-Token", csrf)
		if session != nil {
			r.AddCookie(session)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		route, params, e := router.FindRoute(r)
		if e != nil {
			t.Fatal(e)
		}
		in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		if want != 400 && want != 413 {
			if e = openapi3filter.ValidateRequest(context.Background(), in); e != nil {
				t.Fatal(e)
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, want)
		if e = openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: w.Code, Header: w.Header(), Body: io.NopCloser(strings.NewReader(w.Body.String())), Options: &openapi3filter.Options{IncludeResponseStatus: true}}); e != nil {
			t.Fatal(e)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("CORS widened")
		}
		v := map[string]any{}
		json.Unmarshal(w.Body.Bytes(), &v)
		return v
	}
	base := "/settings/gallery-highlight/connections"
	call("GET", base, "", nil, "", 401)
	code(t, request(t, h, "POST", "/api/v1"+base, `{}`, cookie, ""), 403)
	call("POST", base, `{}`, cookie, "", 403)
	state, _ := db.Enhancements(context.Background())
	body, _ := json.Marshal(model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementGallery, Enabled: enhanceHTTPBool(true)})
	changed := call("PATCH", "/settings/enhancements", string(body), cookie, "", 200)
	feature := changed["features"].(map[string]any)[model.EnhancementGallery].(map[string]any)
	if feature["state"] != "stable" || feature["transition"] != nil || feature["affected_tasks"] != float64(0) || runtime.drains != 0 {
		t.Fatal("gallery invoked switch runtime")
	}
	for _, bad := range []string{`{"name":"browser","account_id":"other"}`, `{"name":"browser","expiry_days":0}`, `{"name":"browser","expiry_days":null}`, `{"name":"browser","expiry_days":31}`, `{"name":""}`, `{"name":"browser"}`, `{"expiry_days":90}`, `null`} {
		call("POST", base, bad, cookie, "", 400)
	}
	created := call("POST", base, `{}`, cookie, "", 201)
	token := created["token"].(string)
	conn := created["connection"].(map[string]any)
	id := conn["id"].(string)
	list := call("GET", base+"?page_size=1", "", cookie, "", 200)
	encoded, _ := json.Marshal(list)
	if strings.Contains(string(encoded), token) || strings.Contains(string(encoded), "token_hash") {
		t.Fatal("secret recoverable from list")
	}
	call("GET", base+"?account_id=other", "", cookie, "", 400)
	// Bearer endpoints never accept ambient session cookies or session secrets.
	call("GET", "/gallery-highlight/status", "", cookie, "", 401)
	call("GET", "/gallery-highlight/status", "", nil, cookie.Value, 401)
	call("GET", "/gallery-highlight/status", "", cookie, token, 401)
	call("GET", "/gallery-highlight/status?token="+token, "", nil, "", 401)
	call("GET", "/gallery-highlight/status?account_id=other", "", nil, token, 400)
	call("GET", "/gallery-highlight/status", `{"account_id":"other"}`, nil, token, 400)
	call("GET", "/gallery-highlight/status?account_id=%ZZ", "", nil, token, 400)
	status := call("GET", "/gallery-highlight/status", "", nil, token, 200)
	if status["protocol_version"] != float64(2) || status["connection_expires_at"] != nil || status["max_batch_size"] != float64(200) {
		t.Fatal(status)
	}
	lookup := call("POST", "/gallery-highlight/lookup", `{"provider":"ehentai","gallery_ids":["001","1","2"]}`, nil, token, 200)
	if len(lookup["items"].([]any)) != 2 || lookup["provider"] != "ehentai" || lookup["query_version"] == "" {
		t.Fatal(lookup)
	}
	for _, bad := range []string{`{"provider":"ehentai","gallery_ids":["0"]}`, `{"provider":"exhentai","gallery_ids":["1"]}`, `{"provider":"ehentai","gallery_ids":[1]}`, `{"provider":"ehentai","gallery_ids":["1"],"library_id":"other"}`, `{"provider":"ehentai","gallery_ids":["1"],"account_id":"other"}`} {
		call("POST", "/gallery-highlight/lookup", bad, nil, token, 400)
	}
	call("POST", "/gallery-highlight/lookup?library_id=other", `{"provider":"ehentai","gallery_ids":["1"]}`, nil, token, 400)
	call("POST", "/gallery-highlight/lookup", strings.Repeat(" ", 16385)+"{}", nil, token, 413)
	// New token has no management power, including as a cookie.
	scopedCookie := &http.Cookie{Name: cookie.Name, Value: token}
	for _, path := range []string{"/settings/enhancements", "/books/book/metadata", "/tag-catalog?library_id=lib", "/tag-catalog/unknown/images"} {
		code(t, request(t, h, "GET", "/api/v1"+path, "", scopedCookie, ""), 401)
		callRoute := httptest.NewRequest("GET", "http://localhost/api/v1"+path, nil)
		callRoute.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, callRoute)
		code(t, w, 401)
	}
	// No external Origin/CORS exception, and OPTIONS does not enable a preflight.
	r := httptest.NewRequest("GET", "http://localhost/api/v1/gallery-highlight/status", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Origin", "https://e-hentai.org")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	code(t, w, 200)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("external CORS")
	}
	r = httptest.NewRequest("OPTIONS", "http://localhost/api/v1/gallery-highlight/lookup", nil)
	r.Header.Set("Origin", "https://exhentai.org")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	code(t, w, 405)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("preflight widened")
	}
	rev := call("DELETE", base+"/"+id, `{"expected_revision":1}`, cookie, "", 200)
	if rev["state"] != "revoked" || rev["revision"] != float64(2) {
		t.Fatal(rev)
	}
	call("DELETE", base+"/"+id, `{"expected_revision":1}`, cookie, "", 409)
	call("GET", "/gallery-highlight/status", "", nil, token, 401)
	if strings.Contains(logs.String(), token) || strings.Contains(logs.String(), cookie.Value) || strings.Contains(logs.String(), "Authorization") || strings.Contains(logs.String(), "Filename") {
		t.Fatal("secret/private data in logs")
	}
}
func enhanceHTTPBool(v bool) *bool { return &v }

type failedGalleryRepository struct{ galleryhighlight.Repository }

func (failedGalleryRepository) GalleryGrant(context.Context, []byte, int64) (model.GalleryGrant, error) {
	return model.GalleryGrant{}, errors.New("RAW-SQL-PRIVATE-TOKEN-PATH")
}
func TestGalleryHTTPStorageErrorsAreSanitized(t *testing.T) {
	h, _, _ := metadataHandler(t)
	h.WithGalleryHighlight(galleryhighlight.New(failedGalleryRepository{}))
	logs := &bytes.Buffer{}
	h.logger = slog.New(slog.NewJSONHandler(logs, nil))
	r := httptest.NewRequest("GET", "http://localhost/api/v1/gallery-highlight/status", nil)
	r.Header.Set("Authorization", "Bearer ngh1_"+strings.Repeat("A", 43))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	code(t, w, 503)
	if strings.Contains(logs.String()+w.Body.String(), "RAW-SQL") || !strings.Contains(w.Body.String(), "gallery_highlight_unavailable") {
		t.Fatal("storage error not sanitized")
	}
	h2, cookie, _ := metadataHandler(t)
	db := handlerDB(t, h2)
	h2.WithGalleryHighlight(galleryhighlight.New(db))
	otherLogs := &bytes.Buffer{}
	h2.logger = slog.New(slog.NewJSONHandler(otherLogs, nil))
	if e := db.Close(); e != nil {
		t.Fatal(e)
	}
	w2 := request(t, h2, "GET", "/api/v1/settings/gallery-highlight/connections", "", cookie, "")
	code(t, w2, 503)
	if strings.Contains(otherLogs.String(), "database is closed") || strings.Contains(otherLogs.String(), cookie.Value) {
		t.Fatal("management auth storage error leaked")
	}

}
func TestGalleryHTTPRateRetryAfterAndFeatureGate(t *testing.T) {
	h, cookie, _ := metadataHandler(t)
	db := handlerDB(t, h)
	svc := galleryhighlight.New(db)
	h.WithGalleryHighlight(svc)
	ctx := context.Background()
	state, _ := db.Enhancements(ctx)
	on := true
	if _, e := db.ChangeEnhancement(ctx, model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementGallery, Enabled: &on}); e != nil {
		t.Fatal(e)
	}
	actor, e := h.svc.Session(ctx, cookie.Value)
	if e != nil {
		t.Fatal(e)
	}
	v, e := svc.Create(ctx, *actor, model.GalleryConnectionCreate{})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 61; i++ {
		r := httptest.NewRequest("GET", "http://localhost/api/v1/gallery-highlight/status", nil)
		r.Header.Set("Authorization", "Bearer "+v.Token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 200
		if i == 60 {
			want = 429
			if w.Header().Get("Retry-After") == "" {
				t.Fatal("Retry-After missing")
			}
		}
		code(t, w, want)
	}
	state, _ = db.Enhancements(ctx)
	on = false
	db.ChangeEnhancement(ctx, model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementGallery, Enabled: &on})
	r := httptest.NewRequest("GET", "http://localhost/api/v1/gallery-highlight/status", nil)
	r.Header.Set("Authorization", "Bearer "+v.Token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	code(t, w, 403)
}

type gallerySlowWriter struct {
	header           http.Header
	entered, release chan struct{}
}

func (w *gallerySlowWriter) Header() http.Header { return w.header }
func (w *gallerySlowWriter) WriteHeader(int)     {}
func (w *gallerySlowWriter) Write(b []byte) (int, error) {
	close(w.entered)
	<-w.release
	return len(b), nil
}
func TestGallerySlowTransportDoesNotHoldAuthorityLock(t *testing.T) {
	h, cookie, _ := metadataHandler(t)
	db := handlerDB(t, h)
	svc := galleryhighlight.New(db)
	h.WithGalleryHighlight(svc)
	ctx := context.Background()
	state, _ := db.Enhancements(ctx)
	on := true
	if _, e := db.ChangeEnhancement(ctx, model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementGallery, Enabled: &on}); e != nil {
		t.Fatal(e)
	}
	actor, e := h.svc.Session(ctx, cookie.Value)
	if e != nil {
		t.Fatal(e)
	}
	v, e := svc.Create(ctx, *actor, model.GalleryConnectionCreate{})
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "http://localhost/api/v1/gallery-highlight/status", nil)
	r.Header.Set("Authorization", "Bearer "+v.Token)
	writer := &gallerySlowWriter{header: http.Header{}, entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	go func() { h.ServeHTTP(writer, r); close(done) }()
	<-writer.entered
	revoked := make(chan error, 1)
	go func() { _, e := svc.Revoke(ctx, *actor, v.Connection.ID, 1); revoked <- e }()
	select {
	case e := <-revoked:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		close(writer.release)
		<-done
		t.Fatal("slow HTTP client held authority mutation")
	}
	close(writer.release)
	<-done
}
