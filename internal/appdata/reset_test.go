package appdata

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func resetFixture(t *testing.T) (string, string, string) {
	t.Helper()
	repo := t.TempDir()
	dev := filepath.Join(repo, ".runtime", "dev")
	data := filepath.Join(dev, "data")
	for _, dir := range []string{data, filepath.Join(dev, "secrets"), filepath.Join(repo, ".runtime/libraries/manual"), filepath.Join(repo, ".runtime/cache"), filepath.Join(repo, ".runtime/m1")} {
		if e := os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module nyamangaserver.local/server\n"), 0600); e != nil {
		t.Fatal(e)
	}
	c := map[string]any{"public_url": "http://localhost:8080", "data_dir": "data", "setup_token_file": "secrets/setup-token", "library_roots": []any{map[string]string{"root_key": "anonymous", "display_name": "Anonymous", "path": "../libraries/manual"}}}
	b, _ := json.Marshal(c)
	for _, name := range []string{"config.json", "vite-config.json"} {
		if e := os.WriteFile(filepath.Join(dev, name), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{"app.sqlite", "app.sqlite-wal", "app.sqlite-shm", "cache/old.png", "cache/covers/v1/aa/old.png", "upgrade-snapshots/old.sqlite"} {
		path := filepath.Join(data, name)
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte("generated"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{".runtime/dev/secrets/setup-token", ".runtime/dev/secrets/provider-token", ".runtime/libraries/manual/anonymous.cbz", ".runtime/cache/tool", ".runtime/m1/history", ".runtime/dev/data/preserve.txt"} {
		if e := os.WriteFile(filepath.Join(repo, name), []byte("preserve unless setup-token"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	return repo, dev, data
}
func assertReset(t *testing.T, repo, dev, data string) {
	t.Helper()
	for _, name := range []string{"app.sqlite", "app.sqlite-wal", "app.sqlite-shm", "cache", "upgrade-snapshots", ResetMarker} {
		if _, e := os.Lstat(filepath.Join(data, name)); !os.IsNotExist(e) {
			t.Fatal("reset target remains", name, e)
		}
	}
	for _, name := range []string{".runtime/dev/config.json", ".runtime/dev/vite-config.json", ".runtime/dev/secrets/provider-token", ".runtime/libraries/manual/anonymous.cbz", ".runtime/cache/tool", ".runtime/m1/history", ".runtime/dev/data/preserve.txt"} {
		if _, e := os.Stat(filepath.Join(repo, name)); e != nil {
			t.Fatal("preserved material lost", name, e)
		}
	}
	token := filepath.Join(dev, "secrets/setup-token")
	info, e := os.Stat(token)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credential privacy", e)
	}
	value, e := os.ReadFile(token)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := base64.RawURLEncoding.DecodeString(string(value))
	if e != nil || len(raw) != 32 {
		t.Fatal("credential format")
	}
	if e = CheckReady(data); e != nil {
		t.Fatal(e)
	}
}
func TestResetPreservesBoundariesAndRotatesCredential(t *testing.T) {
	repo, dev, data := resetFixture(t)
	if e := ResetDev(repo); e != nil {
		t.Fatal(e)
	}
	assertReset(t, repo, dev, data)
	before, _ := os.ReadFile(filepath.Join(dev, "secrets/setup-token"))
	if e := ResetDev(repo); e != nil {
		t.Fatal("repeat reset", e)
	}
	assertReset(t, repo, dev, data)
	after, _ := os.ReadFile(filepath.Join(dev, "secrets/setup-token"))
	if bytes.Equal(before, after) {
		t.Fatal("credential reused")
	}

}
func TestResetRecoversEveryInterruptedStep(t *testing.T) {
	for _, point := range []string{"marked", "app.sqlite", "app.sqlite-wal", "app.sqlite-shm", "cache", "upgrade-snapshots", "credential"} {
		t.Run(point, func(t *testing.T) {
			repo, dev, data := resetFixture(t)
			interrupted := errors.New("simulated interruption")
			e := resetDev(repo, func(step string) error {
				if step == point {
					return interrupted
				}
				return nil
			})
			if !errors.Is(e, interrupted) {
				t.Fatal(e)
			}
			if e = CheckReady(data); e == nil {
				t.Fatal("half-reset instance may start")
			}
			if e = ResetDev(repo); e != nil {
				t.Fatal("recovery", e)
			}
			assertReset(t, repo, dev, data)
		})
	}
}
func TestResetSharesInstanceLock(t *testing.T) {
	repo, dev, data := resetFixture(t)
	before, _ := os.ReadFile(filepath.Join(dev, "secrets/setup-token"))
	lock, e := Acquire(data)
	if e != nil {
		t.Fatal(e)
	}
	if second, e := Acquire(data); e == nil {
		second.Close()
		t.Fatal("second service lock allowed")
	}
	if e = ResetDev(repo); e == nil {
		t.Fatal("running instance reset")
	}
	after, _ := os.ReadFile(filepath.Join(dev, "secrets/setup-token"))
	if !bytes.Equal(before, after) {
		t.Fatal("blocked reset changed credential")
	}
	if _, e = os.Stat(filepath.Join(data, ResetMarker)); !os.IsNotExist(e) {
		t.Fatal("blocked reset created marker")
	}
	lock.Close()
	if e = ResetDev(repo); e != nil {
		t.Fatal(e)
	}
}
func TestResetRejectsRedirectedPaths(t *testing.T) {
	for _, name := range []string{"data", "data/app.sqlite", "data/cache", "data/upgrade-snapshots", "data/app.lock", "data/" + ResetMarker, "secrets", "secrets/setup-token", "config.json", "vite-config.json"} {
		t.Run(name, func(t *testing.T) {
			repo, dev, _ := resetFixture(t)
			outside := t.TempDir()
			sentinel := filepath.Join(outside, "sentinel")
			os.WriteFile(sentinel, []byte("safe"), 0600)
			target := filepath.Join(dev, name)
			if e := os.RemoveAll(target); e != nil {
				t.Fatal(e)
			}
			if e := os.Symlink(outside, target); e != nil {
				t.Fatal(e)
			}
			if e := ResetDev(repo); e == nil {
				t.Fatal("symbolic link reset allowed")
			}
			b, e := os.ReadFile(sentinel)
			if e != nil || string(b) != "safe" {
				t.Fatal("escaped reset boundary", e)
			}
		})
	}
	repo, dev, _ := resetFixture(t)
	c := map[string]any{"public_url": "http://localhost", "data_dir": "../elsewhere", "setup_token_file": "secrets/setup-token"}
	b, _ := json.Marshal(c)
	if e := os.WriteFile(filepath.Join(dev, "config.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	if e := ResetDev(repo); e == nil {
		t.Fatal("redirected config reset allowed")
	}
}
