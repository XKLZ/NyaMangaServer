package library

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"nyamangaserver.local/server/internal/model"
)

type failCoverCommit struct {
	Repository
	cause error
}

func (r failCoverCommit) SetCover(context.Context, string, int64, model.CoverSelection, *model.Asset) (model.CoverSelection, error) {
	return model.CoverSelection{}, r.cause
}
func TestCoverCommitFaultPreservesOldAssetAndRemovesNewFile(t *testing.T) {
	for _, cause := range []error{syscall.ENOSPC, os.ErrPermission} {
		t.Run(cause.Error(), func(t *testing.T) {
			s, l, _ := setup(t)
			ctx := context.Background()
			s.Start(ctx)
			j := scan(t, s, l)
			if j.State != "completed" {
				t.Fatal(j)
			}
			books, _, e := s.store.Editions(ctx, l.ID, "available", 1, 100)
			if e != nil || len(books) == 0 {
				t.Fatal(e)
			}
			book := books[0]
			raw, _, e := s.Image(ctx, book.ID, book.ContentRevision, 1, false)
			if e != nil {
				t.Fatal(e)
			}
			before, e := s.UploadCover(ctx, book.ID, 0, raw)
			if e != nil {
				t.Fatal(e)
			}
			old, e := s.store.Asset(ctx, *before.AssetID)
			if e != nil {
				t.Fatal(e)
			}
			oldBytes, e := os.ReadFile(filepath.Join(s.Config.DataDir, old.Key))
			if e != nil {
				t.Fatal(e)
			}
			failed := New(failCoverCommit{Repository: s.store, cause: &os.PathError{Op: "commit", Path: "anonymous", Err: cause}}, s.Config, s.Logger)
			defer failed.Close()
			_, e = failed.UploadCover(ctx, book.ID, before.Revision, raw)
			if !errors.Is(e, cause) {
				t.Fatal("fault not propagated", e)
			}
			after, e := s.store.CoverSelection(ctx, book.ID)
			if e != nil || after.Revision != before.Revision || after.AssetID == nil || *after.AssetID != *before.AssetID {
				t.Fatal("existing selection changed", after, e)
			}
			saved, e := os.ReadFile(filepath.Join(s.Config.DataDir, old.Key))
			if e != nil || string(saved) != string(oldBytes) {
				t.Fatal("old asset removed", e)
			}
			files := 0
			if e = filepath.WalkDir(filepath.Join(s.Config.DataDir, "assets"), func(path string, d fs.DirEntry, e error) error {
				if e != nil {
					return e
				}
				if !d.IsDir() {
					files++
				}
				return nil
			}); e != nil || files != 1 {
				t.Fatal("new failed asset or temporary file leaked", files, e)
			}
			unchanged, e := s.store.Edition(ctx, book.ID)
			if e != nil || unchanged.Revision != book.Revision || unchanged.ContentRevision != book.ContentRevision {
				t.Fatal("fault damaged index", unchanged, e)
			}
		})
	}
}
