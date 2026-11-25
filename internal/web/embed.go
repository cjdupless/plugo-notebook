package web

import "embed"

// StaticFiles holds the embedded Plugo web frontend assets.
//
//go:embed all:static
var StaticFiles embed.FS
