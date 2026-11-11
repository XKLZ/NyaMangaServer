package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/library"
	"nyamangaserver.local/server/internal/testfixture"
	"strings"
	"testing"
	"time"
)

func catalogHandler(t *testing.T) (*Handler, *http.Cookie, string) {
	h, _ := testHandler(t, "http://localhost")
	root := t.TempDir()
	if e := testfixture.Write(root); e != nil {
		t.Fatal(e)
	}
	c := config.Config{PublicURL: "http://localhost", DataDir: t.TempDir(), Roots: []config.Root{{Key: "fixture", Name: "匿名样本", Path: root}}}
	svc := library.New(handlerDB(t, h), c, h.logger)
	db := handlerDB(t, h)
	if _, e := db.Write.Exec("UPDATE enhancement_settings SET enabled=1,runtime_armed=1"); e != nil {
		t.Fatal(e)
	}
	h = New(c, h.svc, h.assets, h.logger, h.build, svc)
	testDatabases.Store(h, db)
	t.Cleanup(func() { testDatabases.Delete(h) })
	svc.Start(context.Background())
	t.Cleanup(svc.Close)
	cookie, csrf := sessionResponse(t, request(t, h, "POST", "/api/v1/auth/setup", `{"setup_token":"anonymous-setup","password":"anonymous password"}`, nil, ""))
	return h, cookie, csrf
}
func TestCatalogHTTPFlow(t *testing.T) {
	h, cookie, csrf := catalogHandler(t)
	call := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		w := request(t, h, method, "/api/v1"+path, body, cookie, csrf)
		code(t, w, want)
		if strings.Contains(w.Body.String(), h.c.Roots[0].Path) {
			t.Fatal("absolute root path leaked")
		}
		return w
	}
	code(t, request(t, h, "GET", "/api/v1/books", "", nil, ""), 401)
	call("POST", "/libraries", `{"root_key":"unknown","display_name":"Bad"}`, 409)
	created := call("POST", "/libraries", `{"root_key":"fixture","display_name":"匿名书库","scan_interval_minutes":15}`, 201)
	var l struct {
		ID       string `json:"id"`
		Revision int64  `json:"revision"`
		Interval *int   `json:"scan_interval_minutes"`
	}
	if e := json.Unmarshal(created.Body.Bytes(), &l); e != nil {
		t.Fatal(e)
	}
	call("POST", "/libraries", `{"root_key":"fixture","display_name":"Duplicate"}`, 409)
	changed := call("PATCH", "/libraries/"+l.ID, `{"expected_revision":1,"display_name":"新名称"}`, 200)
	if e := json.Unmarshal(changed.Body.Bytes(), &l); e != nil || l.Interval == nil || *l.Interval != 15 {
		t.Fatal("omitted interval changed", e)
	}
	call("PATCH", "/libraries/"+l.ID, `{"expected_revision":1,"display_name":"stale"}`, 409)
	call("PATCH", "/libraries/"+l.ID, `{"expected_revision":2,"scan_interval_minutes":null}`, 200)
	call("GET", "/libraries", "", 200)
	started := call("POST", "/libraries/"+l.ID+"/scans", `{}`, 202)
	var j struct {
		ID string `json:"job_id"`
	}
	json.Unmarshal(started.Body.Bytes(), &j)
	if started.Header().Get("Location") != "/api/v1/jobs/"+j.ID {
		t.Fatal("job location")
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		v, e := h.catalog.Job(context.Background(), j.ID)
		if e != nil {
			t.Fatal(e)
		}
		if v.State == "completed" {
			break
		}
		if v.State == "failed" || time.Now().After(deadline) {
			t.Fatal(v)
		}
		time.Sleep(5 * time.Millisecond)
	}
	call("GET", "/jobs", "", 200)
	call("GET", "/jobs/"+j.ID, "", 200)
	call("GET", "/jobs/"+j.ID+"/items?page_size=2", "", 200)
	out := call("GET", "/books?page_size=1", "", 200)
	var listing struct {
		Total int `json:"total"`
		Items []struct {
			ID string `json:"id"`
		}
	}
	json.Unmarshal(out.Body.Bytes(), &listing)
	if listing.Total != 3 || len(listing.Items) != 1 {
		t.Fatal(listing)
	}
	books, _, _ := h.catalog.Editions(context.Background(), "", "available", 1, 50)
	book := books[0]
	base := "/books/" + book.ID
	call("GET", base, "", 200)
	call("GET", base+"/pages?page_size=2", "", 200)
	cover := call("GET", fmt.Sprintf("%s/cover?content_revision=%d", base, book.ContentRevision), "", 200)
	if got, want := cover.Header().Get("ETag"), fmt.Sprintf("\"%x\"", sha256.Sum256(cover.Body.Bytes())); got != want {
		t.Fatalf("ETag does not match returned bytes: %s", got)
	}
	if cover.Header().Get("Content-Type") != "image/png" {
		t.Fatal("cover content type")
	}
	imageURL := fmt.Sprintf("%s/pages/1/image?content_revision=%d", base, book.ContentRevision)
	call("GET", imageURL, "", 200)
	call("GET", base+"/cover?content_revision=999", "", 409)
	call("GET", base+"/cover", "", 400)
	call("GET", base+"/pages/999/image?content_revision=1", "", 404)
	call("GET", "/books?page=0", "", 400)
	call("GET", "/books?state=invalid", "", 400)
	r := httptest.NewRequest("GET", "http://localhost/api/v1"+imageURL, nil)
	r.Header.Set("If-None-Match", cover.Header().Get("ETag"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	code(t, w, 401)
	code(t, request(t, h, "POST", "/api/v1/libraries/"+l.ID+"/scans", `{}`, cookie, "wrong"), 403)
}
