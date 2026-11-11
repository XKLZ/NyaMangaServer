// Package buildinfo supplies the shared application release version.
package buildinfo

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var rawVersion string

// Version is shared by the server and the offline restore command.
var Version = strings.TrimSpace(rawVersion)
