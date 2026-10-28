# Contributing

Thanks for helping. Bug reports, fixes and features are all welcome; for anything large, open an issue first so we can agree on the approach.

## Setting up

```sh
git clone https://github.com/gurupras/agent-keepalive-manager.git
cd agent-keepalive-manager
make ui build          # UIs first: the Go binaries embed them
bin/agent-keepalive serve
```

For UI work, `npm run dev` in `web/` (or `hub/web/`) runs Vite against a running server; `npm run mock` in `web/` runs the UI against mock data.

## Before you send a pull request

```sh
make test              # go test -race ./...
make lint              # gofmt, go vet, golangci-lint
(cd web && npm run lint && npm run build)
(cd hub/web && npm run lint && npm run build)
```

CI runs the same checks on Linux, macOS and Windows, plus shellcheck and actionlint for the scripts and workflows, a cross-compile of all six targets, and a packaging smoke test.

## Guidelines

- Read [docs/architecture.md](docs/architecture.md) first. The scheduler rules there are requirements, each with a test; a change that touches one needs a test that shows it still holds.
- Keep discovery read-only. agent-keepalive must never write to a session's terminal, read credential files, or log environment values.
- Anything that changes what a usage report contains must update [docs/metrics.md](docs/metrics.md) in the same change.
- Keep the build pure Go (`CGO_ENABLED=0`).
- Write commit messages that say what changed and why.

## Licence

By contributing you agree that your contribution is licensed under the MIT License, like the rest of the project.
