package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"nyamangaserver.local/server/internal/auth"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	"nyamangaserver.local/server/internal/search"
	"nyamangaserver.local/server/internal/testfixture"
	"nyamangaserver.local/server/internal/userstate"
	"strings"
	"testing"
)

func TestAuthorizationViewerRealWebRoutesAndDefaultDeny(t *testing.T) {
	h, adminCookie, adminCSRF := metadataHandler(t)
	db := handlerDB(t, h)
	h.WithOrganization(organization.New(db), search.New(db), userstate.New(db))
	h.WithMaintenance(maintenance.New(db, h.c, h.catalog, h.metadata, h.logger))
	if _, e := db.Write.Exec(`INSERT INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision) VALUES('private','private','private anonymous','utf8','private',1)`); e != nil {
		t.Fatal(e)
	}
	if e := testfixture.SeedEdition(context.Background(), db.Write, "private-edition", "private", "private.zip", "private anonymous"); e != nil {
		t.Fatal(e)
	}
	create := request(t, h, "POST", "/api/v1/accounts", `{"username":"viewer","password":"viewer password"}`, adminCookie, adminCSRF)
	code(t, create, 201)
	var a model.AccountView
	if e := json.Unmarshal(create.Body.Bytes(), &a); e != nil {
		t.Fatal(e)
	}
	code(t, request(t, h, "PUT", "/api/v1/accounts/"+a.ID, `{"expected_revision":1,"role":"user","enabled":true,"library_grants":{"lib":"viewer"}}`, adminCookie, adminCSRF), 204)
	login := request(t, h, "POST", "/api/v1/auth/login", `{"username":"viewer","password":"viewer password"}`, nil, "")
	code(t, login, 200)
	cookie, csrf := sessionResponse(t, login)
	code(t, request(t, h, "GET", "/api/v1/libraries", "", cookie, csrf), 403)
	code(t, request(t, h, "PUT", "/api/v1/auth/password", `{"current_password":"viewer password","new_password":"viewer new password"}`, cookie, csrf), 200)
	login = request(t, h, "POST", "/api/v1/auth/login", `{"username":"viewer","password":"viewer new password"}`, nil, "")
	code(t, login, 200)
	cookie, csrf = sessionResponse(t, login)
	for _, p := range []string{"/api/v1/libraries", "/api/v1/editions?library_id=lib", "/api/v1/editions/book"} {
		w := request(t, h, "GET", p, "", cookie, csrf)
		code(t, w, 200)
		if strings.Contains(w.Body.String(), "private") {
			t.Fatal("private data leaked", p, w.Body.String())
		}
	}
	for _, p := range []string{"/api/v1/editions/private-edition", "/api/v1/editions/private-edition/cover?content_revision=1", "/api/v1/editions/private-edition/pages/1/image?content_revision=1"} {
		code(t, request(t, h, "GET", p, "", cookie, csrf), 404)
	}
	for _, p := range []string{"/api/v1/admin/books/search", "/api/v1/admin/editions/search", "/api/v1/books/search", "/api/v1/editions/search"} {
		w := request(t, h, "POST", p, `{"query":"","library_id":"lib","scope":"all","collapse":"none","page":1,"page_size":20,"sort":"title","order":"asc"}`, cookie, csrf)
		code(t, w, 200)
		if strings.Contains(w.Body.String(), "private") {
			t.Fatal("search leak", p, w.Body.String())
		}
	}
	code(t, request(t, h, "PATCH", "/api/v1/editions/book/user-state", `{"expected_revision":0,"favorite":true}`, cookie, csrf), 200)
	for _, p := range []string{"/api/v1/accounts", "/api/v1/diagnostics", "/api/v1/library-roots", "/api/v1/settings/metadata-provider"} {
		code(t, request(t, h, "GET", p, "", cookie, csrf), 403)
	}
	h.protected.HandleFunc("/api/v1/new-unclassified-route", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	code(t, request(t, h, "GET", "/api/v1/new-unclassified-route", "", cookie, csrf), 403)
}

func authorizedTestEditor(t *testing.T, h *Handler, library string) (*http.Cookie, string, context.Context) {
	t.Helper()
	ctx := context.Background()
	db := handlerDB(t, h)
	admin, e := db.Account(ctx)
	if e != nil {
		t.Fatal(e)
	}
	adminCtx := model.WithActor(ctx, *admin)
	a, e := h.svc.CreateAccount(adminCtx, "deterministic-editor", "anonymous editor password")
	if e != nil {
		t.Fatal(e)
	}
	if e = h.svc.UpdateAccount(adminCtx, a.ID, model.AccountUpdate{Expected: 1, Role: "user", Enabled: true, Grants: map[string]string{library: "editor"}}, nil); e != nil {
		t.Fatal(e)
	}
	if e = h.svc.ChangePassword(ctx, a.ID, "anonymous editor password", "new anonymous editor password"); e != nil {
		t.Fatal(e)
	}
	raw, session, e := h.svc.LoginUsername(ctx, "deterministic-editor", "new anonymous editor password")
	if e != nil {
		t.Fatal(e)
	}
	return &http.Cookie{Name: h.cookieName(), Value: raw}, auth.CSRF(raw), model.WithActor(ctx, *session.Account)
}
