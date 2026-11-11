// instance-restore operates only with the ordinary application's exclusive lock.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"nyamangaserver.local/server/internal/buildinfo"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
	"os"
	"syscall"
	"time"
)

var version = buildinfo.Version
var commit = "unknown"

func run() error {
	filename := flag.String("config", os.Getenv("NYAMANGA_CONFIG"), "explicit target configuration")
	archive := flag.String("archive", "", "explicit private instance ZIP")
	request := flag.String("request", "", "private JSON with request_key, root_mappings and acknowledge_stat_only")
	replaceUnhealthy := flag.Bool("replace-unhealthy-target", false, "explicitly preserve and replace stopped unhealthy target; requires preview then prepare")
	prepare := flag.Bool("prepare", false, "confirm whole replacement on next normal server start")
	status := flag.Bool("status", false, "read independent activation journal")
	rollback := flag.Int64("rollback", 0, "request controlled rollback with activation revision")
	flag.Parse()
	if flag.NArg() != 0 {
		return errors.New("unexpected positional argument")
	}
	syscall.Umask(0077)
	c, e := config.Load(*filename)
	if e != nil {
		return e
	}
	build := maintenance.InstanceBuild{Version: version, Commit: commit}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if *status {
		v, e := maintenance.ReadInstanceActivation(c, build)
		if e != nil {
			return e
		}
		return encoder.Encode(v)
	}
	if *rollback != 0 {
		v, e := maintenance.RequestOfflineInstanceRollback(ctx, c, build, *rollback, store.InstanceOfflineEpoch)
		if e != nil {
			return e
		}
		return encoder.Encode(v)
	}
	if *archive == "" || *request == "" {
		return errors.New("archive and request required")
	}
	f, e := os.Open(*request)
	if e != nil {
		return e
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil {
		return e
	}
	if !i.Mode().IsRegular() || i.Size() > 1<<20 || i.Mode().Perm()&0077 != 0 {
		return errors.New("request must be private bounded regular JSON")
	}
	var in maintenance.InstanceRestoreInput
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if e = d.Decode(&in); e != nil {
		return e
	}
	if *prepare {
		in.Expected = 1
	} else {
		in.Expected = 0
	}
	v, e := maintenance.PrepareOfflineInstanceRestore(ctx, c, build, migrations.Files, *archive, in, maintenance.InstanceOfflineOptions{Inspect: store.ValidateInstanceSchema, PrepareDatabase: store.PrepareInstanceDatabase, CheckTarget: store.CheckInstanceOfflineTarget, ReplaceUnhealthyTarget: *replaceUnhealthy, CheckUnhealthyTarget: func(ctx context.Context, dir string) error {
		return store.CheckInstanceOfflineReplacement(ctx, dir, true)
	}})
	if e != nil {
		return e
	}
	return encoder.Encode(v)
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "instance restore stopped:", e)
		os.Exit(1)
	}
}
