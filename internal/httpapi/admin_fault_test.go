package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"

	"nyamangaserver.local/server/internal/model"
)

func TestAdminFaultErrorMappingAndRedaction(t *testing.T) {
	h, _ := testHandler(t, "http://localhost")
	for _, test := range []struct {
		name   string
		cause  error
		status int
		code   string
	}{
		{"disk full", syscall.ENOSPC, 507, "storage_full"},
		{"permission", os.ErrPermission, 403, "permission_denied"},
		{"timeout", context.DeadlineExceeded, 504, "request_timeout"},
		{"busy", model.ErrBusy, 409, "resource_busy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "http://localhost/api/v1/maintenance/plans", nil)
			out := httptest.NewRecorder()
			out.Header().Set("X-Request-ID", "anonymous-request")
			err := errors.Join(&os.PathError{Op: "write", Path: "/private/anonymous-path-must-not-leak", Err: test.cause})
			h.adminFailure(out, request, err)
			if out.Code != test.status {
				t.Fatal(out.Code, out.Body.String())
			}
			var body struct {
				Error struct {
					Code      string `json:"code"`
					RequestID string `json:"request_id"`
				} `json:"error"`
			}
			if e := json.Unmarshal(out.Body.Bytes(), &body); e != nil || body.Error.Code != test.code || body.Error.RequestID != "anonymous-request" {
				t.Fatal(body, e)
			}
			if strings.Contains(out.Body.String(), "anonymous-path-must-not-leak") {
				t.Fatal("private filesystem path exposed")
			}
		})
	}
}
