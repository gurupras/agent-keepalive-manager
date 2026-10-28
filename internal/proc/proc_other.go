//go:build !linux && !darwin && !windows

package proc

// Unsupported OS: nothing is ever found alive, so nothing is ever pinged.
const hasOpenFiles = false

func startTime(int) string           { return "" }
func list() []Proc                   { return nil }
func env(int, string) (string, bool) { return "", false }
func cwd(int) string                 { return "" }
func openFiles(int) []string         { return nil }
