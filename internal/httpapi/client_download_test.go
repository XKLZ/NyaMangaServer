package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"nyamangaserver.local/server/internal/library"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
)

func readyDownload(t *testing.T, f onlineFixture, token string, b model.Edition, key string) model.Download {
	t.Helper()
	req := fmt.Sprintf(`{"content_revision":%d,"idempotency_key":%q}`, b.ContentRevision, key)
	out := decodeClientTest[model.Download](t, deviceTestCall(t, f.h, "POST", "/client/editions/"+b.ID+"/downloads", req, token, 202))
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		out = decodeClientTest[model.Download](t, deviceTestCall(t, f.h, "GET", "/client/downloads/"+out.ID, "", token, 200))
		if out.State == "ready" {
			return out
		}
		if out.State != "preparing" {
			t.Fatal(out)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("download preparation did not finish")
	return out
}

func TestClientOriginalDownloadHTTP(t *testing.T) {
	f := newOnlineFixture(t)
	_, d, _ := f.viewer(t)
	b := f.books[0]
	out := readyDownload(t, f, d.Token, b, "download-fixture")
	original, err := os.ReadFile(filepath.Join(f.h.c.Roots[0].Path, b.Path))
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(original))
	if out.SHA256 != hash || out.Length != int64(len(original)) {
		t.Fatal(out)
	}
	path := "/client/downloads/" + out.ID
	tag := "\"" + hash + "\""
	call := func(method, byteRange, ifRange string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost/api/v1"+path+"/content", nil)
		r.Header.Set("Authorization", "Bearer "+d.Token)
		if byteRange != "" {
			r.Header.Set("Range", byteRange)
		}
		if ifRange != "" {
			r.Header.Set("If-Range", ifRange)
		}
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, r)
		code(t, w, want)
		return w
	}
	head := call("HEAD", "", "", 200)
	if head.Body.Len() != 0 || head.Header().Get("Content-Length") != strconv.Itoa(len(original)) || head.Header().Get("ETag") != tag ||
		!strings.HasPrefix(head.Header().Get("Content-Disposition"), "attachment") || strings.Contains(head.Header().Get("Content-Disposition"), f.h.c.Roots[0].Path) {
		t.Fatal(head.Header())
	}
	split := len(original) / 2
	first := call(http.MethodGet, fmt.Sprintf("bytes=0-%d", split-1), tag, 206)
	second := call(http.MethodGet, fmt.Sprintf("bytes=%d-", split), tag, 206)
	if !bytes.Equal(append(append([]byte{}, first.Body.Bytes()...), second.Body.Bytes()...), original) ||
		first.Header().Get("Content-Range") != fmt.Sprintf("bytes 0-%d/%d", split-1, len(original)) {
		t.Fatal(first.Header(), second.Header())
	}
	if full := call("GET", "bytes=0-3", "\"other\"", 200); !bytes.Equal(full.Body.Bytes(), original) {
		t.Fatal("If-Range mismatch returned partial")
	}
	call("GET", "bytes=0-0,1-1", "", 400)
	call("GET", fmt.Sprintf("bytes=%d-", len(original)), "", 416)
	req := fmt.Sprintf(`{"content_revision":%d,"idempotency_key":"another-download"}`, b.ContentRevision)
	reuse := decodeClientTest[model.Download](t, deviceTestCall(t, f.h, "POST", "/client/editions/"+b.ID+"/downloads", req, d.Token, 200))
	if reuse.SHA256 != hash || reuse.ID == out.ID {
		t.Fatal(reuse)
	}
	admin := deviceTestLogin(t, f.h, "admin", "anonymous password")
	deviceTestCall(t, f.h, "GET", path, "", admin.Token, 404)
	deviceTestCall(t, f.h, "DELETE", path, "", d.Token, 204)
	call("GET", "", "", 409)
}

