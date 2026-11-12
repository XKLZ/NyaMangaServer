package httpapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"nyamangaserver.local/server/internal/model"
)

func TestClientOfflineManifestHTTP(t *testing.T) {
	f := newOnlineFixture(t)
	_, d, actor := f.viewer(t)
	db := handlerDB(t, f.h)
	b := f.books[0]
	req := fmt.Sprintf(`{"content_revision":%d,"parsing_revision":%d}`, b.ContentRevision, b.ParsingRevision)
	create := "/client/editions/" + b.ID + "/offline-manifests"
	m := decodeClientTest[model.OfflineManifest](t, deviceTestCall(t, f.h, "POST", create, req, d.Token, 200))
	again := decodeClientTest[model.OfflineManifest](t, deviceTestCall(t, f.h, "POST", create, req, d.Token, 200))
	if m.Metadata == nil || m.PageCount != 3 || m.AccountID != d.Actor.ID || m.ID != again.ID || len(m.StructureDigest) != 64 {
		t.Fatal(m, again)
	}
	base := "/client/offline-manifests/" + m.ID
	pages := decodeClientTest[model.OfflinePages](t, deviceTestCall(t, f.h, "GET", base+"/pages?page=2&page_size=2", "", d.Token, 200))
	if pages.Total != 3 || len(pages.Items) != 1 || pages.Items[0].Index != 2 || pages.StructureDigest != m.StructureDigest {
		t.Fatal(pages)
	}
	img := strings.TrimPrefix(pages.Items[0].ImageURL, "/api/v1")
	callPage := func(purpose string, status int) {
		t.Helper()
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, onlineImageRequest(context.Background(), img, d.Token, purpose))
		code(t, w, status)
	}
	callPage("display", 400)
	callPage("prefetch", 200)
	checkReq := fmt.Sprintf(`{"instance_id":%q,"cache_epoch":%q,"objects":[{"id":%q,"metadata_digest":%q},{"id":"unknown"}]}`, m.InstanceID, m.CacheEpoch, m.ID, m.Metadata.Digest)
	check := func(status string) model.OfflineCheckResult {
		t.Helper()
		out := decodeClientTest[struct{ Items []model.OfflineCheckResult }](t, deviceTestCall(t, f.h, "POST", "/client/offline/check", checkReq, d.Token, 200))
		if len(out.Items) != 2 || out.Items[0].Status != status || out.Items[1].Status != "inaccessible" || out.Items[1].Metadata != nil {
			t.Fatal(out)
		}
		return out.Items[0]
	}
	mutate := func(query string, args ...any) {
		t.Helper()
		if _, e := db.Write.Exec(query, args...); e != nil {
			t.Fatal(e)
		}
	}
	check("unchanged")
	mutate("UPDATE works SET name_override='Anonymous changed title' WHERE id=?", b.WorkID)
	if v := check("metadata_changed"); v.Metadata == nil || v.Metadata.Title != "Anonymous changed title" {
		t.Fatal(v)
	}
	callPage("prefetch", 200)
	mutate("UPDATE files SET file_state='missing' WHERE edition_id=?", b.ID)
	check("source_unavailable")
	callPage("prefetch", 409)
	mutate("UPDATE files SET file_state='available',parsing_revision=parsing_revision+1 WHERE edition_id=?", b.ID)
	check("parsing_or_representation_changed")
	deviceTestCall(t, f.h, "GET", base+"/pages", "", d.Token, 409)
	mutate("UPDATE instance_client_identity SET cache_epoch='new-cache-epoch'")
	check("cache_epoch_changed")
	a, _ := model.Actor(actor)
	mutate("DELETE FROM account_library_grants WHERE account_id=?", a.ID)
	if v := check("inaccessible"); v.Metadata != nil {
		t.Fatal("unauthorized metadata", v)
	}
}
