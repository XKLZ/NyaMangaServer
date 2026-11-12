package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"

	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	"nyamangaserver.local/server/internal/search"
	"nyamangaserver.local/server/internal/tagcatalog"
	"nyamangaserver.local/server/internal/testfixture"
	"nyamangaserver.local/server/internal/userstate"
)

func decodeClientTest[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e, w.Body.String())
	}
	return v
}

type deviceLoginResult struct {
	Token  string              `json:"token"`
	Device model.DeviceSession `json:"device"`
	Actor  model.AccountView   `json:"actor"`
}

func deviceTestCall(t *testing.T, h *Handler, method, path, body, token string, want int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://localhost/api/v1"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	code(t, w, want)
	return w
}

func deviceTestLogin(t *testing.T, h *Handler, user, password string) deviceLoginResult {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q,"device_name":"Fixture device","client_instance_id":"sample"}`, user, password)
	w := deviceTestCall(t, h, "POST", "/auth/device-sessions", body, "", 201)
	v := decodeClientTest[deviceLoginResult](t, w)
	if v.Token == "" || v.Device.ID == "" || w.Header().Get("Set-Cookie") != "" {
		t.Fatal("missing device session or emitted cookie")
	}
	return v
}

func TestClientFoundationHTTP(t *testing.T) {
	ctx := context.Background()
	h, adminCookie, csrf := catalogHandler(t)
	db := handlerDB(t, h)
	h.WithOrganization(organization.New(db), search.New(db), userstate.New(db)).WithTagCatalog(tagcatalog.New(db))
	doc, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	if e = doc.Validate(ctx); e != nil {
		t.Fatal(e)
	}
	doc.Servers[0].URL = "http://localhost/api/v1"
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second)
	h.svc.Now = func() time.Time { return now }
	web := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		w := request(t, h, method, "/api/v1"+path, body, adminCookie, csrf)
		code(t, w, want)
		return w
	}
	var l struct{ ID string }
	l = decodeClientTest[struct{ ID string }](t, web("POST", "/libraries", `{"root_key":"fixture","display_name":"Visible Library"}`, 201))
	job := decodeClientTest[struct {
		ID string `json:"job_id"`
	}](t, web("POST", "/libraries/"+l.ID+"/scans", "{}", 202))
	deadline := time.Now().Add(20 * time.Second)
	for {
		v, e := h.catalog.Job(ctx, job.ID)
		if e != nil || v.State == "failed" || time.Now().After(deadline) {
			t.Fatal(v, e)
		}
		if v.State == "completed" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	books, _, e := h.catalog.Editions(ctx, l.ID, "available", 1, 10)
	if e != nil || len(books) != 2 {
		t.Fatal(books, e)
	}
	preferred, entry := books[0], books[1]
	tx, e := db.Write.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "UPDATE editions SET work_id=? WHERE id=?", preferred.WorkID, entry.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM works WHERE id=?", entry.WorkID); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Write.Exec(`INSERT INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision) VALUES('private','private','Private Library','utf8','private',1)`); e != nil {
		t.Fatal(e)
	}
	if e = testfixture.SeedEdition(ctx, db.Write, "private-edition", "private", "private.zip", "private"); e != nil {
		t.Fatal(e)
	}
	if e = testfixture.SeedEdition(ctx, db.Write, "other", l.ID, "fixture-other.zip", "zz fixture other"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Write.Exec("INSERT INTO series VALUES('fixture-series',?,'Fixture series',1)", l.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Write.Exec("INSERT INTO series_members VALUES('fixture-series','i:other',1),('fixture-series',?,2)", preferred.WorkID); e != nil {
		t.Fatal(e)
	}
	create := func(name string, library string) model.AccountView {
		t.Helper()
		v := decodeClientTest[model.AccountView](t, web("POST", "/accounts", fmt.Sprintf(`{"username":%q,"password":"anonymous password"}`, name), 201))
		web("PUT", "/accounts/"+v.ID, fmt.Sprintf(`{"expected_revision":1,"role":"user","enabled":true,"library_grants":{%q:"viewer"}}`, library), 204)
		return v
	}
	if _, e = db.Write.Exec("INSERT INTO tags VALUES('fixture-tag','tag','fixture-name')"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Write.Exec("INSERT INTO effective_edition_tags VALUES(?,'fixture-tag')", entry.ID); e != nil {
		t.Fatal(e)
	}
	viewer := create("viewer", l.ID)
	outsider := create("outsider", "private")
	first := deviceTestLogin(t, h, "viewer", "anonymous password")
	deviceTestCall(t, h, "GET", "/libraries", "", first.Token, 403)
	deviceTestCall(t, h, "POST", "/auth/device-sessions/current/renew", "", first.Token, 403)
	deviceTestCall(t, h, "PUT", "/auth/password", `{"current_password":"anonymous password","new_password":"anonymous changed password"}`, first.Token, 200)
	deviceTestCall(t, h, "GET", "/auth/session", "", first.Token, 401)
	now = now.Add(2 * time.Minute)
	d := deviceTestLogin(t, h, "viewer", "anonymous changed password")
	admin := deviceTestLogin(t, h, "admin", "anonymous password")
	other := deviceTestLogin(t, h, "outsider", "anonymous password")
	call := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := deviceTestCall(t, h, method, path, body, d.Token, status)
		r := httptest.NewRequest(method, "http://localhost/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+d.Token)
		route, params, err := router.FindRoute(r)
		if err != nil {
			t.Fatal(err)
		}
		in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc, ExcludeResponseBody: strings.HasPrefix(w.Header().Get("Content-Type"), "image/"),
		}}
		if status < 400 {
			if err = openapi3filter.ValidateRequest(ctx, in); err != nil {
				t.Fatal(path, err)
			}
		}
		out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: w.Code, Header: w.Header()}
		out.SetBodyBytes(w.Body.Bytes())
		if err = openapi3filter.ValidateResponse(ctx, out); err != nil {
			t.Fatal(path, err)
		}
		if strings.Contains(w.Body.String(), h.c.Roots[0].Path) || strings.Contains(w.Body.String(), "Private Library") {
			t.Fatal("private library or path exposed", path, w.Body.String())
		}
		return w
	}
	for _, deny := range []struct{ method, path string }{
		{"GET", "/accounts"}, {"GET", "/library-roots"}, {"GET", "/events"}, {"GET", "/jobs"},
		{"GET", "/maintenance/backups"}, {"GET", "/settings/provider-credentials"}, {"GET", "/settings/enhancements"},
		{"PATCH", "/editions/" + entry.ID + "/overrides"},
	} {
		deviceTestCall(t, h, deny.method, deny.path, "{}", admin.Token, 403)
	}
	for _, p := range []string{"/client/works/i:private-edition/reading-context", "/editions/private-edition/cover?content_revision=1",
		"/client/editions/private-edition/pages/0/image?content_revision=1&parsing_revision=1", "/editions/private-edition/reading-state"} {
		call("GET", p, "", 404)
	}
	call("POST", "/client/works/search", `{"library_id":"private"}`, 404)
	call("POST", "/client/works/search", `{}`, 422)
	deviceTestCall(t, h, "DELETE", "/auth/device-sessions/"+other.Device.ID, "", d.Token, 204)
	deviceTestCall(t, h, "GET", "/auth/session", "", other.Token, 200)
	for _, mode := range []string{"mixed", "foreign"} {
		r := httptest.NewRequest(http.MethodGet, "http://localhost/api/v1/auth/session", nil)
		r.Header.Set("Authorization", "Bearer "+d.Token)
		want := 403
		if mode == "mixed" {
			r.AddCookie(adminCookie)
			want = 400
		} else {
			r.Header.Set("Origin", "http://foreign.invalid")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		code(t, w, want)
	}
	code(t, request(t, h, "DELETE", "/api/v1/auth/device-sessions/"+d.Device.ID, "", adminCookie, ""), 403)
	web("GET", "/auth/device-sessions", "", 200)
	if w := call("GET", "/auth/device-sessions", "", 200); strings.Contains(w.Body.String(), d.Token) || strings.Contains(w.Body.String(), admin.Device.ID) {
		t.Fatal("device list leaked credential or foreign device")
	}
	list := decodeClientTest[struct{ Items []deviceLibrary }](t, call("GET", "/libraries", "", 200))
	if len(list.Items) != 1 || list.Items[0].ID != l.ID {
		t.Fatal(list)
	}
	capabilities := call("GET", "/client/capabilities", "", 200)
	if !strings.Contains(capabilities.Body.String(), "unsupported") || strings.Contains(capabilities.Body.String(), "root_key") {
		t.Fatal(capabilities.Body.String())
	}
	call("PUT", "/tags/fixture-tag/follow", `{"expected_revision":0}`, 200)
	call("DELETE", "/tags/fixture-tag/follow", `{"expected_revision":1}`, 200)
	tagHits := decodeClientTest[model.ClientSearchResult](t, call("POST", "/client/works/search",
		fmt.Sprintf(`{"library_id":%q,"query":"tag:fixture-name"}`, l.ID), 200))
	if tagHits.Total != 1 || tagHits.Items[0].EntryEditionID != entry.ID {
		t.Fatal(tagHits)
	}

	query := fmt.Sprintf(`{"library_id":%q,"query":%q}`, l.ID, "title:"+entry.Title)
	hits := decodeClientTest[model.ClientSearchResult](t, call("POST", "/client/works/search", query, 200))
	if hits.Total != 1 || len(hits.Items) != 1 || hits.Items[0].EntryEditionID != entry.ID || hits.Items[0].PreferredEditionID == nil ||
		*hits.Items[0].PreferredEditionID != preferred.ID || hits.Items[0].ResumeEditionID != nil || hits.Items[0].Reading.EditionID != entry.ID {
		t.Fatalf("search selection %+v", hits)
	}
	cpath := "/client/works/" + preferred.WorkID + "/reading-context"
	contextResult := decodeClientTest[model.ClientReadingContext](t, call("GET", cpath+"?edition_id="+entry.ID, "", 200))
	if contextResult.Edition.ID != entry.ID || contextResult.SelectionReason != "explicit" || contextResult.Reading.ReadCount != 0 ||
		!contextResult.Edition.Readability.Readable || contextResult.PreviousWorkID == nil || *contextResult.PreviousWorkID != "i:other" {
		t.Fatalf("context %+v", contextResult)
	}
	call("GET", cpath+"?edition_id=private-edition", "", 404)
	adminSession, e := h.svc.DeviceSession(ctx, admin.Token)
	if e != nil {
		t.Fatal(e)
	}
	adminDevice := model.WithDeviceActor(ctx, *adminSession.Account, adminSession.ID, h.svc.Now)
	if e = db.RequireAdministrator(adminDevice); !errors.Is(e, model.ErrForbidden) {
		t.Fatal("repository allowed device administration", e)
	}
	if _, e = db.Write.ExecContext(adminDevice, "UPDATE works SET revision=revision+1 WHERE id=?", preferred.WorkID); e == nil || !strings.Contains(e.Error(), "forbidden:device") {
		t.Fatal("repository allowed device Work mutation", e)
	}
	pagesURL := strings.TrimPrefix(contextResult.PagesURL, "/api/v1")
	pages := decodeClientTest[struct{ Items []model.Page }](t, call("GET", pagesURL, "", 200))
	if len(pages.Items) != 3 || pages.Items[0].Index != 0 || pages.Items[2].Index != 2 || pages.Items[2].Name != "10.PNG" {
		t.Fatal(pages)
	}
	base, revisions, _ := strings.Cut(pagesURL, "?")
	call("GET", base+"?content_revision=1", "", 400)
	firstImage := call("GET", base+"/0/image?"+revisions, "", 200)
	lastImage := call("GET", base+"/2/image?"+revisions, "", 200)
	if !strings.HasPrefix(firstImage.Header().Get("Content-Type"), "image/") || lastImage.Body.Len() == 0 {
		t.Fatal("missing first or last image")
	}
	call("GET", base+"/3/image?"+revisions, "", 404)
	call("GET", base+"/-1/image?"+revisions, "", 400)
	call("GET", base+"/0/image?content_revision=999&parsing_revision=1", "", 409)
	call("GET", base+"/0/image?content_revision=1&parsing_revision=999", "", 409)
	legacy := decodeClientTest[struct{ Items []model.Page }](t, web("GET", "/editions/"+entry.ID+"/pages", "", 200))
	if len(legacy.Items) != 3 || legacy.Items[0].Index != 1 || legacy.Items[2].Index != 3 {
		t.Fatal(legacy)
	}
	readingPath := "/editions/" + entry.ID + "/reading-state"
	before := decodeClientTest[model.ReadingState](t, call("GET", readingPath, "", 200))
	if before.ReadCount != 0 || before.LastReadAt != nil {
		t.Fatal(before)
	}
	startPath := "/editions/" + entry.ID + "/reading-sessions"
	startBody := fmt.Sprintf(`{"expected_revision":0,"content_revision":%d,"parsing_revision":%d,"idempotency_key":"fixture-start"}`, entry.ContentRevision, entry.ParsingRevision)
	start := decodeClientTest[model.ReadingSession](t, call("POST", startPath, startBody, 200))
	replay := decodeClientTest[model.ReadingSession](t, call("POST", startPath, startBody, 200))
	if start.ID != replay.ID || start.State.ReadCount != 1 || replay.State.ReadCount != 1 {
		t.Fatal(start, replay)
	}
	progress := fmt.Sprintf(`{"expected_revision":1,"content_revision":%d,"parsing_revision":%d,"idempotency_key":"fixture-progress","action":"progress","page_index":2}`, entry.ContentRevision, entry.ParsingRevision)
	call("PUT", readingPath, progress, 200)
	after := decodeClientTest[model.ReadingState](t, call("GET", readingPath, "", 200))
	if after.PageIndex == nil || *after.PageIndex != 2 || after.ReadCount != 1 {
		t.Fatal(after)
	}
	resume := decodeClientTest[model.ClientReadingContext](t, call("GET", cpath+"?intent=continue", "", 200))
	if resume.Edition.ID != entry.ID || resume.SelectionReason != "continue" {
		t.Fatal(resume)
	}
	web("GET", "/editions/"+entry.ID+"/reading-state", "", 200)
	if state := decodeClientTest[model.ReadingState](t, web("GET", readingPath, "", 200)); state.ReadCount != 0 {
		t.Fatal("reading state crossed accounts", state)
	}
	call("POST", "/client/works/search", strings.TrimSuffix(query, "}")+fmt.Sprintf(`,"snapshot_revision":%q}`, hits.Snapshot), 409)
	call("PUT", "/editions/other/reading-state", `{"expected_revision":0,"content_revision":1,"parsing_revision":1,"idempotency_key":"other-completed","action":"complete"}`, 200)

	continueQuery := fmt.Sprintf(`{"library_id":%q,"scope":"continue","sort":"last_read_at"}`, l.ID)
	continuing := decodeClientTest[model.ClientSearchResult](t, call("POST", "/client/works/search", continueQuery, 200))
	if continuing.Total != 1 || continuing.Items[0].EntryEditionID != preferred.ID || continuing.Items[0].ResumeEditionID == nil ||
		*continuing.Items[0].ResumeEditionID != entry.ID {
		t.Fatal(continuing)
	}
	if _, e = db.Write.Exec("UPDATE files SET parsing_revision=parsing_revision+1,content_revision=content_revision+1,file_state='missing' WHERE edition_id=?", entry.ID); e != nil {
		t.Fatal(e)
	}
	stale := decodeClientTest[model.ClientReadingContext](t, call("GET", cpath+"?intent=continue", "", 200))
	if stale.Edition.ID != entry.ID || stale.Edition.Readability.Reason != "file_missing" || !stale.RequiresContentConfirmation || !stale.RequiresParsingConfirmation ||
		stale.Reading.PageIndex == nil || *stale.Reading.PageIndex != 2 || stale.ReadableAlternativeID == nil || *stale.ReadableAlternativeID != preferred.ID {
		t.Fatal("stale or missing resume context", stale)
	}
	call("GET", pagesURL, "", 409)
	call("PATCH", "/editions/"+preferred.ID+"/user-state", `{"expected_revision":0,"hidden":true}`, 200)
	call("PATCH", "/editions/other/user-state", `{"expected_revision":0,"hidden":true}`, 200)
	visible := decodeClientTest[model.ClientSearchResult](t, call("POST", "/client/works/search", query, 200))
	if visible.Total != 1 || visible.Items[0].PreferredEditionID != nil || strings.Contains(visible.Items[0].Title, preferred.Title) {
		t.Fatal(visible)
	}
	hidden := decodeClientTest[model.ClientSearchResult](t, call("POST", "/client/works/search", fmt.Sprintf(`{"library_id":%q,"scope":"hidden","page_size":1}`, l.ID), 200))
	if hidden.Total != 2 || len(hidden.Items) != 1 {
		t.Fatal(hidden)
	}
	hidden2 := decodeClientTest[model.ClientSearchResult](t, call("POST", "/client/works/search",
		fmt.Sprintf(`{"library_id":%q,"scope":"hidden","page_size":1,"page":2,"snapshot_revision":%q}`, l.ID, hidden.Snapshot), 200))
	if hidden2.Total != 2 || len(hidden2.Items) != 1 || hidden2.Items[0].WorkID == hidden.Items[0].WorkID {
		t.Fatal(hidden2)
	}
	ctxAfter := decodeClientTest[model.ClientReadingContext](t, call("GET", cpath, "", 200))
	if ctxAfter.Edition.ID != entry.ID || ctxAfter.PreviousWorkID != nil || ctxAfter.PreferredEditionID != nil {
		t.Fatal(ctxAfter)
	}
	call("PATCH", "/editions/"+entry.ID+"/user-state", `{"expected_revision":0,"favorite":true}`, 200)
	favorites := decodeClientTest[model.ClientSearchResult](t, call("POST", "/client/works/search", fmt.Sprintf(`{"library_id":%q,"scope":"favorites"}`, l.ID), 200))
	if favorites.Total != 1 || favorites.Items[0].EntryEditionID != entry.ID || !favorites.Items[0].Edition.UserState.Favorite || favorites.Items[0].Edition.UserState.Revision != 1 {
		t.Fatal(favorites)
	}
	saved := decodeClientTest[model.SavedSearch](t, call("POST", "/saved-searches",
		fmt.Sprintf(`{"name":"Anonymous saved","conditions":{"library_id":%q},"expected_revision":0,"query_version":1}`, l.ID), 200))
	call("GET", "/saved-searches/"+saved.ID, "", 200)

	if _, e = db.Write.Exec("UPDATE libraries SET disabled=1 WHERE id=?", l.ID); e != nil {
		t.Fatal(e)
	}
	disabled := decodeClientTest[model.ClientSearchResult](t, call("POST", "/client/works/search", query, 200))
	if disabled.Total != 1 || disabled.Items[0].Edition.Readability.Reason != "library_disabled" {
		t.Fatal(disabled)
	}

	var rev int64
	if e = db.Read.QueryRow("SELECT revision FROM account_profiles WHERE account_id=?", viewer.ID).Scan(&rev); e != nil {
		t.Fatal(e)
	}
	web("PUT", "/accounts/"+viewer.ID, fmt.Sprintf(`{"expected_revision":%d,"role":"user","enabled":true,"library_grants":{}}`, rev), 204)
	call("POST", "/client/works/search", strings.TrimSuffix(query, "}")+fmt.Sprintf(`,"snapshot_revision":%q}`, visible.Snapshot), 409)
	call("GET", cpath, "", 404)
	call("DELETE", "/auth/device-sessions/"+d.Device.ID, "", 204)
	call("GET", "/auth/session", "", 401)
	web("PUT", "/accounts/"+outsider.ID, `{"expected_revision":2,"role":"user","enabled":false}`, 204)
	deviceTestCall(t, h, "GET", "/auth/session", "", other.Token, 401)
	web("PUT", "/accounts/"+outsider.ID, `{"expected_revision":3,"role":"user","enabled":true,"library_grants":{"private":"viewer"}}`, 204)
	deviceTestCall(t, h, "GET", "/auth/session", "", other.Token, 401)
	now = now.Add(2 * time.Minute)
	other = deviceTestLogin(t, h, "outsider", "anonymous password")
	web("PUT", "/accounts/"+outsider.ID, `{"expected_revision":4,"role":"user","enabled":true,"library_grants":{"private":"viewer"},"new_password":"anonymous reset password"}`, 204)
	deviceTestCall(t, h, "GET", "/auth/session", "", other.Token, 401)
}

func TestClientActivityRespectsMaintenanceAndWriteEpoch(t *testing.T) {
	h, _ := testHandler(t, "http://localhost")
	ctx := context.Background()
	db := handlerDB(t, h)
	h.WithHealth(maintenance.NewHealth(db, h.c, nil))
	now := time.Unix(1800000000, 0)
	h.svc.Now = func() time.Time { return now }
	if _, _, e := h.svc.Setup(ctx, "anonymous-setup", "anonymous password"); e != nil {
		t.Fatal(e)
	}
	w := deviceTestCall(t, h, "GET", "/client/server-info", "", "", 200)
	info := decodeClientTest[struct {
		InstanceID  string `json:"instance_id"`
		Initialized bool   `json:"initialized"`
	}](t, w)
	if info.InstanceID == "" || !info.Initialized {
		t.Fatal(info)
	}
	deviceTestCall(t, h, "POST", "/auth/device-sessions",
		`{"username":"admin","username":"other","password":"anonymous password","device_name":"Fixture","client_instance_id":"fixture"}`, "", 400)
	d := deviceTestLogin(t, h, "admin", "anonymous password")
	epoch := func() int64 {
		t.Helper()
		v, e := db.InstanceState(ctx)
		if e != nil {
			t.Fatal(e)
		}
		return v.WriteEpoch
	}
	setHealth := func(phase string) {
		t.Helper()
		if _, e := db.Write.Exec("UPDATE health_state SET phase=? WHERE singleton=1", phase); e != nil {
			t.Fatal(e)
		}
	}
	setHealth("recovery_required")
	before := epoch()
	now = now.Add(5 * time.Minute)
	deviceTestCall(t, h, "GET", "/auth/session", "", d.Token, 200)
	capability := deviceTestCall(t, h, "GET", "/client/capabilities", "", d.Token, 200)
	if !strings.Contains(capability.Body.String(), `"maintenance_active":true`) ||
		!strings.Contains(capability.Body.String(), `"reason":"maintenance_active"`) || strings.Contains(capability.Body.String(), "/private/") {
		t.Fatal(capability.Body.String())
	}
	deviceTestCall(t, h, "POST", "/auth/device-sessions/current/renew", "", d.Token, 503)
	deviceTestCall(t, h, "POST", "/auth/logout", "", d.Token, 503)
	if epoch() != before {
		t.Fatal("maintenance admitted an implicit write")
	}
	setHealth("idle")
	deviceTestCall(t, h, "GET", "/auth/session", "", d.Token, 200)
	if epoch() != before+1 {
		t.Fatal("activity did not advance the epoch exactly once")
	}
	now = now.Add(time.Minute)
	deviceTestCall(t, h, "GET", "/auth/session", "", d.Token, 200)
	if epoch() != before+1 {
		t.Fatal("recent activity wrote again within five minutes")
	}
}
