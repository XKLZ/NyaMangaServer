package httpapi

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	"nyamangaserver.local/server/internal/search"
	"nyamangaserver.local/server/internal/userstate"
)

type onlineFixture struct {
	h             *Handler
	cookie        *http.Cookie
	csrf, library string
	books         []model.Edition
}

func newOnlineFixture(t *testing.T) onlineFixture {
	t.Helper()
	h, cookie, csrf := catalogHandler(t)
	db := handlerDB(t, h)
	h.WithOrganization(organization.New(db), search.New(db), userstate.New(db))
	h.WithHealth(maintenance.NewHealth(db, h.c, h.catalog.Pool))
	call := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := request(t, h, method, "/api/v1"+path, body, cookie, csrf)
		code(t, w, status)
		return w
	}
	library := decodeClientTest[struct{ ID string }](t, call("POST", "/libraries", `{"root_key":"fixture","display_name":"Fixture library"}`, 201)).ID
	job := decodeClientTest[struct {
		ID string `json:"job_id"`
	}](t, call("POST", "/libraries/"+library+"/scans", "{}", 202)).ID
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for {
		v, e := h.catalog.Job(ctx, job)
		if e != nil || v.State == "failed" {
			t.Fatal(v, e)
		}
		if v.State == "completed" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("fixture scan did not complete")
		case <-time.After(5 * time.Millisecond):
		}
	}
	books, _, e := h.catalog.Editions(ctx, library, "available", 1, 10)
	if e != nil || len(books) != 2 || books[0].PageCount != 3 {
		t.Fatal(books, e)
	}
	return onlineFixture{h, cookie, csrf, library, books}
}

func (f onlineFixture) viewer(t *testing.T) (*http.Cookie, deviceLoginResult, context.Context) {
	t.Helper()
	web, _, actor := authorizedTestEditor(t, f.h, f.library)
	db := handlerDB(t, f.h)
	if _, err := db.Write.Exec("UPDATE account_library_grants SET role='viewer' WHERE library_id=?", f.library); err != nil {
		t.Fatal(err)
	}
	return web, deviceTestLogin(t, f.h, "deterministic-editor", "new anonymous editor password"), actor
}

func onlineImagePath(b model.Edition, index int) string {
	return fmt.Sprintf("/client/editions/%s/pages/%d/image?content_revision=%d&parsing_revision=%d", b.ID, index, b.ContentRevision, b.ParsingRevision)
}

func onlineImageRequest(ctx context.Context, path, token, purpose string, tags ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://localhost/api/v1"+path, nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+token)
	if purpose != "" {
		r.Header.Set(imagePurposeHeader, purpose)
	}
	for _, tag := range tags {
		r.Header.Add("If-None-Match", tag)
	}
	return r
}

