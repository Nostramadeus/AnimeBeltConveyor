// Package web holds the static UI, embedded into the binary.
package web

import "embed"

//go:embed *
var FS embed.FS
