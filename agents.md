# Cata — 项目级 AI 上下文（给 agent 阅读项目本身）

本文档是**终端优先个人 Agent** 的目标、设计、实现边界与 AI 约束的唯一入口
（合并自原 `agents.md` 与 `design.md`），与当前 Go 代码对齐。

# 最重要的事情：严格遵守第一性原理
---

## 愿景

**终端原生 AI 助手**：编排与记忆可审计、可 fork；推理外置为可配置 HTTP LLM；主入口为 `cata`（默认 `chat`）流式对话。

---

## 架构概览

```
┌─ 用户 ─┐
    │  cata chat [--dir <产出区>]
    ▼
┌─ Client (internal/cata/client) ────────────────┐
│  Bubble Tea TUI → Unix Socket → NDJSON 事件流   │
└────────────────────────────────────────────────┘
    │  Unix Socket (~/.cata/sockets/<ws_id>.sock, per-ws agent)
    ▼
┌─ Server (internal/cata/server) ────────────────┐
│  连接管理 → 脑子解析 → 聊天循环 → 工具执行       │
└────────────────────────────────────────────────┘
    │
    ├── LLM (internal/llm) ─── OpenAI 兼容 API（含角色卡片 + brain 节选注入）
    │
    ├── CATA_HOME (~/.cata/)
    │   ├── global/                    ← 引导型提示词（constraints、behavior、delegate-guide）
    │   └── brain/workspaces/<id>/     ← 运行时记忆（memory/、index、evolution_log）
    │
    ├── 项目主要内容 (focus_path/.cata/)
    │   ├── persona.local.md
    │   ├── modes/<mode>/persona|behavior|constraints|capabilities.yaml
    │   └── skills/<id>/
    │
    └── Evolve (internal/cata/evolve) ─── 后台异步（Observe → LLM → 补丁 → 索引）
```

**卫星组件**（外围接入，非核心环）：`cata-gateway`（Web 控制台 / Telegram / QQ /
远程隧道）、`cata-pet`（桌宠）、`cata-desktop`（工作空间浏览器），见「卫星客户端」。

**两层循环**：

```
外层 while true              对话级，等待触发（用户输入 / cron / 外部事件）
    └── 内层 while true      任务级，LLM + 工具链执行
            └── break: LLM 返回最终文本（无 tool_calls）或超限
```

**三种触发**：
| 触发源 | 说明 |
|--------|------|
| 用户消息 | `cata chat` 交互式对话 |
| 定时演进 | `evolve.cycle_interval`（默认 600s）后台自主运行 |
| 定时任务 | 自托管调度框架 `internal/cata/scheduler` + `cata schedule` 守护：`tick_seconds`（默认 30s）扫描，到点**作为真实 socket 客户端自发起**一轮 chat（`scheduler/runner`）；chat 内 `schedule_task` / `schedule_list` / `schedule_cancel` / `schedule_remove` 管理，见 `docs/schedules.md` |

---

## 目录边界（重要）

见 **`brain/directory-plan.md`**（布局）与 **`brain/brain-files.md`**（各文件作用与 evolve 边界，代码：`internal/cata/brain/evolve_boundary.go`）。

| 位置 | 角色 |
|------|------|
| **`~/.cata/`** | **CATA_HOME**：引导型提示词（`global/`）、运行时记忆（`brain/workspaces/<id>/memory`）、config、socket、registry |
| **`focus_path/.cata/`** | **项目主要内容**：`persona.local`、`modes/<mode>/*`、`skills/<id>/*`、`workspace.yaml` |
| **当前工作目录 cwd** | **产出区**：代码与命令结果；`run_command` 与默认文件工具在此 |
| **仓库 `internal/cata/brain/guidance/` + `internal/llm/rolecards/`** | 引导层模板 + 角色卡片（`cata init` seed 到 `~/.cata/global/`；`cata initconfig` 种子 config.json） |

`focus_path`（git 根 / `.cata/workspace.yaml` / cwd）决定绑定哪一格脑子（`ws_id`），**不把产出存进 `~/.cata`**。

**双根写入**：
- **引导**（`~/.cata/global/`）：constraints、behavior、delegate-guide — 用户或 `cata init` 维护；**evolve 不写**（角色身份见 `internal/llm/rolecards/`）
- **主要内容**（`focus_path/.cata/`）：persona、modes、skills — **server + 后台 evolve** 维护
- **运行时记忆**（`~/.cata/brain/workspaces/<id>/`）：short/long/archive、index、evolution_log — server 追加 + evolve 提炼

首次 `ResolveWorkspace` → `EnsureScaffold` 会把旧版 home 格内的 persona/modes/skills **迁移**到项目 `.cata/`（`workspace_migrate_project.go`）。

---

## 代码布局（internal）

| 包 | 角色 |
|----|------|
| **`internal/cata/`** | 核心 agent：`server`、`client`（TUI）、`brain`、`evolve`、`config`、`clock`、`execcmd`、`link`、`scheduler`、`socketclient` |
| **`internal/gateway/`** | 渠道适配子模块（Telegram / QQ / Web UI / 隧道）→ 同一 Unix socket worker |
| **`internal/llm/`** | OpenAI 兼容 Chat；出站前注入角色卡片 + brain 节选 |
| **`internal/mcp/`** | MCP 客户端（browser 等） |

