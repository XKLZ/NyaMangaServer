package main

import (
	"log/slog"
	"nyamangaserver.local/server/internal/app"
	"nyamangaserver.local/server/internal/buildinfo"
	"os"
)

var version = buildinfo.Version
var commit = "unknown"

func main() {
	if e := app.Run(version, commit); e != nil {
		slog.Error("server failed", "error", e)
		os.Exit(1)
	}
}
