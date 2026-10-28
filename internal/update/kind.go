package update

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gurupras/agent-keepalive-manager/internal/distro"
)

// Install kinds (docs/release-artifacts.md).
const (
	KindDeb             = "deb"
	KindRPM             = "rpm"
	KindArch            = "arch"
	KindBrew            = "brew"
	KindAppImage        = "appimage"
	KindWindowsSetup    = "windows-setup"
	KindWindowsPortable = "windows-portable"
	KindMacOSPkg        = "macos-pkg"
	KindArchive         = "archive"
	KindDev             = "dev"
)

// KnownKind reports whether k is an install kind.
func KnownKind(k string) bool {
	switch k {
	case KindDeb, KindRPM, KindArch, KindBrew, KindAppImage, KindWindowsSetup,
		KindWindowsPortable, KindMacOSPkg, KindArchive, KindDev:
		return true
	}
	return false
}

// KindFile is the name of the file that overrides the linked install kind
// when it sits next to the executable or in ../libexec/ (Homebrew).
const KindFile = "install-kind"

// Install says how the running program was installed.
type Install struct {
	Kind string
	// Exe is the running executable, symlinks resolved.
	Exe string
	// Target is the file an update replaces: Exe, or the AppImage.
	Target string
}

// DetectInstall works out how the running program was installed: the kind
// it was linked with (distro), overridden by an install-kind file, or
// "appimage" when it runs from inside an AppImage.
func DetectInstall() Install {
	exe, _ := os.Executable()
	return detectInstall(distro.Current().InstallKind, exe, os.Getenv)
}

func detectInstall(linked, exe string, getenv func(string) string) Install {
	resolved := exe
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		resolved = r
	}
	in := Install{Kind: linked, Exe: resolved, Target: resolved}
	if !KnownKind(in.Kind) {
		in.Kind = KindDev
	}
	if k := readKindFile(resolved, exe); k != "" {
		in.Kind = k
	}
	// $APPIMAGE is inherited by everything an AppImage starts, so it only
	// counts when this binary is the one inside the AppImage's mount.
	if img := getenv("APPIMAGE"); img != "" {
		dir := getenv("APPDIR")
		inside := dir != "" && within(resolved, dir)
		if inside || (in.Kind == KindAppImage && dir == "") {
			in.Kind, in.Target = KindAppImage, img
		}
	}
	return in
}

// readKindFile returns the kind named by an install-kind file next to one
// of the executables or in its ../libexec/.
func readKindFile(exes ...string) string {
	for _, exe := range exes {
		if exe == "" {
			continue
		}
		dir := filepath.Dir(exe)
		for _, p := range []string{filepath.Join(dir, KindFile), filepath.Join(dir, "..", "libexec", KindFile)} {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			if k := strings.TrimSpace(string(b)); KnownKind(k) {
				return k
			}
		}
	}
	return ""
}

func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// SelfUpdates reports whether a program of kind installs updates itself.
func SelfUpdates(kind string) bool {
	switch kind {
	case KindAppImage, KindWindowsSetup, KindWindowsPortable, KindMacOSPkg, KindArchive:
		return true
	}
	return false
}

// Command is the package-manager command that updates a program of kind,
// or "" for a kind that is not updated by a package manager.
// exists reports whether a file exists (nil: os.Stat).
func Command(kind string, exists func(string) bool) string {
	if exists == nil {
		exists = func(p string) bool { _, err := os.Stat(p); return err == nil }
	}
	switch kind {
	case KindDeb:
		return "sudo apt update && sudo apt install --only-upgrade agent-keepalive"
	case KindRPM:
		if exists("/usr/bin/zypper") {
			return "sudo zypper update agent-keepalive"
		}
		return "sudo dnf upgrade --refresh agent-keepalive"
	case KindArch:
		return "sudo pacman -Syu agent-keepalive"
	case KindBrew:
		return "brew upgrade agent-keepalive"
	}
	return ""
}
