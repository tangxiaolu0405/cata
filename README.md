# Cata — 终端原生 AI Agent

Go 编写的终端个人 AI 助手。单二进制，Unix socket 架构，后台自主演进记忆。

## 安装

从 [GitHub Releases](https://github.com/tangxiaolu0405/cata/releases) 下载对应平台包，或使用根目录安装脚本（自动下载、解压、配置 PATH；首次安装会执行 `cata init`）。每个平台包同时包含 `cata` 与 `cata-gateway`。

**Linux（x86_64）**

```bash
curl -fsSL https://raw.githubusercontent.com/tangxiaolu0405/cata/main/install_cata_linux.sh | bash
```

**macOS（Apple Silicon / Intel）**

```bash
curl -fsSL https://raw.githubusercontent.com/tangxiaolu0405/cata/main/install_cata_macos.sh | bash
```

**Windows（PowerShell）**

```powershell
irm https://raw.githubusercontent.com/tangxiaolu0405/cata/main/install_cata_windows.ps1 | iex
```

安装脚本默认安装到：

| 平台 | 默认路径 |
|------|----------|
| Linux / macOS | `~/.local/bin/cata` |
| Windows | `%LOCALAPPDATA%\cata\bin\cata.exe` |

可选环境变量：

| 变量 | 说明 |
|------|------|
| `CATA_VERSION` | 指定版本，如 `v0.1.9`；默认 latest |
| `CATA_INSTALL_DIR` | 自定义安装目录 |
| `CATA_REPO` | 自定义仓库，默认 `tangxiaolu0405/cata` |

示例：安装指定版本

```bash
CATA_VERSION=v0.1.9 ./install_cata_linux.sh
```

```powershell
$env:CATA_VERSION = "v0.1.9"; .\install_cata_windows.ps1
```

安装完成后**新开终端**，确认 `cata` 在 PATH 中，再配置 `~/.cata/config.json` 中的 LLM API Key。

## Gateway 独立部署

`cata-gateway` 自带 Web 控制台（多项目对话、渠道面板）。需要把它单独跑在一台机器上时，用仓库根目录的
`install_gateway.sh`：它从 GitHub Releases 下载二进制、写 `~/.cata/gateway.json`、再启动（**编译已在
CI 完成，脚本不本地编译**）。

**Linux / macOS（一键，从远端拉脚本执行）**

```bash
curl -fsSL https://raw.githubusercontent.com/tangxiaolu0405/cata/main/install_gateway.sh | \
  GATEWAY_UI_PASSWORD=你的口令 bash
```

**下载到本地再执行**（便于审查 / 离线）

```bash
curl -fsSL https://raw.githubusercontent.com/tangxiaolu0405/cata/main/install_gateway.sh -o install_gateway.sh
chmod +x install_gateway.sh
GATEWAY_UI_PASSWORD=你的口令 ./install_gateway.sh
```

启动方式与环境变量：

| 参数 / 变量 | 说明 |
|------|------|
| `GATEWAY_UI_PASSWORD` | 控制台访问口令；**设了才启用登录页**，否则仅本机/局域网可访问 |
| `GATEWAY_UI_LISTEN` | 监听地址，默认 `0.0.0.0:8787` |
| `GATEWAY_EDITION` | 默认 `channel`（仅 gateway）；`base` 会额外拉起本机 cata server |
| `INSTALL_DIR` | 二进制安装目录，默认 `~/.local/bin` |
| `GATEWAY_REPO` | 自定义仓库，默认 `tangxiaolu0405/cata` |
| `--version=v1.2.3` | 指定 release tag，默认 latest |
| `--run=systemd` | 生成并启用 systemd unit（需 root）；默认 nohup 后台 + pidfile |

登录安全：设了口令后，UI 所有请求需登录会话 cookie；**连续 5 次登录失败封该 IP 10 分钟**
（阈值/时长可改 `gateway.json` 的 `login_ban_max_attempts` / `login_ban_duration_seconds`）。

示例：指定版本 + systemd 托管

```bash
GATEWAY_UI_PASSWORD=你的口令 ./install_gateway.sh --version=v1.2.3 --run=systemd
```

## 更新

已安装带 `update` 子命令的版本后，可直接：

```bash
cata version          # 查看当前版本
cata update --check   # 仅检查是否有新版
cata update           # 从 GitHub Releases 下载并替换本机 cata + cata-gateway
```

`cata update` 会替换**当前可执行文件所在目录**下的二进制（与安装脚本默认路径一致时即为 `~/.local/bin` 或 Windows `%LOCALAPPDATA%\cata\bin`）。可用 `CATA_REPO` 覆盖仓库，`GITHUB_TOKEN` 降低 GitHub API 限流。

若当前还是**不含** `cata update` 的旧版，需先再跑一次上方安装脚本拿到新二进制，之后即可用 `cata update` 升级。

## 快速开始

```bash
# 已安装二进制时
cata init         # 布局 ~/.cata（不写 config.json）
cata initconfig   # 首次种子 config.json（合并写回；保留 llm_xxx 等未知顶层键）
cata chat         # Bubble Tea TUI（默认命令）
```

从源码构建：

```bash
go build -o cata ./cmd/cata
go build -o cata-gateway ./cmd/cata-gateway
# 桌宠（可选）：先构建前端再编译（必须带 Wails tags）
(cd cmd/cata-pet/frontend && npm install && npm run build)
# macOS:
CGO_ENABLED=1 CGO_LDFLAGS="-framework UniformTypeIdentifiers" \
  go build -tags desktop,production -o cata-pet ./cmd/cata-pet
# 或一键：./scripts/build-pet.sh
./cata init
./cata initconfig
./cata chat
# 或：./cata-pet   # 透明置顶桌宠；需 PATH 上有 cata（或 CATA_BIN）
```

## 桌宠（`cata-pet`，可选）

跨平台桌面伴侣（Go + Wails + React）：勾线猫猫浮窗、默认置顶、透明区鼠标穿透；**单击猫猫**展开后可用**文字或语音**发消息，协议与 `cata chat` 相同（`~/.cata/cata.sock`）。

- 不替换 TUI；不跑 pet 时行为不变
- 托盘/面板内可关「保持最前」
- 设置：`~/.cata/pet.json`（cwd、always_on_top）
- 语音：展开后面板点 🎤，Web Speech 转文字后走同一 `Send`；中间结果会填入输入框。macOS 需麦克风/语音识别权限（`build/darwin/Info.plist`）；裸二进制权限不稳时用 `wails build` 打成 `.app`
- **构建**：必须 `-tags desktop,production`；macOS 另加 `CGO_LDFLAGS="-framework UniformTypeIdentifiers"`。推荐 `./scripts/build-pet.sh`
- 代码：`cmd/cata-pet/`（`main.go` + `pet/` 后端 + `frontend/`），不再使用 `internal/cata/pet`
- 开发：`cd cmd/cata-pet && wails dev`

## Gateway（本地 UI + Telegram / QQ）

> 部署模式详见 **`docs/gateway.md`**。**当前仅实现模式一（同机）**；模式二（云端 gateway + 内网 worker）、模式三（全云端）保留设计，待模式一完成后再扩展。

`cata-gateway` 内置 Web 控制台（默认 `http://0.0.0.0:8787`：多项目真实目录对话 + 渠道只读面板；手机用本机局域网 IP），并把 Telegram / QQ 接到本机 **cata worker**（Unix socket），**不**重复实现 LLM/工具/脑子逻辑。无渠道凭证时可 **UI-only**；`CATA_GATEWAY_UI=0` 或 `ui_listen: "off"` 关闭页面。

### 产出区（worker 目录）

每个渠道会话独立目录，gateway 发给 cata 的 `cwd` 为：

```
~/.cata_worker/<channel>/<chat_id>/
```

例如 Telegram chat `12345` → `~/.cata_worker/telegram/12345/`。脑子绑定、工具、evolve 规则与终端 chat 相同，仅产出区按会话隔离。

### 发行档位（edition）

`~/.cata/gateway.json` 中 `edition` 决定 gateway 是否自带本机 cata server（同一二进制）：

- **base**（基础版）：`cata_server.auto_start: true`，`cata-gateway` 启动时自动 `cata run`
- **channel**（渠道版，默认）：不拉起 server；需另开 `cata run`

### 运行（模式一）

**base 版（单进程）** — 初始化配置并填入 token：

```bash
go build -o cata-gateway ./cmd/cata-gateway
cata-gateway init              # 默认 edition=base
# 或 channel 版: cata-gateway init --edition channel
# 编辑 ~/.cata/gateway.json：ui_listen / projects / 渠道凭证
# 也可用 docs/gateway-config.html 生成配置
cata-gateway
# 浏览器打开 http://127.0.0.1:8787 或手机打开 http://<电脑局域网IP>:8787 添加项目并对话
```

可同时启用 Telegram + QQ（凭证都有则并发跑）。QQ 为 **WebSocket 试验**；连不上则仅该渠道失败，不影响 TG。调试：`cata-gateway qq`。

**channel 版** — 两进程：

```bash
go build -o cata-gateway ./cmd/cata-gateway

export TELEGRAM_BOT_TOKEN="your-bot-token"
# 可选 QQ
# export QQ_APP_ID=... QQ_APP_SECRET=...
export CATA_WORKER_ROOT="$HOME/.cata_worker"
export CATA_SOCKET="$HOME/.cata/cata.sock"
export TELEGRAM_ALLOWED_USERS="123456789"

cata agent --workspace <ws_id>   # 每个工作空间一个 agent（默认由 cata chat 自动拉起）
cata-gateway                     # 终端 2
```

或设置 `CATA_GATEWAY_EDITION=channel` / `edition: channel`（见 `gateway.example.channel.json`）。

Telegram：`/start` `/help` `/clear`（危险命令按钮确认）。QQ：`/help` `/clear`（确认回复 yes/no）。

## 架构

```
cata chat ──Unix Socket──▶ cata agent (per-ws, ~/.cata/sockets/<ws_id>.sock) ──HTTP──▶ LLM
cata-pet ──▶ legacy cata run（仅桌宠/scheduler 内部支撑）──┘
Telegram 等 ──▶ cata-gateway ──▶ 在线/本机 agent
                                │
                                ├── ~/.cata/global/          引导型提示词
                                ├── ~/.cata/brain/ws/<id>/   运行时记忆
                                ├── <focus_path>/.cata/      项目主要内容（persona、modes、skills）
                                └── evolve engine（默认 600s）
```

**双根目录**：
- **`~/.cata/`** — 引导（`global/constraints|behavior|delegate-guide`）、记忆（`brain/workspaces/<id>/memory`）、config、socket
- **`focus_path/.cata/`** — 项目主要内容（`persona.local`、`modes/<mode>/*`、`skills/*`）；可随 git 提交（按需 `.gitignore`）

`focus_path` 由产出区向上解析（git 根 / `.cata/workspace.yaml` / cwd），决定绑定哪一格脑子，**不等于**产出区。

## 配置

`~/.cata/config.json`：

```json
{
  "llm": {
    "provider": "deepseek",
    "model": "deepseek-v4-flash",
    "models": {
      "chat": "deepseek-v4-pro",
      "evolution": "deepseek-v4-flash",
      "worker": "deepseek-v4-flash"
    },
    "api_key": "sk-..."
  },
  "exec": { "enabled": true },
  "evolution": { "enabled": true, "cycle_interval": 600 }
}
```

只配置 `llm.model` 时，所有 role（chat / evolution / worker）共用同一模型。`models` 可按 role 覆盖。

## 目录

```
internal/
  cata/       # 核心 agent + TUI（server / client / brain / evolve / config / …）
  gateway/    # 渠道适配（Telegram / QQ）→ 连同一 cata worker
  llm/        # OpenAI 兼容 LLM
  mcp/        # MCP 客户端（如 browser）
```

| 位置 | 用途 |
|------|------|
| `cmd/cata/` | CLI 入口 (`chat`, `init`, `run`, `config`) |
| `cmd/cata-gateway/` | Gateway 入口 |
| `internal/cata/` | 核心：server、client（TUI）、brain、evolve、config、clock、execcmd |
| `internal/gateway/` | 渠道适配（Telegram / QQ WebSocket） |
| `internal/llm/` | OpenAI 兼容 LLM；出站前注入 boot + brain 节选 |
| `internal/mcp/` | MCP 工具对接 |
| `brain/` | 模板种子（`cata init` → `~/.cata/global/`） |

## 设计文档

- `agents.md` — 项目边界与 AI 约束（与代码对齐）
- `design.md` — 完整系统设计（双根存储、Context 组装、演进、TUI 协议）
- `brain/directory-plan.md` — 脑子 vs 产出区 vs 项目 `.cata`
- `internal/cata/brain/guidance/` — 引导层模板（constraints / behavior / delegate-guide）→ `cata init` seed 到 `~/.cata/global/`
- `internal/llm/rolecards/` — 角色卡片（chat / worker / evolve 的身份 + 协议，编译期 embed）

## 依赖

仅需 Go 1.21+。无 Python、Node.js 依赖（MCP browser 可选，需 Node/npx）。

## License

MIT
