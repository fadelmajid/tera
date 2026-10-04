#!/usr/bin/env bash
# Rebuild Tera from source on this Zo computer: front end first, then the
# binary that embeds it (Makefile `release`, with the local Go toolchain).
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# The distro Go is 1.19; go.mod needs 1.25+.
export PATH="/opt/go1.27/bin:$PATH"
export CGO_ENABLED=0

cd "$REPO/web"
npm ci --no-audit --no-fund
npm run build
touch dist/.gitkeep

cd "$REPO"
go build -trimpath \
  -ldflags "-s -w -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" \
  -o bin/tera ./cmd/tera
