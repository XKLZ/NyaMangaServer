package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/maintenance"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type cleanupSlowReader struct {
	io.Reader
	reads *atomic.Int64
}

func (r cleanupSlowReader) Read(p []byte) (int, error) {
	r.reads.Add(1)
	time.Sleep(20 * time.Millisecond)
	if len(p) > 4096 {
		p = p[:4096]
	}
	return r.Reader.Read(p)
}

type cleanupDeadlineWriter struct {
	*httptest.ResponseRecorder
	writeDeadline time.Time
}

func (w *cleanupDeadlineWriter) SetReadDeadline(time.Time) error    { return nil }
func (w *cleanupDeadlineWriter) SetWriteDeadline(d time.Time) error { w.writeDeadline = d; return nil }

func TestCleanupPreviewSlowIOFiniteDeadlineCancellationAndAuthentication(t *testing.T) {
	h, _ := testHandler(t, "http://localhost")
	db := handlerDB(t, h)
	h.c.DataDir = t.TempDir()
	cookie, csrf := sessionResponse(t, request(t, h, "POST", "/api/v1/auth/setup", `{"setup_token":"anonymous-setup","password":"anonymous password"}`, nil, ""))
	h.requestTimeout = 100 * time.Millisecond
	s := maintenance.New(db, h.c, nil, nil, h.logger)
	h.WithMaintenance(s)
	dir := filepath.Join(h.c.DataDir, "tmp")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "anonymous")
	if e := os.WriteFile(path, []byte(strings.Repeat("x", 64<<10)), 0600); e != nil {
		t.Fatal(e)
	}
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(path, old, old)
	var reads atomic.Int64
	s.CleanupPreviewReader = func(r io.Reader) io.Reader { return cleanupSlowReader{r, &reads} }
	body := `{"operation":"cleanup","expected_revisions":{},"options":{"categories":["tmp"]}}`
	makeRequest := func(ctx context.Context) *http.Request {
		r := httptest.NewRequest("POST", "http://localhost/api/v1/maintenance/plans", strings.NewReader(body)).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", h.c.PublicURL)
		r.Header.Set("X-CSRF-Token", csrf)
		r.AddCookie(cookie)
		return r
	}
	w := &cleanupDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	start := time.Now()
	h.ServeHTTP(w, makeRequest(context.Background()))
	code(t, w.ResponseRecorder, 200)
	if time.Since(start) < 300*time.Millisecond || reads.Load() < 16 {
		t.Fatal("slow IO was not exercised")
	}
	if d := time.Until(w.writeDeadline); d < 29*time.Minute || d > 30*time.Minute {
		t.Fatal("bulk socket deadline", d)
	}
	// Ordinary endpoints and non-cleanup plans retain their ordinary context.
	h.mux.HandleFunc("/ordinary-probe", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done(); w.WriteHeader(204) })
	start = time.Now()
	code(t, request(t, h, "GET", "/ordinary-probe", "", nil, ""), 204)
	if d := time.Since(start); d < 80*time.Millisecond || d > time.Second {
		t.Fatal("ordinary timeout changed", d)
	}
	ordinary := &cleanupDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	r := makeRequest(context.Background())
	r.Body = io.NopCloser(strings.NewReader(`{"operation":"not-valid","expected_revisions":{}}`))
	h.ServeHTTP(ordinary, r)
	if !ordinary.writeDeadline.IsZero() {
		t.Fatal("non-cleanup plan widened")
	}
	n := reads.Load()
	code(t, request(t, h, "POST", "/api/v1/maintenance/plans", body, nil, csrf), 401)
	code(t, request(t, h, "POST", "/api/v1/maintenance/plans", body, cookie, "invalid"), 403)
	if reads.Load() != n {
		t.Fatal("unauthorized hashing")
	}
	// Cancel an in-progress read and a second request waiting on the plan mutex.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w = &cleanupDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	done := make(chan struct{})
	go func() { h.ServeHTTP(w, makeRequest(ctx)); close(done) }()
	for until := time.Now().Add(time.Second); reads.Load() == n; {
		if time.Now().After(until) {
			t.Fatal("hash did not start")
		}
		time.Sleep(time.Millisecond)
	}
	waiting, stop := context.WithCancel(context.Background())
	waitDone := make(chan struct{})
	ww := &cleanupDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	go func() { h.ServeHTTP(ww, makeRequest(waiting)); close(waitDone) }()
	time.Sleep(10 * time.Millisecond)
	stop()
	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("plan mutex ignored cancel")
	}
	code(t, ww.ResponseRecorder, 504)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("hash ignored client cancel")
	}
	code(t, w.ResponseRecorder, 504)
	var plans int
	if e := db.Read.QueryRow("SELECT count(*) FROM maintenance_plans").Scan(&plans); e != nil || plans != 1 {
		t.Fatal("cancelled preview persisted", plans, e)
	}
	// A shorter upstream deadline also wins over the bulk budget.
	ctx, stop = context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer stop()
	w = &cleanupDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(w, makeRequest(ctx))
	code(t, w.ResponseRecorder, 504)
}
