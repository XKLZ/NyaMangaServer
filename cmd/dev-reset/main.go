package main

import (
	"flag"
	"fmt"
	"nyamangaserver.local/server/internal/appdata"
	"os"
	"syscall"
)

func main() {
	repository := flag.String("repository", ".", "NyaMangaServer repository root")
	flag.Parse()
	syscall.Umask(0077)
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional arguments")
		os.Exit(1)
	}
	if e := appdata.ResetDev(*repository); e != nil {
		fmt.Fprintln(os.Stderr, "development reset failed:", e)
		os.Exit(1)
	}
	fmt.Println("Development instance reset. Private setup credential regenerated; initialize through the browser.")
}
