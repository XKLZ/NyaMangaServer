package appdata

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"nyamangaserver.local/server/internal/config"
)

// ResetDev is restricted to this repository's .runtime/dev instance. The
// interrupt hook is private and used only to prove restart after each step.
func ResetDev(repository string) error { return resetDev(repository, nil) }
func resetDev(repository string, after func(string) error) error {
	repository, e := filepath.Abs(repository)
	if e != nil {
		return e
	}
	// The repository itself may be reached through an OS alias; managed children
	// must be real paths. No source root is traversed or modified here.
	repository, e = filepath.EvalSymlinks(repository)
	if e != nil {
		return e
	}
	module, e := os.ReadFile(filepath.Join(repository, "go.mod"))
	if e != nil || !hasModule(module) {
		return errors.New("reset requires the NyaMangaServer repository root")
	}
	dev := filepath.Join(repository, ".runtime", "dev")
	data := filepath.Join(dev, "data")
	token := filepath.Join(dev, "secrets", "setup-token")
	for _, path := range []string{dev, data, token, filepath.Join(dev, "config.json"), filepath.Join(dev, "vite-config.json")} {
		if e = NoSymlinks(path); e != nil {
			return e
		}
	}
	// Validate both modes before touching anything; neither may redirect reset to
	// another instance. At least the primary config must exist.
	for _, name := range []string{"config.json", "vite-config.json"} {
		file := filepath.Join(dev, name)
		if _, err := os.Stat(file); os.IsNotExist(err) && name == "vite-config.json" {
			continue
		}
		c, err := config.Load(file)
		if err != nil {
			return fmt.Errorf("reset configuration: %w", err)
		}
		if filepath.Clean(c.DataDir) != data || filepath.Clean(c.SetupTokenFile) != token {
			return errors.New("reset configuration must use .runtime/dev/data and secrets/setup-token")
		}
		for _, root := range c.Roots {
			source, err := filepath.EvalSymlinks(root.Path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if contains(data, source) || contains(source, data) || contains(source, token) {
				return errors.New("library source overlaps reset targets")
			}
		}
	}
	lock, e := Acquire(data)
	if e != nil {
		return e
	}
	defer lock.Close()
	targets := []string{"app.sqlite", "app.sqlite-wal", "app.sqlite-shm", "cache", "upgrade-snapshots"}
	for _, name := range append(targets, ResetMarker) {
		path := filepath.Join(data, name)
		if e = NoSymlinks(path); e != nil {
			return e
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		directory := name == "cache" || name == "upgrade-snapshots"
		if (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
			return errors.New("unexpected file type in reset targets")
		}
	}
	if e = os.MkdirAll(filepath.Dir(token), 0700); e != nil {
		return e
	}
	root, e := os.OpenRoot(data)
	if e != nil {
		return e
	}
	defer root.Close()
	marker, e := root.OpenFile(ResetMarker, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscallNoFollow(), 0600)
	if e != nil {
		return e
	}
	if _, e = marker.WriteString("development reset incomplete\n"); e != nil {
		marker.Close()
		return e
	}
	if e = marker.Sync(); e != nil {
		marker.Close()
		return e
	}
	if e = marker.Close(); e != nil {
		return e
	}
	if e = syncDir(data); e != nil {
		return e
	}
	step := func(name string) error {
		if after != nil {
			return after(name)
		}
		return nil
	}
	if e = step("marked"); e != nil {
		return e
	}
	for _, name := range targets {
		if e = root.RemoveAll(name); e != nil {
			return e
		}
		if e = step(name); e != nil {
			return e
		}
	}
	secretRoot, e := os.OpenRoot(filepath.Dir(token))
	if e != nil {
		return e
	}
	defer secretRoot.Close()
	if e = secretRoot.Remove("setup-token"); e != nil && !os.IsNotExist(e) {
		return e
	}
	// Never leave the replacement credential half-written. A repeated reset
	// removes this known temporary file and starts a new private credential.
	if e = secretRoot.Remove("setup-token.reset-pending"); e != nil && !os.IsNotExist(e) {
		return e
	}
	f, e := secretRoot.OpenFile("setup-token.reset-pending", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	raw := make([]byte, 32)
	if _, e = rand.Read(raw); e != nil {
		f.Close()
		return e
	}
	if _, e = f.WriteString(base64.RawURLEncoding.EncodeToString(raw)); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = secretRoot.Rename("setup-token.reset-pending", "setup-token"); e != nil {
		return e
	}
	if e = syncDir(filepath.Dir(token)); e != nil {
		return e
	}
	if e = step("credential"); e != nil {
		return e
	}
	if e = syncDir(data); e != nil {
		return e
	}
	if e = root.Remove(ResetMarker); e != nil {
		return e
	}
	return syncDir(data)
}
func contains(parent, child string) bool {
	rel, e := filepath.Rel(parent, child)
	return e == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}
func hasModule(b []byte) bool {
	return strings.HasPrefix(string(b), "module nyamangaserver.local/server\n")
}
func syscallNoFollow() int { return syscall.O_NOFOLLOW }
func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