## 当前实现

| 区域 | 作用 |
|------|------|
| **`cmd/cata`** | `init`（初始化 ~/.cata）、`initconfig`（种子 config.json）、`config`、`agent`（per-ws）、`supervisor`（保活守护）、`link`（网关注册/隧道）、`schedule`、`update`、`chat`（默认 Bubble Tea TUI） |
| **`cmd/cata-gateway`** | 渠道入口；凭证驱动并发启渠道；Web 控制台；remote 隧道（逐机 token） |
| **`cmd/cata-pet`** | 可选桌宠客户端（Wails + `cmd/cata-pet/pet` + `frontend/`）；同一 socket，不改 server |
| **`cmd/cata-desktop`** | 桌面工作空间浏览器（Fyne，只读） |
| **`internal/cata/server`** | Unix socket、终端 chat 工具循环（NDJSON 流式 + 工具环） |
| **`internal/llm`** | OpenAI 兼容 Chat；出站前注入 **角色卡片** + **brain 节选**（见「Context 组装」） |
| **`internal/cata/brain`** | 双根路径（`project_paths.go`）、工作区解析、终端节选 |
| **`internal/cata/evolve`** | **仅后台**自主演进：Observe → LLM → 文档补丁 → `evolution_log.json`（无手动 CLI） |
| **`internal/cata/scheduler`** | 自托管调度框架：排程（机器 `~/.cata/schedules/` + 项目 `<root>/.cata/schedules/`）、cron / interval、环境发现、到点触发（`cata schedule` 守护 + `--once`）；执行走 `scheduler/runner` 真实客户端自发起（见 `docs/schedules.md`） |
| **`internal/cata/socketclient`** | 共享 socket 客户端协议（gateway 与调度框架共用；`ChatAs(…, runAs="scheduled")`） |
| **`internal/cata/config`** | `~/.cata/config.json`：LLM、exec、`evolution.enabled` / `cycle_interval` / `short_term_trigger_bytes`、`schedules.enabled` / `tick_seconds` |

**已移除**：`internal/memory`、`internal/evolution`（旧任务引擎）、`internal/git`、`skills/` 服务端加载。

---

## 存储层结构（双根）

```
~/.cata/                                    # CATA_HOME
├── config.json
├── cata.sock / supervisor.sock / sockets/<ws_id>.sock
├── registry/workspaces.json  link.json  machines.json
├── global/                                 # 引导型提示词（evolve 禁止 patch）
│   ├── constraints.md  behavior.md  delegate-guide.md
├── locks/  run/（pid 文件）  logs/  schedules/  skills/
└── brain/workspaces/<ws_id>/               # home 脑子格（运行时记忆）
    ├── meta.json  evolution_log.json  tasks/current.json
    └── memory/
        ├── index.json  short/current.md  long/  archive/

<focus_path>/.cata/                         # 项目主要内容（evolve 维护）
├── workspace.yaml（可选：name、active_mode）   workspace.link（可选：id → home 格）
├── persona.local.md
├── modes/<mode>/ persona.md  behavior.md  constraints.md  capabilities.yaml
└── skills/<id>/ SKILL.md  manifest.yaml  script.*
```

**路径路由**（`internal/cata/brain/ResolveBrainDocAbs`、`chat_paths.go`）：
- `memory/*`、`meta.json`、`evolution_log.json` → home 格
- `persona.local`、`modes/*`、`skills/*` → 项目 `.cata/`
- `global/*` → `~/.cata/global/`

---

## 产出区设计（参考 Claude Code）

核心问题：用户不一定在项目目录下运行 `cata`，需要能指定"在哪个目录干活"。

```
cata chat                         # 产出区 = 当前目录（默认，向后兼容）
cata chat --dir ~/project         # 产出区 = ~/project
cata chat --dir ~/a --dir ~/b     # 多产出区，第一个是主产出区
```

**产出区 vs 脑子（双根）**：
- **产出区** = 用户项目文件（代码、文档、构建产物）；文件工具、`run_command` 在此
- **引导** = `~/.cata/global/`（全机约束与 SOP；**evolve 不写**）
- **主要内容** = `focus_path/.cata/`（身份、项目说明、mode 文档、技能；**evolve 迭代 active_mode**）
- **运行时记忆** = `~/.cata/brain/workspaces/<id>/`（short/long/archive、index、审计）
- **focus_path** = 从产出区向上查找 `.git` 或 `.cata/workspace.yaml`，决定绑定哪格脑子

**规则**：
1. `--dir` 指定的目录成为文件工具和 `run_command` 的操作根目录
2. 文件工具只能访问产出区内的路径（`safePathUnder` 检查）
3. 脑子格子选择基于主产出区解析（`focus_path` 逻辑不变）
4. 同一个产出区目录只能开一个 chat session（output lock）
5. 不同产出区可以并行开多个 chat（各自独立 output lock）

