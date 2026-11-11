package httpapi

import (
	"context"
	"encoding/json"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/metadata"
	"nyamangaserver.local/server/internal/provider"
	"nyamangaserver.local/server/internal/testfixture"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func metadataHandler(t *testing.T) (*Handler, *http.Cookie, string) {
	t.Helper()
	h, cookie, csrf := catalogHandler(t)
	db := handlerDB(t, h)
	ctx := context.Background()
	_, e := db.Write.Exec(`INSERT INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision) VALUES('lib','anonymous','Anonymous','utf8','test',1);`)
	if e != nil {
		t.Fatal(e)
	}
	if e = testfixture.SeedEdition(ctx, db.Write, "book", "lib", "sample.cbz", "Filename"); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	remote := &testfixture.Remote{Vault: provider.NewVault(filepath.Join(dir, "key"), filepath.Join(dir, "credentials"))}
	h.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	meta := metadata.New(db, h.catalog, remote, dir, h.logger)
	h.WithMetadata(meta, ctx)
	return h, cookie, csrf
}
func TestMetadataHTTPContractAndIsolation(t *testing.T) {
	h, cookie, csrf := metadataHandler(t)
	ctx := context.Background()
	doc, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers[0].URL = "http://localhost/api/v1"
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path, body string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", h.c.PublicURL)
		r.Header.Set("X-CSRF-Token", csrf)
		r.AddCookie(cookie)
		route, params, e := router.FindRoute(r)
		if e != nil {
			t.Fatal(path, e)
		}
		in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		if e = openapi3filter.ValidateRequest(ctx, in); e != nil {
			t.Fatal(path, e)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, want)
		out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: w.Code, Header: w.Header()}
		out.SetBodyBytes(w.Body.Bytes())
		if e = openapi3filter.ValidateResponse(ctx, out); e != nil {
			t.Fatal(path, e)
		}
		var result map[string]any
		if e = json.Unmarshal(w.Body.Bytes(), &result); e != nil {
			t.Fatal(e)
		}
		return result
	}

	call("PATCH", "/books/absent/overrides", `{"expected_revision":1,"fields":{"language":{"op":"set","value":"english"}}}`, 404)
	call("PATCH", "/books/absent/overrides", `{"expected_revision":1,"fields":{"title":{"op":"empty"}}}`, 404)
	call("GET", "/books/book", "", 200)
	call("PATCH", "/books/book/overrides", `{"expected_revision":1,"fields":{"title":{"op":"set","value":"人工标题"},"description":{"op":"empty"},"tags":{"op":"set","value":[{"namespace":"artist","name":"anonymous"}]}}}`, 200)
	call("PATCH", "/books/book/overrides", `{"expected_revision":1,"fields":{"title":{"op":"inherit"}}}`, 409)

	result := call("PATCH", "/books/book/overrides", `{"expected_revision":2,"fields":{"languages":{"op":"set","value":["japanese","english"]},"translated":{"op":"set","value":false},"color_mode":{"op":"empty"},"censorship":{"op":"set","value":"mosaic"},"extraneous_ads":{"op":"inherit"}}}`, 200)
	if result["metadata"].(map[string]any)["properties"] == nil {
		t.Fatal("properties missing")
	}
	call("PATCH", "/books/book/overrides", `{"expected_revision":3,"fields":{"language":{"op":"empty"}}}`, 409)
	call("PATCH", "/books/book/overrides", `{"expected_revision":3,"fields":{"language":{"op":"empty"},"languages":{"op":"empty"}}}`, 400)
	suggestions := call("POST", "/tag-suggestions", `{"namespace":"artist","q":"anonymous","page_size":1}`, 200)
	snapshot := suggestions["snapshot_revision"].(string)
	call("POST", "/tag-suggestions", `{"page":2}`, 400)
	tags := call("GET", "/tags?q=anonymous", "", 200)["items"].([]any)
	id := tags[0].(map[string]any)["id"].(string)
	call("PUT", "/tags/"+id+"/translations/zh-CN", `{"expected_revision":0,"text":"人工译文"}`, 200)
	call("POST", "/tag-suggestions", `{"namespace":"artist","q":"anonymous","page_size":1,"page":2,"snapshot_revision":"`+snapshot+`"}`, 409)
	call("DELETE", "/tags/"+id+"/translations/zh-CN", `{"expected_revision":1}`, 200)
	call("GET", "/settings/metadata-provider", "", 200)
	call("PATCH", "/settings/metadata-provider", `{"expected_revision":1,"site":"ex","proxy_url":"socks5://127.0.0.1:7891"}`, 200)
	secrets := call("PUT", "/settings/provider-credentials", `{"expected_revision":2,"fields":{"ipb_pass_hash":{"op":"set","value":"anonymous-secret-never-echo"}}}`, 200)
	encoded, _ := json.Marshal(secrets)
	if strings.Contains(string(encoded), "anonymous-secret") {
		t.Fatal("secret echoed")
	}
	call("GET", "/settings/metadata-provider", "", 200)
	call("POST", "/settings/metadata-provider/test", "", 200)
	call("GET", "/settings/tag-translations", "", 200)
	job := call("POST", "/metadata-jobs", `{"book_ids":["book"],"gallery_urls":{"book":"https://e-hentai.org/g/1/0123456789/"}}`, 202)["job_id"].(string)
	call("POST", "/metadata-jobs", `{"book_ids":["book"]}`, 409)
	call("POST", "/jobs/"+job+"/pause", "", 202)
	call("POST", "/jobs/"+job+"/resume", "", 202)
	call("POST", "/jobs/"+job+"/cancel", "", 202)
	call("POST", "/jobs/"+job+"/retry", `{"ordinals":[1]}`, 202)
	call("POST", "/tag-translation-jobs", "", 202)
	code(t, request(t, h, "PATCH", "/api/v1/books/book/overrides", `{"expected_revision":2,"fields":{"title":{"op":"inherit"}}}`, cookie, "wrong"), 403)
	code(t, request(t, h, "GET", "/api/v1/settings/metadata-provider", "", nil, ""), 401)
	code(t, request(t, h, "GET", "/api/v1/events", "", nil, ""), 401)
}

