package main

import (
	"fmt"
	"nyamangaserver.local/server/internal/app"
	"os"
)

func main() {
	if e := app.RunRehash(); e != nil {
		fmt.Fprintln(os.Stderr, "offline rehash failed:", e)
		os.Exit(1)
	}
}