func TestClientDownloadPreparationRejectsChangedSource(t *testing.T) {
	f := newOnlineFixture(t)
	_, d, _ := f.viewer(t)
	b := f.books[0]
	entered, resume := make(chan struct{}), make(chan struct{})
	f.h.catalog.SetDownloadBlockHook(func(ctx context.Context) {
		close(entered)
		select {
		case <-resume:
		case <-ctx.Done():
		}
	})
	defer func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	}()
	req := fmt.Sprintf(`{"content_revision":%d,"idempotency_key":"changed-source-download"}`, b.ContentRevision)
	out := decodeClientTest[model.Download](t, deviceTestCall(t, f.h, "POST", "/client/editions/"+b.ID+"/downloads", req, d.Token, 202))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not enter first block")
	}
	file := filepath.Join(f.h.c.Roots[0].Path, b.Path)
	st, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(file, st.ModTime().Add(time.Second), st.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	close(resume)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		v := decodeClientTest[model.Download](t, deviceTestCall(t, f.h, "GET", "/client/downloads/"+out.ID, "", d.Token, 200))
		if v.State == "failed" {
			if v.Error != "content_changed" || v.SHA256 != "" {
				t.Fatal(v)
			}
			deviceTestCall(t, f.h, "GET", "/client/downloads/"+out.ID+"/content", "", d.Token, 409)
			return
		}
		if v.State != "preparing" {
			t.Fatal(v)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("changed download remained in preparing state")
}

func TestClientDownloadDigestSurvivesReopen(t *testing.T) {
	f := newOnlineFixture(t)
	_, d, actor := f.viewer(t)
	b := f.books[0]
	first := readyDownload(t, f, d.Token, b, "before-reopen")
	db := handlerDB(t, f.h)
	var idx int
	var name, file string
	if err := db.Read.QueryRow("PRAGMA database_list").Scan(&idx, &name, &file); err != nil {
		t.Fatal(err)
	}
	f.h.catalog.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), filepath.Dir(file), migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	svc := library.New(reopened, f.h.c, f.h.logger)
	defer svc.Close()
	v, err := svc.PrepareDownload(actor, b.ID, model.DownloadRequest{ContentRevision: b.ContentRevision, Key: "after-reopen"})
	if err != nil || v.State != "ready" || v.SHA256 != first.SHA256 || v.ID == first.ID {
		t.Fatal(v, err)
	}
}

type callbackDownloadWriter struct {
	*httptest.ResponseRecorder
	first func()
}

func (w *callbackDownloadWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(data)
	if w.first != nil {
		f := w.first
		w.first = nil
		f()
	}
	return n, err
}

type disconnectedDownloadWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w disconnectedDownloadWriter) Write(_ []byte) (int, error) {
	w.cancel()
	return 0, io.ErrClosedPipe
}

