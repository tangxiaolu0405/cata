# Cata — 终端原生 AI Agent

Go 编写的终端个人 AI 助手。单二进制，Unix socket 架构，后台自主演进记忆。

## 快速开始

```bash
# 构建
go build -o cata ./cmd/cata

# 初始化 CATA_HOME（~/.cata）
./cata init

# 开始对话（Bubble Tea TUI）
./cata
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
