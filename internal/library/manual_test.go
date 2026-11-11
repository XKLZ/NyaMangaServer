//go:build manual

package library

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
)

// This opt-in acceptance only accesses the explicitly authorized local dev
// instance and manual sample root. Default tests/CI do not compile this file.
func TestManualLibraryAcceptance(t *testing.T) {
	filename := os.Getenv("NYAMANGA_MANUAL_CONFIG")
	if filename == "" {
		t.Skip("explicit manual configuration required")
	}
	repository, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	c, e := config.Load(filename)
	if e != nil {
		t.Fatal("manual configuration invalid")
	}
	expected := filepath.Join(repository, ".runtime/libraries/manual")
	if c.DataDir != filepath.Join(repository, ".runtime/dev/data") {
		t.Fatal("manual acceptance restricted to repository dev database")
	}
	var root string
	for _, r := range c.Roots {
		if r.Key == "dev-manual" {
			root = r.Path
		}
	}
	if root != expected {
		t.Fatal("manual acceptance restricted to authorized sample directory")
	}
	lock, e := os.OpenFile(filepath.Join(c.DataDir, "app.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		t.Fatal("stop development server before manual acceptance")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	digest := func() map[string]string {
		out := map[string]string{}
		r, e := os.OpenRoot(root)
		if e != nil {
			t.Fatal("manual root unavailable")
		}
		defer r.Close()
		e = fs.WalkDir(r.FS(), ".", func(p string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(p))
			if ext != ".zip" && ext != ".cbz" {
				return nil
			}
			f, e := r.Open(p)
			if e != nil {
				return e
			}
			h := sha256.New()
			_, e = io.Copy(h, f)
			f.Close()
			if e != nil {
				return e
			}
			out[p] = hex.EncodeToString(h.Sum(nil))
			return nil
		})
		if e != nil {
			t.Fatal("source verification failed")
		}
		return out
	}
	before := digest()
	ctx := context.Background()
	db, e := store.Open(ctx, c.DataDir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	account, e := db.Account(ctx)
	if e != nil || account == nil {
		t.Fatal("expected existing administrator")
	}
	var sessions int
	if e = db.Read.QueryRow("SELECT count(*) FROM sessions").Scan(&sessions); e != nil {
		t.Fatal(e)
	}
	s := New(db, c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer s.Close()
	libraries, e := db.Libraries(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var l *model.Library
	for _, v := range libraries {
		if v.RootKey == "dev-manual" {
			copy := v
			l = &copy
		}
	}
	if l == nil {
		l, e = s.SaveLibrary(ctx, model.Library{RootKey: "dev-manual", Name: "本机测试书库", Encoding: "utf8"}, 0)
		if e != nil {
			t.Fatal(e)
		}
	}
	s.Start(ctx)
	run := func() *model.Job {
		j, e := s.StartScan(ctx, l.ID)
		if e != nil {
			t.Fatal("manual scan could not start")
		}
		deadline := time.Now().Add(10 * time.Minute)
		for time.Now().Before(deadline) {
			v, e := db.Job(ctx, j.ID)
			if e != nil {
				t.Fatal(e)
			}
			if v.State == "completed" || v.State == "failed" {
				return v
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("manual scan timeout")
		return nil
	}
	first := run()
	if first.State != "completed" {
		t.Fatal("manual enumeration/finalization did not complete")
	}
	books, total, e := db.Editions(ctx, l.ID, "", 1, 100)
	if e != nil || total != len(before) || total > 100 {
		t.Fatal("manual book count mismatch")
	}
	type result struct {
		RelativePath  string `json:"relative_path"`
		State         string `json:"state"`
		Error         string `json:"error_code"`
		Pages         int    `json:"pages"`
		SampledImages int    `json:"sampled_images"`
	}
	results := []result{}
	identities := map[string][2]any{}
	available, images := 0, 0
	for _, b := range books {
		identities[b.Path] = [2]any{b.ID, b.ContentRevision}
		entry := result{RelativePath: b.Path, State: b.State, Error: b.Error, Pages: b.PageCount}
		if b.State == "available" {
			available++
			for _, index := range []int{1, b.PageCount} {
				raw, _, err := s.Image(ctx, b.ID, b.ContentRevision, index, false)
				if err == nil {
					if _, _, err = image.DecodeConfig(bytes.NewReader(raw)); err != nil {
						t.Fatal("returned sample image invalid")
					}
					entry.SampledImages++
					images++
				}
			}
			_, _, _ = s.Image(ctx, b.ID, b.ContentRevision, 1, true)
		}
		results = append(results, entry)
	}
	second := run()
	if second.State != "completed" {
		t.Fatal("manual rescan failed")
	}
	again, _, _ := db.Editions(ctx, l.ID, "", 1, 100)
	for _, b := range again {
		if identities[b.Path] != [2]any{b.ID, b.ContentRevision} {
			t.Fatal("manual rescan changed unchanged book identity")
		}
	}
	after := digest()
	if len(before) != len(after) {
		t.Fatal("source set changed during acceptance")
	}
	for p, h := range before {
		if after[p] != h {
			t.Fatal("source content changed during acceptance")
		}
	}
	preserved, e := db.Account(ctx)
	if e != nil || *preserved != *account {
		t.Fatal("administrator was changed")
	}
	var currentSessions int
	db.Read.QueryRow("SELECT count(*) FROM sessions").Scan(&currentSessions)
	if sessions != currentSessions {
		t.Fatal("existing sessions changed")
	}
	report := map[string]any{"sample_count": len(before), "indexed": total, "available": available, "failed_items": second.Failed, "sampled_images": images, "source_unchanged": true, "administrator_preserved": true, "sessions_preserved": true, "source_sha256": after, "books": results}
	encoded, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(repository, ".runtime/tests/manual")
	os.MkdirAll(dir, 0700)
	if e = os.WriteFile(filepath.Join(dir, "report.json"), encoded, 0600); e != nil {
		t.Fatal(e)
	}
	t.Logf("Manual sample acceptance: indexed=%d available=%d item_failures=%d sampled_images=%d source_unchanged=true admin_preserved=true", total, available, second.Failed, images)
}
