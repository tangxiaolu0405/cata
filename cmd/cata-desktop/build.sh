#!/bin/sh
# 编译并安装 cata-desktop 到 ~/.local/bin
# 纯 Go + Fyne：一条 go build 即可，无 npm、无 build tags、无 WebView
set -e
cd "$(dirname "$0")/../.." # 仓库根
OUT="${OUT:-/Users/lucas/.local/bin/cata-desktop}"

echo "==> go build ./cmd/cata-desktop"
go build -o /tmp/cata-desktop.new ./cmd/cata-desktop

echo "==> install -> $OUT"
cp /tmp/cata-desktop.new "$OUT"
# macOS：cp 后的二进制偶发被判定 Code Signature Invalid 而启动即被杀(SIGKILL)，
# 这里重新做 ad-hoc 签名（其它平台无 codesign，自动跳过）。
if command -v codesign >/dev/null 2>&1; then
  codesign --force --sign - "$OUT" >/dev/null 2>&1 || true
fi
rm -f /tmp/cata-desktop.new
echo "done: $OUT"