**实现要点**：Client 将 `--dir` 解析后的路径作为 `cwd` 发给 Server；Server 的 `ResolveWorkspace(cwd)` 逻辑不变。

---

## 记忆流与提示词分层

| 层 | 物理位置 | 类型 | 写入方 | 作用 |
|----|----------|------|--------|------|
| Socket 会话历史 | server 内存 | 运行时 | 每轮对话 | 当前 session，`chat_reset` 清空 |
| short/current.md | home 脑子格 | 运行时记忆 | server 每轮追加 | evolve 输入 |
| memory/index.json | home 脑子格 | 运行时记忆 | evolve 同步 | 摘要索引，注入 brain 节选（≤2800B） |
| global/constraints、behavior | ~/.cata/global | **引导** | 用户 / init | 全机硬规则与 SOP；evolve **不写** |
| modes/…/persona 等 | 项目 `.cata/` | **主要内容** | evolve（active_mode） | 身份、项目 SOP，注入 brain 节选 |
| persona.local.md | 项目 `.cata/` | **主要内容** | evolve | 仓库用途、栈、当前任务 |
| long/ + archive/ | home 脑子格 | 运行时记忆 | evolve 归档 | 低频事实，经 index 召回 |
| 角色卡片（chat/worker/evolve） | `internal/llm/rolecards/` | **身份+协议** | 随版本 | ① system 前缀 |

详见 **`internal/cata/brain/guidance/constraints.md` §记忆分层**。

---

## Context 组装（每次终端 LLM 出站前）

**调用链**（终端 chat）：

```
socket_chat history (user/assistant/tool only)
    → llm.Client.ChatStreamRoundFor(..., injectBrain=true)
        → buildHTTPChatRequest
            → assembleSystemForRole（角色卡片 → 检索 → brain 节选）
    → adapter.BuildRequest → HTTP API
```

**代码索引**：

| 职责 | 包 / 符号 |
|------|-----------|
| 注入入口 | `internal/llm/client.go` — `assembleSystemForRole`, `buildHTTPChatRequest` |
| 角色卡片 | `internal/llm/rolecard.go` — `CardForRole` → `internal/llm/rolecards/{chat,worker,evolve}.md` |
| brain 节选正文 | `internal/cata/brain/terminal_context.go` — `TerminalBrainSystemExtension` |
| 路径 / 运行时 | `internal/cata/brain/context_paths.go` — `TerminalPathsSystemBlock`, `SetOutputCwd` |
| Skills 块 | `internal/cata/brain/skills_prompt.go` — `SkillsPromptBlock` |
| 记忆索引块 | `internal/cata/brain/memory_index.go` — `MemoryIndexPromptBlock` |
| 会话 history | `internal/cata/server/socket_chat.go` — 不存 system |
| 日志拆解 | `internal/llm/prompt_log.go` — `buildPromptManifest`（`LLM_LOG_FILE`） |

**发往 API 的 `messages` 顺序**（终端）：

```
[0] system  ① 角色身份（rolecards/chat.md，≤10000 runes）— 身份 + 优先级栈 + 交互底线
[1] system  ② 单条 brain 节选（terminal_context.go），自上而下：
        · 【Cata 路径】TerminalPathsSystemBlock — 仅本轮绝对路径与本机环境
        · 【Cata Skills】capabilities → SKILL.md（项目 → ~/.cata/skills/ → ~/.cursor/skills-cursor/）
        · memory/index.json 紧凑块（home 格）
        · 【Cata 引导】constraints（写入边界）+ behavior（协作 SOP）
        · 【Cata 项目内容】modes/<active_mode>/persona|behavior|constraints、persona.local
[2…] user / assistant / tool   ← socket 内存 history
```

**防重复原则**：路径路由只写在 constraints；工作方式只写在 behavior；绝对路径只写在路径块；boot 只指向这三层，不复述表格。

**工具**：OpenAI `tools` 数组（`server.buildTerminalChatToolsForTier`：按 ContextTier 内置 + MCP），**不**拼进 system 正文。

**演进与其它 LLM**：

| 调用方 | messages 构造 | 注入 boot/brain |
|--------|---------------|-----------------|
| 终端 chat | history only | 是（`ChatStreamRoundFor` → `assembleSystemForRole`） |
| `evolve` 决策 | `system` + `user` | **否**（`ChatEvolution` → `NoBrainInject`） |
| `Summarize` / 查询预处理 | 内联 `system` + `user` | 是（经 `chat()`） |

**会话压缩**（与 system 无关）：估算 token ≥ `context_window × context_compress_ratio`（默认 85%）→ `evolve.RunSessionCompress` → history 裁到窗口比例内；**先剥掉最早带 Media 的 user 轮（保留文本），再裁条数**；每张图按 `llm.image_token_estimate`（默认 1000）计入预算。

**硬限制**（与代码常量一致）：

