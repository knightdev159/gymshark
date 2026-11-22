// Package web embeds the static web UI into the binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var content embed.FS

// FS returns the UI files rooted at the static directory.
func FS() fs.FS {
	sub, err := fs.Sub(content, "static")
	if err != nil {
		panic(err) // the embedded path is fixed at compile time
	}
	return sub
}
