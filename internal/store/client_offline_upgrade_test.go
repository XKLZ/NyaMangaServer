package store

import (
	"context"
	"reflect"
	"testing"
	"testing/fstest"

	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/migrations"
)

func TestClientOfflineMigration30Preserves29AndRollsBack(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	old := migrationsThrough(t, 29)
	s, e := Open(ctx, dir, old)
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, s)
	if _, e = s.Write.Exec("INSERT INTO accounts VALUES(1,'anonymous-reader','fixture',1,1)"); e != nil {
		t.Fatal(e)
	}
	account, e := s.Account(ctx)
	if e != nil || account == nil {
		t.Fatal(account, e)
	}
	actor := model.WithActor(ctx, *account)
	req := model.ReadingSessionStart{Expected: 0, ContentRevision: 1, IdempotencyKey: "pre-offline-fixture"}
	first, e := s.StartReadingSession(actor, "book", req)
	if e != nil {
		t.Fatal(e)
	}
	ledger := dumpRows(t, s, "SELECT * FROM schema_migrations ORDER BY version")
	s.Close()
	const file = "030_offline_client.sql"
	raw, e := migrations.Files.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	bad := migrationsThrough(t, 30)
	bad[file] = &fstest.MapFile{Data: append(raw, []byte("\nINSERT INTO deliberately_missing_table VALUES(1);")...)}
	if invalid, e := Open(ctx, dir, bad); e == nil {
		invalid.Close()
		t.Fatal("failed migration committed")
	}
	for _, version := range []int{29, 30} {
		db, e := Open(ctx, dir, migrationsThrough(t, version))
		if e != nil {
			t.Fatal(e)
		}
		if db.Version != version || !reflect.DeepEqual(ledger, dumpRows(t, db, "SELECT * FROM schema_migrations WHERE version<=29 ORDER BY version")) {
			t.Fatal("old ledger changed")
		}
		var tables int
		if e = db.Read.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN('client_downloads','client_offline_manifests','client_offline_receipts')").Scan(&tables); e != nil || tables != 3*(version-29) {
			t.Fatal(tables, e)
		}
		replayed, e := db.StartReadingSession(actor, "book", req)
		if e != nil || !reflect.DeepEqual(first, replayed) {
			t.Fatal(replayed, e)
		}
		db.Close()
	}
}
