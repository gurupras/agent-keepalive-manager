---
layout: home
title: agent-keepalive
titleTemplate: Keep your coding agents' prompt caches warm

hero:
  name: agent-keepalive
  text: Come back to a warm cache.
  tagline: Keeps the prompt caches of your Claude Code and Codex CLI sessions warm while you step away, so returning to a long conversation reads the cache instead of paying to write it again.
  image:
    src: /favicon.svg
    alt: agent-keepalive
  actions:
    - theme: brand
      text: Get started
      link: /getting-started
    - theme: alt
      text: Install
      link: /install
    - theme: alt
      text: View on GitHub
      link: https://github.com/gurupras/agent-keepalive-manager

features:
  - icon: 🔎
    title: Finds your sessions without touching them
    details: A local server discovers the Claude Code and Codex CLI sessions running on your machine. It only reads their session records and transcripts. It never types into a terminal.
  - icon: ⏱️
    title: Pings just in time
    details: For the sessions you pick, and for as long as you choose, it sends a short marked ping shortly before the cache would expire, and never while the session is working.
  - icon: 🧾
    title: Shows what it cost and saved
    details: A ledger books every idle period as saved (you came back to a warm cache) or wasted (you did not), using list prices you can override. Plans stop where pings would cost more than a re-cache.
  - icon: 📦
    title: One file, any OS
    details: Pure Go with the UI embedded. Linux, macOS and Windows on amd64 and arm64, from signed apt, dnf and pacman repositories, Homebrew, installers or a single executable.
  - icon: 🔄
    title: Stays up to date
    details: Your package manager updates it, or it updates itself (AppImage, Windows and macOS installers) from a signed feed.
  - icon: 🔒
    title: Private by default
    details: The server listens on 127.0.0.1 only. Usage reports are pseudonymous daily counts with no prompts, paths, names or session ids, and you can turn them off in one click.
---

<div class="screenshot">
  <img src="/screenshot.png" alt="The agent-keepalive UI: running sessions grouped by project, each with its cache countdown and keepalive plan">
</div>

<div class="home-section vp-doc">

## Install

::: code-group

```sh [Debian, Ubuntu]
curl -fsSL https://agent-keepalive.gurupras.me/download/repo/agent-keepalive-archive-keyring.gpg \
  | sudo tee /usr/share/keyrings/agent-keepalive-archive-keyring.gpg >/dev/null
curl -fsSL https://agent-keepalive.gurupras.me/download/repo/agent-keepalive.sources \
  | sudo tee /etc/apt/sources.list.d/agent-keepalive.sources >/dev/null
sudo apt update && sudo apt install agent-keepalive
```

```sh [Fedora, RHEL, openSUSE]
sudo curl -fsSL -o /etc/yum.repos.d/agent-keepalive.repo \
  https://agent-keepalive.gurupras.me/download/repo/agent-keepalive.repo
sudo dnf install agent-keepalive
```

```sh [Arch Linux]
curl -fsSL -o /tmp/agent-keepalive.asc \
  https://agent-keepalive.gurupras.me/download/repo/agent-keepalive-archive-keyring.asc
sudo pacman-key --add /tmp/agent-keepalive.asc
sudo pacman-key --lsign-key "$(curl -fsSL https://agent-keepalive.gurupras.me/download/repo/FINGERPRINT)"
curl -fsSL https://agent-keepalive.gurupras.me/download/repo/pacman.conf \
  | sudo tee -a /etc/pacman.conf >/dev/null
sudo pacman -Sy agent-keepalive
```

```sh [Homebrew]
brew install gurupras/tap/agent-keepalive
```

:::

On **Windows**, download `agent-keepalive-<version>-setup-x64.exe` (or `arm64`) from the [downloads](/download/). It installs for your user only, with no administrator rights. On **macOS**, the `.pkg` installer adds an app to Applications. On **any Linux**, the `.AppImage` runs without installing. [Every option, and how to verify a download](/install).

## Then

```sh
agent-keepalive open
```

This starts the server in the background if it is not running and opens the UI in your browser. Pick a session, choose how long to keep it warm, and walk away. [Getting started](/getting-started) explains what happens next and when keeping a cache warm is worth it.

</div>