| 项 | 上限 | 位置 |
|----|------|------|
| 角色卡片身份 | 10000 runes | `maxBootLeaderRunes` |
| 单文件 brain 摘录 | 6500 bytes | `maxBrainExcerptBytesPerFile` |
| brain 摘录合计 | 20000 bytes | `maxBrainExcerptBytesTotal` |
| memory/index 注入 | 2800 bytes | `maxIndexPromptBytes` |
| 单 skill / 合计 | 8000 / 16000 bytes | `skills_prompt.go` |
| short-term 文件 | 96KB | `session_memory.go` |

**日志拆解**：`internal/llm/prompt_log.go`（`LLM_LOG_FILE`）；组件 id：`boot-leader`、`brain-excerpt`、`conversation`、`tools`。

---

## 自主演进

```
双根 patch（per workspace，active_mode）:
    主要内容 → focus_path/.cata/（persona.local、modes/<active_mode>/*、skills/*）
    运行时记忆 → ~/.cata/brain/workspaces/<id>/（memory/*、meta.json、evolution_log.json）
    禁止 → global/*（引导层）

触发条件:
    short-term 有新内容（见 internal/cata/evolve/gate.go）
    或 fill:*（项目 .cata 空壳文档须本轮填充）

周期:
    默认 600s，由 evolution.cycle_interval 控制

动作:
    observe → LLM 决策 (idle|consolidate|crystallize|crystallize_mode|crystallize_skill|…)
      → ApplyUpdates → 确定性 compact（去重）→ 索引同步

项目主要内容约束:
    按意图选 patch 模式（见 internal/llm/rolecards/evolve.md）
    更新已有 ## 节 → replace_section；memory 流水 → append；fill/compact → overwrite
    超 3500B 触发 compact:*；补丁后 CompactMarkdown 去重

决策 prompt 含: workspace_scope（id、focus_path、active_mode、project_cata、home_brain）

无手动演进命令。
```

**记忆命中闭环（P4⑦）**：检索命中的记忆来源记录到 llm.log（`retrieved_memory`）；各记忆条目累计命中（`Hits`），evolve Evaluate 按命中强化/降权、淘汰僵尸条目。

---

## 保护机制

```
内层循环:
    上下文超 context_window × 85% → 触发 session compress → 历史截断
    任务级 max_tool_rounds（declare_task 声明；0 = 仅全局 hard ceiling 200）：
        预算耗尽（budget_exhausted）→ 失败可恢复，「继续」时自动放开任务级预算到硬顶
    连续失败 / 无进展 → 分别按 max_consecutive_failures / max_stale_rounds 熔断

工具执行:
    run_command 输出上限 256KB；命令黑名单 + 用户确认；路径遍历防护 (safePathUnder)

并发:
    同一产出区只能一个 chat session（output lock）
    每工作空间独立 agent 进程（per-ws socket 锁）
    记忆读写由 evolve 引擎串行化（单 goroutine）

记忆膨胀:
    short/current.md 上限 96KB → 触发 trim
    modes/<mode>/persona.md 超 6500 bytes → 触发 consolidate（写入项目 .cata）
    index.json 超 2800 bytes → 触发 summary 压缩
```

---

## 交互层：Bubble Tea TUI

`cata chat` 使用 **Bubble Tea** 全屏 TUI（`internal/cata/client/tui.go`）：主区滚动对话、底栏 `›` 输入、宽屏 **右侧状态栏**（`tui_stats.go`，≥96 列；`CATA_NO_SIDEBAR=1` 关闭）。**不再**使用 stdout/stderr 分流，故不支持 `cata chat "…" > file` 管道模式。

### Server → Client（NDJSON）

`internal/cata/server/socket_chat.go` → `emitStreamLine`；TUI 在 `stream.go` 消费。

| `type` | TUI | 字段 |
|--------|-----|------|
| `token` | 主区流式正文 | `content` |
| `thinking` | 「┈ 思考中 ┈」块（`--show-thinking`） | `content` |
| `progress` | 侧栏 state | `message` |
| `tool_start` / `tool_result` | 主区或侧栏 | `name`, `output`, `level` |
| `exec_confirm_required` | 列表菜单 Run/Cancel | `confirm_id`, `argv`, `cwd` |
| `user_choice` | 列表菜单（↑↓/j/k，Enter） | `id`, `prompt`, `options` |
| `stats` | 右侧栏（token/round/model/effective_model） | `model` 等 |
| `attachment_rejected` | 主区错误行 | `path`, `reason` |
| `model_switch` | 主区提示行 | `from`, `to` |
| `error` | 主区 | `message` |
| `done` | 结束本轮；`cancelled`=Ctrl+C | `success`, `cancelled` |

**注意**：`reasoning_content` 只进 LLM history（tool 轮次 API 要求回传），默认**不发** `thinking` 事件；`--show-thinking` 时才实时下发。

斜杠：`/help` `/status` `/clear` `/exit` `/retry` `/config` `/attach`。

**预留**（server 未发或未接）：无（`file_written` 与 `diff` 均已落地：写入工具成功时发
`file_written{name,path,bytes,id,diff}`——unified diff 在 verbose 模式展示全文、auto 记入侧栏详情）。

### 分级显示（已落地 v0.1.16）

