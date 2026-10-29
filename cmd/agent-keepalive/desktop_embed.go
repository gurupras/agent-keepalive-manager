//go:build desktopembed

package main

import _ "embed"

// embeddedDesktop is the desktop window, built into the Windows portable
// exe (scripts/build-go.sh with DESKTOP_EMBED) so it stays one file.
//
//go:embed desktop/agent-keepalive-desktop.bin
var embeddedDesktop []byte
