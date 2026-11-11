package httpapi

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDedicatedCookieCoexistsAcrossPorts(t *testing.T) {
	daily, _ := testHandler(t, "http://localhost:5173")
	pilot, _ := testHandler(t, "http://localhost:5174")
	pilot.c.SessionCookieName = "nyamanga_recommendation_pilot"
	a, _ := sessionResponse(t, request(t, daily, "POST", "/api/v1/auth/setup", `{"setup_token":"anonymous-setup","password":"anonymous daily password"}`, nil, ""))
	b, bcsrf := sessionResponse(t, request(t, pilot, "POST", "/api/v1/auth/setup", `{"setup_token":"anonymous-setup","password":"anonymous pilot password"}`, nil, ""))
	if a.Name != CookieName || b.Name == a.Name {
		t.Fatal(a.Name, b.Name)
	}
	jar, _ := cookiejar.New(nil)
	aURL, _ := url.Parse(daily.c.PublicURL)
	bURL, _ := url.Parse(pilot.c.PublicURL)
	jar.SetCookies(aURL, []*http.Cookie{a})
	jar.SetCookies(bURL, []*http.Cookie{b})
	if len(jar.Cookies(aURL)) != 2 {
		t.Fatal("cookies did not coexist")
	}
	call := func(h *Handler, path, body, csrf string) *httptest.ResponseRecorder {
		t.Helper()
		method := "GET"
		if body != "" {
			method = "POST"
		}
		r := httptest.NewRequest(method, h.c.PublicURL+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", h.c.PublicURL)
		r.Header.Set("X-CSRF-Token", csrf)
		u, _ := url.Parse(h.c.PublicURL)
		for _, c := range jar.Cookies(u) {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		jar.SetCookies(u, w.Result().Cookies())
		return w
	}
	code(t, call(daily, "/api/v1/auth/session", "", ""), 200)
	code(t, call(pilot, "/api/v1/auth/session", "", ""), 200)
	code(t, call(pilot, "/api/v1/auth/logout", "{}", bcsrf), 204)
	code(t, call(daily, "/api/v1/auth/session", "", ""), 200)
	code(t, call(pilot, "/api/v1/auth/session", "", ""), 401)
	if len(jar.Cookies(aURL)) != 1 || jar.Cookies(aURL)[0].Name != CookieName {
		t.Fatal("pilot logout changed daily cookie")
	}
}
