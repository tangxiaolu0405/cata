#!/usr/bin/env bash
# Build cata-pet (frontend + Go/Wails binary). Requires Node.js and CGO toolchain.
# Wails needs: -tags desktop,production
# macOS often also needs: -framework UniformTypeIdentifiers (UTType linker symbol)
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/cmd/cata-pet/frontend"
npm install
npm run build
cd "$ROOT"

TAGS="desktop,production"
export CGO_ENABLED=1
case "$(uname -s)" in
  Darwin)
    export CGO_LDFLAGS="${CGO_LDFLAGS:-} -framework UniformTypeIdentifiers"
    ;;
esac

go build -tags "$TAGS" -o cata-pet ./cmd/cata-pet
echo "built ./cata-pet"
