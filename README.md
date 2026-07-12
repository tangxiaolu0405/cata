# Cata — 终端原生 AI Agent

Go 编写的终端个人 AI 助手。单二进制，Unix socket 架构，后台自主演进记忆。

## 安装

从 [GitHub Releases](https://github.com/tangxiaolu0405/cata/releases) 下载对应平台包，或使用根目录安装脚本（自动下载、解压、配置 PATH；首次安装会执行 `cata init`）。

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

## 快速开始

```bash
# 已安装二进制时
cata init    # 若安装脚本未自动执行
cata chat    # Bubble Tea TUI（默认命令）
```

从源码构建：

```bash
go build -o cata ./cmd/cata
./cata init
./cata chat
```

## 架构

```
cata chat [--dir <产出区>] ──Unix Socket──▶ cata run (server) ──HTTP──▶ LLM
                                │
                                ├── ~/.cata/global/          引导型提示词
                                ├── ~/.cata/brain/ws/<id>/   运行时记忆
                                ├── <focus_path>/.cata/      项目主要内容（persona、modes、skills）
                                └── evolve engine（默认 600s）
```

**双根目录**：
- **`~/.cata/`** — 引导（`global/constraints|behavior|boot-assembler`）、记忆（`brain/workspaces/<id>/memory`）、config、socket
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

| 位置 | 用途 |
|------|------|
| `cmd/cata/` | CLI 入口 (`chat`, `init`, `run`, `config`) |
| `internal/server/` | Unix socket 服务端，聊天循环，工具执行 |
| `internal/client/` | Bubble Tea TUI，NDJSON 事件流 |
| `internal/llm/` | OpenAI 兼容 LLM；出站前注入 boot + brain 节选 |
| `internal/brain/` | 双根路径、工作区解析、上下文组装 |
| `internal/evolve/` | 后台自主演进（项目内容 + home 记忆） |
| `internal/config/` | 配置加载与校验 |
| `brain/` | 模板种子（`cata init` → `~/.cata/global/`） |

## 设计文档

- `agents.md` — 项目边界与 AI 约束（与代码对齐）
- `design.md` — 完整系统设计（双根存储、Context 组装、演进、TUI 协议）
- `brain/directory-plan.md` — 脑子 vs 产出区 vs 项目 `.cata`
- `brain/constraints.md` — 约束种子 → `~/.cata/global/constraints.md`
- `brain/behavior.md` — 行为 SOP 种子 → `~/.cata/global/behavior.md`
- `brain/boot-assembler.md` — 运行时引导种子 → `~/.cata/global/boot-assembler.md`

## 依赖

仅需 Go 1.21+。无 Python、Node.js 依赖（MCP browser 可选，需 Node/npx）。

## License

MIT
