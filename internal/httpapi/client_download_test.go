package httpapi

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"nyamangaserver.local/server/internal/model"
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