type streamWriter struct {
	*httptest.ResponseRecorder
	chunks    chan string
	fail      bool
	deadlines int
}

func (w *streamWriter) Write(b []byte) (int, error) {
	if w.fail {
		return 0, io.ErrClosedPipe
	}
	select {
	case w.chunks <- string(b):
	default:
	}
	return w.ResponseRecorder.Write(b)
}
func (w *streamWriter) SetWriteDeadline(time.Time) error { w.deadlines++; return nil }
func TestSSEReplayGapRevocationShutdownAndWriteFailure(t *testing.T) {
	for _, scenario := range []string{"initial", "replay", "gap", "revoked", "shutdown", "slow"} {
		t.Run(scenario, func(t *testing.T) {
			h, cookie, csrf := metadataHandler(t)
			_, _ = handlerDB(t, h).Write.Exec("UPDATE editions SET revision=revision+1 WHERE id='book'")
			h.streamInterval = 5 * time.Millisecond
			h.heartbeatInterval = 10 * time.Millisecond
			lifecycle, stop := context.WithCancel(context.Background())
			defer stop()
			h.lifecycle = lifecycle
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			r := httptest.NewRequest("GET", "http://localhost/api/v1/events", nil).WithContext(ctx)
			r.AddCookie(cookie)
			if scenario == "replay" {
				r.Header.Set("Last-Event-ID", "1")
			}
			if scenario == "gap" {
				r.Header.Set("Last-Event-ID", "99999")
			}
			w := &streamWriter{ResponseRecorder: httptest.NewRecorder(), chunks: make(chan string, 20), fail: scenario == "slow"}
			done := make(chan struct{})
			go func() { h.ServeHTTP(w, r); close(done) }()
			if scenario != "slow" {
				select {
				case first := <-w.chunks:
					if scenario == "replay" {
						if !strings.Contains(first, "event: change") {
							t.Fatal(first)
						}
					} else if !strings.Contains(first, "resync_required") {
						t.Fatal(first)
					}
				case <-ctx.Done():
					t.Fatal("no initial event")
				}
				if scenario == "revoked" {
					code(t, request(t, h, "POST", "/api/v1/auth/logout", "", cookie, csrf), 204)
				} else if scenario == "shutdown" {
					stop()
				} else {
					select {
					case <-w.chunks:
					case <-ctx.Done():
						t.Fatal("heartbeat missing")
					}
					cancel()
				}
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("stream failed to close")
			}
			if w.deadlines == 0 {
				t.Fatal("stream did not install write deadline")
			}
			if strings.Contains(w.Body.String(), "Filename") || strings.Contains(w.Body.String(), "csrf") {
				t.Fatal("business or secret payload leaked")
			}
		})
	}
}
