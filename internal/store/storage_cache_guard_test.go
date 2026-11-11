package store

import (
	"context"
	"database/sql"
	"nyamangaserver.local/server/internal/model"
	"path/filepath"
	"testing"
)

func TestStorageCacheGuardInvalidatesBothConnections(t *testing.T) {
	s, dir := openTest(t)
	seedBook(t, s)
	ctx := context.Background()
	key := func(rev int64) string {
		name := model.SelectedCoverCacheName("book", 1, 1, rev, "page")
		return "cache/covers/v1/" + name[:2] + "/" + name
	}
	lookup := func(want bool) {
		t.Helper()
		tx, e := s.Write.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		got, e := storageCandidateCacheKeys(ctx, tx, []model.StorageDeletion{{Kind: "covers", Key: key(1), OnlyRedundant: true}})
		if e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
		if got[key(1)] != want {
			tx.Rollback()
			t.Fatal(got, want)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	lookup(false)
	if _, e := s.Write.Exec("INSERT INTO cover_selections(edition_id,mode,page_index,content_revision,revision,parsing_revision) VALUES('book','page',0,1,1,1)"); e != nil {
		t.Fatal(e)
	}
	lookup(true)
	// Simulate another process connection; data_version must invalidate the index.
	conn, e := sql.Open("sqlite", filepath.Join(dir, "app.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	if _, e = conn.ExecContext(ctx, "UPDATE cover_selections SET revision=2 WHERE edition_id='book'"); e != nil {
		t.Fatal(e)
	}
	lookup(false)
	// Transaction rollback also rolls back the temporary index invalidation.
	tx, e := s.Write.BeginTx(ctx, &sql.TxOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec("UPDATE cover_selections SET revision=1 WHERE edition_id='book'"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	lookup(false)
}
