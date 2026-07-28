// Package web embeds the HTML templates and compiled frontend assets.
// Run `make assets` (esbuild) before `go build` to refresh dist/.
package web

import "embed"

//go:embed all:dist templates
var FS embed.FS
