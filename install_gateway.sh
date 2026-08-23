#!/usr/bin/env bash
# install_gateway.sh — 单独部署 cata-gateway（二进制由 GitHub Actions 构建，本脚本只负责下载/配置/启动）。
#
# 用法:
#   ./install_gateway.sh                         # 下载最新 release，安装 cata-gateway，写 gateway.json，nohup 后台启动
#   GATEWAY_UI_PASSWORD=xxx ./install_gateway.sh # 非交互：直接指定控制台口令
#   ./install_gateway.sh --run=systemd           # 改为生成并安装 systemd unit（需 root）
#   ./install_gateway.sh --version v1.2.3        # 指定 release tag（默认 latest）
#
# 环境变量:
#   INSTALL_DIR         默认 $HOME/.local/bin
#   GATEWAY_UI_PASSWORD  控制台访问口令（ui_password）；空则不启用登录页（仍 LAN-only）
#   GATEWAY_UI_LISTEN    默认 0.0.0.0:8787
#   GATEWAY_REPO        默认 tangxiaolu0405/cata
#   GATEWAY_EDITION     默认 channel（仅 gateway；base 会额外拉起本机 cata server）

set -euo pipefail

REPO="${GATEWAY_REPO:-tangxiaolu0405/cata}"
INSTALL_DIR="${INSTALL_DIR:-${HOME}/.local/bin}"
UI_LISTEN="${GATEWAY_UI_LISTEN:-0.0.0.0:8787}"
EDITION="${GATEWAY_EDITION:-channel}"
VERSION="latest"
RUN_MODE="nohup"

for arg in "$@"; do
  case "$arg" in
    --run=systemd) RUN_MODE="systemd" ;;
    --version=*)   VERSION="${arg#--version=}" ;;
    --help|-h)     sed -n '2,18p' "$0"; exit 0 ;;
    *) echo "error: unknown arg: $arg" >&2; exit 2 ;;
  esac
done

log() { printf '==> %s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
need_cmd() { command -v "$1" >/dev/null 2>&1 || die "missing command: $1"; }

need_cmd curl
need_cmd tar
need_cmd go || true   # go 仅用于本地构建 push 提示，部署本身不需要

# ---- 平台探测（与 CI artifact 命名对齐）----
OS="$(uname -s)"; ARCH="$(uname -m)"
case "$OS" in
  Linux)  GOOS=linux ;;
  Darwin) GOOS=darwin ;;
  *) die "unsupported OS: $OS (仅支持 Linux / macOS)" ;;
esac
case "$ARCH" in
  x86_64|amd64) GOARCH=amd64 ;;
  arm64|aarch64) GOARCH=arm64 ;;
  *) die "unsupported arch: $ARCH" ;;
esac
ARTIFACT="cata-${GOOS}-${GOARCH}"
log "platform: ${ARTIFACT}"

# ---- 解析 release tag / 资产下载 URL ----
resolve_asset_url() {
  if [ "$VERSION" = "latest" ]; then
    local api="https://api.github.com/repos/${REPO}/releases/latest"
    VERSION="$(curl -fsSL "$api" | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')"
    [ -n "$VERSION" ] || die "无法解析最新 release tag（检查仓库 ${REPO} 是否已有 release）"
  fi
  local dl="https://github.com/${REPO}/releases/download/${VERSION}/${ARTIFACT}.tar.gz"
  echo "$dl"
}

ASSET_URL="$(resolve_asset_url)"
log "release: ${VERSION}"
log "asset:  ${ASSET_URL}"

# ---- 下载 + 解包（只取 cata-gateway）----
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
log "download cata-gateway binary"
curl -fL "$ASSET_URL" -o "$TMP/archive.tar.gz" || die "下载失败：$ASSET_URL"
tar -xzf "$TMP/archive.tar.gz" -C "$TMP" cata-gateway || die "解包失败（归档中未找到 cata-gateway）"

mkdir -p "$INSTALL_DIR"
install -m 0755 "$TMP/cata-gateway" "$INSTALL_DIR/cata-gateway"
if command -v codesign >/dev/null 2>&1 && [ "$OS" = "Darwin" ]; then
  codesign --force --sign - "$INSTALL_DIR/cata-gateway" >/dev/null 2>&1 || true
fi
log "installed: $INSTALL_DIR/cata-gateway"

# ---- 写 gateway.json（仅在不存在或用户确认时）----
CONF_DIR="$HOME/.cata"
mkdir -p "$CONF_DIR"
CONF="$CONF_DIR/gateway.json"

if [ -z "${GATEWAY_UI_PASSWORD:-}" ]; then
  printf '控制台访问口令 (ui_password，留空则仅 LAN 可访问): '
  IFS= read -r -s GATEWAY_UI_PASSWORD; echo
fi

if [ ! -f "$CONF" ]; then
  cat > "$CONF" <<EOF
{
  "edition": "${EDITION}",
  "ui_listen": "${UI_LISTEN}",
  "ui_password": "${GATEWAY_UI_PASSWORD}",
  "login_ban_max_attempts": 5,
  "login_ban_duration_seconds": 600,
  "projects": []
}
EOF
  chmod 600 "$CONF"
  log "wrote ${CONF}"
else
  log "已存在 ${CONF}（未覆盖）；如需启用登录页请确保含 ui_password / login_ban_* 字段"
fi

# ---- 启动 ----
if [ "$RUN_MODE" = "systemd" ]; then
  UNIT="/etc/systemd/system/cata-gateway.service"
  [ "$(id -u)" -eq 0 ] || die "安装 systemd unit 需要 root"
  cat > "$UNIT" <<EOF
[Unit]
Description=Cata Gateway (standalone)
After=network.target

[Service]
ExecStart=$INSTALL_DIR/cata-gateway
Restart=on-failure
User=$USER
Environment=CATA_GATEWAY_UI=${UI_LISTEN}

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable --now cata-gateway
  log "started via systemd: $UNIT"
else
  PIDFILE="$CONF_DIR/cata-gateway.pid"
  nohup "$INSTALL_DIR/cata-gateway" > "$CONF_DIR/cata-gateway.log" 2>&1 &
  echo $! > "$PIDFILE"
  log "started (nohup): pid=$(cat "$PIDFILE")"
  log "log: $CONF_DIR/cata-gateway.log"
fi

log "done — UI: http://${UI_LISTEN}"
