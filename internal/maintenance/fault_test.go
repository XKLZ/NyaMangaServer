package maintenance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"nyamangaserver.local/server/internal/model"
)

type failCacheRemoval struct {
	Catalog
	cause error
}

func (c failCacheRemoval) RemoveCoverIfUnchanged(string, int64, int64) error { return c.cause }
func TestCleanupFaultPreservesCacheAssetAndIndex(t *testing.T) {
	for _, test := range []struct {
		cause error
		code  string
	}{{os.ErrPermission, "permission_denied"}, {syscall.ENOSPC, "storage_full"}} {
		t.Run(test.code, func(t *testing.T) {
			s, db, cat, l, _ := fixture(t)
			ctx := context.Background()
			book := indexed(t, db, cat, l)
			raw, _, e := cat.Image(ctx, book.ID, book.ContentRevision, 1, false)
			if e != nil {
				t.Fatal(e)
			}
			choice, e := cat.UploadCover(ctx, book.ID, 0, raw)
			if e != nil {
				t.Fatal(e)
			}
			before, _, e := cat.Image(ctx, book.ID, book.ContentRevision, 1, true)
			if e != nil {
				t.Fatal(e)
			}
			asset, e := db.Asset(ctx, *choice.AssetID)
			if e != nil {
				t.Fatal(e)
			}
			s.catalog = failCacheRemoval{Catalog: cat, cause: &os.PathError{Op: "unlink", Path: "anonymous", Err: test.cause}}
			plan, e := s.Plan(ctx, model.MaintenanceInput{Operation: "cleanup", Expected: map[string]int64{}, Options: json.RawMessage(`{"categories":["covers"]}`)})
			if e != nil {
				t.Fatal(e)
			}
			job, e := s.Execute(ctx, plan.ID)
			if e != nil {
				t.Fatal(e)
			}
			s.Start(ctx)
			done := wait(t, db, job.ID)
			if done.State != "failed" || done.Error != test.code {
				t.Fatal("failure not recorded", done)
			}
			after, e := db.CoverSelection(ctx, book.ID)
			if e != nil || after.Revision != choice.Revision || after.AssetID == nil || *after.AssetID != *choice.AssetID {
				t.Fatal("cleanup changed choice", after, e)
			}
			if _, e = os.Stat(filepath.Join(s.c.DataDir, asset.Key)); e != nil {
				t.Fatal("protected asset removed", e)
			}
			image, _, e := cat.Image(ctx, book.ID, book.ContentRevision, 1, true)
			if e != nil || string(image) != string(before) {
				t.Fatal("cache image damaged", e)
			}
			current, e := db.Edition(ctx, book.ID)
			if e != nil || current.Revision != book.Revision || current.ContentRevision != book.ContentRevision {
				t.Fatal("index changed", current, e)
			}
		})
	}
}
