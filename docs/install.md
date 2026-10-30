# Installing agent-keepalive

Downloads: <https://agent-keepalive.gurupras.me/download/> (one folder per version, with `SHA256SUMS`) and [GitHub Releases](https://github.com/gurupras/agent-keepalive-manager/releases). `V` below is the version.

How each install updates:

| Install | Updates |
|---|---|
| apt, dnf, pacman, Homebrew | through the package manager; the UI shows the command, then offers to restart a running server into the new version |
| AppImage, Windows setup, Windows portable, archive | the UI's **Update** button (signed feed, verified download, restart) |
| macOS `.pkg` | **Update** downloads and verifies the new `.pkg` and opens Installer |

## Debian, Ubuntu (apt)

```sh
curl -fsSL https://agent-keepalive.gurupras.me/download/repo/agent-keepalive-archive-keyring.gpg \
  | sudo tee /usr/share/keyrings/agent-keepalive-archive-keyring.gpg >/dev/null
curl -fsSL https://agent-keepalive.gurupras.me/download/repo/agent-keepalive.sources \
  | sudo tee /etc/apt/sources.list.d/agent-keepalive.sources >/dev/null
sudo apt update && sudo apt install agent-keepalive
```

Or download `agent-keepalive_V_amd64.deb` (or `arm64`) and `sudo apt install ./agent-keepalive_V_amd64.deb`; the package registers the repository itself.

## Fedora, RHEL, openSUSE (dnf, zypper)

```sh
sudo curl -fsSL -o /etc/yum.repos.d/agent-keepalive.repo \
  https://agent-keepalive.gurupras.me/download/repo/agent-keepalive.repo
sudo dnf install agent-keepalive
```

dnf asks once to import the repository key; compare it with the fingerprint at `…/download/repo/FINGERPRINT`. On openSUSE put the same file in `/etc/zypp/repos.d/` and use `zypper install agent-keepalive`. Or install `agent-keepalive-V-1.x86_64.rpm` (or `aarch64`) directly.

## Arch Linux

```sh
curl -fsSL -o /tmp/agent-keepalive.asc \
  https://agent-keepalive.gurupras.me/download/repo/agent-keepalive-archive-keyring.asc
sudo pacman-key --add /tmp/agent-keepalive.asc
sudo pacman-key --lsign-key "$(curl -fsSL https://agent-keepalive.gurupras.me/download/repo/FINGERPRINT)"
curl -fsSL https://agent-keepalive.gurupras.me/download/repo/pacman.conf | sudo tee -a /etc/pacman.conf >/dev/null
sudo pacman -Sy agent-keepalive
```

Or `sudo pacman -U agent-keepalive-V-1-x86_64.pkg.tar.zst`, which registers the repository itself.

### What the Linux packages install

- `/usr/bin/agent-keepalive`; with no command it opens the desktop window, or runs `serve` when there is no display (an ssh login, a server)
- `/usr/lib/agent-keepalive/agent-keepalive-desktop`, the window. It uses WebKitGTK 4.1 and GTK 3, which the deb and rpm packages recommend and the Arch package requires. Leave them out (`apt-get install --no-install-recommends`, `dnf install --setopt=install_weak_deps=False`) for a server-only install; `agent-keepalive app` then opens your browser
- `/usr/share/applications/agent-keepalive.desktop` (runs `agent-keepalive app`) and the icon
- `/usr/lib/systemd/user/agent-keepalive.service`, **not enabled**. To start the server at login: `systemctl --user enable --now agent-keepalive`
- the repository entry and key (see [package-repositories.md](package-repositories.md))

## AppImage (any Linux)

```sh
chmod +x agent-keepalive-V-x86_64.AppImage
./agent-keepalive-V-x86_64.AppImage            # = agent-keepalive app: the window
./agent-keepalive-V-x86_64.AppImage open       # the UI in your browser instead
./agent-keepalive-V-x86_64.AppImage status     # other commands pass through
```

The window uses the system's WebKitGTK 4.1 (`libwebkit2gtk-4.1-0` on Debian and Ubuntu, `webkit2gtk4.1` on Fedora, `webkit2gtk-4.1` on Arch), which most desktops have; without it, the AppImage opens the UI in your browser.

Updates replace the AppImage file in place. On systems without FUSE 2, run it with `--appimage-extract-and-run`.

## Homebrew (macOS, Linux)

```sh
brew install gurupras/tap/agent-keepalive
brew services start agent-keepalive     # optional: run the server at login
```

The formula is the server and web UI, without the window: `agent-keepalive open` opens the UI in your browser. For the window on macOS, use the installer below.

## macOS installer

`agent-keepalive-V-darwin-arm64.pkg` (Apple silicon) or `-x64.pkg` (Intel) installs `/usr/local/bin/agent-keepalive` and `/Applications/agent-keepalive.app`, the window, which starts the server if it is not running. If the package is not notarized, open it with right-click › Open. When the server is running during an install or update, the package's post-install script stops it, starts the new version and brings up the app.

## Windows

- **Setup** (`agent-keepalive-V-setup-x64.exe`, or `-arm64`): installs for the current user to `%LOCALAPPDATA%\Programs\agent-keepalive`, without administrator rights, with a Start-menu shortcut that opens the window and an optional "Start at login" (the server only, in the background). Silent install: `setup.exe /S`. Uninstall from Settings › Apps, or `%LOCALAPPDATA%\Programs\agent-keepalive\uninstall.exe /S`. Your data stays after uninstalling.
- **Portable** (`agent-keepalive-V-portable-x64.exe`): a single file; double-click to open the window. It keeps its data next to itself in `agent-keepalive-data\`, including the window, which it writes out there on first start.

The window needs the Microsoft Edge WebView2 Runtime, which comes with Windows 11 and current Windows 10. Without it, agent-keepalive opens the UI in your browser.

If SmartScreen warns about an unsigned file, choose More info › Run anyway.

## Archives

`agent-keepalive_V_<os>_<arch>.tar.gz` (`.zip` on Windows) contains the binary, `LICENSE` and `README.md`. Put the binary on your `PATH`. Archive installs update themselves.

## Verifying a download

```sh
sha256sum -c --ignore-missing SHA256SUMS
gh attestation verify <file> --repo gurupras/agent-keepalive-manager
```
