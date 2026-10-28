//go:build !windows

package claude

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// defaultSinkDir is a per-user directory under the temp dir.
func defaultSinkDir() string {
	return filepath.Join(os.TempDir(), "agent-keepalive-"+strconv.Itoa(os.Getuid()))
}

// checkPrivateDir refuses a sink directory that other users could reach
// into: it must be a real directory, owned by us, with no group or other
// permissions. The temp dir is shared, so someone else could have created
// the path first.
func checkPrivateDir(path string, fi os.FileInfo) error {
	if !fi.IsDir() || fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s must be a directory private to the user (mode %v)", path, fi.Mode())
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s is owned by uid %d, not us", path, st.Uid)
	}
	return nil
}

func restrictSocket(path string) error { return os.Chmod(path, 0o600) }

// socketExists reports whether path is a unix socket.
func socketExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode()&os.ModeSocket != 0
}

func dialSocket(ctx context.Context, path string) (io.WriteCloser, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", path)
}
