package store

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/testfixture"
	"strings"
	"testing"
)

func aclFixture(t *testing.T) (*Store, map[string]context.Context) {
	t.Helper()
	s, _ := openTest(t)
	ctx := context.Background()
	if e := s.Initialize(ctx, Account{ID: "admin", PasswordHash: "opaque", Revision: 1}, Session{Hash: make([]byte, 32), AccountID: "admin", Created: 1, Expires: 9999999999}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Write.Exec(`INSERT INTO accounts VALUES(2,'viewer','opaque',1,1),(3,'editor','opaque',1,1);INSERT INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision) VALUES('a','a','anonymous a','utf8','a',1),('b','b','anonymous b','utf8','b',1),('c','c','anonymous c','utf8','c',1);INSERT INTO account_library_grants VALUES('viewer','a','viewer'),('editor','a','editor'),('editor','b','viewer')`); e != nil {
		t.Fatal(e)
	}
	for _, lib := range []string{"a", "b", "c"} {
		if e := testfixture.SeedEdition(ctx, s.Write, lib, lib, lib+".zip", lib); e != nil {
			t.Fatal(e)
		}
	}
	out := map[string]context.Context{}
	for _, id := range []string{"admin", "viewer", "editor"} {
		a, e := s.AccountByID(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		out[id] = model.WithActor(ctx, *a)
	}
	return s, out
}
func TestAuthorizationReadScopesAndViewerPersonalWrites(t *testing.T) {
	s, ctx := aclFixture(t)
	for _, who := range []string{"admin", "viewer", "editor"} {
		libs, e := s.Libraries(ctx[who])
		want := map[string]int{"admin": 3, "viewer": 1, "editor": 2}[who]
		if e != nil || len(libs) != want {
			t.Fatalf("%s libraries %d %v", who, len(libs), e)
		}
	}
	if _, e := s.Edition(ctx["viewer"], "b"); !errors.Is(e, model.ErrNotFound) {
		t.Fatal("hidden edition", e)
	}
	for _, sql := range []string{"SELECT count(*) FROM editions", "SELECT count(*) FROM (SELECT * FROM editions)", "WITH nested AS (SELECT * FROM editions) SELECT count(*) FROM nested", "SELECT count(*) FROM editions WHERE id=?"} {
		var n int
		args := []any{}
		if strings.Contains(sql, "?") {
			args = append(args, "a")
		}
		if e := scopeRow(ctx["viewer"], s.Read, sql, args...).Scan(&n); e != nil || n != 1 {
			t.Fatal(sql, n, e)
		}
	}
	var n int
	if e := scopeRow(ctx["viewer"], s.Read, "SELECT count(*) FROM main.editions").Scan(&n); e == nil {
		t.Fatal("main bypass accepted")
	}
	if e := scopeRow(ctx["viewer"], s.Read, "WITH editions AS (SELECT 1) SELECT count(*) FROM editions").Scan(&n); e == nil {
		t.Fatal("scope shadow accepted")
	}
	yes := true
	if _, e := s.SetUserState(ctx["viewer"], "viewer", "a", model.StatePatch{Favorite: &yes}); e != nil {
		t.Fatal("viewer personal write", e)
	}
	if _, e := s.SetUserState(ctx["viewer"], "editor", "a", model.StatePatch{Favorite: &yes}); e == nil {
		t.Fatal("another owner write")
	}
	if _, e := s.StartScan(ctx["viewer"], "no-viewer-scan", "a"); e == nil {
		t.Fatal("viewer scan")
	}
	if _, e := s.StartScan(ctx["editor"], "editor-scan", "a"); e != nil {
		t.Fatal("editor scan", e)
	}
	if _, e := s.StartScan(ctx["editor"], "no-editor-scan", "b"); e == nil {
		t.Fatal("viewer library scan")
	}
}

func TestAuthorizationEditorNewSourceSharedUpdateAndRevocation(t *testing.T) {
	s, ctx := aclFixture(t)
	j, e := s.CreateMetadataJob(ctx["editor"], "editor-metadata", "", []string{"a"}, map[string]model.MetadataInput{})
	if e != nil {
		t.Fatal("create metadata", e)
	}
	if j.LibraryID != "a" || j.ActorID != "editor" {
		t.Fatal("actor/scope not persisted", j)
	}
	worker, e := s.JobContext(context.Background(), j.ID)
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := s.Claim(worker, j.ID)
	if e != nil {
		t.Fatal("claim", e)
	}
	title := "anonymous remote"
	remote := model.RemoteMetadata{ExternalID: "123", URL: "https://e-hentai.org/g/123/abcdefghij/", Method: "manual", Fields: model.Metadata{Title: &title, Tags: []model.TagName{{Namespace: "artist", Name: "anonymous new artist"}}}, Raw: []byte(`{"gid":123}`)}
	tx, e := s.Write.BeginTx(worker, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = publishSource(worker, tx, "a", remote); e != nil {
		tx.Rollback()
		t.Fatal("new source and tag", e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	// A second library points to the same canonical source. Viewer access is enough
	// to read its visible Edition, but updating the source requires every editor grant.
	tx, e = s.Write.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = publishSource(context.Background(), tx, "b", remote); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if _, d, e := s.EditionWithMetadata(ctx["viewer"], "a"); e != nil || d.Source == nil {
		t.Fatal("shared source read", d, e)
	}
	tx, e = s.Write.BeginTx(worker, nil)
	if e != nil {
		t.Fatal(e)
	}
	e = publishSource(worker, tx, "a", remote)
	tx.Rollback()
	if !errors.Is(e, model.ErrSharedSourcePermission) {
		t.Fatal("partial shared update allowed", e)
	}
	rows, e := s.Accounts(ctx["admin"])
	if e != nil {
		t.Fatal(e)
	}
	var revision int64
	for _, a := range rows {
		if a.ID == "editor" {
			revision = a.Revision
		}
	}
	if e = s.UpdateAccount(ctx["admin"], "editor", model.AccountUpdate{Expected: revision, Role: "user", Enabled: true, Grants: map[string]string{"a": "viewer"}}, "", 1); e != nil {
		t.Fatal(e)
	}
	current, e := s.Job(context.Background(), j.ID)
	if e != nil || current.State != "paused" || current.Generation <= claimed.Generation {
		t.Fatal("revocation did not pause", current, e)
	}
	tx, e = s.Write.BeginTx(worker, nil)
	if e != nil {
		t.Fatal(e)
	}
	e = guard(worker, tx, *claimed)
	tx.Rollback()
	if e == nil {
		t.Fatal("stale worker retained publish access")
	}
	if _, e = s.JobContext(context.Background(), j.ID); e == nil {
		t.Fatal("revoked worker restarted")
	}
}
func TestAuthorizationEditorScanPhases(t *testing.T) {
	s, ctx := aclFixture(t)
	j, e := s.StartScan(ctx["editor"], "scan-phases", "a")
	if e != nil {
		t.Fatal(e)
	}
	worker, e := s.JobContext(context.Background(), j.ID)
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Claim(worker, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.StageEntries(worker, *j, []model.Item{{Ordinal: 1, Path: "anonymous.cbz", Size: 1, Mtime: 1}}); e != nil {
		t.Fatal("entries", e)
	}
	if e = s.Freeze(worker, *j); e != nil {
		t.Fatal("freeze", e)
	}
	if e = s.Finish(worker, *j, false, "anonymous_failure"); e != nil {
		t.Fatal("finish", e)
	}
}

func TestAuthorizationConnectionReplacementAndOldNewWriteScope(t *testing.T) {
	s, ctx := aclFixture(t)
	s.Write.db.SetMaxIdleConns(0)
	yes := true
	if _, e := s.SetUserState(ctx["viewer"], "viewer", "a", model.StatePatch{Favorite: &yes}); e != nil {
		t.Fatal("replacement actor/triggers", e)
	}
	if _, e := s.Write.ExecContext(ctx["editor"], "UPDATE editions SET library_id='a' WHERE id='b'"); e == nil {
		t.Fatal("move from unauthorized old scope")
	}
	if _, e := s.Write.ExecContext(ctx["editor"], "UPDATE editions SET library_id='b' WHERE id='a'"); e == nil {
		t.Fatal("move to unauthorized new scope")
	}
	s.Write.db.SetMaxIdleConns(1)
}

func TestAuthorizationTagImagesSuggestionsAndEventsDoNotLeak(t *testing.T) {
	s, ctx := aclFixture(t)
	for _, id := range []string{"a", "b", "c"} {
		tx, e := s.Write.BeginTx(context.Background(), nil)
		if e != nil {
			t.Fatal(e)
		}
		r := model.RemoteMetadata{ExternalID: id, URL: "https://e-hentai.org/g/1/0123456789/", Method: "manual", Fields: model.Metadata{Tags: []model.TagName{{Namespace: "artist", Name: "anonymous-" + id}}}, Raw: []byte(`{}`)}
		if e = publishSource(context.Background(), tx, id, r); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	tags, e := s.Tags(ctx["viewer"], "", "", "zh-CN")
	if e != nil || len(tags) != 1 || tags[0].Name != "anonymous-a" {
		t.Fatal("tag identity leak", tags, e)
	}
	for _, lib := range []string{"a", "b", "c"} {
		v, e := s.TagCatalog(ctx["viewer"], tagInput(lib))
		if lib == "a" {
			if e != nil || v.Total != 1 {
				t.Fatal(v, e)
			}
		} else if !errors.Is(e, model.ErrNotFound) {
			t.Fatal("catalog hidden scope", lib, e)
		}
	}
	yes := true
	if _, e = s.SetUserState(ctx["viewer"], "viewer", "a", model.StatePatch{Favorite: &yes}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetUserState(context.Background(), "editor", "a", model.StatePatch{Hidden: &yes}); e != nil {
		t.Fatal(e)
	}
	events, _, _, e := s.EventsForAccount(ctx["viewer"], "viewer", 0, 1000)
	if e != nil {
		t.Fatal(e)
	}
	for _, event := range events {
		if event.ResourceID == "b" || event.ResourceID == "c" {
			t.Fatal("event leaked", event)
		}
	}
	// Unresolvable/deleted-object events must never turn into global notifications.
	if _, e = s.Write.Exec(`INSERT INTO events(type,resource_id,revision,created_at) VALUES('edition','deleted-private-object',1,unixepoch())`); e != nil {
		t.Fatal(e)
	}
	events, _, _, e = s.EventsForAccount(ctx["viewer"], "viewer", 0, 1000)
	if e != nil {
		t.Fatal(e)
	}
	for _, event := range events {
		if event.ResourceID == "deleted-private-object" {
			t.Fatal("deleted event leaked")
		}
	}
	// An image-only lookup must carry the tag visibility dependency too.
	if _, e = s.Write.Exec(`INSERT INTO assets VALUES('tag-art','anonymous-art','opaque',1,1);INSERT INTO tag_images SELECT id,'foreground','tag-art',1,1,'png',1 FROM tags WHERE name='anonymous-c'`); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = scopeRow(ctx["viewer"], s.Read, "SELECT count(*) FROM tag_images").Scan(&count); e != nil || count != 0 {
		t.Fatal("image identity leak", count, e)
	}
}

func TestAuthorizationHighlightUsesCurrentAccountAndLibraryGrants(t *testing.T) {
	s, ctx := aclFixture(t)
	root := context.Background()
	tx, e := s.Write.BeginTx(root, nil)
	if e != nil {
		t.Fatal(e)
	}
	r := model.RemoteMetadata{ExternalID: "123", URL: "https://e-hentai.org/g/123/0123456789/", Method: "manual", Fields: model.Metadata{Tags: []model.TagName{}}, Raw: []byte(`{}`)}
	if e = publishSource(root, tx, "c", r); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	hash := make([]byte, 32)
	hash[0] = 7
	if _, e = s.Write.Exec(`UPDATE gallery_highlight_settings SET enabled=1;INSERT INTO gallery_highlight_connections(id,account_id,name,token_hash,auth_revision,created_at,revision) VALUES('editor-token','editor','anonymous',?,1,1,1)`, hash); e != nil {
		t.Fatal(e)
	}
	grant, e := s.GalleryGrant(root, hash, 2)
	if e != nil {
		t.Fatal(e)
	}
	result, e := s.GalleryLookup(root, grant, []string{"123"}, 2)
	if e != nil || result[0].State != "none" {
		t.Fatal("highlight leaked", result, e)
	}
	if e = s.UpdateAccount(ctx["admin"], "editor", model.AccountUpdate{Expected: 1, Role: "user", Enabled: true, Grants: map[string]string{"c": "viewer"}}, "", 3); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GalleryGrant(root, hash, 4); e != nil {
		t.Fatal("ACL edit permanently revoked token", e)
	}
	result, e = s.GalleryLookup(root, grant, []string{"123"}, 4)
	if e != nil || result[0].State == "none" {
		t.Fatal("grant not reflected", result, e)
	}
	if e = s.UpdateAccount(ctx["admin"], "editor", model.AccountUpdate{Expected: 2, Role: "user", Enabled: false, Grants: map[string]string{"c": "viewer"}}, "", 5); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GalleryGrant(root, hash, 6); !errors.Is(e, model.ErrGalleryAuth) {
		t.Fatal("disabled highlight", e)
	}
}
func TestAuthorizationAdministratorMayDemoteSelfWithSuccessor(t *testing.T) {
	s, ctx := aclFixture(t)
	if _, e := s.Write.Exec("UPDATE account_profiles SET role='admin' WHERE account_id='editor'"); e != nil {
		t.Fatal(e)
	}
	if e := s.UpdateAccount(ctx["admin"], "admin", model.AccountUpdate{Expected: 1, Role: "user", Enabled: true, Grants: map[string]string{"a": "viewer"}}, "", 2); e != nil {
		t.Fatal("self demotion", e)
	}
	if _, e := s.Accounts(ctx["admin"]); !errors.Is(e, model.ErrForbidden) {
		t.Fatal("stale administrator retained authority", e)
	}
}
func TestAuthorizationPeriodicAndEnhancementActorsPersistAcrossRevocation(t *testing.T) {
	s, ctx := aclFixture(t)
	if _, e := s.Write.Exec("UPDATE libraries SET scan_actor_id='editor' WHERE id='a'"); e != nil {
		t.Fatal(e)
	}
	scheduled, e := s.StartScan(context.Background(), "scheduled-editor", "a")
	if e != nil || scheduled.ActorID != "editor" {
		t.Fatal(scheduled, e)
	}
	if _, e = s.Write.Exec("UPDATE account_profiles SET role='admin' WHERE account_id='editor';INSERT INTO enhancement_transitions(id,feature,attempt,state,target_enabled,error_code,affected_tasks,created_at,updated_at) VALUES('editor-transition','chinese_dictionary',1,'pending',1,'',0,1,1)"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("UPDATE enhancement_transitions SET actor_id='editor' WHERE id='editor-transition'"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.EnhancementContext(context.Background(), "editor-transition"); e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateAccount(ctx["admin"], "editor", model.AccountUpdate{Expected: 1, Role: "user", Enabled: false, Grants: map[string]string{}}, "", 2); e != nil {
		t.Fatal(e)
	}
	if _, e = s.StartScan(context.Background(), "scheduled-revoked", "a"); !errors.Is(e, model.ErrForbidden) {
		t.Fatal("system scheduler lost actor", e)
	}
	if _, e = s.EnhancementContext(context.Background(), "editor-transition"); !errors.Is(e, model.ErrForbidden) {
		t.Fatal("enhancement lost actor", e)
	}
	var state string
	if e = s.Read.QueryRow("SELECT state FROM enhancement_transitions WHERE id='editor-transition'").Scan(&state); e != nil || state != "failed" {
		t.Fatal(state, e)
	}
}

func TestAuthorizationLibraryPurgeClearsGrantsAtomically(t *testing.T) {
	s, contexts := aclFixture(t)
	ctx := contexts["admin"]
	var original int64
	if e := s.Read.QueryRow("SELECT acl_revision FROM account_profiles WHERE account_id='viewer'").Scan(&original); e != nil {
		t.Fatal(e)
	}
	run := func(operation string) {
		t.Helper()
		lib, e := s.Library(ctx, "a")
		if e != nil {
			t.Fatal(e)
		}
		p, e := s.CreateMaintenancePlan(ctx, model.MaintenanceInput{Operation: operation, LibraryID: "a", Expected: map[string]int64{"library:a": lib.Revision}})
		if e != nil {
			t.Fatal(e)
		}
		job, e := s.ExecuteMaintenance(ctx, p.ID)
		if e != nil {
			t.Fatal(e)
		}
		j, e := s.Claim(ctx, job.ID)
		if e != nil {
			t.Fatal(e)
		}
		items, e := s.Items(ctx, j.ID, 1, 1, true)
		if e != nil || len(items) != 1 {
			t.Fatal(items, e)
		}
		if e = s.PauseMaintenanceResources(ctx, p); e != nil {
			t.Fatal(e)
		}
		if e = s.ApplyMaintenance(ctx, *j, items[0], p, "", "", ""); e != nil {
			t.Fatal(e)
		}
		if e = s.FinishBackground(ctx, *j, ""); e != nil {
			t.Fatal(e)
		}
	}
	check := func(want int, wantRev int64) {
		t.Helper()
		var n int
		var rev int64
		if e := s.Read.QueryRow("SELECT count(*) FROM account_library_grants WHERE library_id='a'").Scan(&n); e != nil || n != want {
			t.Fatal(n, e)
		}
		if e := s.Read.QueryRow("SELECT acl_revision FROM account_profiles WHERE account_id='viewer'").Scan(&rev); e != nil || rev != wantRev {
			t.Fatal(rev, e)
		}
	}
	run("trash")
	check(2, original)
	run("restore")
	check(2, original)
	run("trash")
	check(2, original)
	snapshot, e := checkSearchSnapshot(contexts["editor"], s.Read, model.SearchInput{})
	if e != nil {
		t.Fatal(e)
	}
	// A late SQL failure must roll back removed books, grants, audit and ACL events.
	if _, e := s.Write.Exec(`CREATE TRIGGER fail_library_purge BEFORE DELETE ON libraries WHEN OLD.id='a' BEGIN SELECT RAISE(ABORT,'injected purge failure'); END`); e != nil {
		t.Fatal(e)
	}
	lib, _ := s.Library(ctx, "a")
	p, e := s.CreateMaintenancePlan(ctx, model.MaintenanceInput{Operation: "purge", LibraryID: "a", Expected: map[string]int64{"library:a": lib.Revision}})
	if e != nil {
		t.Fatal(e)
	}
	job, e := s.ExecuteMaintenance(ctx, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.Claim(ctx, job.ID)
	if e != nil {
		t.Fatal(e)
	}
	items, e := s.Items(ctx, j.ID, 1, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.PauseMaintenanceResources(ctx, p); e != nil {
		t.Fatal(e)
	}
	if e = s.ApplyMaintenance(ctx, *j, items[0], p, "", "", ""); e == nil {
		t.Fatal("injected failure committed")
	}
	check(2, original)
	if _, e = s.Write.Exec("DROP TRIGGER fail_library_purge"); e != nil {
		t.Fatal(e)
	}
	if e = s.ApplyMaintenance(ctx, *j, items[0], p, "", "", ""); e != nil {
		t.Fatal(e)
	}
	check(0, original+1)
	var audit, events, invalid int
	for q, dst := range map[string]*int{
		"SELECT count(*) FROM account_security_audit WHERE action='library_purged'":                               &audit,
		"SELECT count(*) FROM events WHERE type='account_permissions' AND owner_account_id IN('viewer','editor')": &events,
		"SELECT count(*) FROM pragma_foreign_key_check":                                                           &invalid,
	} {
		if e = s.Read.QueryRow(q).Scan(dst); e != nil {
			t.Fatal(e)
		}
	}
	if audit != 2 || events != 2 || invalid != 0 {
		t.Fatal(audit, events, invalid)
	}
	if _, e := checkSearchSnapshot(contexts["editor"], s.Read, model.SearchInput{Snapshot: snapshot}); !errors.Is(e, model.ErrSnapshot) {
		t.Fatal("old pagination snapshot remained valid", e)
	}
	visible, _, _, e := s.EventsForAccount(contexts["viewer"], "viewer", 0, 1000)
	if e != nil {
		t.Fatal(e)
	}
	permissionEvents := 0
	for _, v := range visible {
		if v.Type == "account_permissions" {
			if v.ResourceID != "viewer" || v.Revision != original+1 {
				t.Fatal("foreign ACL event", v)
			}
			permissionEvents++
		}
	}
	if permissionEvents != 1 {
		t.Fatal("missing own ACL invalidation", visible)
	}
	libs, e := s.Libraries(contexts["viewer"])
	if e != nil || len(libs) != 0 {
		t.Fatal(libs, e)
	}
}
