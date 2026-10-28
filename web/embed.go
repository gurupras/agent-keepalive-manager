// Package web embeds the local UI, built into dist by `npm run build`.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// UI returns the built UI with index.html at its root. When the UI was not
// built (dist holds only .gitkeep) it has no index.html, and internal/api
// serves its placeholder page instead.
func UI() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded; Sub cannot fail
	}
	return sub
}
