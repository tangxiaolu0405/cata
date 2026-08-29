# cata-pet — 桌宠客户端

跨平台桌面伴侣（Go + Wails + React）：勾线猫猫浮窗、默认置顶、透明区鼠标穿透；
**单击猫猫**展开后可用**文字或语音**发消息，协议与 `cata chat` 相同
（`~/.cata/cata.sock`，同一 Unix socket）。

- **不替换 TUI**；不跑 pet 时行为不变
- 托盘/面板内可关「保持最前」
- 设置：`~/.cata/pet.json`（`cwd`、`always_on_top`）
- 语音：展开后面板点 🎤，Web Speech 转文字后走同一 `Send`；中间结果会填入输入框。
  macOS 需麦克风/语音识别权限（`build/darwin/Info.plist`）；裸二进制权限不稳时用
  `wails build` 打成 `.app`

## 构建

必须带 Wails tags（桌面 + production）：

```bash
# 先构建前端再编译
(cd frontend && npm install && npm run build)
# macOS：
CGO_ENABLED=1 CGO_LDFLAGS="-framework UniformTypeIdentifiers" \
  go build -tags desktop,production -o cata-pet ./cmd/cata-pet
# 或一键：./scripts/build-pet.sh
```

运行：`./cata-pet`（需 PATH 上有 `cata`，或设置 `CATA_BIN`）。

## 开发

```bash
cd cmd/cata-pet && wails dev
```

## 代码结构

- `main.go` — Wails 应用入口
- `pet/` — 后端（socket 客户端、设置）
- `frontend/` — React UI（猫猫浮窗、输入、语音）
- `build/` — 平台打包配置（含 macOS `Info.plist` 权限声明）

不再使用 `internal/cata/pet`（历史遗留）。