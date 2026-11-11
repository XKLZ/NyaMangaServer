package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"image"
	"image/png"
	"io"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/tagcatalog"
	"nyamangaserver.local/server/internal/tagimages"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTagImagesHTTPContractAuthCASContentAndSummaries(t *testing.T) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	ctx := context.Background()
	images := tagimages.New(db, h.c, h.catalog.Pool)
	h.WithTagCatalog(tagcatalog.New(db)).WithTagImages(images).WithMaintenance(maintenance.New(db, h.c, h.catalog, h.metadata, h.logger).WithTagImages(images))
	if e := db.ApplyOverrides(ctx, "book", 1, map[string]model.Override{"tags": {Op: "set", Value: json.RawMessage(`[{"namespace":"parody","name":"alpha"},{"namespace":"character","name":"hero"},{"namespace":"artist","name":"author"}]`)}}); e != nil {
		t.Fatal(e)
	}
	id := catalogTagID("parody", "alpha")
	base := "/tag-catalog/" + id + "/images"
	var raw bytes.Buffer
	png.Encode(&raw, image.NewNRGBA(image.Rect(0, 0, 3, 4)))
	openapi3filter.RegisterBodyDecoder("image/png", openapi3filter.FileBodyDecoder)
	defer openapi3filter.UnregisterBodyDecoder("image/png")
	doc, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers[0].URL = "http://localhost/api/v1"
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path, media string, body []byte, rev int, want int, authenticated bool, origin, token, etag string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost/api/v1"+path, bytes.NewReader(body))
		if media != "" {
			r.Header.Set("Content-Type", media)
		}
		if rev >= 0 {
			r.Header.Set("X-Expected-Revision", strconv.Itoa(rev))
		}
		if authenticated {
			r.AddCookie(cookie)
		}
		r.Header.Set("Origin", origin)
		r.Header.Set("X-CSRF-Token", token)
		r.Header.Set("If-None-Match", etag)
		route, params, e := router.FindRoute(r)
		if e != nil {
			t.Fatal(e)
		}
		in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		if want == 200 || want == 304 || want == 409 {
			if e = openapi3filter.ValidateRequest(ctx, in); e != nil {
				t.Fatal(path, e)
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatal(path, w.Code, w.Body.String())
		}
		out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: w.Code, Header: w.Header(), Body: io.NopCloser(bytes.NewReader(w.Body.Bytes())), Options: &openapi3filter.Options{IncludeResponseStatus: true}}
		if e = openapi3filter.ValidateResponse(ctx, out); e != nil {
			t.Fatal(path, e)
		}
		return w
	}
	get := func(path string, want int) *httptest.ResponseRecorder {
		return call("GET", path, "", nil, -1, want, true, "", "", "")
	}
	state := get(base, 200)
	if !strings.Contains(state.Body.String(), `"revision":0`) {
		t.Fatal(state.Body.String())
	}
	call("GET", base, "", nil, -1, 401, false, "", "", "")
	call("PUT", base+"/foreground", "image/png", raw.Bytes(), 0, 403, true, h.c.PublicURL, "", "")
	call("PUT", base+"/foreground", "image/png", raw.Bytes(), 0, 403, true, "http://hostile.invalid", csrf, "")
	call("PUT", base+"/foreground", "image/png", raw.Bytes(), 0, 401, false, h.c.PublicURL, csrf, "")
	before, _ := db.Edition(ctx, "book")
	work, _ := db.Work(ctx, "i:book")
	first := get("/tag-catalog?library_id=lib", 200)
	var firstPage model.TagCatalogPage
	json.Unmarshal(first.Body.Bytes(), &firstPage)
	var eventCursor int64
	db.Read.QueryRow("SELECT COALESCE(max(id),0) FROM events").Scan(&eventCursor)
	put := call("PUT", base+"/foreground", "image/png", raw.Bytes(), 0, 200, true, h.c.PublicURL, csrf, "")
	var fg model.TagImage
	json.Unmarshal(put.Body.Bytes(), &fg)
	if fg.Revision != 1 || fg.AssetID == nil || fg.Width != 3 || fg.Height != 4 {
		t.Fatal(fg)
	}
	call("PUT", base+"/background", "image/png", raw.Bytes(), 0, 200, true, h.c.PublicURL, csrf, "")
	call("PUT", base+"/foreground", "image/png", raw.Bytes(), 0, 409, true, h.c.PublicURL, csrf, "")
	call("PUT", base+"/unknown", "image/png", raw.Bytes(), 0, 400, true, h.c.PublicURL, csrf, "")
	call("PUT", base+"/foreground", "image/png", []byte("corrupt"), 1, 415, true, h.c.PublicURL, csrf, "")
	call("PUT", base+"/foreground", "image/png", make([]byte, 10<<20+1), 1, 413, true, h.c.PublicURL, csrf, "")
	call("DELETE", base+"/foreground", "", nil, -1, 400, true, h.c.PublicURL, csrf, "")
	get(base+"/foreground/content", 400)
	content := get(base+"/foreground/content?revision=1", 200)
	if content.Header().Get("ETag") == "" || content.Header().Get("Content-Type") != "image/png" {
		t.Fatal(content.Header())
	}
	call("GET", base+"/foreground/content?revision=1", "", nil, -1, 304, true, "", "", content.Header().Get("ETag"))
	call("GET", base+"/foreground/content?revision=1", "", nil, -1, 401, false, "", "", "")
	get("/tag-catalog?library_id=lib&snapshot="+firstPage.Snapshot, 409)
	for _, path := range []string{"/tag-catalog?library_id=lib", "/tag-catalog/" + id + "?library_id=lib", "/tag-catalog/" + id + "/relations?library_id=lib&view=display"} {
		w := get(path, 200)
		if !strings.Contains(w.Body.String(), *fg.AssetID) {
			t.Fatal("batch summary missing", path, w.Body.String())
		}
	}
	after, _ := db.Edition(ctx, "book")
	afterWork, _ := db.Work(ctx, "i:book")
	if before.Revision != after.Revision || work.Revision != afterWork.Revision {
		t.Fatal("unrelated edit revision changed")
	}
	var relationRevision int
	db.Read.QueryRow("SELECT revision FROM tag_relation_projection WHERE singleton=1").Scan(&relationRevision)
	if relationRevision != 1 {
		t.Fatal("relation revision changed", relationRevision)
	}
	var events int
	db.Read.QueryRow("SELECT count(*) FROM events WHERE type='tag_image' AND resource_id=?", id+":foreground").Scan(&events)
	if events != 1 {
		t.Fatal(events)
	}
	// Actual authenticated SSE replay carries the new resource type and role revision.
	streamCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req := httptest.NewRequest("GET", "http://localhost/api/v1/events", nil).WithContext(streamCtx)
	req.AddCookie(cookie)
	req.Header.Set("Last-Event-ID", strconv.FormatInt(eventCursor, 10))
	stream := &streamWriter{ResponseRecorder: httptest.NewRecorder(), chunks: make(chan string, 20)}
	done := make(chan struct{})
	go func() { h.ServeHTTP(stream, req); close(done) }()
	foundImage := false
	for !foundImage {
		select {
		case chunk := <-stream.chunks:
			foundImage = strings.Contains(chunk, `"type":"tag_image"`) && strings.Contains(chunk, id+":foreground")
		case <-streamCtx.Done():
			t.Fatal("tag_image SSE missing")
		}
	}
	cancel()
	<-done

	db.Write.Exec("UPDATE libraries SET disabled=1 WHERE id='lib'")
	get(base, 200)
	get(base+"/foreground/content?revision=1", 200)
	call("DELETE", base+"/foreground", "", nil, 1, 200, true, h.c.PublicURL, csrf, "")
	get(base+"/foreground/content?revision=1", 409)
	get(base+"/foreground/content?revision=2", 404)
	call("PUT", base+"/foreground", "image/png", raw.Bytes(), 0, 409, true, h.c.PublicURL, csrf, "")
	get("/tag-catalog/"+catalogTagID("artist", "author")+"/images", 400)
	get("/tag-catalog/missing/images", 404)
	storage := get("/maintenance/storage", 200)
	if !strings.Contains(storage.Body.String(), `"kind":"tag_images"`) {
		t.Fatal(storage.Body.String())
	}
	call("POST", "/maintenance/plans", "application/json", []byte(`{"operation":"cleanup","expected_revisions":{},"options":{"categories":["tag_images","orphan_assets"]}}`), -1, 200, true, h.c.PublicURL, csrf, "")
	// Storage failures are stable and sanitized; no key, filename, image or host path.
	db.Write.Exec("DROP TABLE tag_images")
	failed := get(base, 503)
	if strings.Contains(failed.Body.String(), h.c.DataDir) {
		t.Fatal("storage path leaked")
	}
}
