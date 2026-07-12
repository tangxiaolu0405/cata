#!/usr/bin/env bash
# install_cata_linux.sh — 下载 GitHub Release、解压并配置 PATH（Linux amd64）
#
# 用法:
#   curl -fsSL https://raw.githubusercontent.com/tangxiaolu0405/cata/main/install_cata_linux.sh | bash
#   CATA_VERSION=v0.1.9 ./install_cata_linux.sh
#
# 环境变量:
#   CATA_REPO        默认 tangxiaolu0405/cata
#   CATA_VERSION     默认 latest（GitHub 最新 release tag）
#   CATA_INSTALL_DIR 默认 $HOME/.local/bin

set -euo pipefail

REPO="${CATA_REPO:-tangxiaolu0405/cata}"
INSTALL_DIR="${CATA_INSTALL_DIR:-${HOME}/.local/bin}"
VERSION="${CATA_VERSION:-}"
BIN_NAME="cata"

log() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing command: $1"
}

resolve_version() {
  if [ -n "$VERSION" ]; then
    return
  fi
  need_cmd curl
  VERSION="$(
    curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
      | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
      | head -1
  )"
  [ -n "$VERSION" ] || die "failed to resolve latest release tag"
}

detect_artifact() {
  local arch
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) ARTIFACT="cata-linux-amd64" ;;
    *) die "unsupported linux arch: ${arch} (releases: linux-amd64 only)" ;;
  esac
  ARCHIVE="${ARTIFACT}.tar.gz"
}

download() {
  local url="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  log "version: ${VERSION}"
  log "artifact: ${ARCHIVE}"
  log "download: ${url}"

  curl -fL --retry 3 --retry-delay 2 -o "${tmp}/${ARCHIVE}" "$url"
  tar -xzf "${tmp}/${ARCHIVE}" -C "$tmp"
  [ -f "${tmp}/${BIN_NAME}" ] || die "archive missing ${BIN_NAME}"

  mkdir -p "$INSTALL_DIR"
  install -m 0755 "${tmp}/${BIN_NAME}" "${INSTALL_DIR}/${BIN_NAME}"
  log "installed: ${INSTALL_DIR}/${BIN_NAME}"
}

ensure_path() {
  local line export_line="export PATH=\"${INSTALL_DIR}:\$PATH\""
  local profiles=()

  if [ -n "${ZSH_VERSION:-}" ] || [ -f "${HOME}/.zshrc" ]; then
    profiles+=("${HOME}/.zshrc")
  fi
  if [ -f "${HOME}/.bashrc" ]; then
    profiles+=("${HOME}/.bashrc")
  fi
  profiles+=("${HOME}/.profile")

  for profile in "${profiles[@]}"; do
    [ -f "$profile" ] || continue
    if grep -Fq "$INSTALL_DIR" "$profile" 2>/dev/null; then
      log "PATH already configured in ${profile}"
      return
    fi
  done

  local target="${profiles[0]}"
  {
    echo ""
    echo "# cata installer"
    echo "$export_line"
  } >>"$target"
  log "added PATH to ${target}"
  log "run: source ${target}   (or open a new shell)"
}

maybe_init() {
  if [ ! -d "${HOME}/.cata" ]; then
    log "running cata init"
    "${INSTALL_DIR}/${BIN_NAME}" init
  fi
}

main() {
  need_cmd curl
  need_cmd tar
  need_cmd install
  resolve_version
  detect_artifact
  download
  ensure_path
  maybe_init
  log "done — try: cata chat"
}

main "$@"
