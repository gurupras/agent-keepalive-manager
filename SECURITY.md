# Security policy

## Reporting a vulnerability

Please report security problems privately through GitHub: **Security › Report a vulnerability** on [the repository](https://github.com/gurupras/agent-keepalive-manager/security/advisories/new). Do not open a public issue.

Include what you found, how to reproduce it, and the version (`agent-keepalive version`). You should get an answer within a week. Fixes are released as soon as they are ready and credited in the release notes unless you prefer otherwise.

## Supported versions

Only the latest release gets security fixes. Every install kind can update to it: the package repositories and Homebrew through the package manager, everything else through the built-in updater.

## What is in scope

- The local server: it must only be reachable from this machine (`127.0.0.1`, Host checks against DNS rebinding, same-origin checks on state-changing requests and the WebSocket). See [docs/architecture.md](docs/architecture.md#security).
- Anything that lets another user or a web page make agent-keepalive send text to a session.
- The update path: the signed feed (`latest.json`, Ed25519), artifact hashes, and the swap of the binary.
- The package repositories' signatures and the repository registration done by the Linux packages.
- The hub: report validation, rate limiting, and the dashboard token.
- Usage reports containing anything beyond what [docs/metrics.md](docs/metrics.md) lists.

## Verifying downloads

Each release folder has `SHA256SUMS`, and every artifact on GitHub has a build provenance attestation:

```sh
gh attestation verify agent-keepalive_1.2.0_linux_amd64.tar.gz --repo gurupras/agent-keepalive-manager
```

The repository signing key's fingerprint is published at `https://agent-keepalive.gurupras.me/download/repo/FINGERPRINT` and committed in `packaging/repository/keys/`.