func TestOnlineImageConditionsCheckLiveAccessAndSource(t *testing.T) {
	f := newOnlineFixture(t)
	web, device, actor := f.viewer(t)
	db := handlerDB(t, f.h)
	b := f.books[0]
	ctx := context.Background()
	path := onlineImagePath(b, 0)
	call := func(r *http.Request, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, r)
		code(t, w, status)
		return w
	}
	get := func(purpose string, tags ...string) *http.Request {
		return onlineImageRequest(ctx, path, device.Token, purpose, tags...)
	}
	first := call(get("display"), 200)
	tag := first.Header().Get("ETag")
	if want := fmt.Sprintf("\"%x\"", sha256.Sum256(first.Body.Bytes())); tag != want || first.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatal("incorrect strong validator or cache policy", first.Header())
	}
	for _, values := range [][]string{{"\"old\"", "W/" + tag}, {"\"old\", W/" + tag}, {"*"}} {
		unchanged := call(get("prefetch", values...), 304)
		if unchanged.Body.Len() != 0 || unchanged.Header().Get("ETag") != tag || unchanged.Header().Get("Cache-Control") != "private, no-cache" {
			t.Fatal("incorrect conditional response", unchanged.Header())
		}
	}
	missed := call(get("prefetch", "\"old\""), 200)
	if missed.Header().Get("ETag") != tag || missed.Body.String() != first.Body.String() {
		t.Fatal("purpose changed representation")
	}
	precondition := get("", tag)
	precondition.Header.Set("If-Match", "\"bad\"")
	call(precondition, 412)
	call(get("invalid", tag), 400)
	legacy := fmt.Sprintf("/editions/%s/pages/1/image?content_revision=%d&parsing_revision=%d", b.ID, b.ContentRevision, b.ParsingRevision)
	oldWeb := httptest.NewRequest("GET", "http://localhost/api/v1"+legacy, nil)
	oldWeb.AddCookie(web)
	oldWeb.Header.Set("If-None-Match", "W/"+tag)
	call(oldWeb, 304)

	file := filepath.Join(f.h.c.Roots[0].Path, b.Path)
	parked := file + ".parked"
	original, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	st, e := os.Stat(file)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(file, parked); e != nil {
		t.Fatal(e)
	}
	call(get("", tag), 409)
	if e = os.WriteFile(file, original, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Chtimes(file, st.ModTime(), st.ModTime()); e != nil {
		t.Fatal(e)
	}
	call(get("", tag), 409)
	if e = os.Rename(parked, file); e != nil {
		t.Fatal(e)
	}
	call(get("", tag), 304)
	mutate := func(sql string, values ...any) {
		t.Helper()
		if _, e := db.Write.Exec(sql, values...); e != nil {
			t.Fatal(e)
		}
	}
	mutate("UPDATE files SET content_revision=content_revision+1 WHERE edition_id=?", b.ID)
	call(get("", tag), 409)
	mutate("UPDATE files SET content_revision=?,parsing_revision=parsing_revision+1 WHERE edition_id=?", b.ContentRevision, b.ID)
	call(get("", tag), 409)
	mutate("UPDATE files SET parsing_revision=? WHERE edition_id=?", b.ParsingRevision, b.ID)
	mutate("UPDATE libraries SET disabled=1 WHERE id=?", f.library)
	call(get("", tag), 409)
	mutate("UPDATE libraries SET disabled=0 WHERE id=?", f.library)
	call(get("", tag), 304)
	ready := make(chan struct{})
	resume := make(chan struct{})
	f.h.beforeImageResponse = func(ctx context.Context) {
		close(ready)
		select {
		case <-resume:
		case <-ctx.Done():
		}
	}
	t.Cleanup(func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	})
	pending := httptest.NewRecorder()
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		f.h.ServeHTTP(pending, get("", tag))
	}()
	select {
	case <-ready:
	case <-exited:
		t.Fatal("conditional request returned before the final access check")
	case <-time.After(5 * time.Second):
		t.Fatal("conditional request did not reach the access check")
	}
	a, _ := model.Actor(actor)
	mutate("DELETE FROM account_library_grants WHERE account_id=? AND library_id=?", a.ID, f.library)
	close(resume)
	<-exited
	code(t, pending, 404)
	f.h.beforeImageResponse = nil
	call(get("", tag), 404)
	call(onlineImageRequest(ctx, path, "invalid-device", "", tag), 401)
}

type stalledImageWriter struct {
	*httptest.ResponseRecorder
	ctx     context.Context
	writing chan struct{}
}

func (w stalledImageWriter) Write(_ []byte) (int, error) {
	close(w.writing)
	<-w.ctx.Done()
	return 0, w.ctx.Err()
}

func TestOnlineImageAdmissionHoldsThroughSlowSendAndCancellation(t *testing.T) {
	f := newOnlineFixture(t)
	_, device, _ := f.viewer(t)
	path := onlineImagePath(f.books[0], 0)
	held := make([]context.CancelFunc, 0, imagesPerSession)
	done := make([]<-chan struct{}, 0, imagesPerSession)
	for range imagesPerSession {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		w := stalledImageWriter{httptest.NewRecorder(), ctx, make(chan struct{})}
		exited := make(chan struct{})
		go func() {
			defer close(exited)
			f.h.ServeHTTP(w, onlineImageRequest(ctx, path, device.Token, "display"))
		}()
		select {
		case <-w.writing:
		case <-exited:
			t.Fatal("image returned without reaching the blocked body", w.Code, w.Body.String())
		case <-time.After(5 * time.Second):
			t.Fatal("image did not reach the blocked body")
		}
		held, done = append(held, cancel), append(done, exited)
	}
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, onlineImageRequest(context.Background(), path, device.Token, "prefetch"))
	code(t, rr, 429)
	if rr.Header().Get("Retry-After") != "1" {
		t.Fatal(rr.Header())
	}
	for i, cancel := range held {
		cancel()
		<-done[i]
	}
	rr = httptest.NewRecorder()
	f.h.ServeHTTP(rr, onlineImageRequest(context.Background(), path, device.Token, "prefetch"))
	code(t, rr, 200)
	f.h.imageWait = time.Nanosecond
	rr = httptest.NewRecorder()
	f.h.ServeHTTP(rr, onlineImageRequest(context.Background(), path, device.Token, "display"))
	code(t, rr, 503)
	if rr.Header().Get("Retry-After") != "1" || !strings.Contains(rr.Body.String(), "image_busy") {
		t.Fatal(rr.Header(), rr.Body.String())
	}
}

