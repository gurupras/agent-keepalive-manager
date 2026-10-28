# Release artifacts and the update feed

Everything a release produces lands in `dist/` with these names (`V` is the version without a leading `v`). The feed tool (`go run ./tools/release`), the packaging scripts and the updater all rely on them.

| File | Install kind | Self-updates |
|---|---|---|
| `agent-keepalive_V_linux_{amd64,arm64}.tar.gz` | `archive` | yes |
| `agent-keepalive_V_darwin_{amd64,arm64}.tar.gz` | `archive` | yes |
| `agent-keepalive_V_windows_{amd64,arm64}.zip` | `archive` | yes |
| `agent-keepalive_V_{amd64,arm64}.deb` | `deb` | no: apt |
| `agent-keepalive-V-1.{x86_64,aarch64}.rpm` | `rpm` | no: dnf/zypper |
| `agent-keepalive-V-1-{x86_64,aarch64}.pkg.tar.zst` | `arch` | no: pacman |
| `agent-keepalive-V-{x86_64,aarch64}.AppImage` | `appimage` | yes |
| `agent-keepalive-V-setup-{x64,arm64}.exe` | `windows-setup` | yes |
| `agent-keepalive-V-portable-{x64,arm64}.exe` | `windows-portable` | yes |
| `agent-keepalive-V-darwin-{x64,arm64}.pkg` | `macos-pkg` | yes (opens Installer) |
| (Homebrew formula in the tap, from the darwin/linux archives) | `brew` | no: brew |
| `agent-keepalive-hub_V_linux_{amd64,arm64}.tar.gz` | | |
| `SHA256SUMS` | | |
| `latest.json` | | the feed |

Each artifact contains a binary linked with its install kind (`-X …/internal/distro.installKind=<kind>`), so the program knows how it was installed. The Homebrew formula installs the `archive` binary, so it marks the install instead: it writes a file named `install-kind` containing `brew` into the formula's `libexec/`. `internal/update` reads an `install-kind` file next to the executable or in `../libexec/` and lets it override the linked kind; any other packager can use the same file.

## Inside the packages

| Kind | Program | Also installs |
|---|---|---|
| deb, rpm, arch | `/usr/bin/agent-keepalive` | `/usr/share/applications/agent-keepalive.desktop` (runs `agent-keepalive open`), icon, `/usr/lib/systemd/user/agent-keepalive.service` (not enabled), the repository entry and key (see packaging/README.md) |
| AppImage | the AppImage itself; running it = `agent-keepalive open` (arguments are passed through) | |
| windows-setup | `%LOCALAPPDATA%\Programs\agent-keepalive\agent-keepalive.exe` (per-user, no admin) | Start-menu shortcut (`open`), optional "start at login", uninstaller |
| windows-portable | the single `.exe`; double-click = `open`; data next to it in `agent-keepalive-data\` | |
| macos-pkg | `/usr/local/bin/agent-keepalive` and `/Applications/agent-keepalive.app` (a launcher that runs `open`) | |

`agent-keepalive open` starts the server in the background if none is running (detached, logging to `<data dir>/server.log`), waits for `api/health`, and opens the UI in the default browser. The UI's menu has **Quit**, which calls `POST /api/quit` (token required) and shows a "server stopped" page.

## The feed

`latest.json`, produced by `go run ./tools/release feed -version V -base-url <url of the version folder> -notes notes.md -page <release page> -out latest.json dist/*` and signed with `UPDATE_SIGNING_KEY` (hex Ed25519 seed; `go run ./tools/release keygen` prints a new pair):

```json
{
  "version": "1.5.0",
  "publishedAt": "2026-10-27T12:00:00Z",
  "notes": "markdown",
  "page": "https://github.com/gurupras/agent-keepalive-manager/releases/tag/v1.5.0",
  "artifacts": [
    { "kind": "appimage", "os": "linux", "arch": "amd64",
      "name": "agent-keepalive-1.5.0-x86_64.AppImage",
      "url": "https://agent-keepalive.gurupras.me/download/1.5.0/agent-keepalive-1.5.0-x86_64.AppImage",
      "sha256": "…", "size": 12345678 }
  ],
  "signature": "<hex Ed25519 signature over the canonical JSON of every field above, without signature>"
}
```

The canonical form is the object without `signature`, marshalled by Go's `encoding/json` with the fields in the order above (the tool and the updater share `internal/update/feed.go`). The updater refuses a feed whose signature does not verify against the profile's `UPDATE_PUBLIC_KEY`, and refuses to run at all when the profile has a feed URL but no key. It picks the artifact whose `kind`, `os` and `arch` match the running program, downloads it to a temporary file next to the target, checks size and sha256, and only then swaps it in.

`arch` in the feed is Go's (`amd64`, `arm64`); file names use each format's own spelling as in the table.
