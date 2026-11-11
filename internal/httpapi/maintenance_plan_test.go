package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/model"
)

func TestMaintenancePlanReadPublicContractResultsAndPermissions(t *testing.T) {
	h, admin, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	ctx := context.Background()
	m := maintenance.New(db, h.c, h.catalog, h.metadata, h.logger)
	var reads atomic.Int64
	m.CleanupPreviewReader = func(r io.Reader) io.Reader { reads.Add(1); return r }
	h.WithMaintenance(m)
	dir := filepath.Join(h.c.DataDir, "tmp")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	key := "tmp/private-frozen-fixture.bin"
	path := filepath.Join(h.c.DataDir, key)
	raw := []byte("anonymous bytes actually removed")
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	old := time.Now().Add(-48 * time.Hour)
	if e := os.Chtimes(path, old, old); e != nil {
		t.Fatal(e)
	}
	doc, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers[0].URL = "http://localhost/api/v1"
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path, body string, cookie *http.Cookie, token string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", h.c.PublicURL)
		r.Header.Set("X-CSRF-Token", token)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		route, params, e := router.FindRoute(r)
		if e != nil {
			t.Fatal(e)
		}
		in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
		if e = openapi3filter.ValidateRequest(ctx, in); e != nil {
			t.Fatal(e)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, want)
		out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: w.Code, Header: w.Header()}
		out.SetBodyBytes(w.Body.Bytes())
		if e = openapi3filter.ValidateResponse(ctx, out); e != nil {
			t.Fatal(e)
		}
		for _, private := range []string{h.c.DataDir, key, "_cleanup_files", "_storage", "_internal_test", "frozen-secret-marker", "/anonymous/external/archive", "\"input\"", "\"options\"", "\"device\"", "\"inode\""} {
			if strings.Contains(w.Body.String(), private) {
				t.Fatal("private execution material leaked", private)
			}
		}
		result := map[string]any{}
		if e = json.Unmarshal(w.Body.Bytes(), &result); e != nil {
			t.Fatal(e)
		}
		return result
	}
	plan := call("POST", "/maintenance/plans", `{"operation":"cleanup","expected_revisions":{},"options":{"categories":["tmp"]}}`, admin, csrf, 200)
	id := plan["id"].(string)
	endpoint := "/maintenance/plans/" + id
	if _, ok := plan["job_id"]; ok {
		t.Fatal("unexecuted plan already has job")
	}
	frozen, e := db.MaintenancePlan(ctx, id)
	if e != nil || !strings.Contains(string(frozen.Input.Options), "_cleanup_files") || !strings.Contains(string(frozen.Input.Options), key) {
		t.Fatal("internal frozen plan missing", e)
	}
	// Prove internal input remains usable and private, even with external paths
	// and secrets in a future internal extension. Never put these in summary.
	private := `{"archive_manifest":"/anonymous/external/archive/originals.json","secret":"frozen-secret-marker"}`
	if _, e = db.Write.Exec("UPDATE maintenance_plans SET input_json=json_set(input_json,'$.options._internal_test',json(?)) WHERE id=?", private, id); e != nil {
		t.Fatal(e)
	}
	got := call("GET", endpoint, "", admin, "", 200)
	if got["id"] != id || got["operation"] != "cleanup" || got["expires_at"] != plan["expires_at"] {
		t.Fatal(got)
	}
	if reads.Load() != 1 {
		t.Fatal("GET walked/hashed preview", reads.Load())
	}
	call("GET", "/maintenance/plans/nonexistent", "", admin, "", 404)
	call("GET", endpoint, "", nil, "", 401)
	owner, e := db.Account(ctx)
	if e != nil {
		t.Fatal(e)
	}
	adminCtx := model.WithActor(ctx, *owner)
	for _, role := range []string{"editor", "viewer"} {
		account, e := h.svc.CreateAccount(adminCtx, "plan-"+role, "anonymous initial password")
		if e != nil {
			t.Fatal(e)
		}
		if e = h.svc.UpdateAccount(adminCtx, account.ID, model.AccountUpdate{Expected: 1, Role: "user", Enabled: true, Grants: map[string]string{"lib": role}}, nil); e != nil {
			t.Fatal(e)
		}
		if e = h.svc.ChangePassword(ctx, account.ID, "anonymous initial password", "anonymous changed password"); e != nil {
			t.Fatal(e)
		}
		token, session, e := h.svc.LoginUsername(ctx, "plan-"+role, "anonymous changed password")
		if e != nil {
			t.Fatal(e)
		}
		cookie := &http.Cookie{Name: h.cookieName(), Value: token}
		call("GET", endpoint, "", cookie, "", 403)
		call("GET", "/maintenance/plans/nonexistent", "", cookie, "", 403)
		if _, e = m.GetPlan(model.WithActor(ctx, *session.Account), id); !errors.Is(e, model.ErrForbidden) {
			t.Fatal("domain admission bypassed", role, e)
		}
	}
	// Queue before starting the worker: GET must immediately return the durable ID.
	execution := call("POST", endpoint+"/execute", `{}`, admin, csrf, 202)
	jobID := execution["job_id"].(string)
	got = call("GET", endpoint, "", admin, "", 200)
	if got["job_id"] != jobID {
		t.Fatal(got)
	}
	m.Start(ctx)
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if e := m.Shutdown(stop); e != nil {
			t.Error(e)
		}
	})
	for deadline := time.Now().Add(5 * time.Second); ; {
		job, e := db.Job(ctx, jobID)
		if e != nil {
			t.Fatal(e)
		}
		if job.State == "completed" {
			break
		}
		if job.State == "failed" || time.Now().After(deadline) {
			t.Fatal(job)
		}
		time.Sleep(time.Millisecond)
	}
	got = call("GET", endpoint, "", admin, "", 200)
	result := got["summary"].(map[string]any)["cleanup_result"].(map[string]any)
	if result["released_bytes"] != float64(len(raw)) || result["deleted_files"] != float64(1) || result["next_index"] != float64(1) || result["skipped_files"] != float64(0) || result["failed_files"] != float64(0) {
		t.Fatal(result)
	}
	if result["status"] != "completed" || result["phase"] != "finished" || result["committed_batches"] != float64(1) {
		t.Fatal(result)
	}
	if _, e = os.Stat(path); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("bytes were not really removed", e)
	}
	if reads.Load() != 1 {
		t.Fatal("history GET rehashed preview", reads.Load())
	}
	if _, e = db.Write.Exec("UPDATE maintenance_plans SET expires_at=1,summary_json=json_set(summary_json,'$.expires_at',1) WHERE id=?", id); e != nil {
		t.Fatal(e)
	}
	got = call("GET", endpoint, "", admin, "", 200)
	if got["expires_at"] != float64(1) || got["job_id"] != jobID || got["summary"].(map[string]any)["cleanup_result"].(map[string]any)["released_bytes"] != float64(len(raw)) {
		t.Fatal("terminal history not readable", got)
	}
}
