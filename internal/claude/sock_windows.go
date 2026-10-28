package claude

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// defaultSinkDir is a per-user directory under the temp dir. Windows has
// no numeric uid (os.Getuid is -1), and %TEMP% is already inside the
// user's profile, so the user name only keeps two accounts that share a
// temp dir apart.
func defaultSinkDir() string {
	u := regexp.MustCompile(`[^A-Za-z0-9_.-]+`).ReplaceAllString(os.Getenv("USERNAME"), "_")
	if u == "" {
		u = "user"
	}
	return filepath.Join(os.TempDir(), "agent-keepalive-"+u)
}

// checkPrivateDir only checks that the path is a directory: Windows has no
// unix permission bits, and access is governed by the ACLs inherited from
// the user's profile, which a temp dir under it already has.
func checkPrivateDir(path string, fi os.FileInfo) error {
	if !fi.IsDir() {
		return fmt.Errorf("%s must be a directory", path)
	}
	return nil
}

// restrictSocket is a no-op: chmod cannot express "owner only" on Windows.
func restrictSocket(string) error { return nil }

const pipePrefix = `\\.\pipe\`

func isPipe(path string) bool { return strings.HasPrefix(strings.ToLower(path), pipePrefix) }

// socketExists reports whether path names a usable endpoint. A named pipe
// is not probed (opening it would take one of its instances); an AF_UNIX
// socket shows up as a reparse point Go reports as a socket, or, on older
// Go/Windows combinations, as some non-directory file.
func socketExists(path string) bool {
	if isPipe(path) {
		return true
	}
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func dialSocket(ctx context.Context, path string) (io.WriteCloser, error) {
	if !isPipe(path) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}
	// A byte-mode named pipe can be opened as a file. Opening does not
	// block (a busy pipe fails at once), so ctx is only checked first.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return nil, errors.Join(errors.New("open named pipe"), err)
	}
	return f, nil
}