Server 在 `tool_start` / `tool_result` 事件附带 `level` 字段（`silent` / `normal` / `verbose`）：read/list 成功 → `silent`；常规编辑/委派 → `normal`；`run_command`、出错 → `verbose`。TUI 按 `displayMode` 渲染：

| CLI | displayMode | 行为 |
|-----|-------------|------|
| （默认） | auto | 按事件 `level`：silent 不显示正文、normal 摘要、verbose 完整 |
| `--quiet` / `-q` | quiet | 工具输出全部静默，只看结论与 token 流 |
| `--verbose` / `-v` | verbose | 所有工具结果完整显示 |

### 推理/思考（DeepSeek thinking）

- Server 在流式轮次收集 `reasoning_content` 并写入 **history**（供 DeepSeek tool 轮次回传）；客户端带 `--show-thinking` 时实时下发 `thinking` 事件，默认不展示
- 配置：`config.json` → `llm.thinking`（`auto` / `enabled` / `disabled`），见 `internal/llm/provider.go`

### 文件操作确认

- `search_replace` / `append_file`：默认不确认（可逆操作）
- `run_command`：黑名单命令或 `require_confirm` 时弹出确认

---

## 多模态（已实现 A/B + M2/M3 + M4 基础：出站编码 + 能力路由 + 附件摄取 + TUI /attach/@路径 + 事件 + 压缩与 token 估算）

**目标**：终端 chat 可向模型附带**图片**（音频/PDF 基础已就绪，按模型 capabilities 启用）；**换模型**时只改配置，不改业务代码路径。
**非目标（v1）**：演进 LLM、Summarize、MCP 默认不走 vision；不做视频流；不在 `~/.cata` 脑子正文里存原图。

### 能力模型（可切换多模态模型）

在 `~/.cata/config.json` 增加 **`llm.capabilities`**（按**模型名**键，与 `llm.model` / `llm.models.*` 解耦）：

```json
{
  "llm": {
    "provider": "deepseek",
    "api_url": "https://api.deepseek.com/chat/completions",
    "model": "deepseek-v4-flash",
    "models": { "chat": "deepseek-v4-flash", "chat_vision": "gpt-4o" },
    "capabilities": {
      "deepseek-v4-flash": { "modalities": ["text"] },
      "gpt-4o": {
        "modalities": ["text", "image"],
        "max_images_per_message": 4,
        "image_mime_allow": ["image/png", "image/jpeg", "image/webp", "image/gif"]
      }
    },
    "image_token_estimate": 1000,
    "attachment_dir": ""
  }
}
```

**运行时选模型**（`internal/llm/capability.go` → `resolveModelForMessages`）：

```
若本 turn 无附件 → models["chat"] ?? model
若本 turn 有图/音频（modality）：
  若当前 chat 模型 capabilities 含该 modality → 继续用 chat 模型
  否则若配置了 models["chat_vision"] 且其支持 → 换用 chat_vision
  否则 → 向客户端返回 error（NDJSON），不静默丢图
```

演进 / 摘要**不**因用户附图切换；`capabilities` 缺省时对未知模型保守视为**仅 text**。切换时下发 `model_switch` 事件，侧栏展示真实模型（`→` 后缀）。

| 配置键 | 含义 |
|--------|------|
| `modalities` | `text` \| `image` \| `audio` \| `document`（按能力） |
| `max_images_per_message` | 单条 user 消息最多几张 |
| `max_image_bytes` | 单张解码后上限（默认 10MiB） |
| `image_mime_allow` | 白名单 MIME（默认 png/jpeg/webp/gif） |
| `image_token_estimate` | 单图估算 token（默认 1000，计入压缩预算） |
| `attachment_dir` | 附件白名单目录（默认仅产出区） |

换供应商时：只改 `api_url` + `model` + `capabilities` 三条，**不改** server 工具循环与 socket 协议字段名。

### 内容表示（`internal/llm`）

**原则**：内存 history 与 API wire **解耦**——history 存引用，出站再展开为 provider 格式。

```go
type Message struct {
    Role    string
    Content string // 仍用于 system / 纯文本 user / assistant / tool
    Media   []MediaRef `json:"media,omitempty"` // 仅 role=user 且为「多模态轮次」时使用
}

type MediaRef struct {
    ID   string
    MIME string
    // Data 出站时填充的 base64（history 引用态为空；写 llm.log 前必须剥离）
}
```

**出站编码**（`encodeContentForWire`，按 `caps` + MIME modality）：
- **text-only 模型**：`content` 仍为 string；媒体轮次在出站前**拒绝**或要求用户改配置
- **vision 模型**：user 消息 `content` 为数组 `[{type:text}, {type:image_url, image_url:{url: data:<mime>;base64,…}}]`
- **audio 模型**：`{type:input_audio, input_audio:{data, format}}`（`audioFormatFromMIME`：wav/mp3/mp4/ogg/flac/aac/webm）
- **document（PDF）**：server 侧 ingest 时用系统 `pdftotext` 提取为文本拼入 user 正文
  （不依赖模型 document modality；`pdftotext` 缺失则拒绝并提示安装 poppler-utils）
