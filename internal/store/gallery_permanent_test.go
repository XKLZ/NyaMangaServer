package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/galleryhighlight"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestGalleryPermanentMigrationPreservesLegacyExpiry(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Unix()
	base := fstest.MapFS{}
	entries, _ := migrations.Files.ReadDir(".")
	for _, n := range entries {
		if strings.HasSuffix(n.Name(), ".sql") && n.Name() < "022" {
			raw, _ := migrations.Files.ReadFile(n.Name())
			base[n.Name()] = &fstest.MapFile{Data: raw}
		}
	}
	dir := t.TempDir()
	old, e := Open(ctx, dir, base)
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, old)
	actor := model.Session{Hash: make([]byte, 32), AccountID: "anonymous", Created: now, Expires: now + 86400}
	if e = old.Initialize(ctx, Account{ID: actor.AccountID, PasswordHash: "anonymous", Revision: 1}, actor); e != nil {
		t.Fatal(e)
	}
	gallerySQL(t, old, "UPDATE gallery_highlight_settings SET enabled=1")
	setTagNames(t, old, "book", "parody", "anonymous")
	if _, e = old.SetTagImage(ctx, tagID("parody", "anonymous"), "foreground", 0, model.TagImage{Width: 2, Height: 3, Format: "png", Asset: &model.Asset{ID: "preserved", Key: "assets/tags/aa/anonymous.png", Digest: "anonymous", Size: 4}}); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(dir, "assets", "tags", "aa", "anonymous.png")
	if e = os.MkdirAll(filepath.Dir(file), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(file, []byte("anon"), 0600); e != nil {
		t.Fatal(e)
	}

	tokens := []string{}
	hashes := [][]byte{}
	for i := 0; i < 4; i++ {
		raw := "ngh1_" + base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat(string(rune('A'+i)), 32)))
		hash, _ := galleryhighlight.TokenHash(raw)
		tokens = append(tokens, raw)
		hashes = append(hashes, hash)
		expiry := now + 1000
		var revoked any
		revision := int64(1)
		authrev := 1
		if i == 1 {
			expiry = now - 1
		}
		if i == 2 {
			revoked = now - 1
			revision = 8
		}
		if i == 3 {
			authrev = 2
		}
		gallerySQL(t, old, "INSERT INTO gallery_highlight_connections VALUES(?,?,?,?,?,?,?,?,?)", fmt.Sprint("legacy-", i), actor.AccountID, fmt.Sprint("old-name-", i), hash, authrev, now-86400, expiry, revoked, revision)
	}
	before := map[string][][]any{}
	rows, _ := old.Read.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	names := []string{}
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	rows.Close()
	for _, n := range names {
		before[n] = dumpRows(t, old, fmt.Sprintf("SELECT * FROM %q ORDER BY rowid", n))
	}
	old.Close()
	bad := fstest.MapFS{}
	for k, v := range base {
		bad[k] = v
	}
	raw, _ := migrations.Files.ReadFile("022_gallery_permanent_connections.sql")
	bad["022_gallery_permanent_connections.sql"] = &fstest.MapFile{Data: append(append([]byte{}, raw...), []byte("INSERT INTO absent VALUES(1);")...)}
	if failed, e := Open(ctx, dir, bad); e == nil {
		failed.Close()
		t.Fatal("failed rebuild committed")
	}
	prior, e := Open(ctx, dir, base)
	if e != nil || prior.Version != 21 {
		t.Fatal(e)
	}
	prior.Close()
	for repeat := 0; repeat < 2; repeat++ {
		up, e := Open(ctx, dir, migrationsThrough(t, 24))
		if e != nil {
			t.Fatal(e)
		}
		for _, n := range names {
			q := fmt.Sprintf("SELECT * FROM %q ORDER BY rowid", n)
			if n == "schema_migrations" {
				q = "SELECT * FROM schema_migrations WHERE version<=21 ORDER BY rowid"
			}
			if !reflect.DeepEqual(before[n], dumpRows(t, up, q)) {
				t.Fatal("old values changed", n)
			}
		}
		var count int
		if e = up.Read.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&count); e != nil || count != 0 {
			t.Fatal(count, e)
		}
		svc := galleryhighlight.New(up)
		svc.Now = func() time.Time { return time.Unix(now, 0) }
		for i, token := range tokens {
			e := svc.Status(ctx, token, func(v model.GalleryStatus) error {
				if v.ProtocolVersion != 2 || v.ConnectionExpiresAt == nil || *v.ConnectionExpiresAt != now+1000 {
					t.Fatal("legacy expiry/protocol changed")
				}
				return nil
			})
			if i == 0 && e != nil {
				t.Fatal(e)
			}
			if i > 0 && !errors.Is(e, model.ErrGalleryAuth) {
				t.Fatal("invalid legacy token revived", i, e)
			}
		}
		// Preserved v1 digest domain authorizes the valid old token under v2 only until its real expiry.
		if _, e = up.GalleryGrant(ctx, hashes[0], now+1000); !errors.Is(e, model.ErrGalleryAuth) {
			t.Fatal("old finite token became permanent", e)
		}

		b, err := os.ReadFile(file)
		if err != nil || string(b) != "anon" {
			t.Fatal("asset bytes changed", err)
		}
		if repeat == 1 {
			for i := 0; i < 19; i++ {
				galleryMint(t, svc, actor)
			}
			if _, e := svc.Create(ctx, actor, model.GalleryConnectionCreate{}); !errors.Is(e, model.ErrGalleryCapacity) {
				t.Fatal("mixed finite/null limit not enforced", e)
			}
		}
		up.Close()
	}
}
func TestGalleryPermanentSurvivesTimeButNotRevocation(t *testing.T) {
	s, _ := openTest(t)
	now := time.Now().Unix()
	actor, _ := galleryActors(t, s, now)
	galleryToggle(t, s, true)
	svc := galleryhighlight.New(s)
	v := galleryMint(t, svc, actor)
	if v.ProtocolVersion != 2 || v.Connection.ExpiresAt != nil || !strings.HasPrefix(v.Connection.Name, "授权-") || len([]rune(v.Connection.Name)) > 16 {
		t.Fatal(v.Connection)
	}
	svc.Now = func() time.Time { return time.Unix(now+20*365*86400, 0) }
	if e := svc.Status(context.Background(), v.Token, func(status model.GalleryStatus) error {
		if status.ConnectionExpiresAt != nil || status.ProtocolVersion != 2 {
			t.Fatal(status)
		}
		return nil
	}); e != nil {
		t.Fatal("indefinite token expired", e)
	}
	svc.Now = time.Now
	if _, e := svc.Revoke(context.Background(), actor, v.Connection.ID, 1); e != nil {
		t.Fatal(e)
	}
	if e := svc.Status(context.Background(), v.Token, func(model.GalleryStatus) error { return nil }); !errors.Is(e, model.ErrGalleryAuth) {
		t.Fatal(e)
	}
}
