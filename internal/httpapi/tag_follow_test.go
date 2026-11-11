package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"io"
	"net/http"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/auth"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/tagcatalog"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTagFollowHTTPContractAndIdentity(t *testing.T) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	h.WithTagCatalog(tagcatalog.New(db))
	ctx := context.Background()
	if e := db.ApplyOverrides(ctx, "book", 1, map[string]model.Override{"tags": {Op: "set", Value: json.RawMessage(`[{"namespace":"artist","name":"author"}]`)}}); e != nil {
		t.Fatal(e)
	}
	id := catalogTagID("artist", "author")
	path := "/api/v1/tags/" + id + "/follow"
	doc, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers[0].URL = "http://localhost/api/v1"
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, url, body string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost"+url, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", h.c.PublicURL)
		r.Header.Set("X-CSRF-Token", csrf)
		r.AddCookie(cookie)
		route, params, e := router.FindRoute(r)
		if e != nil {
			t.Fatal(e)
		}
		in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		if want != 400 {
			if e = openapi3filter.ValidateRequest(ctx, in); e != nil {
				t.Fatal(e)
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, want)
		if e = openapi3filter.ValidateResponse(ctx, &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: w.Code, Header: w.Header(), Body: io.NopCloser(strings.NewReader(w.Body.String())), Options: &openapi3filter.Options{IncludeResponseStatus: true}}); e != nil {
			t.Fatal(e)
		}
		v := map[string]any{}
		json.Unmarshal(w.Body.Bytes(), &v)
		return v
	}
	code(t, request(t, h, "PUT", path, `{"expected_revision":0}`, nil, ""), 401)
	code(t, request(t, h, "PUT", path, `{"expected_revision":0}`, cookie, ""), 403)
	for _, body := range []string{`{}`, `{"expected_revision":null}`, `{"expected_revision":-1}`, `{"expected_revision":9223372036854775807}`, `{"expected_revision":9223372036854775808}`, `{"expected_revision":1.5}`, `{"expected_revision":0,"account_id":"other"}`} {
		call("PUT", path, body, 400)
	}
	call("PUT", path+"?account_id=other", `{"expected_revision":0}`, 400)
	call("PUT", "/api/v1/tags/missing/follow", `{"expected_revision":0}`, 404)
	first := call("PUT", path, `{"expected_revision":0}`, 200)
	if first["followed"] != true || first["revision"] != float64(1) {
		t.Fatal(first)
	}
	conflict := call("PUT", path, `{"expected_revision":0}`, 409)
	if conflict["error"].(map[string]any)["code"] != "tag_follow_revision_conflict" {
		t.Fatal(conflict)
	}
	call("PUT", path, `{"expected_revision":1}`, 200)
	page := call("GET", "/api/v1/tag-catalog?library_id=lib&followed_only=true", "", 200)
	items := page["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["followed"] != true {
		t.Fatal(page)
	}
	detail := call("GET", "/api/v1/tag-catalog/"+id+"?library_id=lib", "", 200)
	if detail["follow_revision"] != float64(1) {
		t.Fatal(detail)
	}
	for _, suffix := range []string{"", "/works", "/editions", "/relations"} {
		call("GET", "/api/v1/tag-catalog/"+id+suffix+"?library_id=lib&followed_only=false", "", 400)
	}
	call("GET", "/api/v1/tag-catalog?followed_only=true", "", 400)
	call("GET", "/api/v1/tag-catalog?library_id=lib&followed_only=1", "", 400)
	call("GET", "/api/v1/tag-catalog?library_id=lib&account_id=other", "", 400)
	call("POST", "/api/v1/tag-suggestions", `{"account_id":"other"}`, 400)
	call("POST", "/api/v1/tag-suggestions?account_id=other", `{}`, 400)
	code(t, request(t, h, "GET", "/api/v1/events?account_id=other", "", cookie, ""), 400)
	call("POST", "/api/v1/tag-suggestions", `{"namespace":"artist"}`, 200)
	last := call("DELETE", path, `{"expected_revision":1}`, 200)
	if last["followed"] != false || last["revision"] != float64(2) {
		t.Fatal(last)
	}
	code(t, request(t, h, "GET", path, "", cookie, ""), 405)
	for _, tc := range []struct {
		media, body string
		want        int
	}{{"text/plain", `{"expected_revision":2}`, 415}, {"application/json", strings.Repeat(" ", 1<<20) + `{}`, 413}} {
		r := httptest.NewRequest("PUT", "http://localhost"+path, strings.NewReader(tc.body))
		r.AddCookie(cookie)
		r.Header.Set("Origin", h.c.PublicURL)
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("Content-Type", tc.media)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, tc.want)
	}
}

