package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/library"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

// RunRehash is an explicit, offline operation. It never starts HTTP or workers.
func RunRehash() error {
	flags := flag.NewFlagSet("dev-rehash", flag.ContinueOnError)
	filename := flags.String("config", os.Getenv("NYAMANGA_CONFIG"), "explicit JSON configuration")
	reportPath := flags.String("report", "", "private result file (default: data/tmp/rehash-report.json)")
	if e := flags.Parse(os.Args[1:]); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	c, e := config.Load(*filename)
	if e != nil {
		return e
	}
	syscall.Umask(0077)
	lock, e := appdata.Acquire(c.DataDir)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = appdata.CheckReady(c.DataDir); e != nil {
		return e
	}
	// Never create an empty instance in place of a missing source database.
	if _, e = os.Stat(filepath.Join(c.DataDir, "app.sqlite")); e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	db, e := store.Open(ctx, c.DataDir, migrations.Files)
	if e != nil {
		return e
	}
	defer db.Close()
	var active int
	if e = db.Read.QueryRowContext(ctx, "SELECT count(*) FROM jobs WHERE state IN('queued','running')").Scan(&active); e != nil {
		return e
	}
	if active != 0 {
		return errors.New("active tasks exist; finish or pause them before offline rehash")
	}
	service := library.New(db, c, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	defer service.Close()
	type failure struct {
		BookID string `json:"book_id"`
		Code   string `json:"code"`
	}
	report := struct {
		Algorithm string    `json:"algorithm"`
		Updated   int       `json:"updated"`
		Failed    []failure `json:"failed"`
	}{Algorithm: "content-page8-v1", Failed: []failure{}}
	after := ""
	snapshot := false
	for ctx.Err() == nil {
		books, e := db.RehashCandidates(ctx, after, 100)
		if e != nil {
			return e
		}
		if len(books) == 0 {
			break
		}
		if !snapshot {
			if e = db.PrivateSnapshot(ctx, c.DataDir, "before-rehash-"); e != nil {
				return e
			}
			snapshot = true
		}
		for _, b := range books {
			if ctx.Err() != nil {
				break
			}
			after = b.ID
			hash, pages, e := service.Rehash(ctx, b)
			if e == nil {
				e = db.UpdateLookup(ctx, b, hash, pages)
			}
			if e != nil {
				code := "rehash_failed"
				var ce library.Error
				if errors.As(e, &ce) {
					code = string(ce)
				}
				report.Failed = append(report.Failed, failure{b.ID, code})
				continue
			}
			report.Updated++
		}
	}
	if *reportPath == "" {
		*reportPath = filepath.Join(c.DataDir, "tmp", "rehash-report.json")
	}
	if e = appdata.NoSymlinks(*reportPath); e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(*reportPath), 0700); e != nil {
		return e
	}
	raw, _ := json.MarshalIndent(report, "", "  ")
	f, e := os.CreateTemp(filepath.Dir(*reportPath), "pending-rehash-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(raw); e != nil {
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
	if e = os.Rename(f.Name(), *reportPath); e != nil {
		return e
	}
	fmt.Printf("Offline rehash: %d updated, %d deferred. Private report saved.\n", report.Updated, len(report.Failed))
	return ctx.Err()
}
