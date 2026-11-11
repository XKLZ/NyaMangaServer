// Package webassets embeds the actual Vite production build. Run make web-build first.
package webassets

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var content embed.FS

func Files() fs.FS {
	f, e := fs.Sub(content, "dist")
	if e != nil {
		panic(e)
	}
	return f
}
