package main

import (
	"flag"
	"fmt"
	"nyamangaserver.local/server/internal/testfixture"
	"os"
)

func main() {
	output := flag.String("output", "", "explicit isolated fixture directory")
	serve := flag.String("serve", "", "serve an isolated anonymous acceptance instance with an offline provider")
	recovery := flag.String("recovery-instance", "", "create new isolated full-state recovery fixture; password documented in backup-restore.md")
	flag.Parse()
	if *recovery != "" {
		if *serve != "" || *output != "" {
			fmt.Fprintln(os.Stderr, "choose one fixture mode")
			os.Exit(1)
		}
		filename, e := PrepareRecoveryFixture(*recovery)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		fmt.Println(filename)
		return
	}
	if *serve != "" {
		if e := Serve(*serve); e != nil {
			fmt.Fprintln(os.Stderr, e)
			os.Exit(1)
		}
		return
	}
	if *output == "" {
		fmt.Fprintln(os.Stderr, "--output is required")
		os.Exit(1)
	}
	if e := testfixture.Write(*output); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
