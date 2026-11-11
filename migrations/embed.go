// Package migrations contains the versioned application SQL, never legacy source data.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
