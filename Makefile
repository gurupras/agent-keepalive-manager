# agent-keepalive build entry points.
#
#   make build                 local binary in bin/agent-keepalive (dev build: no profile)
#   PROFILE=profiles/public.env make dist
#                              every release artifact this machine can make, in dist/
#
# PROFILE selects the distribution profile (see scripts/profile.sh); without
# it a build contacts nothing. VERSION overrides scripts/version.sh.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c

export PROFILE
export VERSION
GOOS   ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
EXE    := $(if $(filter windows,$(GOOS)),.exe,)
UNAME_S := $(shell uname -s)

.PHONY: all web hub-web ui build hub test lint fmt-check dist dist-linux linux-packages \
        deb rpm arch appimage windows macos-pkg archives hub-archives checksums cross clean help

all: build

help:
	@sed -n 's/^#   //p' Makefile

## UIs (embedded into the binaries; build them before the Go code)
web:
	cd web && npm ci && npm run build

hub-web:
	cd hub/web && npm ci && npm run build

ui: web hub-web

## Go
build:
	scripts/build-go.sh $(GOOS) $(GOARCH) dev bin/agent-keepalive$(EXE)

hub:
	scripts/build-go.sh --hub $(GOOS) $(GOARCH) archive bin/agent-keepalive-hub$(EXE)

test:
	go test -race ./...

fmt-check:
	@out="$$(gofmt -l $$(git ls-files '*.go' 2>/dev/null || find . -name '*.go' -not -path './web/*'))"; \
	  if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

lint: fmt-check
	go vet ./...
	golangci-lint run ./...

## Every target the release builds, as plain archive-kind binaries (a compile check).
cross:
	for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do \
	  scripts/build-go.sh "$${t%/*}" "$${t#*/}" archive; done

## Packages (dist/, names as in docs/release-artifacts.md)
deb:  ; packaging/linux/build-packages.sh deb
rpm:  ; packaging/linux/build-packages.sh rpm
arch: ; packaging/linux/build-packages.sh arch
linux-packages: ; packaging/linux/build-packages.sh deb rpm arch
appimage: ; packaging/appimage/build-appimage.sh
windows: ; packaging/windows/build-windows.sh setup portable
macos-pkg: ; packaging/macos/build-pkg.sh
archives: ; packaging/archives.sh
hub-archives: ; packaging/archives.sh --hub
checksums: ; packaging/checksums.sh

# What a Linux machine can make: everything but the macOS .pkg.
dist-linux: linux-packages appimage windows archives hub-archives

ifeq ($(UNAME_S),Darwin)
dist: macos-pkg archives checksums
else
dist: dist-linux
	$(MAKE) checksums
endif

clean:
	rm -rf bin dist
