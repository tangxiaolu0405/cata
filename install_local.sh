#!/usr/bin/env bash
# install_local.sh — 本地从源码编译 cata 全家桶并安装到 ~/.local/bin
#
# 用法:
#   ./install_local.sh                 # 编译 cata + cata-gateway + cata-desktop
#   ./install_local.sh --with-pet      # 额外编译桌宠 cata-pet（需 npm 前端 + CGO）
#   INSTALL_DIR=/path ./install_local.sh
#
# 环境变量:
#   INSTALL_DIR  默认 $HOME/.local/bin
#   CATA_BINARIES 默认 "cata cata-gateway cata-desktop"

set -euo pipefail

# 仓库根：脚本所在目录（本脚本位于仓库根）。
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="${INSTALL_DIR:-${HOME}/.local/bin}"
BINARIES="${CATA_BINARIES:-cata cata-gateway cata-desktop}"
WITH_PET=0
for arg in "$@"; do
  case "$arg" in
    --with-pet) WITH_PET=1 ;;
    *) echo "error: unknown arg: $arg" >&2; exit 2 ;;
  esac
done

log() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

need_cmd() { command -v "$1" >/dev/null 2>&1 || die "missing command: $1"; }

need_cmd go

mkdir -p "$INSTALL_DIR"

build_one() {
  local pkg="$1" out="$2"
  local tmp
  tmp="$(mktemp -d)/bin"
  log "build: $pkg"
  (cd "$REPO_ROOT" && go build -o "$tmp" "$pkg")
  install -m 0755 "$tmp" "$INSTALL_DIR/$out"
  # macOS：install 覆盖后偶发被判定 Code Signature Invalid 而启动即 SIGKILL，
  # 重新 ad-hoc 签名（其它平台无 codesign，自动跳过）。
  if command -v codesign >/dev/null 2>&1 && [ "$(uname -s)" = "Darwin" ]; then
    codesign --force --sign - "$INSTALL_DIR/$out" >/dev/null 2>&1 || true
  fi
  rm -rf "$(dirname "$tmp")"
  log "installed: $INSTALL_DIR/$out"
}

for name in $BINARIES; do
  case "$name" in
    cata)           build_one ./cmd/cata "$name" ;;
    cata-gateway)   build_one ./cmd/cata-gateway "$name" ;;
    cata-desktop)   build_one ./cmd/cata-desktop "$name" ;;
    *) die "unknown binary: $name (set CATA_BINARIES explicitly)" ;;
  esac
done

if [ "$WITH_PET" -eq 1 ]; then
  need_cmd npm
  log "build: cata-pet frontend"
  (cd "$REPO_ROOT/cmd/cata-pet/frontend" && npm install && npm run build)
  CGO_ENABLED=1 CGO_LDFLAGS="-framework UniformTypeIdentifiers" \
    go build -tags desktop,production -o "$INSTALL_DIR/cata-pet" ./cmd/cata-pet
  if command -v codesign >/dev/null 2>&1 && [ "$(uname -s)" = "Darwin" ]; then
    codesign --force --sign - "$INSTALL_DIR/cata-pet" >/dev/null 2>&1 || true
  fi
  log "installed: $INSTALL_DIR/cata-pet"
fi

log "done — try: cata chat   (desktop: cata-desktop; gateway: cata-gateway)"
