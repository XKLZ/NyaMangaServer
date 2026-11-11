package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestOfflineCommandLocksVerifiesAndPreservesDatabase(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	db, e := store.Open(context.Background(), data, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	raw := []byte("anonymous original")
	sum := fmt.Sprintf("%x", sha256.Sum256(raw))
	key := "assets/treec-originals/one.bin"
	path := filepath.Join(data, key)
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, raw, 0600)
	if _, e = db.Write.Exec("INSERT INTO assets VALUES('one',?,?,?,1)", key, sum, len(raw)); e != nil {
		t.Fatal(e)
	}
	db.Close()
	archive := t.TempDir()
	os.WriteFile(filepath.Join(archive, "one.bin"), raw, 0600)
	manifest := filepath.Join(archive, "originals.json")
	encoded, _ := json.Marshal(maintenance.MigrationOriginalManifest{Format: 1, Files: []model.InstanceFile{{Key: "one.bin", Size: int64(len(raw)), SHA256: sum}}})
	os.WriteFile(manifest, encoded, 0600)
	config := filepath.Join(root, "config.json")
	encoded, _ = json.Marshal(map[string]any{"public_url": "http://localhost", "data_dir": data})
	os.WriteFile(config, encoded, 0600)
	before, e := os.ReadFile(filepath.Join(data, "app.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	priorArgs, priorFlags := os.Args, flag.CommandLine
	mask := syscall.Umask(0077)
	defer func() { os.Args = priorArgs; flag.CommandLine = priorFlags; syscall.Umask(mask) }()
	invoke := func() error {
		os.Args = []string{"storage-verify", "--config", config, "--archive-manifest", manifest}
		flag.CommandLine = flag.NewFlagSet("storage-verify", flag.ContinueOnError)
		return run()
	}
	lock, e := appdata.Acquire(data)
	if e != nil {
		t.Fatal(e)
	}
	if e = invoke(); e == nil {
		t.Fatal("active instance lock ignored")
	}
	lock.Close()
	if e = invoke(); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(filepath.Join(data, "app.sqlite"))
	if e != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("offline verifier changed database", e)
	}
	for _, p := range []string{path, filepath.Join(archive, "one.bin")} {
		got, e := os.ReadFile(p)
		if e != nil || string(got) != string(raw) {
			t.Fatal("original changed", e)
		}
	}
	info, e := os.Stat(filepath.Join(data, "health/storage-original-verification.json"))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private receipt", e)
	}
}
