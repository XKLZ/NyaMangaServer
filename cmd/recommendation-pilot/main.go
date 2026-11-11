// recommendation-pilot only prepares/imports the fixed isolated validation instance.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/config"
	pilot "nyamangaserver.local/server/internal/recommendationpilot"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"strings"
)

func exclusive(path string, data []byte) error {
	if e := appdata.NoSymlinks(path); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".pilot-write-")
	if e != nil {
		return e
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	_, w := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if e = errors.Join(w, syncErr, closeErr); e != nil {
		return e
	}
	return os.Link(temporary, path)
}
func run() error {
	root := flag.String("root", "", "absolute NyaMangaServer repository root")
	action := flag.String("action", "", "prepare or import")
	flag.Parse()
	if !filepath.IsAbs(*root) || filepath.Clean(*root) != *root {
		return errors.New("explicit absolute repository root required")
	}
	if e := appdata.NoSymlinks(*root); e != nil {
		return e
	}
	mod, e := os.ReadFile(filepath.Join(*root, "go.mod"))
	if e != nil || !strings.Contains(string(mod), "module nyamangaserver.local/server") {
		return errors.New("wrong project root")
	}
	base := filepath.Join(*root, ".runtime/tests/runs/work-recommendation-pilot/instance")
	data := filepath.Join(base, "data")
	if e = appdata.NoSymlinks(base); e != nil {
		return e
	}
	if *action == "prepare" {
		for _, p := range []string{base, data, filepath.Join(base, "secrets"), filepath.Join(base, "reports")} {
			if e = os.MkdirAll(p, 0700); e != nil {
				return e
			}
			if e = os.Chmod(p, 0700); e != nil {
				return e
			}
		}
		token := make([]byte, 32)
		if _, e = rand.Read(token); e != nil {
			return e
		}
		cfg := config.Config{Listen: "127.0.0.1:8081", PublicURL: "http://127.0.0.1:5174", SessionCookieName: pilot.CookieName, DataDir: "data", SetupTokenFile: "secrets/setup-token", Roots: []config.Root{{Key: pilot.LibraryID, Name: "隔离快照，无源漫画", Path: "source-unavailable"}}, WorkRecommendations: config.WorkRecommendations{Python: filepath.Join(*root, ".runtime/cache/recommendations/venv/bin/python"), Worker: filepath.Join(*root, "runtime/recommendations/worker.py"), ModelsDir: filepath.Join(*root, ".runtime/cache/models/recommendations")}}
		b, _ := json.MarshalIndent(cfg, "", "  ")
		configPath := filepath.Join(base, "config.json")
		if e = appdata.NoSymlinks(configPath); e != nil {
			return e
		}
		if existing, re := os.ReadFile(configPath); re == nil {
			if string(existing) != string(b) {
				return errors.New("existing pilot config differs; do not overwrite")
			}
		} else if errors.Is(re, os.ErrNotExist) {
			if e = exclusive(configPath, b); e != nil {
				return e
			}
		} else {
			return re
		}
		tokenPath := filepath.Join(base, "secrets/setup-token")
		if e = appdata.NoSymlinks(tokenPath); e != nil {
			return e
		}
		if existing, re := os.ReadFile(tokenPath); re == nil {
			decoded, de := base64.RawURLEncoding.DecodeString(string(existing))
			if de != nil || len(decoded) < 32 {
				return errors.New("existing pilot credential invalid; do not overwrite")
			}
		} else if errors.Is(re, os.ErrNotExist) {
			if e = exclusive(tokenPath, []byte(base64.RawURLEncoding.EncodeToString(token))); e != nil {
				return e
			}
		} else {
			return re
		}
		fmt.Println("Isolated configuration prepared; setup credential saved privately, not printed.")
		return nil
	}
	if *action != "import" {
		return errors.New("choose --action prepare or import")
	}
	c, e := config.Load(filepath.Join(base, "config.json"))
	if e != nil {
		return e
	}
	if c.DataDir != data || c.Listen != "127.0.0.1:8081" || c.PublicURL != "http://127.0.0.1:5174" || c.SessionCookieName != pilot.CookieName || len(c.Roots) != 1 || c.Roots[0].Key != pilot.LibraryID || c.Roots[0].Path != filepath.Join(base, "source-unavailable") {
		return errors.New("pilot configuration boundary mismatch")
	}
	if _, e = os.Stat(c.Roots[0].Path); !errors.Is(e, os.ErrNotExist) {
		return errors.New("snapshot source root must not exist")
	}
	lock, e := appdata.Acquire(data)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = appdata.CheckReady(data); e != nil {
		return e
	}
	ctx := context.Background()
	s, e := store.Open(ctx, data, migrations.Files)
	if e != nil {
		return e
	}
	defer s.Close()
	if _, e = os.Lstat(filepath.Join(base, "reports/import.json")); !errors.Is(e, os.ErrNotExist) {
		return errors.New("existing import audit; do not overwrite")
	}
	evaluation := filepath.Join(*root, ".runtime/tests/runs/recommendations/run-v3/analysis/evaluation")
	manifestPath := filepath.Join(evaluation, "input-manifest.json")
	if e = appdata.NoSymlinks(manifestPath); e != nil {
		return e
	}
	manifestRaw, e := os.ReadFile(manifestPath)
	if e != nil {
		return e
	}
	var manifest struct {
		Files map[string]string `json:"files"`
	}
	if e = json.Unmarshal(manifestRaw, &manifest); e != nil {
		return e
	}
	booksPath := filepath.Join(evaluation, "books.json")
	if e = appdata.NoSymlinks(booksPath); e != nil {
		return e
	}
	booksRaw, e := os.ReadFile(booksPath)
	if e != nil {
		return e
	}
	if pilot.Hash(booksRaw) != manifest.Files["books.json"] {
		return errors.New("whitelist manifest checksum mismatch")
	}
	report, e := pilot.Import(ctx, s, data, filepath.Join(*root, ".runtime/tests/runs/recommendations/run-v3/analysis/evaluation/books.json"), filepath.Join(*root, ".runtime/tests/runs/recommendations/run-v2/books.json"), filepath.Join(*root, ".runtime/tests/fixtures/treec-recommendations-input/Covers"))
	if e != nil {
		return e
	}
	b, _ := json.MarshalIndent(report, "", "  ")
	if e = exclusive(filepath.Join(base, "reports/import.json"), b); e != nil {
		return e
	}
	fmt.Printf("Imported %d isolated snapshot Editions; no source files or credentials imported.\n", len(report.Records))
	return nil
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
