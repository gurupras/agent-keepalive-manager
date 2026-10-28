// Package web embeds the hub dashboard, built into dist by `npm run build`.
package web

import (
	"embed"
	"io/fs"
	"testing/fstest"
)

//go:embed all:dist
var dist embed.FS

// placeholder is served when the dashboard was not built into this binary.
const placeholder = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>agent-keepalive hub</title></head>
<body>
<h1>agent-keepalive hub</h1>
<p>The dashboard was not built into this binary. Run <code>npm ci &amp;&amp; npm run build</code> in hub/web, then rebuild.</p>
<p>The API is available under <code>v1/</code>.</p>
</body>
</html>
`

// FS returns the dashboard's files with index.html at the root, or a
// placeholder page when the dashboard was not built.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err == nil {
		if _, err := fs.Stat(sub, "index.html"); err == nil {
			return sub
		}
	}
	return fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(placeholder), Mode: 0o444}}
}
