package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"nyamangaserver.local/server/internal/auth"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/enhancements"
	"nyamangaserver.local/server/internal/httpapi"
	"nyamangaserver.local/server/internal/importflow"
	"nyamangaserver.local/server/internal/library"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/metadata"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"nyamangaserver.local/server/internal/recommendation"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/internal/tagimages"
	"nyamangaserver.local/server/internal/testfixture"
	"nyamangaserver.local/server/migrations"
)

func TestAppHealthAdmissionPreservesOrdinaryAccountPermissions(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	c := config.Config{PublicURL: "http://localhost", DataDir: data, ProviderKeyFile: filepath.Join(data, "provider.key"), ProviderCredentialsFile: filepath.Join(data, "credentials")}
	db, e := store.Open(ctx, data, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := auth.New(db, "anonymous-setup")
	catalog := library.New(db, c, logger)
	defer catalog.Close()
	meta := metadata.New(db, catalog, provider.New(provider.NewVault(c.ProviderKeyFile, c.ProviderCredentialsFile)), data, logger)
	rec := recommendation.New(db, catalog, nil, data, logger)
	images := tagimages.New(db, c, catalog.Pool)
	maint := maintenance.New(db, c, catalog, drainServices{meta, rec}, logger).WithTagImages(images)
	imports := importflow.New(db, catalog, logger, func() {})
	enhancement := enhancements.New(db, enhancementRuntime{rec, meta, db})
	h, health, _ := assembleHTTP(ctx, c, logger, svc, db, catalog, meta, maint, rec, imports, images, enhancement, httpapi.Build{Version: "test", Commit: "anonymous"}, maintenance.InstanceBuild{Version: "test", Commit: "anonymous"})
	// Use the production handler assembly; no workers, listeners, real files or IO start.
	call := func(t *testing.T, method, path, body string, cookie *http.Cookie, csrf string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, c.PublicURL+"/api/v1"+path, strings.NewReader(body))
		r.RemoteAddr = "192.0.2.10:12345"
		if strings.HasSuffix(t.Name(), "/viewer") {
			r.RemoteAddr = "192.0.2.11:12345"
		}
		if strings.HasSuffix(t.Name(), "/editor") {
			r.RemoteAddr = "192.0.2.12:12345"
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", c.PublicURL)
		r.Header.Set("X-CSRF-Token", csrf)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	session := func(t *testing.T, w *httptest.ResponseRecorder) (*http.Cookie, string) {
		t.Helper()
		var out struct {
			CSRF string `json:"csrf_token"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(e)
		}
		cookies := w.Result().Cookies()
		if len(cookies) == 0 || out.CSRF == "" {
			t.Fatal("missing session", w.Body.String())
		}
		return cookies[0], out.CSRF
	}
	admin, adminCSRF := session(t, call(t, "POST", "/auth/setup", `{"setup_token":"anonymous-setup","password":"anonymous admin password"}`, nil, "", 200))
	if _, e = db.Write.Exec(`INSERT INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision) VALUES('lib','anonymous','anonymous','utf8','anonymous',1),('private','private','private','utf8','private',1)`); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{"lib", "private"} {
		if e = testfixture.SeedEdition(ctx, db.Write, id, id, id+".zip", id); e != nil {
			t.Fatal(e)
		}
	}
	if e = db.ApplyOverrides(ctx, "lib", 1, map[string]model.Override{"tags": {Op: "set", Value: json.RawMessage(`[{"namespace":"artist","name":"anonymous"}]`)}}); e != nil {
		t.Fatal(e)
	}
	for _, role := range []string{"viewer", "editor"} {
		t.Run(role, func(t *testing.T) {
			w := call(t, "POST", "/accounts", fmt.Sprintf(`{"username":%q,"password":"anonymous initial password"}`, role), admin, adminCSRF, 201)
			var account model.AccountView
			if e := json.Unmarshal(w.Body.Bytes(), &account); e != nil {
				t.Fatal(e)
			}
			call(t, "PUT", "/accounts/"+account.ID, fmt.Sprintf(`{"expected_revision":1,"role":"user","enabled":true,"library_grants":{"lib":%q}}`, role), admin, adminCSRF, 204)
			cookie, csrf := session(t, call(t, "POST", "/auth/login", fmt.Sprintf(`{"username":%q,"password":"anonymous initial password"}`, role), nil, "", 200))
			call(t, "GET", "/maintenance/health-state", "", cookie, csrf, 403)
			call(t, "PUT", "/auth/password", `{"current_password":"anonymous initial password","new_password":"anonymous new password"}`, cookie, csrf, 200)
			cookie, csrf = session(t, call(t, "POST", "/auth/login", fmt.Sprintf(`{"username":%q,"password":"anonymous new password"}`, role), nil, "", 200))
			call(t, "GET", "/maintenance/health-state", "", cookie, csrf, 403)
			call(t, "GET", "/maintenance/instance-state", "", cookie, csrf, 403)
			call(t, "GET", "/settings/enhancements", "", cookie, csrf, 403)
			call(t, "PATCH", "/editions/lib/user-state", `{"expected_revision":0,"favorite":true}`, cookie, csrf, 200)
			call(t, "POST", "/editions/lib/reading-sessions", `{"expected_revision":0,"content_revision":1,"idempotency_key":"anonymous-start"}`, cookie, csrf, 200)
			tag := fmt.Sprintf("%x", sha256.Sum256([]byte("artist\x00anonymous")))
			call(t, "PUT", "/tags/"+tag+"/follow", `{"expected_revision":0}`, cookie, csrf, 200)
			call(t, "PATCH", "/editions/private/user-state", `{"expected_revision":0,"favorite":true}`, cookie, csrf, 404)
			actor, e := db.AccountByID(ctx, account.ID)
			if e != nil {
				t.Fatal(e)
			}
			actorCtx := model.WithActor(ctx, *actor)
			if release, e := db.BeginHealthIO(actorCtx); e != nil {
				t.Fatal("ordinary IO rejected", e)
			} else {
				release()
			}
			if e = health.CheckWriteAllowed(actorCtx); e != nil {
				t.Fatal(e)
			}
			if _, e = health.State(actorCtx); e == nil {
				t.Fatal("ordinary maintenance details exposed")
			}
			want := 403
			if role == "editor" {
				want = 200
			}
			book, e := db.Edition(ctx, "lib")
			if e != nil {
				t.Fatal(e)
			}
			call(t, "PATCH", "/editions/lib/overrides", fmt.Sprintf(`{"expected_revision":%d,"fields":{"description":{"op":"set","value":"anonymous edit"}}}`, book.Revision), cookie, csrf, want)
			// Durable barrier remains authoritative even after an HTTP admission succeeds.
			if _, e = db.Write.Exec("UPDATE health_state SET phase='recovery_required',revision=revision+1"); e != nil {
				t.Fatal(e)
			}
			call(t, "PATCH", "/editions/lib/user-state", `{"expected_revision":1,"favorite":false}`, cookie, csrf, 503)
			call(t, "PUT", "/auth/password", `{"current_password":"anonymous new password","new_password":"another anonymous password"}`, cookie, csrf, 503)
			call(t, "PUT", "/editions/lib/reading-state", `{"expected_revision":1,"content_revision":1,"idempotency_key":"blocked","action":"heartbeat"}`, cookie, csrf, 503)
			if release, e := db.BeginHealthIO(actorCtx); e == nil {
				release()
				t.Fatal("maintenance IO allowed")
			}
			no := false
			if _, e = db.SetUserState(actorCtx, account.ID, "lib", model.StatePatch{Expected: 1, Favorite: &no}); e == nil || !strings.Contains(e.Error(), "maintenance_active") {
				t.Fatal("SQL final barrier bypassed")
			}
			if _, e = db.Write.Exec("UPDATE health_state SET phase='idle',revision=revision+1"); e != nil {
				t.Fatal(e)
			}
		})
	}
}