func TestTagFollowSSEOwnerReplayAndFilteredCursor(t *testing.T) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	h.WithTagCatalog(tagcatalog.New(db))
	ctx := context.Background()
	if e := db.ApplyOverrides(ctx, "book", 1, map[string]model.Override{"tags": {Op: "set", Value: json.RawMessage(`[{"namespace":"artist","name":"author"}]`)}}); e != nil {
		t.Fatal(e)
	}
	id := catalogTagID("artist", "author")
	var owner string
	db.Read.QueryRow("SELECT id FROM accounts WHERE singleton=1").Scan(&owner)
	c, e := db.Write.Conn(ctx)
	if e != nil {
		t.Fatal(e)
	}
	c.ExecContext(ctx, "PRAGMA ignore_check_constraints=ON")
	if _, e = c.ExecContext(ctx, "INSERT INTO accounts VALUES(2,'second-owner','anonymous',1,1)"); e != nil {
		t.Fatal(e)
	}
	c.ExecContext(ctx, "PRAGMA ignore_check_constraints=OFF")
	c.Close()
	if _, e = db.Write.Exec("INSERT INTO account_library_grants VALUES('second-owner','lib','viewer')"); e != nil {
		t.Fatal(e)
	}
	raw := strings.Repeat("o", 43)
	hash := sha256.Sum256([]byte(raw))
	if _, e = db.Write.Exec("INSERT INTO sessions VALUES(?,'second-owner',?,?)", hash[:], time.Now().Unix(), time.Now().Add(time.Hour).Unix()); e != nil {
		t.Fatal(e)
	}
	other := &http.Cookie{Name: cookie.Name, Value: raw}
	var cursor int64
	db.Read.QueryRow("SELECT max(id) FROM events").Scan(&cursor)
	otherAccount, e := db.AccountByID(ctx, "second-owner")
	if e != nil {
		t.Fatal(e)
	}
	_, otherCursor, _, e := db.EventsForAccount(model.WithActor(ctx, *otherAccount), "second-owner", 0, 200)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.SetTagFollow(ctx, owner, id, 0, true); e != nil {
		t.Fatal(e)
	}
	h.streamInterval = 5 * time.Millisecond
	h.heartbeatInterval = 10 * time.Millisecond
	for _, tc := range []struct {
		name   string
		cookie *http.Cookie
		want   bool
	}{{"self", cookie, true}, {"other", other, false}} {
		t.Run(tc.name, func(t *testing.T) {
			streamCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			r := httptest.NewRequest("GET", "http://localhost/api/v1/events", nil).WithContext(streamCtx)
			r.AddCookie(tc.cookie)
			eventCursor := cursor
			if !tc.want {
				eventCursor = otherCursor
			}
			r.Header.Set("Last-Event-ID", strconv.FormatInt(eventCursor, 10))
			w := &streamWriter{ResponseRecorder: httptest.NewRecorder(), chunks: make(chan string, 50)}
			done := make(chan struct{})
			go func() { h.ServeHTTP(w, r); close(done) }()
			if tc.want {
				select {
				case chunk := <-w.chunks:
					if !strings.Contains(chunk, `"type":"tag_follow"`) || !strings.Contains(chunk, id) {
						t.Fatal(chunk)
					}
				case <-streamCtx.Done():
					t.Fatal("private self event absent")
				}
			} else {
				// The per-account cursor skips private rows; a later authorized-library event must arrive.
				select {
				case chunk := <-w.chunks:
					if strings.Contains(chunk, id) || strings.Contains(chunk, "resync_required") {
						t.Fatal(chunk)
					}
				case <-streamCtx.Done():
					t.Fatal("heartbeat absent")
				}
				if _, e = db.Write.Exec("INSERT INTO events(type,resource_id,revision,created_at) VALUES('edition','book',2,unixepoch())"); e != nil {
					t.Fatal(e)
				}
				found := false
				for !found {
					select {
					case chunk := <-w.chunks:
						if strings.Contains(chunk, id) || strings.Contains(chunk, "second-owner") || strings.Contains(chunk, "resync_required") {
							t.Fatal(chunk)
						}
						found = strings.Contains(chunk, `"resource_id":"book"`)
					case <-streamCtx.Done():
						t.Fatal("authorized library event lost after private filter")
					}
				}
			}
			cancel()
			<-done
			if strings.Contains(w.Body.String(), `owner_account_id`) || strings.Contains(w.Body.String(), owner) {
				t.Fatal("owner leaked")
			}
		})
	}
	// A second-account cookie cannot change the first account follow.
	code(t, request(t, h, "PUT", "/api/v1/tags/"+id+"/follow", `{"expected_revision":0}`, other, auth.CSRF(raw)), 200)
	var n int
	db.Read.QueryRow("SELECT count(*) FROM tag_follows WHERE tag_id=? AND followed=1", id).Scan(&n)
	if n != 2 {
		t.Fatal(n)
	}
	// Existing revocation/heartbeat/resync/shutdown paths are covered by TestSSEReplayGapRevocationShutdownAndWriteFailure.
	code(t, request(t, h, "DELETE", "/api/v1/tags/"+id+"/follow", `{"expected_revision":1}`, cookie, csrf), 200)
}
