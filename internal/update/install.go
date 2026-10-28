package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tempPrefix names the downloads and extractions left next to the target.
const tempPrefix = ".agent-keepalive-update-"

// swapFile puts the verified file src in place of dst, the running program,
// with dst's mode. On unix the running file is simply renamed over. A
// running .exe cannot be replaced on Windows, but it can be renamed: it
// moves aside to dst.old, which Cleanup removes at the next start.
func swapFile(dst, src, goos string) error {
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(dst); err == nil {
		mode = fi.Mode().Perm() | 0o100 // keep it executable by its owner
	}
	if err := os.Chmod(src, mode); err != nil {
		return err
	}
	if goos != "windows" {
		return os.Rename(src, dst)
	}
	old := dst + ".old"
	_ = os.Remove(old)
	if err := os.Rename(dst, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		_ = os.Rename(old, dst)
		return err
	}
	return nil
}

// Cleanup removes what an earlier update left next to the running
// executable: the previous Windows executable (.old) and abandoned
// downloads. Call it once at start-up.
func Cleanup() {
	in := DetectInstall()
	cleanup(in.Target, time.Now())
}

func cleanup(target string, now time.Time) {
	if target == "" {
		return
	}
	_ = os.Remove(target + ".old")
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(target), tempPrefix+"*"))
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && now.Sub(fi.ModTime()) > time.Hour {
			_ = os.RemoveAll(m)
		}
	}
}

// extractBinary writes the regular file called name (at any depth) of the
// .tar.gz or .zip archive to dst.
func extractBinary(archive, archiveName, name, dst string) error {
	if strings.HasSuffix(archiveName, ".zip") {
		z, err := zip.OpenReader(archive)
		if err != nil {
			return fmt.Errorf("update archive: %w", err)
		}
		defer z.Close()
		for _, f := range z.File {
			if f.FileInfo().Mode().IsRegular() && pathBase(f.Name) == name {
				r, err := f.Open()
				if err != nil {
					return err
				}
				defer r.Close()
				return writeFile(dst, r)
			}
		}
		return fmt.Errorf("%s is not in the update archive", name)
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("update archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s is not in the update archive", name)
		}
		if err != nil {
			return fmt.Errorf("update archive: %w", err)
		}
		if h.Typeflag == tar.TypeReg && pathBase(h.Name) == name {
			return writeFile(dst, tr)
		}
	}
}

// pathBase is the last element of an archive path, which uses "/" (and,
// in zips made on Windows, sometimes "\").
func pathBase(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return p[strings.LastIndex(p, "/")+1:]
}

func writeFile(dst string, r io.Reader) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
