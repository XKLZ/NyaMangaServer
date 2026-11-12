package httpapi

import (
	"fmt"
	"testing"

	"nyamangaserver.local/server/internal/model"
)

func TestClientSearchSnapshotBindsAccountAndLiveAuthorization(t *testing.T) {
	f := newOnlineFixture(t)
	_, viewer, _ := f.viewer(t)
	admin := deviceTestLogin(t, f.h, "admin", "anonymous password")
	query := fmt.Sprintf(`{"library_id":%q,"page_size":1}`, f.library)
	search := func(token string) model.ClientSearchResult {
		return decodeClientTest[model.ClientSearchResult](t, deviceTestCall(t, f.h, "POST", "/client/works/search", query, token, 200))
	}
	first := search(viewer.Token)
	other := search(admin.Token)
	if first.Snapshot == other.Snapshot {
		t.Fatal("accounts with identical personal state share a pagination snapshot")
	}
	next := fmt.Sprintf(`{"library_id":%q,"page_size":1,"page":2,"snapshot_revision":%q}`, f.library, first.Snapshot)
	deviceTestCall(t, f.h, "POST", "/client/works/search", next, admin.Token, 409)
	db := handlerDB(t, f.h)
	if _, err := db.Write.Exec("UPDATE account_profiles SET acl_revision=acl_revision+1 WHERE account_id=?", viewer.Actor.ID); err != nil {
		t.Fatal(err)
	}
	deviceTestCall(t, f.h, "POST", "/client/works/search", next, viewer.Token, 409)
	refreshed := search(viewer.Token)
	if refreshed.Snapshot == first.Snapshot {
		t.Fatal("authorization revision did not invalidate the old snapshot")
	}
}