func TestOnlineReadingTwoDevicesRestartAndRetry(t *testing.T) {
	f := newOnlineFixture(t)
	_, a, _ := f.viewer(t)
	bDevice := deviceTestLogin(t, f.h, "deterministic-editor", "new anonymous editor password")
	db := handlerDB(t, f.h)
	b, other := f.books[0], f.books[1]
	ctx := context.Background()
	tx, e := db.Write.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "UPDATE editions SET work_id=? WHERE id=?", b.WorkID, other.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM works WHERE id=?", other.WorkID); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	call := func(device deviceLoginResult, method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		return deviceTestCall(t, f.h, method, path, body, device.Token, status)
	}
	contextPath := "/client/works/" + b.WorkID + "/reading-context"
	startPath := "/editions/" + b.ID + "/reading-sessions"
	statePath := "/editions/" + b.ID + "/reading-state"
	getState := func(device deviceLoginResult) model.ReadingState {
		t.Helper()
		return decodeClientTest[model.ReadingState](t, call(device, "GET", statePath, "", 200))
	}
	initial := decodeClientTest[model.ClientReadingContext](t, call(a, "GET", contextPath, "", 200))
	call(a, "GET", strings.TrimPrefix(initial.PagesURL, "/api/v1"), "", 200)
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, onlineImageRequest(ctx, onlineImagePath(b, 1), a.Token, "prefetch"))
	code(t, w, 200)
	w = httptest.NewRecorder()
	f.h.ServeHTTP(w, onlineImageRequest(ctx, onlineImagePath(b, 0), a.Token, "display"))
	code(t, w, 200)
	if state := getState(a); state.ReadCount != 0 || state.LastReadAt != nil {
		t.Fatal("image reading changed user state", state)
	}
	startBody := func(rev int64, key, intent string) string {
		return fmt.Sprintf(`{"expected_revision":%d,"content_revision":%d,"parsing_revision":%d,"idempotency_key":%q,"intent":%q}`, rev, b.ContentRevision, b.ParsingRevision, key, intent)
	}
	startReq := startBody(0, "displayed-first", "start")
	start := decodeClientTest[model.ReadingSession](t, call(a, "POST", startPath, startReq, 200))
	replayed := decodeClientTest[model.ReadingSession](t, call(a, "POST", startPath, startReq, 200))
	if replayed.ID != start.ID || replayed.State.ReadCount != 1 {
		t.Fatal("lost start response caused duplicate read")
	}
	frozenProgress := func(rev int64, page int, key string) string {
		return fmt.Sprintf(`{"expected_revision":%d,"content_revision":%d,"parsing_revision":%d,"idempotency_key":%q,"action":"progress","page_index":%d}`, rev, b.ContentRevision, b.ParsingRevision, key, page)
	}
	// Only the last pending page is sent. Further user navigation leaves the
	// submitted payload and idempotency key frozen until its result is known.
	pending := 0
	for _, page := range []int{1, 0, 2} {
		pending = page
	}
	frozen := frozenProgress(start.State.Revision, pending, "pending-progress")
	pending = 1
	call(a, "PUT", statePath, frozen, 200) // response lost locally
	applied := decodeClientTest[model.ReadingState](t, call(a, "PUT", statePath, frozen, 200))
	if applied.PageIndex == nil || *applied.PageIndex != 2 || applied.Revision != start.State.Revision+1 {
		t.Fatal(applied)
	}
	conflict := call(bDevice, "PUT", statePath, frozenProgress(start.State.Revision, pending, "other-device-write"), 409)
	if !strings.Contains(conflict.Body.String(), `"page_index":2`) {
		t.Fatal("conflict lacks remote state", conflict.Body.String())
	}
	setHealth := func(phase string) {
		t.Helper()
		if _, e := db.Write.Exec("UPDATE health_state SET phase=? WHERE singleton=1", phase); e != nil {
			t.Fatal(e)
		}
	}
	waiting := frozenProgress(applied.Revision, pending, "maintenance-pending")
	setHealth("recovery_required")
	call(a, "PUT", statePath, waiting, 503)
	if state := getState(a); state.Revision != applied.Revision {
		t.Fatal("maintenance committed a write", state)
	}
	setHealth("idle")
	applied = decodeClientTest[model.ReadingState](t, call(a, "PUT", statePath, waiting, 200))
	complete := fmt.Sprintf(`{"expected_revision":%d,"content_revision":%d,"parsing_revision":%d,"idempotency_key":"explicit-complete","action":"complete"}`, applied.Revision, b.ContentRevision, b.ParsingRevision)
	completed := decodeClientTest[model.ReadingState](t, call(a, "PUT", statePath, complete, 200))
	reopened := decodeClientTest[model.ReadingSession](t, call(a, "POST", startPath, startBody(completed.Revision, "opened-complete", "start"), 200))
	turned := decodeClientTest[model.ReadingState](t, call(a, "PUT", statePath, frozenProgress(reopened.State.Revision, 2, "flip-completed"), 200))
	if !reopened.State.Completed || !turned.Completed {
		t.Fatal("ordinary reading cleared completion", reopened.State, turned)
	}
	searchPath := "/client/works/search"
	query := fmt.Sprintf(`{"library_id":%q,"scope":"continue","sort":"last_read_at"}`, f.library)
	empty := decodeClientTest[model.ClientSearchResult](t, call(a, "POST", searchPath, query, 200))
	if empty.Total != 0 {
		t.Fatal("completed work entered continue", empty)
	}
	restart := startBody(turned.Revision, "explicit-restart", "restart")
	r := decodeClientTest[model.ReadingSession](t, call(bDevice, "POST", startPath, restart, 200))
	retry := decodeClientTest[model.ReadingSession](t, call(bDevice, "POST", startPath, restart, 200))
	if r.State.Completed || r.State.CompletedAt != nil || r.State.ReadCount != reopened.State.ReadCount+1 ||
		r.State.PageIndex == nil || *r.State.PageIndex != 0 || retry.ID != r.ID || retry.State.Revision != r.State.Revision {
		t.Fatal("restart or replay", r, retry)
	}
	call(bDevice, "POST", startPath, startBody(turned.Revision, "explicit-restart", "start"), 409)
	call(a, "POST", searchPath, strings.TrimSuffix(query, "}")+fmt.Sprintf(`,"snapshot_revision":%q}`, empty.Snapshot), 409)
	if _, e = db.Write.Exec("UPDATE works SET preferred_edition_id=? WHERE id=?", other.ID, b.WorkID); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Write.Exec("UPDATE files SET file_state='missing',content_revision=content_revision+1 WHERE edition_id=?", b.ID); e != nil {
		t.Fatal(e)
	}
	resumed := decodeClientTest[model.ClientReadingContext](t, call(a, "GET", contextPath+"?intent=continue", "", 200))
	hits := decodeClientTest[model.ClientSearchResult](t, call(bDevice, "POST", searchPath, query, 200))
	if resumed.Edition.ID != b.ID || resumed.Edition.Readability.Readable || !resumed.RequiresContentConfirmation ||
		hits.Total != 1 || hits.Items[0].ResumeEditionID == nil || *hits.Items[0].ResumeEditionID != b.ID || hits.Items[0].EntryEditionID != other.ID {
		t.Fatal("continue changed Edition after preferred change or offline", resumed, hits)
	}
	legacy := "/api/v1/books/" + b.ID + "/reading-state"
	if w = request(t, f.h, "GET", legacy, "", f.cookie, f.csrf); w.Code != 200 {
		t.Fatal("legacy reading route failed", w.Code, w.Body.String())
	}
}
