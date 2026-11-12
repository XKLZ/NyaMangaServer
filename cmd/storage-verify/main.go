// storage-verify records a private offline proof; it never deletes assets.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
	"os"
	"syscall"
	"time"
)

func run() error {
	configPath := flag.String("config", "", "explicit stopped-instance configuration")
	manifest := flag.String("archive-manifest", "", "absolute JSON manifest in a separately prepared originals directory (not a ZIP path)")
	flag.Parse()
	if flag.NArg() != 0 || *configPath == "" || *manifest == "" {
		return errors.New("config and archive-manifest required")
	}
	syscall.Umask(0077)
	c, e := config.Load(*configPath)
	if e != nil {
		return e
	}
	lock, e := appdata.Acquire(c.DataDir)
	if e != nil {
		return e
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	assets, e := store.MigrationOriginalAssets(ctx, c.DataDir, migrations.Files)
	if e != nil {
		return e
	}
	n, e := maintenance.VerifyMigrationOriginals(ctx, c, *manifest, assets)
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"verified_assets": n, "deletion_performed": false})
}
func main() {
	if e := run(); e != nil {
		code := "verification_failed"
		switch {
		case errors.Is(e, os.ErrNotExist):
			code = "file_missing"
		case errors.Is(e, os.ErrPermission):
			code = "permission_denied"
		case errors.Is(e, model.ErrConflict):
			code = "verification_mismatch"
		case errors.Is(e, model.ErrInput):
			code = "invalid_manifest_or_schema"
		}
		fmt.Fprintln(os.Stderr, "original verification stopped:", code)
		os.Exit(1)
	}
}
