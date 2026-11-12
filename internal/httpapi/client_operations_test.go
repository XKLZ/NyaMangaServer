package httpapi

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"nyamangaserver.local/server/internal/model"
)

func TestClientOfflineOperationsReceiptsDependenciesAndTime(t *testing.T) {
	f := newOnlineFixture(t)
	_, first, actor := f.viewer(t)
	second := deviceTestLogin(t, f.h, "deterministic-editor", "new anonymous editor password")
	db := handlerDB(t, f.h)
	b, other := f.books[0], f.books[1]
	now := time.Now().Truncate(time.Second)
	f.h.svc.Now = func() time.Time { return now }
	identity, e := db.ClientIdentity(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	queue := model.OfflineQueue{ClientIdentity: identity, AccountID: first.Actor.ID, ClientInstanceID: "sample"}
	post := func(device deviceLoginResult, path string, in any, want int) []model.OfflineOperationResult {
		t.Helper()
		body, e := json.Marshal(in)
		if e != nil {
			t.Fatal(e)
		}
		w := deviceTestCall(t, f.h, "POST", "/client/offline/"+path, string(body), device.Token, want)
		if want != 200 {
			return nil
		}
		return decodeClientTest[struct {
			Items []model.OfflineOperationResult
		}](t, w).Items
	}
	ops := func(device deviceLoginResult, operations ...model.OfflineOperation) []model.OfflineOperationResult {
		t.Helper()
		return post(device, "operations", model.OfflineOperationsRequest{OfflineQueue: queue, Operations: operations}, 200)
	}
	status := func(device deviceLoginResult, ids ...string) []model.OfflineOperationResult {
		t.Helper()
		return post(device, "operation-status", model.OfflineStatusRequest{OfflineQueue: queue, OperationIDs: ids}, 200)
	}
	rev0 := int64(0)
	body := model.OfflineOperationBody{ContentRevision: b.ContentRevision, ParsingRevision: b.ParsingRevision}
	makeOp := func(seq int64, id, typ, previous string) model.OfflineOperation {
		return model.OfflineOperation{ID: id, Sequence: seq, ObjectID: b.ID, Type: typ, Previous: previous, Session: "offline-session-1", Body: body}
	}
	start := makeOp(1, "read-offline-start", "start", "")
	start.Expected = &rev0
	start.ReportedAt = json.RawMessage(fmt.Sprint(now.Unix() - 900))
	progress := makeOp(2, "read-offline-progress", "progress", start.ID)
	page := 2
	progress.Body.PageIndex = &page
	progress.ReportedAt = json.RawMessage(fmt.Sprint(now.Unix() - 1000))
	complete := makeOp(3, "read-offline-complete", "complete", progress.ID)
	complete.ReportedAt = json.RawMessage(fmt.Sprint(now.Unix() - 800))
	if blocked := ops(first, progress)[0]; blocked.Status != "blocked" || blocked.HTTPStatus != 409 {
		t.Fatal(blocked)
	}
	out := ops(first, start, progress, complete)
	if len(out) != 3 || out[0].Reading == nil || out[1].Reading == nil || out[2].Reading == nil ||
		out[0].Reading.ReadCount != 1 || *out[1].Reading.PageIndex != 2 || *out[1].Reading.LastReadAt != now.Unix()-900 ||
		*out[2].Reading.CompletedAt != now.Unix()-800 || out[2].ReceivedAt != now.Unix() {
		t.Fatal(out)
	}
	// The first response may have been lost. A new credential for this account
	// can query it, and repeating the same operation retains the first receipt.
	if got := status(second, start.ID, "unknown-operation"); got[0].Revision != 1 || got[0].SessionID != out[0].SessionID || got[1].Status != "unknown" {
		t.Fatal(got)
	}
	if again := ops(second, start)[0]; again.Revision != 1 || again.SessionID != out[0].SessionID {
		t.Fatal(again)
	}
	modified := start
	modified.Type = "restart"
	if got := ops(second, modified)[0]; got.HTTPStatus != 409 || got.Error != "client_idempotency_conflict" {
		t.Fatal(got)
	}
	duplicate := start
	duplicate.ID, duplicate.Sequence = "duplicate-offline-start", 4
	if got := ops(second, duplicate)[0]; got.SessionID != out[0].SessionID || got.Revision != 1 {
		t.Fatal(got)
	}

	w := deviceTestCall(t, f.h, "PUT", "/editions/"+b.ID+"/reading-state", fmt.Sprintf(`{"expected_revision":3,"content_revision":%d,"parsing_revision":%d,"idempotency_key":"online-edit","action":"progress","page_index":1}`, b.ContentRevision, b.ParsingRevision), second.Token, 200)
	online := decodeClientTest[model.ReadingState](t, w)
	conflict := makeOp(5, "offline-conflict", "reset", complete.ID)
	next := makeOp(6, "offline-blocked", "reset", conflict.ID)
	favorite := model.OfflineOperation{ID: "offline-favorite", Sequence: 7, ObjectID: other.ID, Type: "favorite", Expected: &rev0}
	yes := true
	favorite.Body.Value = &yes
	batch := ops(first, conflict, next, favorite)
	if batch[0].Status != "conflict" || batch[0].Reading == nil || batch[0].Revision != online.Revision ||
		batch[1].Status != "blocked" || batch[2].Status != "applied" || !batch[2].EditionState.Favorite {
		t.Fatal(batch)
	}
	restart := makeOp(8, "explicit-offline-restart", "restart", "")
	restart.Expected = &online.Revision
	restart.Session = "offline-session-2"
	restart.ReportedAt = json.RawMessage("999999999999999999999999")
	if _, e = db.Write.Exec("UPDATE health_state SET phase='recovery_required'"); e != nil {
		t.Fatal(e)
	}
	post(first, "operations", model.OfflineOperationsRequest{OfflineQueue: queue, Operations: []model.OfflineOperation{restart}}, 503)
	if got := status(first, restart.ID); got[0].Status != "unknown" {
		t.Fatal(got)
	}
	if _, e = db.Write.Exec("UPDATE health_state SET phase='idle'"); e != nil {
		t.Fatal(e)
	}
	got := ops(first, restart)[0]
	if got.Status != "applied" || got.Reading.Completed || *got.Reading.PageIndex != 0 ||
		got.Reading.ReadCount != 2 || !got.Adjusted || got.EventAt != now.Unix() || string(got.ReportedAt) != string(restart.ReportedAt) {
		t.Fatal(got)
	}
	if replay := ops(second, restart)[0]; replay.Revision != got.Revision || replay.SessionID != got.SessionID {
		t.Fatal(replay)
	}
	if _, e = db.Write.Exec("INSERT INTO tags VALUES('offline-tag','tag','offline-fixture-tag')"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Write.Exec("INSERT INTO effective_edition_tags VALUES(?,'offline-tag')", other.ID); e != nil {
		t.Fatal(e)
	}
	follow := model.OfflineOperation{ID: "offline-follow", Sequence: 9, ObjectID: "offline-tag", Type: "follow", Expected: &rev0,
		ReportedAt: json.RawMessage(`"invalid-clock"`), Body: model.OfflineOperationBody{Value: &yes}}
	no := false
	unfollow := model.OfflineOperation{ID: "offline-unfollow", Sequence: 10, ObjectID: follow.ObjectID, Type: "unfollow", Previous: follow.ID,
		ReportedAt: json.RawMessage(fmt.Sprint(now.Unix() + 301)), Body: model.OfflineOperationBody{Value: &no}}
	tagOps := ops(second, follow, unfollow)
	if tagOps[0].TagFollow == nil || !tagOps[0].TagFollow.Followed || !tagOps[0].Adjusted ||
		tagOps[1].TagFollow == nil || tagOps[1].TagFollow.Followed || !tagOps[1].Adjusted || tagOps[1].EventAt != now.Unix() {
		t.Fatal(tagOps)
	}
	deviceTestCall(t, f.h, "DELETE", "/auth/device-sessions/"+first.Device.ID, "", second.Token, 204)
	post(first, "operation-status", model.OfflineStatusRequest{OfflineQueue: queue, OperationIDs: []string{start.ID}}, 401)
	first = deviceTestLogin(t, f.h, "deterministic-editor", "new anonymous editor password")
	if resumed := status(first, restart.ID); resumed[0].Revision != got.Revision || resumed[0].SessionID != got.SessionID {
		t.Fatal(resumed)
	}

	admin := deviceTestLogin(t, f.h, "admin", "anonymous password")
	post(admin, "operations", model.OfflineOperationsRequest{OfflineQueue: queue, Operations: []model.OfflineOperation{start}}, 403)
	a, _ := model.Actor(actor)
	if _, e = db.Write.Exec("DELETE FROM account_library_grants WHERE account_id=?", a.ID); e != nil {
		t.Fatal(e)
	}
	if r := status(first, start.ID)[0]; r.Status != "inaccessible" || r.Reading != nil {
		t.Fatal(r)
	}
	if _, e = db.Write.Exec("UPDATE instance_client_identity SET cache_epoch='new-cache-epoch'"); e != nil {
		t.Fatal(e)
	}
	post(first, "operation-status", model.OfflineStatusRequest{OfflineQueue: queue, OperationIDs: []string{start.ID}}, 409)
}
