package main

import (
	"context"
	"flag"
	"fmt"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
	"os"
	"time"
)

func main() {
	dir := flag.String("data-dir", "", "existing stopped instance directory")
	out := flag.String("output", "", "new private output directory outside instance")
	preserve := flag.Bool("preserve", false, "retain DB/WAL/SHM material copies (not a complete backup)")
	flag.Parse()
	if *dir == "" || *out == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "explicit --data-dir and --output required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	report, e := store.DiagnoseOffline(ctx, *dir, *out, *preserve, migrations.Files)
	if e != nil {
		fmt.Fprintln(os.Stderr, "offline_diagnosis_failed; no source database repair was attempted")
		os.Exit(1)
	}
	fmt.Printf("diagnostic_report_written readable=%v integrity=%s preserved=%v\n", report.Readable, report.Integrity, report.Preserved)
}