func TestClientDownloadStreamAbortsOnRevocation(t *testing.T) {
	f := newOnlineFixture(t)
	_, dev, actor := f.viewer(t)
	b := f.books[0]
	db := handlerDB(t, f.h)
	file := filepath.Join(f.h.c.Roots[0].Path, b.Path)
	var raw bytes.Buffer
	zipper := zip.NewWriter(&raw)
	entry, e := zipper.CreateHeader(&zip.FileHeader{Name: "page.png", Method: zip.Store})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = entry.Write(make([]byte, 2<<20)); e != nil {
		t.Fatal(e)
	}
	if e = zipper.Close(); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(file, raw.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(file)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Write.Exec("UPDATE files SET size=?,mtime_ns=?,content_revision=content_revision+1 WHERE edition_id=?", info.Size(), info.ModTime().UnixNano(), b.ID); e != nil {
		t.Fatal(e)
	}
	b.ContentRevision++
	d := readyDownload(t, f, dev.Token, b, "large-original-download")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	contentPath := "/api/v1/client/downloads/" + d.ID + "/content"
	disconnected := disconnectedDownloadWriter{httptest.NewRecorder(), cancel}
	r1 := httptest.NewRequest("GET", "http://localhost"+contentPath, nil).WithContext(ctx)
	r1.Header.Set("Authorization", "Bearer "+dev.Token)
	f.h.ServeHTTP(disconnected, r1)
	if ctx.Err() == nil || len(f.h.downloads.active) != 0 {
		t.Fatal("disconnect did not release slot")
	}
	retry := httptest.NewRequest("GET", "http://localhost"+contentPath, nil)
	retry.Header.Set("Authorization", "Bearer "+dev.Token)
	retry.Header.Set("Range", "bytes=0-1023")
	resumed := httptest.NewRecorder()
	f.h.ServeHTTP(resumed, retry)
	code(t, resumed, 206)
	if !bytes.Equal(resumed.Body.Bytes(), raw.Bytes()[:1024]) {
		t.Fatal("range retry after disconnect differed")
	}

	a, _ := model.Actor(actor)
	w := &callbackDownloadWriter{ResponseRecorder: httptest.NewRecorder(), first: func() {
		if _, e := db.Write.Exec("DELETE FROM account_library_grants WHERE account_id=?", a.ID); e != nil {
			t.Error(e)
		}
	}}
	r := httptest.NewRequest("GET", "http://localhost/api/v1/client/downloads/"+d.ID+"/content", nil)
	r.Header.Set("Authorization", "Bearer "+dev.Token)
	var interrupted any
	func() { defer func() { interrupted = recover() }(); f.h.ServeHTTP(w, r) }()
	if interrupted != http.ErrAbortHandler || w.Code != 200 || w.Body.Len() >= raw.Len() || w.Body.Len() == 0 {
		t.Fatal("revoked transfer was not aborted", interrupted, w.Code, w.Body.Len(), raw.Len())
	}
	if len(f.h.downloads.active) != 0 {
		t.Fatal("aborted transfer retained a slot")
	}
	deviceTestCall(t, f.h, "HEAD", "/client/downloads/"+d.ID+"/content", "", dev.Token, 404)
}

func TestClientDownloadRangeAndAdmissionBounds(t *testing.T) {
	for _, raw := range []string{"bytes=0-16777216", "bytes=-16777217", "bytes=0-", "bytes=0-1,2-3"} {
		if _, ok := downloadRangeEnd([]string{raw}, 32<<20); ok {
			t.Fatal(raw)
		}
	}
	if end, ok := downloadRangeEnd([]string{"bytes=0-16777215"}, 32<<20); !ok || end != 16<<20 {
		t.Fatal(end, ok)
	}
	d := downloadRequests{}
	a, status := d.acquire("a")
	if status != 0 {
		t.Fatal(status)
	}
	defer a()
	if _, status = d.acquire("a"); status != 429 {
		t.Fatal(status)
	}
	b, status := d.acquire("b")
	if status != 0 {
		t.Fatal(status)
	}
	defer b()
	if _, status = d.acquire("c"); status != 503 {
		t.Fatal(status)
	}
}

func TestClientDownloadPreparationStopsAfterRevocation(t *testing.T) {
	f := newOnlineFixture(t)
	_, dev, actor := f.viewer(t)
	b := f.books[0]
	entered, resume := make(chan struct{}), make(chan struct{})
	f.h.catalog.SetDownloadBlockHook(func(ctx context.Context) {
		close(entered)
		select {
		case <-resume:
		case <-ctx.Done():
		}
	})
	defer func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	}()
	req := fmt.Sprintf(`{"content_revision":%d,"idempotency_key":"download-revoke-preparation"}`, b.ContentRevision)
	d := decodeClientTest[model.Download](t, deviceTestCall(t, f.h, "POST", "/client/editions/"+b.ID+"/downloads", req, dev.Token, 202))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not reach first block")
	}
	a, _ := model.Actor(actor)
	db := handlerDB(t, f.h)
	if _, e := db.Write.Exec("DELETE FROM account_library_grants WHERE account_id=?", a.ID); e != nil {
		t.Fatal(e)
	}
	close(resume)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var state, digest string
		if e := db.Read.QueryRow("SELECT state,sha256 FROM client_downloads WHERE id=?", d.ID).Scan(&state, &digest); e != nil {
			t.Fatal(e)
		}
		if state == "failed" && digest == "" {
			deviceTestCall(t, f.h, "GET", "/client/downloads/"+d.ID, "", dev.Token, 404)
			return
		}
		if state != "preparing" {
			t.Fatal(state, digest)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("revoked download was not stopped")
}