- **Anthropic 适配器**：图片编码为 content blocks（`{type:image, source:{type:base64, media_type, data}}`）

`assistant` / `tool` / `system` **保持字符串**。

### 附件生命周期

```
产出区路径 / TUI inline（剪贴板粘贴）
        ↓ ingest（校验 MIME、大小、路径 safePathUnder；逐条失败发 attachment_rejected）
        ↓ history 只保留 MediaRef{id,mime}（base64 仅在出站前填充）
        ↓ 出站时 base64 data URL / input_audio / Anthropic source
```

| 规则 | 说明 |
|------|------|
| 可读路径 | 仅**产出区**内相对/绝对路径 + `llm.attachment_dir` 白名单目录 |
| 禁止 | 直接读 `~/.cata/brain/**` 原图进模型（防泄露脑子全文） |
| short-term 记忆 | **只写** `text` + `[attachments: a.png, …]` 一行摘要，**不写** base64 |
| 会话压缩 | 优先裁掉**最早**带 `Media` 的 user 轮中的图片（保留文本），再裁消息条数 |
| llm.log | 日志克隆剥离 Media.Data（`cloneMessagesForLLMLog` 只留 id/mime） |

### Socket / Client 协议扩展

**请求**（`server.Request` 的 `attachments`，向后兼容）：

```json
{
  "command": "chat",
  "text": "这张图里有什么？",
  "stream": true,
  "cwd": "D:\\stock",
  "attachments": [
    { "path": "charts/k.png" },
    { "inline": { "mime": "image/png", "base64": "..." } }
  ]
}
```

| 字段 | 说明 |
|------|------|
| `attachments[].path` | 相对产出区或绝对路径（经 `safePathUnder`） |
| `attachments[].inline` | TUI 粘贴/拖拽时用（`InlineAttachment{MIME, Base64}`） |

**事件**：`attachment_rejected`（`reason`, `path`）、`model_switch`（`from`, `to`）。

**TUI（`internal/cata/client`）**：

| 操作 | 行为 |
|------|------|
| `/attach <path>` | 加入待发送附件队列，发送时一并提交（`/attach clear` 清空，`/attach` 查看）；行内 `@path` 亦识别 |
| 发送 | `text` + `attachments` 一并提交（队列 + 本行 `@` 合并） |
| 主区展示 | 不嵌大图，一行附件摘要；被拒发错误行 |

### Provider 抽象（便于换模型）

```
Provider (现有 adapter)
  + BuildRequest(..., caps ModelCaps, messages, ...)   ← 带图按 caps 编码 content[]
  + ParseResponse(body)
```

DeepSeek / 千问 / OpenAI / 本地 vLLM / Anthropic 的差异集中在**出站编码**（`encodeContentForWire`）与 wire 形态；业务代码只依赖 `llm.Message` + `MediaRef`。

### 分阶段实现

| 阶段 | 内容 | 验收 |
|------|------|------|
| **M0** ✅ | `capabilities` 配置解析；模型路由；无附件时行为不变 | 改 config 换模型名即可 |
| **M1** ✅ | socket `attachments` + history `MediaRef` + wire 数组 | `chat` + 附件路径可问图 |
| **M2** ✅ | TUI `/attach`、发送栏附件提示、`model_switch` / `attachment_rejected` 事件 | 交互式附图 |
| **M3** ✅ | 压缩优先去旧轮 Media、按图 token 估算（`image_token_estimate`） | 长会话不爆窗 |
| **M4** ✅ | audio wire（`input_audio`）+ 能力路由 + Anthropic 图片 + **PDF 文本提取**（pdftotext 拼入 user 正文） | 按所选模型 capabilities 启用 |

**刻意不做（与多模态相关）**：不把图片写入 `memory/short/current.md` 全文；不让 evolve 自动选 vision 模型；不在 v1 支持「管道 `cata chat < img.png`」。

---

## LLM（DeepSeek）

