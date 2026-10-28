package main

import "github.com/gurupras/agent-keepalive-manager/internal/update"

// target is where an artifact installs.
type target struct{ kind, os, arch string }

// archNames spells each Go architecture the way each format does.
var archNames = []struct{ goarch, debian, rpm, win string }{
	{"amd64", "amd64", "x86_64", "x64"},
	{"arm64", "arm64", "aarch64", "arm64"},
}

// artifactNames returns every file name of release version v that goes in
// the feed (docs/release-artifacts.md), with its kind, os and arch.
func artifactNames(v string) map[string]target {
	m := map[string]target{}
	for _, a := range archNames {
		for _, goos := range []string{"linux", "darwin"} {
			m["agent-keepalive_"+v+"_"+goos+"_"+a.goarch+".tar.gz"] = target{update.KindArchive, goos, a.goarch}
		}
		m["agent-keepalive_"+v+"_windows_"+a.goarch+".zip"] = target{update.KindArchive, "windows", a.goarch}
		m["agent-keepalive_"+v+"_"+a.debian+".deb"] = target{update.KindDeb, "linux", a.goarch}
		m["agent-keepalive-"+v+"-1."+a.rpm+".rpm"] = target{update.KindRPM, "linux", a.goarch}
		m["agent-keepalive-"+v+"-1-"+a.rpm+".pkg.tar.zst"] = target{update.KindArch, "linux", a.goarch}
		m["agent-keepalive-"+v+"-"+a.rpm+".AppImage"] = target{update.KindAppImage, "linux", a.goarch}
		m["agent-keepalive-"+v+"-setup-"+a.win+".exe"] = target{update.KindWindowsSetup, "windows", a.goarch}
		m["agent-keepalive-"+v+"-portable-"+a.win+".exe"] = target{update.KindWindowsPortable, "windows", a.goarch}
		m["agent-keepalive-"+v+"-darwin-"+a.win+".pkg"] = target{update.KindMacOSPkg, "darwin", a.goarch}
	}
	return m
}

// classify maps a dist file name of release version v to where it
// installs; hub archives, SHA256SUMS, the feed and anything unknown are
// not artifacts.
func classify(name, v string) (target, bool) {
	t, ok := artifactNames(v)[name]
	return t, ok
}
