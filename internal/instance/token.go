package instance

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// TokenFile is the API token's file name in the data directory.
const TokenFile = "token"

// Token returns the API token kept in <dataDir>/token, creating a random
// 32-byte (64 hex characters) one, mode 0600, on first use. A file that is
// readable by others is tightened, since the token is what keeps other
// local users out of the API.
func Token(dataDir string) (string, error) {
	path := filepath.Join(dataDir, TokenFile)
	if t, err := ReadToken(dataDir); err == nil {
		if runtime.GOOS != "windows" {
			if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
				if err := os.Chmod(path, 0o600); err != nil {
					return "", fmt.Errorf("token file %s is readable by others and cannot be fixed: %w", path, err)
				}
			}
		}
		return t, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	t := hex.EncodeToString(b)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", err
	}
	// O_EXCL: when two servers start at once, one creates the token and the
	// other reads it.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		// The other server may not have written it yet.
		for range 20 {
			if t, err := ReadToken(dataDir); err == nil {
				return t, nil
			}
			time.Sleep(50 * time.Millisecond)
		}
		return ReadToken(dataDir)
	}
	if err != nil {
		return "", err
	}
	_, werr := f.WriteString(t + "\n")
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(path)
		return "", werr
	}
	return t, nil
}

// ReadToken reads the token of the server whose data directory is dataDir
// (for the CLI and for proxying), without creating one.
func ReadToken(dataDir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, TokenFile))
	if err != nil {
		return "", err
	}
	t := strings.TrimSpace(string(b))
	if len(t) < 32 {
		return "", fmt.Errorf("token file %s is malformed", filepath.Join(dataDir, TokenFile))
	}
	return t, nil
}