- **provider**：`deepseek`（[OpenAI 兼容](https://api-docs.deepseek.com/zh-cn/)，代码走 `OpenAICompatAdapter`）
- **默认**：`https://api.deepseek.com/chat/completions`，模型 `deepseek-v4-flash`（更强用 `deepseek-v4-pro`）
- **密钥**：`llm.api_key` 或 `DEEPSEEK_API_KEY`
- **`llm.thinking`**：`auto`（默认，有 tools 时 `disabled`，避免 tool 轮次 400）、`enabled`、`disabled`
- 思考模式 + tool 调用时须回传 `reasoning_content`（已实现）；见 [Thinking Mode](https://api-docs.deepseek.com/guides/thinking_mode)
- **仅 DeepSeek / MiMo 类网关**会下发非标准 `thinking` / `reasoning_content`；OpenAI、Gemini OpenAI 兼容层等通用端点不发这些字段，换模型只需改 `api_url` / `model` / `api_key`（`api_format=openai`）。`api_url` 可写 base；缺路径时运行时探测并记住（`~/.cata/api_url_resolved.json`）。
- 原千问配置备份在 `~/.cata/config.json` → `llm_previous_qwen`（不参与加载）

---

## MCP 与 Skill（已接入）

- **MCP browser**：默认**不**在 capabilities 启用；`~/.cata/config.json` → `mcp.servers` 可配 Playwright，仅当 `modes/*/capabilities.yaml` 含 `mcp: [browser]` 时才会连接。未安装时失败只记日志，对话继续。小红书见 **`docs/mcp-browser.md`**。
- **双模型**：`llm.models.chat`（主对话）/ `evolution` / `worker`（`delegate_task`，可选 `mode_id`）；未配置时回退 `llm.model`
- **委派**：`delegate_task` 无 `mode_id` = 廉价 worker；带 `mode_id`+`case_id` = 专职 mode（`delegate_mode` 为其别名）。主 chat 会注入【专职 Modes】目录；有专职时首轮至少 standard（含委派工具）。`crystallize_mode` 会在 `_default/behavior.md` 登记委派路由。
- **长期记忆**：`memory/long/learnings.md` 等除 index 外注入【长期记忆节选】近条；需要全文时 `read_file brain/memory/long/…`。
- **evolve**：对外主行动 `consolidate` / `crystallize`（+ `crystallize_mode`）；`mode_evolve`/`orch_evolve` 仍为别名，Observe 读 `mode_buckets` 内部路由
- **run_skill**：执行项目 `.cata/skills/<id>/` 的 `manifest.yaml` + 脚本（cwd=产出区）；由演进 `crystallize_skill` 固化。
- **crystallize_skill**：高 token / 重复 browser 任务后，evolve 写 `skills/<id>/` 到**项目 `.cata`** 并自动 append capabilities；下次 chat 生效。
- **api_url**：可写 base 或完整路径；运行时会试「原样 / +默认路径」，成功后写入 `~/.cata/api_url_resolved.json` 记住。

---

## 子 Agent（delegate_task / worker）

主 Agent（`llm.models.chat`）负责规划与整合；**worker**（`llm.models.worker`）执行有界子任务，降低成本。

| 项 | 行为 |
|----|------|
| 触发 | 仅主 Agent 调用 `delegate_task` |
| 上下文 | worker **不注入** boot/brain；仅 task + context + 工具结果 |
| 工具 | 默认全套（可 `subagent.default_tools` 白名单）；禁止 `ask_user`、嵌套 `delegate_task` |
| 并行 | `subagent.max_concurrent`（默认 4）；槽满时 `subagent_queued` 事件 |
| 收集 | `delegate_wait`：`ids` 拉指定任务（含已完成）；`all:true` 本会话全部；空 ids 仅等运行中 |
| 留痕 | `~/.cata/subagent_runs/<产出区路径_.csv>`；`delegate_wait` 摘要写入 short-term |
| 审计 | `llm.log` 中 `kind: worker_round` + `subagent_id` + `session_id` |

**browser/MCP**：勿多 worker 并行浏览器；宜父 Agent 串行或单 worker + tools 白名单。

---

## 定时任务（自托管调度框架）

- **入口**：chat 内工具 `schedule_task` / `schedule_list` / `schedule_cancel` / `schedule_remove`（Standard/Full 档）；排程定义落在**机器级** `~/.cata/schedules/<id>.json` 或**项目级** `<project>/.cata/schedules/<id>.json`（git/workspace.yaml 工作区写项目级，随项目 `.cata` 分发），id 由名称稳定生成（保留中文/字母/数字）。
- **调度框架**：`cata schedule` 守护进程**发现环境里的任务**（机器级 + 所有已注册工作区的项目级，`ListAll`），按 `schedules.tick_seconds`（默认 30s）扫描；到点且未在运行即触发（错过只补一次，无历史补跑队列）。`schedule_task` 创建/启用任务时自动拉起守护（`EnsureDaemonRunning`，`setsid` + `~/.cata/schedules/daemon.sock` 单例锁，日志 `daemon.log`）——**chat 里指定后不用管，后台自动执行**；`cata schedule --once` 可挂系统 cron。
- **执行**：到点后由 `internal/cata/scheduler/runner` **作为真实 socket 客户端自发起**一轮 chat（`run_as=scheduled`，与客户自己发起一致）；`ask_user` 自动跳过、`user_choice` 全空、`run_command` 需 `allow_exec=true`。产出：报告 `<project>/.cata/schedule-runs/<id>/<ts>.md`（可 `output_dir` 改绝对目录）+ 审计 `<存储目录>/runs/<id>/<ts>.jsonl`；chat 循环照常写短期记忆。
- **server 不内嵌调度**：执行由独立 `cata schedule` 守护承担（无 server 时内嵌一个，已有则复用）。
- **工作区边界**：排程绑定创建它的工作区；管理命令只作用于当前工作区，不跨工作区管理；执行也在任务自己的 `cwd`/`ws_id` 下进行。守护的环境发现仍全环境。
- **边界**：定时任务跳过任务状态机（不污染前台 `declare_task`）；与前台并行时复用 server 全局 Active 模式。详见 **`docs/schedules.md`**。

---

## 卫星客户端（非核心环）

| 入口 | 角色 |
|------|------|
| `cata chat` TUI | **核心**交互 |
| `cata-gateway`（Telegram / QQ / Web UI / remote 隧道） | 渠道适配 → 同一 socket / WSS 隧道；独立部署见 `docs/gateway.md` |
| `cata-pet` | 桌面宠物 UI（Wails）→ 同一 socket；见 `cmd/cata-pet/README.md` |
| `cata-desktop` | 桌面工作空间浏览器（Fyne，只读），见 `docs/desktop.md` |

改核心优先 `internal/cata/{server,client,evolve,brain}` + `internal/llm`；渠道与 pet 默认不进必读路径。

---

## 刻意排除

- **旧 `skills/` 服务端调度、`scripts/` 主线**：已废弃；仅保留 MD 提示词加载。
- **手动演进命令、任务队列、MemoryManager 索引**：已废弃（`internal/cata/scheduler` 是**定时触发引擎**，不是任务队列）。
- 无内置 git 操作。
- 无独立 TUI Web UI（终端为主；`cata-gateway` 自带本机管理控制台与远程模式，见 `docs/gateway.md`）。
- 无多机分布式（网关 remote 模式为「云端注册中心 + 各机 agent 回连」，见 `docs/tunnel.md`）。
- 无 `catacli` 独立二进制（统一 `cata` TUI）。

---

## 敏感值脱敏

`internal/cata/secrets` Redactor 集中登记运行时已知敏感值（环境变量疑似 secret、config 的 api_key、link.json 的 machine_token/gateway_token），替换为 `***REDACTED***`（长度 ≥8 才替换）。两处接线：
- **llm 侧**：`llm.log` 写盘前 `redactLogBytes` 掩盖密钥明文
- **server 侧**：工具结果进 history（→LLM 上下文）前 `serverRedactor.Redact`

---

## Supervisor 进程管理（每机器一个）

- 只负责注册（link add）工作空间 agent 进程的生命周期：拉起 / 保活（30s tick）/ 停止；不转发对话、不持隧道。
- 控制口：`~/.cata/supervisor.sock`（ping/add/ensure/stop/list/status/shutdown）。
- **级联关闭**：`cata supervisor stop` 或 SIGTERM/SIGINT → `stopAllAgents()` 立即停；SIGKILL（kill -9）→ 各 keep-alive agent 靠控制口心跳（失联默认 30s；`CATA_SUPERVISOR_INTERVAL` / `CATA_SUPERVISOR_DEADLINE` 可调）自检退出。
- **不打断活跃会话**：心跳 `Busy`（`srv.HasActiveChat`）为 true 时推迟退出，空闲或 supervisor 恢复后再判——避免正在进行的对话/任务被误杀。
- 一个工作空间 = 一个 agent 进程（`cata chat` 按需拉起，注册的常驻，空闲超时回收；keep-alive 不因空闲退出）。

---

## 给 AI 的约束

1. 改核心先看 **`internal/cata/`**（`server` / `client` / `evolve` / `brain`）与 **`cmd/cata`**；改渠道看 **`internal/gateway/`**；桌宠看 **`cmd/cata-pet`**（含 `pet/` 子包与 `frontend/`）。
2. 路径以 **`internal/cata/brain/project_paths.go`**、`paths.go`、`context_paths.go` 为准；产出区 = chat 请求的 `cwd`（`--dir` 时 client 会 `chdir` 到主产出区）。
3. **一个工作空间 = 一个 agent 进程**（`cata chat` 按需拉起，注册的常驻，空闲超时回收）；**同一产出区目录只能开一个 chat**；per-ws socket 在 `~/.cata/sockets/<ws_id>.sock`。legacy `cata run` 仅作 cata-pet / scheduler 内部支撑，chat 不依赖。
4. 勿虚构路径；勿把角色卡片/引导模板（`internal/llm/rolecards/`、`internal/cata/brain/guidance/`）与 `~/.cata`（运行时）混为一谈；勿把 focus_path 当成产出区；**主要内容**在 `focus_path/.cata/`，不在 home 脑子格。
5. **提交前必须全量自检**：`gofmt -l .`（应为空）→ `go build ./...` → `go test ./...`，三者通过才提交并推送。只对改动文件逐个 gofmt 会漏掉其它未格式化文件（CI 会因 `gofmt -l .` 失败）。

---

## 建议阅读顺序

1. 本文件（项目边界 + AI 约束）
2. `~/.cata/global/constraints.md`（或模板 `internal/cata/brain/guidance/constraints.md`）
3. 提示词代码：`internal/llm/client.go`、`internal/cata/brain/terminal_context.go`、`internal/llm/prompt_log.go`
4. 对话循环：`internal/cata/server/socket_chat.go`、`internal/cata/client/tui.go`
5. 演进：`internal/cata/evolve/engine.go`
6. 相关专题：`docs/gateway.md`、`docs/tunnel.md`、`docs/schedules.md`、`docs/mcp-browser.md`、`docs/desktop.md`