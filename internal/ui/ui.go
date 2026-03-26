// Package ui embeds the dashboard static assets.
package ui

import "embed"

//go:embed dist/index.html
var IndexHTML []byte

//go:embed dist
var StaticFiles embed.FS
