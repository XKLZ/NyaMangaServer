package store

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateSnapshotAfterAuthorizationAndCancellation(t *testing.T) {
	s, dir := openTest(t)
	ctx := context.Background()
	if err := s.PrivateSnapshot(ctx, dir, "offline-"); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "upgrade-snapshots", "offline-*.sqlite"))
	if err != nil || len(paths) != 1 {
		t.Fatal(paths, err)
	}
	info, err := os.Stat(paths[0])
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	u := url.URL{Scheme: "file", Path: paths[0], RawQuery: "mode=ro"}
	backup, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var integrity string
	if err = backup.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
	var version, enabled int
	if err = backup.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&version); err != nil || version != s.Version {
		t.Fatal(version, err)
	}
	if err = backup.QueryRow("SELECT enabled FROM enhancement_settings WHERE feature='work_recommendations'").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatal(enabled, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = s.PrivateSnapshot(canceled, dir, "canceled-"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, "upgrade-snapshots", "canceled-*.sqlite"))
	if err != nil || len(leftovers) != 0 {
		t.Fatal(leftovers, err)
	}
	// The pinned connection is returned and ordinary writes retain their wrapper.
	if _, err = s.Write.Exec("UPDATE enhancement_settings SET enabled=0 WHERE feature='work_recommendations'"); err != nil {
		t.Fatal(err)
	}
	if err = backup.QueryRow("SELECT enabled FROM enhancement_settings WHERE feature='work_recommendations'").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatal("snapshot changed with source", enabled, err)
	}
}
