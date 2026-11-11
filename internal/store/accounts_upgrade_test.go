package store

import (
	"context"
	"io/fs"
	"nyamangaserver.local/server/migrations"
	"testing"
	"testing/fstest"
)

func TestAccountsUpgradePreservesIdentityAndRollback(t *testing.T) {
	ctx := context.Background()
	oldFS := fstest.MapFS{}
	names, _ := fs.Glob(migrations.Files, "*.sql")
	for _, n := range names {
		if n[:3] <= "024" {
			b, _ := fs.ReadFile(migrations.Files, n)
			oldFS[n] = &fstest.MapFile{Data: b}
		}
	}
	dir := t.TempDir()
	s, e := Open(ctx, dir, oldFS)
	if e != nil {
		t.Fatal(e)
	}
	a := Account{ID: "legacy", PasswordHash: "opaque", Revision: 9}
	v := Session{Hash: make([]byte, 32), AccountID: a.ID, Created: 1, Expires: 9999999999}
	if e = s.Initialize(ctx, a, v); e != nil {
		t.Fatal(e)
	}
	s.Close()
	bad := fstest.MapFS{}
	for k, v := range oldFS {
		bad[k] = v
	}
	bad["025_accounts_authorization.sql"] = &fstest.MapFile{Data: []byte("DROP TABLE accounts;SELECT * FROM nonexistent;")}
	if x, e := Open(ctx, dir, bad); e == nil {
		x.Close()
		t.Fatal("failed migration accepted")
	}
	s, e = Open(ctx, dir, oldFS)
	if e != nil {
		t.Fatal("rollback", e)
	}
	s.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	got, e := s.Account(ctx)
	if e != nil || got.ID != a.ID || got.PasswordHash != a.PasswordHash || got.Revision != 9 || got.Username != "admin" || got.Role != "admin" || !got.Enabled {
		t.Fatal(got, e)
	}
	sess, e := s.Session(ctx, v.Hash, 2)
	if e != nil || sess == nil || sess.AccountID != a.ID {
		t.Fatal("session lost", sess, e)
	}
	var fk int
	if e = s.Write.QueryRow("PRAGMA foreign_keys").Scan(&fk); e != nil || fk != 1 {
		t.Fatal("foreign keys disabled", fk, e)
	}
	rows, e := s.Read.Query("PRAGMA foreign_key_check")
	if e != nil {
		t.Fatal(e)
	}
	if rows.Next() {
		t.Fatal("broken foreign key")
	}
	rows.Close()
	s.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal("repeat open", e)
	}
	s.Close()
}
