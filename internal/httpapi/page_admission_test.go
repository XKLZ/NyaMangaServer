package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"nyamangaserver.local/server/internal/model"
)

func TestImageAdmissionCountsSessionsAndAllRequests(t *testing.T) {
	h, _ := testHandler(t, "http://localhost")
	type testAdmission struct {
		principal identity
		release   func()
	}
	var holds []testAdmission
	admit := func(principal identity, purpose string, expected int) func() {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/fixture", nil)
		r = r.WithContext(context.WithValue(r.Context(), sessionKey{}, principal))
		r.Header.Set(imagePurposeHeader, purpose)
		w := httptest.NewRecorder()
		_, free, admitted := h.admitImage(w, r)
		if expected == 0 {
			if !admitted {
				t.Fatal("unexpected denial", w.Body.String())
			}
		} else {
			if admitted || w.Code != expected || w.Header().Get("Retry-After") != "1" {
				t.Fatal(w.Code, w.Header(), w.Body.String())
			}
		}
		return free
	}
	for i := range imagesGlobal / imagesPerSession {
		principal := identity{device: &model.DeviceSession{ID: strconv.Itoa(i)}}
		if i == 0 {
			principal = identity{session: &model.Session{Hash: []byte("anonymous-cookie-digest")}}
		}
		for range imagesPerSession {
			holds = append(holds, testAdmission{principal, admit(principal, "display", 0)})
		}
		admit(principal, "prefetch", http.StatusTooManyRequests)
	}
	extra := identity{device: &model.DeviceSession{ID: "extra"}}
	admit(extra, "display", http.StatusServiceUnavailable)
	holds[0].release()
	free := admit(extra, "prefetch", 0)
	free()
	for _, held := range holds[1:] {
		held.release()
	}
	if h.pageRequests.total != 0 || len(h.pageRequests.bySession) != 0 {
		t.Fatal("leaked HTTP admission")
	}
}
