# packaging/

Scripts that turn the Go binaries into the release artifacts listed in [docs/release-artifacts.md](../docs/release-artifacts.md). Each writes to `dist/` (override with `DIST`) and keeps intermediate files under `bin/`.

| Script | Makes | Runs on |
|---|---|---|
| `linux/build-packages.sh [deb] [rpm] [arch]` | `.deb`, `.rpm`, `.pkg.tar.zst` (nfpm) | Linux |
| `appimage/build-appimage.sh` | `.AppImage` x86_64 + aarch64 | Linux x86_64 |
| `windows/build-windows.sh [setup] [portable]` | NSIS setup and portable `.exe`, x64 + arm64 | Linux (makensis, or Docker) |
| `../scripts/build-desktop.sh <goos> <goarch>` | the desktop window, `bin/desktop/<goos>_<goarch>/agent-keepalive-desktop[.exe]` | that OS (cargo; Linux: `libwebkit2gtk-4.1-dev`) |
| `macos/build-pkg.sh` | `.pkg` x64 + arm64 | macOS |
| `archives.sh [--hub]` | `.tar.gz` / `.zip` archives, hub archives | anywhere |
| `checksums.sh` | `SHA256SUMS` | anywhere |
| `homebrew/render-formula.sh`, `homebrew/publish-tap.sh` | the tap's `Formula/agent-keepalive.rb` | anywhere |
| `repository/publish.sh` | signed apt/rpm/arch repositories (see [docs/package-repositories.md](../docs/package-repositories.md)) | Linux with Docker |
| `upload-ssh.sh` | uploads to the download host; moves `latest.json` | anywhere with ssh |
| `windows/smoke-test.ps1` | runs the Windows artifacts | Windows |

The `Makefile` wraps them (`make deb`, `make appimage`, `make windows`, `make dist`, …).

## Common environment

| Variable | |
|---|---|
| `PROFILE` | distribution profile, e.g. `profiles/public.env` (none: a dev build that contacts nothing) |
| `VERSION` | overrides `scripts/version.sh` |
| `ARCHES` | Go architectures to package, default `amd64 arm64` |
| `REBUILD=1` | rebuild binaries even if `bin/<kind>/<os>_<arch>/` already has them |
| `REPO_PUBLIC_KEY` | the repository key the Linux packages ship (default `repository/keys/agent-keepalive.asc`) |
| `DESKTOP` | unset: every package but the archives carries the desktop window, and a missing one is an error. `optional`: package without the windows that are missing. `none`: no window anywhere |

## The desktop window

`desktop/` is a Tauri program built per target by `scripts/build-desktop.sh`. It links the platform's webview, so each OS builds its own (Linux natively per architecture, Windows and macOS both architectures); the packaging scripts look for it in `bin/desktop/<goos>_<goarch>/` (`lib.sh` `ensure_desktop`) and build it there when this machine can. The release workflow builds all six on their own runners and hands them to the packaging jobs. Where it goes: `/usr/lib/agent-keepalive/` in the Linux packages, `usr/bin/` in the AppImage, next to the exe in the Windows setup, built into the portable exe (`scripts/build-go.sh` with `DESKTOP_EMBED`), and as the executable of the macOS app bundle.

## Install kinds

Every artifact carries a binary linked with its own install kind (`scripts/build-go.sh <os> <arch> <kind>` sets `-X …/internal/distro.installKind`), which decides how the program updates. The Windows kinds are linked with `-H windowsgui` (no console window when started from the Start menu or a double-click). Homebrew installs the `archive` binary and writes `libexec/install-kind` = `brew`. Any other packager can do the same: an `install-kind` file next to the binary (or in `../libexec/`) holding one of `deb rpm arch brew appimage windows-setup windows-portable macos-pkg archive dev` overrides the linked kind.

## The Linux packages

`linux/nfpm.yaml` is rendered per format and architecture into `bin/stage/`. The packages install `/usr/bin/agent-keepalive`, the desktop window in `/usr/lib/agent-keepalive/` (with `nfpm-desktop.yaml`'s WebKitGTK dependencies), the desktop entry (`agent-keepalive app`), the icon, a systemd **user** unit that is not enabled, and the repository key. `postinstall.sh.in` registers the profile's `REPO_URL` repository (opt out: `AGENT_KEEPALIVE_INSTALL_NO_REPOSITORY=1` or `/etc/agent-keepalive/no-repository`), and `postremove.sh.in` removes what it registered on a real removal. Without a committed repository key, the packages register nothing.

## Signing

All optional and skipped with a message when not configured:

| | Variables |
|---|---|
| Windows (Authenticode, osslsigncode) | `WINDOWS_SIGN_PFX` or `WINDOWS_SIGN_PFX_BASE64`, `WINDOWS_SIGN_PASSWORD`, `WINDOWS_SIGN_TIMESTAMP_URL` |
| macOS | `MACOS_APP_IDENTITY`, `MACOS_INSTALLER_IDENTITY`, `MACOS_KEYCHAIN`, `MACOS_NOTARY_APPLE_ID`, `MACOS_NOTARY_TEAM_ID`, `MACOS_NOTARY_PASSWORD` |
| rpm packages, repositories | `REPO_SIGNING_KEY` (`repository/publish.sh sign dist`) |
| update feed | `UPDATE_SIGNING_KEY` (`go run ./tools/release feed`) |

## Icons

`icons/agent-keepalive.svg` is the source; the PNGs and the `.ico` are rendered from it (`inkscape -w 256 -h 256`, `convert … -define icon:auto-resize=256,128,64,48,32,16`). The macOS `.icns` is made at build time from the 512 px PNG.
