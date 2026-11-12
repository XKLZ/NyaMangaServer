package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"nyamangaserver.local/server/internal/model"
)

type savedOfflineFixture struct {
	dir       string
	count     int
	available int64
	seen      map[int]string
	write     func(io.Writer, []byte) (int, error)
}

func (s *savedOfflineFixture) save(index int, length int64, tag string, data []byte) error {
	if index < 0 || index >= s.count || int64(len(data)) != length ||
		tag != fmt.Sprintf("\"%x\"", sha256.Sum256(data)) {
		return errors.New("invalid offline page")
	}
	if previous, ok := s.seen[index]; ok {
		if previous != tag {
			return errors.New("duplicate with different bytes")
		}
		return nil
	}
	if length > s.available {
		return syscall.ENOSPC
	}
	f, err := os.CreateTemp(s.dir, "page-*.partial")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	n, err := s.write(f, data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), filepath.Join(s.dir, strconv.Itoa(index))); err != nil {
		return err
	}
	s.seen[index] = tag
	s.available -= length
	return nil
}

func TestOfflineSimulatorSavesVerifiedPagesAndPreservesQueue(t *testing.T) {
	f := newOnlineFixture(t)
	_, dev, _ := f.viewer(t)
	b := f.books[0]
	req := fmt.Sprintf(`{"content_revision":%d,"parsing_revision":%d}`, b.ContentRevision, b.ParsingRevision)
	m := decodeClientTest[model.OfflineManifest](t, deviceTestCall(t, f.h, "POST", "/client/editions/"+b.ID+"/offline-manifests", req, dev.Token, 200))
	base := "/client/offline-manifests/" + m.ID
	pages := decodeClientTest[model.OfflinePages](t, deviceTestCall(t, f.h, "GET", base+"/pages", "", dev.Token, 200))
	root := t.TempDir()
	account := filepath.Join(root, m.InstanceID, m.CacheEpoch, m.AccountID)
	v1 := filepath.Join(account, "downloads", m.EditionID, m.StructureDigest)
	if e := os.MkdirAll(v1, 0700); e != nil {
		t.Fatal(e)
	}
	s := savedOfflineFixture{dir: v1, count: m.PageCount, available: 1 << 20, seen: map[int]string{}, write: func(w io.Writer, b []byte) (int, error) { return w.Write(b) }}
	get := func(i int) ([]byte, int64, string) {
		t.Helper()
		p := strings.TrimPrefix(pages.Items[i].ImageURL, "/api/v1")
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, onlineImageRequest(context.Background(), p, dev.Token, "prefetch"))
		code(t, w, 200)
		n, e := strconv.ParseInt(w.Header().Get("Content-Length"), 10, 64)
		if e != nil {
			t.Fatal(e)
		}
		return w.Body.Bytes(), n, w.Header().Get("ETag")
	}
	raw, n, tag := get(0)
	if e := s.save(0, n, tag, raw); e != nil {
		t.Fatal(e)
	}
	if e := s.save(0, n, tag, raw); e != nil || len(s.seen) != 1 {
		t.Fatal("duplicate page counted", e)
	}
	bad := append([]byte(nil), raw...)
	bad[0] ^= 1
	if s.save(1, n, tag, bad) == nil || len(s.seen) != 1 {
		t.Fatal("damaged page marked complete")
	}
	one, oneN, oneTag := get(1)
	saveWrite := s.write
	s.write = func(w io.Writer, b []byte) (int, error) { _, _ = w.Write(b[:1]); return 1, syscall.ENOSPC }
	if e := s.save(1, oneN, oneTag, one); !errors.Is(e, syscall.ENOSPC) || len(s.seen) != 1 {
		t.Fatal(e)
	}
	if files, e := filepath.Glob(filepath.Join(v1, "*.partial")); e != nil || len(files) != 0 {
		t.Fatal(files, e)
	}
	s.write = saveWrite
	if e := s.save(1, oneN, oneTag, one); e != nil || len(s.seen) == s.count {
		t.Fatal("missing page marked complete", e)
	}
	last, lastN, lastTag := get(2)
	if e := s.save(2, lastN, lastTag, last); e != nil || len(s.seen) != s.count {
		t.Fatal("complete manifest was not registered", e)
	}

	queue := model.OfflineOperationsRequest{OfflineQueue: model.OfflineQueue{ClientIdentity: m.ClientIdentity, AccountID: m.AccountID, ClientInstanceID: "sample"}}
	zero := int64(0)
	queue.Operations = []model.OfflineOperation{{ID: "offline-local-read", Sequence: 1, ObjectID: b.ID, Type: "start", Expected: &zero,
		Session: "offline-local-session", Body: model.OfflineOperationBody{ContentRevision: m.ContentRevision, ParsingRevision: m.ParsingRevision}}}
	payload, e := json.Marshal(queue)
	if e != nil {
		t.Fatal(e)
	}
	queueFile := filepath.Join(account, "pending.json")
	if e = os.WriteFile(queueFile, payload, 0600); e != nil {
		t.Fatal(e)
	}
	// With no HTTP requests, the simulator can read the completed local page.
	if local, e := os.ReadFile(filepath.Join(v1, "2")); e != nil || string(local) != string(last) {
		t.Fatal(e)
	}
	db := handlerDB(t, f.h)
	if _, e = db.Write.Exec("UPDATE works SET name_override='Updated offline fixture' WHERE id=?", b.WorkID); e != nil {
		t.Fatal(e)
	}
	check := fmt.Sprintf(`{"instance_id":%q,"cache_epoch":%q,"objects":[{"id":%q,"metadata_digest":%q}]}`, m.InstanceID, m.CacheEpoch, m.ID, m.Metadata.Digest)
	results := decodeClientTest[struct{ Items []model.OfflineCheckResult }](t, deviceTestCall(t, f.h, "POST", "/client/offline/check", check, dev.Token, 200))
	if results.Items[0].Status != "metadata_changed" || len(s.seen) != s.count {
		t.Fatal(results)
	}
	receipt := decodeClientTest[struct {
		Items []model.OfflineOperationResult
	}](t, deviceTestCall(t, f.h, "POST", "/client/offline/operations", string(payload), dev.Token, 200))
	if receipt.Items[0].Status != "applied" || receipt.Items[0].Reading.ReadCount != 1 {
		t.Fatal(receipt)
	}

	// Prepare a distinct version before switching. A local space failure keeps
	// the verified previous directory and the independent queue.
	if _, e = db.Write.Exec("UPDATE files SET content_revision=content_revision+1 WHERE edition_id=?", b.ID); e != nil {
		t.Fatal(e)
	}
	req = fmt.Sprintf(`{"content_revision":%d,"parsing_revision":%d}`, b.ContentRevision+1, b.ParsingRevision)
	next := decodeClientTest[model.OfflineManifest](t, deviceTestCall(t, f.h, "POST", "/client/editions/"+b.ID+"/offline-manifests", req, dev.Token, 200))
	if next.StructureDigest == m.StructureDigest {
		t.Fatal("content versions shared a directory")
	}
	v2 := filepath.Join(account, "downloads", m.EditionID, next.StructureDigest)
	if e = os.MkdirAll(v2, 0700); e != nil {
		t.Fatal(e)
	}
	staged := savedOfflineFixture{dir: v2, count: next.PageCount, seen: map[int]string{}, write: saveWrite}
	if !errors.Is(staged.save(0, n, tag, raw), syscall.ENOSPC) {
		t.Fatal("space limit not exercised")
	}
	if _, e = os.Stat(filepath.Join(v1, "2")); e != nil {
		t.Fatal("previous complete version lost", e)
	}
	if e = os.RemoveAll(v1); e != nil {
		t.Fatal(e)
	}
	if persisted, e := os.ReadFile(queueFile); e != nil || string(persisted) != string(payload) {
		t.Fatal("deleting a download lost queued intent", e)
	}
}
