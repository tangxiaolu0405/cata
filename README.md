# Cata — 终端原生 AI Agent

Go 编写的终端个人 AI 助手。单二进制，Unix socket 架构，后台自主演进记忆。

**卫星组件**（不在本 README 范围内）：
- [`cata-gateway`](docs/gateway.md) — 渠道适配 / Web 控制台 / 远程隧道（独立部署见 [`install_gateway.sh`](docs/gateway.md#独立部署install_gatewaysh)）
- [`cata-pet`](cmd/cata-pet/README.md) — 桌面桌宠客户端

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
./cata init
./cata initconfig
./cata chat
```

> 卫星组件构建见各自文档：`go build -o cata-gateway ./cmd/cata-gateway`（见
> [docs/gateway.md](docs/gateway.md)）、桌宠见 [cmd/cata-pet/README.md](cmd/cata-pet/README.md)。

## 架构

```
cata chat ──Unix Socket──▶ cata agent (per-ws, ~/.cata/sockets/<ws_id>.sock) ──HTTP──▶ LLM
                                                      │
                                                      ├── ~/.cata/global/          引导型提示词
                                                      ├── ~/.cata/brain/ws/<id>/   运行时记忆
                                                      ├── <focus_path>/.cata/      项目主要内容（persona、modes、skills）
                                                      └── evolve engine（默认 600s）
```

**卫星组件**（外围，同一 socket / 隧道接入，见各自文档）：
- `cata-gateway`（Web 控制台 / Telegram / QQ / 远程隧道）→ 在线或本机 agent
- `cata-pet`（桌宠）→ 同一 `~/.cata/cata.sock`

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
| `cmd/cata/` | CLI 入口 (`chat`, `init`, `run`, `config`, `agent`, `supervisor`, `link`) |
| `cmd/cata-gateway/` | Gateway 入口（见 [docs/gateway.md](docs/gateway.md)） |
| `cmd/cata-pet/` | 桌宠客户端（见 [cmd/cata-pet/README.md](cmd/cata-pet/README.md)） |
| `internal/cata/` | 核心：server、client（TUI）、brain、evolve、config、clock、execcmd、link |
| `internal/gateway/` | 渠道适配（Telegram / QQ WebSocket）与隧道 |
| `internal/llm/` | OpenAI 兼容 LLM；出站前注入角色卡片 + brain 节选 |
| `internal/mcp/` | MCP 工具对接 |
| `brain/` | 模板种子（`cata init` → `~/.cata/global/`） |

## 设计文档

- `agents.md` — 项目级 AI 上下文（给 agent 阅读项目本身）：架构、双根存储、Context 组装、演进、多模态、TUI 协议与 AI 约束（合并自原 `agents.md` + `design.md`）
- `brain/directory-plan.md` — 脑子 vs 产出区 vs 项目 `.cata`
- `internal/cata/brain/guidance/` — 引导层模板（constraints / behavior / delegate-guide）→ `cata init` seed 到 `~/.cata/global/`
- `internal/llm/rolecards/` — 角色卡片（chat / worker / evolve 的身份 + 协议，编译期 embed）

## 依赖

仅需 Go 1.21+。无 Python、Node.js 依赖（MCP browser 可选，需 Node/npx）。

## License

MIT
