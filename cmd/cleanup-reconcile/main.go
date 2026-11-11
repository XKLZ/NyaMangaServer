// cleanup-reconcile only corrects a proven completed, expired-finalization job.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
	"os"
	"syscall"
	"time"
)

func run() error {
	path := flag.String("config", "", "stopped instance configuration")
	id := flag.String("job", "", "terminal cleanup job ID")
	apply := flag.Bool("apply", false, "correct proven finalization only; never delete files")
	flag.Parse()
	if flag.NArg() != 0 || *path == "" || *id == "" {
		return model.ErrInput
	}
	syscall.Umask(0077)
	c, e := config.Load(*path)
	if e != nil {
		return e
	}
	lock, e := appdata.Acquire(c.DataDir)
	if e != nil {
		return e
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	result, e := store.ReconcileCleanup(ctx, c.DataDir, *id, *apply, migrations.Files)
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
func main() {
	if e := run(); e != nil {
		code := "reconciliation_rejected"
		switch {
		case errors.Is(e, model.ErrBusy):
			code = "instance_busy"
		case errors.Is(e, model.ErrConflict):
			code = "evidence_incomplete_or_conflicting"
		case errors.Is(e, model.ErrInput):
			code = "invalid_input_or_schema"
		}
		fmt.Fprintln(os.Stderr, code)
		os.Exit(1)
	}
}
