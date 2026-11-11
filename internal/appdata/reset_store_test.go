package appdata_test

import (
	"context"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
	"testing"
)

func TestResetReinitializesWithoutAccountOrScan(t *testing.T) {
	repo, _, data := appdata.ResetFixtureForIntegrationTest(t)
	if e := appdata.ResetDev(repo); e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(context.Background(), data, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	account, e := db.Account(context.Background())
	if e != nil || account != nil {
		t.Fatal("reset initialized account automatically", e)
	}
	libraries, e := db.Libraries(context.Background())
	if e != nil || len(libraries) != 0 {
		t.Fatal("reset added or scanned libraries", e)
	}
}
