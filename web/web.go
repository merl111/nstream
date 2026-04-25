// Package webui embeds the compiled React SPA into the Go binary.
package webui

import "embed"

//go:embed all:dist
var FS embed.FS
