package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"nyamangaserver.local/server/internal/auth"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
)

var testDatabases sync.Map

func handlerDB(t *testing.T, h *Handler) *store.Store {
	t.Helper()
	v, ok := testDatabases.Load(h)
	if !ok {
		t.Fatal("missing test database")
	}
	return v.(*store.Store)
}

type testHash struct{}

func (testHash) Hash(_ context.Context, p string) (string, error)    { return "test:" + p, nil }
func (testHash) Verify(_ context.Context, h, p string) (bool, error) { return h == "test:"+p, nil }
func testHandler(t *testing.T, origin string) (*Handler, *bytes.Buffer) {
	t.Helper()
	db, e := store.Open(context.Background(), t.TempDir(), migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	svc := auth.New(db, "anonymous-setup")
	svc.Hasher = testHash{}
	logs := &bytes.Buffer{}
	h := New(config.Config{PublicURL: origin, Roots: []config.Root{{Key: "sample", Name: "匿名目录", Path: "/private/never-expose"}}}, svc, fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>NyaManga</title>")}}, slog.New(slog.NewJSONHandler(logs, nil)), Build{"test", "local"})
	testDatabases.Store(h, db)
	t.Cleanup(func() { testDatabases.Delete(h) })
	return h, logs
}
func request(t *testing.T, h *Handler, method, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", h.c.PublicURL)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func code(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status %d want %d: %s", w.Code, want, w.Body.String())
	}
}
func sessionResponse(t *testing.T, w *httptest.ResponseRecorder) (*http.Cookie, string) {
	t.Helper()
	code(t, w, 200)
	var v struct {
		CSRF string `json:"csrf_token"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || v.CSRF == "" {
		t.Fatal("session response", w.Body.String())
	}
	return cookies[0], v.CSRF
}
func TestHTTPAuthenticationLifecycle(t *testing.T) {
	h, logs := testHandler(t, "http://localhost")
	now := time.Now()
	h.svc.Now = func() time.Time { return now }
	code(t, request(t, h, "GET", "/api/v1/auth/status", "", nil, ""), 200)
	for _, p := range []string{"/api/v1/library-roots", "/api/v1/diagnostics", "/api/v1/books/b1/pages/1/image"} {
		code(t, request(t, h, "GET", p, "", nil, ""), 401)
	}
	setup := request(t, h, "POST", "/api/v1/auth/setup", `{"setup_token":"anonymous-setup","password":"old anonymous password"}`, nil, "")
	cookie, csrf := sessionResponse(t, setup)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Secure || cookie.Domain != "" || cookie.MaxAge != 7*24*3600 {
		t.Fatal("cookie policy", cookie)
	}
	code(t, request(t, h, "POST", "/api/v1/auth/setup", `{"setup_token":"anonymous-setup","password":"other anonymous password"}`, nil, ""), 409)
	code(t, request(t, h, "POST", "/api/v1/auth/login", `{"password":"wrong"}`, nil, ""), 401)
	otherCookie, _ := sessionResponse(t, request(t, h, "POST", "/api/v1/auth/login", `{"password":"old anonymous password"}`, nil, ""))
	code(t, request(t, h, "GET", "/api/v1/auth/session", "", cookie, ""), 200)
	roots := request(t, h, "GET", "/api/v1/library-roots", "", cookie, "")
	code(t, roots, 200)
	if strings.Contains(roots.Body.String(), "/private/") {
		t.Fatal("root path exposed")
	}
	code(t, request(t, h, "GET", "/api/v1/books/b1/pages/1/image", "", cookie, ""), 404)
	code(t, request(t, h, "POST", "/api/v1/auth/logout", "", cookie, ""), 403)
	now = now.Add(time.Minute)
	wrongCurrent := request(t, h, "PUT", "/api/v1/auth/password", `{"current_password":"incorrect","new_password":"new anonymous password"}`, cookie, csrf)
	code(t, wrongCurrent, 401)
	if !strings.Contains(wrongCurrent.Body.String(), "invalid_credentials") {
		t.Fatal("incorrect password was confused with expired session")
	}
	code(t, request(t, h, "GET", "/api/v1/auth/session", "", cookie, ""), 200)
	changed := request(t, h, "PUT", "/api/v1/auth/password", `{"current_password":"old anonymous password","new_password":"new anonymous password"}`, cookie, csrf)
	code(t, changed, 200)
	for _, c := range []*http.Cookie{cookie, otherCookie} {
		code(t, request(t, h, "GET", "/api/v1/auth/session", "", c, ""), 401)
	}
	code(t, request(t, h, "POST", "/api/v1/auth/login", `{"password":"old anonymous password"}`, nil, ""), 401)
	next, nextCSRF := sessionResponse(t, request(t, h, "POST", "/api/v1/auth/login", `{"password":"new anonymous password"}`, nil, ""))
	logout := request(t, h, "POST", "/api/v1/auth/logout", "", next, nextCSRF)
	code(t, logout, 204)
	if logout.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("cookie not removed")
	}
	code(t, request(t, h, "GET", "/api/v1/auth/session", "", next, ""), 401)
	for _, secret := range []string{"old anonymous password", "new anonymous password", "anonymous-setup", cookie.Value, csrf, "/private/"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("secret leaked into log")
		}
	}
}
func TestOriginCSRFAndBoundaries(t *testing.T) {
	h, _ := testHandler(t, "https://library.example")
	w := request(t, h, "POST", "/api/v1/auth/setup", `{"setup_token":"anonymous-setup","password":"anonymous password"}`, nil, "")
	cookie, csrf := sessionResponse(t, w)
	if !cookie.Secure {
		t.Fatal("HTTPS cookie not secure")
	}
	for _, origin := range []string{"", "null", "https://attacker.example", "https://library.example.attacker"} {
		r := httptest.NewRequest("POST", "http://localhost/api/v1/auth/logout", nil)
		r.AddCookie(cookie)
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", csrf)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		code(t, out, 403)
	}
	code(t, request(t, h, "PUT", "/api/v1/auth/password", `{"current_password":"anonymous password","new_password":"new anonymous password"}`, cookie, "wrong"), 403)
	for _, body := range []string{`{"password":12}`, `{"password":"x","typo":true}`, `{"password":"x"}{}`, `{`} {
		code(t, request(t, h, "POST", "/api/v1/auth/login", body, nil, ""), 400)
	}
	// Decode budget tested with a fresh rate window.
	h.svc.Now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	huge := fmt.Sprintf(`{"password":"%s"}`, strings.Repeat("x", (1<<20)+1))
	code(t, request(t, h, "POST", "/api/v1/auth/login", huge, nil, ""), 413)
	r := httptest.NewRequest("POST", "http://localhost/api/v1/auth/login", strings.NewReader(`{}`))
	r.Header.Set("Origin", h.c.PublicURL)
	out := httptest.NewRecorder()
	h.ServeHTTP(out, r)
	code(t, out, 415)
}
func TestRateLimitIgnoresForwardedHeaders(t *testing.T) {
	h, _ := testHandler(t, "http://localhost")
	for i := 0; i < 6; i++ {
		r := httptest.NewRequest("POST", "http://localhost/api/v1/auth/login", strings.NewReader(`{"password":"anonymous password"}`))
		r.Header.Set("Origin", h.c.PublicURL)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d", i))
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		if i == 5 {
			code(t, out, 429)
			if out.Header().Get("Retry-After") == "" {
				t.Fatal("retry header missing")
			}
		} else {
			code(t, out, 401)
		}
	}
}
func TestStaticAndHealth(t *testing.T) {
	h, _ := testHandler(t, "http://localhost")
	for _, p := range []string{"/", "/login", "/setup", "/settings/security", "/works/stable-work", "/editions/stable-edition", "/books/stable-edition", "/admin/editions/stable-edition"} {
		w := request(t, h, "GET", p, "", nil, "")
		code(t, w, 200)
		if !strings.Contains(w.Body.String(), "<!doctype html>") {
			t.Fatal("SPA refresh")
		}
		if w.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatal("headers")
		}
	}
	for _, p := range []string{"/api/v1/health/live", "/api/v1/health/ready"} {
		code(t, request(t, h, "GET", p, "", nil, ""), 200)
	}
	w := request(t, h, "GET", "/api/unknown", "", nil, "")
	code(t, w, 404)
	if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Fatal("API fell through to HTML")
	}
	handlerDB(t, h).Read.Close()
	code(t, request(t, h, "GET", "/api/v1/health/ready", "", nil, ""), 503)
}
