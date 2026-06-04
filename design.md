## Cata 系统设计

### 架构概览

```
┌─ 用户 ─┐
    │  cata chat [--dir <产出区>]
    ▼
┌─ Client (internal/client) ─────────────────────────────┐
│  REPL 循环 → Unix Socket → NDJSON 事件流 → 终端渲染     │
└────────────────────────────────────────────────────────┘
    │  Unix Socket (~/.cata/cata.sock)
    ▼
┌─ Server (internal/server) ─────────────────────────────┐
│  连接管理 → 脑子解析 → 聊天循环 → 工具执行               │
│  (managed 模式: 最后一个 chat 断开后自动退出)            │
└────────────────────────────────────────────────────────┘
    │
    ├── LLM (internal/llm) ─── OpenAI 兼容 API
    │
    ├── 脑子 (~/.cata/brain/workspaces/<id>/)
    │   ├── memory/short/current.md    ← 每轮写入
    │   ├── memory/long/               ← evolve 归档
    │   ├── memory/index.json          ← 记忆索引
    │   ├── modes/<mode>/persona.md    ← evolve 维护
    │   └── skills/                    ← evolve 固化
    │
    └── Evolve (internal/evolve) ─── 后台异步
        观察 → LLM 决策 → 文档补丁 → 索引同步
```

### 核心模型

**两层循环**：

```
外层 while true              对话级，等待触发（用户输入 / cron / 外部事件）
    └── 内层 while true      任务级，LLM + 工具链执行
            └── break: LLM 返回最终文本（无 tool_calls）或超限
```

**两种触发**：
| 触发源 | 说明 |
|--------|------|
| 用户消息 | `cata chat` 交互式对话 |
| 定时演进 | `evolve.cycle_interval`（默认 600s）后台自主运行 |

---

### 产出区设计（参考 Claude Code）

核心问题：用户不一定在项目目录下运行 `cata`，需要能指定"在哪个目录干活"。

**方案**：

```
cata chat                         # 产出区 = 当前目录 (默认，向后兼容)
cata chat --dir ~/project         # 产出区 = ~/project
cata chat --dir ~/a --dir ~/b     # 多产出区，第一个是主产出区
```

**产出区 vs 脑子的关系**：

```
产出区 (output dirs)          脑子 (~/.cata)
─────────────────────         ─────────────────────
文件工具操作范围              persona / 记忆 / 技能
run_command 执行目录          演进日志 / 注册表
项目 .git 检测起点            不存用户代码
```

- **产出区** = 用户的项目文件所在位置（代码、文档、构建产物）
- **脑子** = Agent 的记忆和 persona（永远在 `~/.cata/`）
- **focus_path** = 从产出区向上查找 `.git` 或 `.cata/workspace.yaml`，决定绑定哪个脑子格子
- 借鉴 Claude Code：`--add-dir` → cata 的多个 `--dir`；Claude 的 launch dir → cata 的第一个 `--dir` 或 cwd

**规则**：
1. `--dir` 指定的目录成为文件工具和 `run_command` 的操作根目录
2. 文件工具只能访问产出区内的路径（`safePathUnder` 检查）
3. 脑子格子选择基于主产出区解析（`focus_path` 逻辑不变）
4. 同一个产出区目录只能开一个 chat session（output lock 不变）
5. 不同产出区可以并行开多个 chat

---

### 交互层设计：对话交付

**终端**：`cata chat` 使用 Bubble Tea 全屏 TUI（`internal/client/tui.go`），主区对话 + 底栏输入 + 宽屏右侧状态；**不**再依赖 stdout/stderr 分流（不可 `> file` 管道正文）。

#### 事件类型（当前实现）

Server（`internal/server/socket_chat.go`）→ Client（`tui.go` / `stream.go`）单行 JSON：

| `type` | TUI | 字段 |
|--------|-----|------|
| `token` | 主区流式 | `content` |
| `progress` | 侧栏 state | `message` |
| `tool_start` / `tool_result` | 主区 | `name`, `output` |
| `exec_confirm_required` | 列表覆盖层 | `confirm_id`, `argv`, `cwd` |
| `user_choice` | 列表覆盖层 | `id`, `prompt`, `options` |
| `stats` | 右侧栏 | token、round 等 |
| `error` | 主区 | `message` |
| `done` | — | `success`, `cancelled` |

**注意**：`reasoning_content` 只进 LLM history，**不**发 `thinking` 事件。

#### 规划中的分级显示（未接线）

Server 尚未发 `display`；TUI 尚未做 silent/normal/verbose 分级。目标矩阵：

| 级别 | 含义 | 适用工具 |
|------|------|----------|
| `silent` | 不显示输出内容 | `read_file` 成功时 |
| `normal` | 摘要/截断 | `search_replace`、`run_skill` |
| `verbose` | 完整输出 | `run_command`、出错时 |

```
# 正常模式（默认）
› add tests for auth
⟳ round 2                                    ← stderr
📄 reading internal/auth/auth.go              ← stderr, tool:start
✏ editing internal/auth/auth_test.go          ← stderr, tool:start
  + func TestAuthenticate(t *testing.T) {     ← stderr, tool:output (normal)
  +   ...
⚙ go test ./internal/auth/...                 ← stderr, tool:start
ok  internal/auth  0.234s                     ← stderr, tool:output (verbose)
我已经添加了测试...                            ← stdout, token stream

# 安静模式 (--quiet)
› add tests for auth
我已经添加了测试...                            ← stdout only，工具静默

# 详细模式 (--verbose)
› add tests for auth
⟳ round 1                                    ← stderr
📄 reading internal/auth/auth.go              ← stderr
  (145 lines)                                 ← stderr, 输出摘要
✏ editing internal/auth/auth_test.go          ← stderr
  + func TestAuthenticate...                  ← stderr, 完整 diff
⚙ go test ./internal/auth/...                 ← stderr
ok  internal/auth  0.234s                     ← stderr, 完整命令输出
我已经添加了测试...                            ← stdout
```

#### 推理/思考（DeepSeek thinking）

- **默认**：不向终端展示；Server 收集后写入 assistant 消息的 `reasoning_content`（tool 轮次 API 要求）
- **规划**：`thinking` 事件 + `cata chat --show-thinking`
- 配置：`config.json` → `llm.thinking`（`auto` / `enabled` / `disabled`），见 `internal/llm/provider.go`

#### 文件操作确认

- `search_replace`：默认不确认（可逆操作）
- `append_file`：默认不确认
- `run_command`：黑名单命令或 `require_confirm` 时弹出确认

---

### 存储层结构

```
~/.cata/
├── registry/workspaces.json      # 工作区注册表
├── global/
│   ├── constraints.md            # 全局约束
│   ├── behavior.md               # 全局行为 SOP
│   └── boot-assembler.md         # Boot leader 指令
├── brain/workspaces/<ws_id>/
│   ├── meta.json
│   ├── persona.local.md          # 聚焦上下文
│   ├── evolution_log.json        # 演进日志
│   ├── memory/
│   │   ├── index.json            # 记忆索引（常驻 context）
│   │   ├── short/current.md      # 短期记忆（每轮写入）
│   │   ├── long/                 # 长期记忆（evolve 归档）
│   │   └── archive/              # 冷记忆
│   ├── modes/<mode>/
│   │   ├── persona.md            # 模式 persona（evolve 维护）
│   │   ├── behavior.md
│   │   ├── constraints.md
│   │   └── capabilities.yaml     # MCP + skills 声明
│   └── skills/<id>/
│       ├── SKILL.md
│       ├── manifest.yaml
│       └── script.py
├── skills/                       # 全局共享技能
├── locks/                        # 产出区锁文件
└── cata.sock                     # Unix socket
```

### 记忆分层（与 design.md 对齐）

| 层 | 位置 | 写入方 | 作用 |
|----|------|--------|------|
| Socket 会话历史 | server 内存 | 每轮对话 | 当前 session 上下文，chat_reset 清空 |
| short/current.md | 每格脑子 | 每轮 chat 成功后追加 | 对话原文，evolve 的输入 |
| memory/index.json | 每格脑子 | evolve 同步 | 摘要索引，常驻 context（< 2800 bytes） |
| modes/…/persona.md | modes/<mode>/ | evolve 提炼 | 偏好 + 流程，注入 ② brain 节选 |
| long/ + archive/ | 每格脑子 | evolve 归档 | 低频事实，按需召回 |

### Context 组装（每次终端 LLM 出站前）

**调用链**（终端 chat）：

```
socket_chat history (user/assistant/tool only)
    → llm.Client.ChatStreamRound(..., injectBrain=true)
        → buildHTTPChatRequest
            → withBootLeaderSystemMessage
                → ensureCataBrainExcerptSystem
    → Provider.BuildRequest → HTTP API
```

**代码索引**：

| 职责 | 包 / 符号 |
|------|-----------|
| 注入入口 | `internal/llm/client.go` — `withBootLeaderSystemMessage`, `ensureCataBrainExcerptSystem`, `buildHTTPChatRequest` |
| boot 文件路径 | `internal/brain/paths.go` — `BootLeaderPath()` → `~/.cata/global/boot-assembler.md`（优先）或 `brain/boot-assembler.md` |
| brain 节选正文 | `internal/brain/terminal_context.go` — `TerminalBrainSystemExtension` |
| 路径 / 运行时 | `internal/brain/context_paths.go` — `TerminalPathsSystemBlock`, `SetOutputCwd` |
| Skills 块 | `internal/brain/skills_prompt.go` — `SkillsPromptBlock` |
| 记忆索引块 | `internal/brain/memory_index.go` — `MemoryIndexPromptBlock` |
| 会话 history | `internal/server/socket_chat.go` — 不存 system |
| 日志拆解 | `internal/llm/prompt_log.go` — `buildPromptManifest`（`LLM_LOG_FILE`） |

**发往 API 的 `messages` 顺序**（终端，有 boot 文件时）：

```
[0] system  ① boot-assembler 全文（≤10000 runes，internal/llm/client.go）
[1] system  ② 单条 brain 节选（internal/brain/terminal_context.go），结构为：
        · 路径块 TerminalPathsSystemBlock
        · 【Cata Skills】…（capabilities.yaml → SKILL.md）
        · memory/index.json 紧凑块
        · 【Cata 脑子节选…】global/constraints, global/behavior,
          modes/<mode>/persona.md, persona.local.md
[2…] user / assistant / tool   ← socket 内存 history
```

**工具**：OpenAI `tools` 数组（`server.buildTerminalChatTools`：内置 + MCP），**不**拼进 system 正文。

**演进与其它 LLM**：

| 调用方 | messages 构造 | injectBrain |
|--------|---------------|-------------|
| 终端 chat | history only | `true`（流式 `ChatStreamRound`） |
| `evolve` 决策 | `system`（`evolutionSystemPrompt` 等）+ `user`（`buildDecisionPrompt`） | `true`（经 `ChatEvolution` → `chat()`，仍会前置 ①②） |
| `Summarize` / 查询预处理 | 内联 `system` + `user` | `true` |

**会话压缩**（与 system 无关）：估算 token ≥ `context_window × context_compress_ratio`（默认 85%）→ `evolve.RunSessionCompress` → history 裁到 `context_window × 40%`（`socket_chat.go`）。

**硬限制**（与代码常量一致）：

| 项 | 上限 | 位置 |
|----|------|------|
| boot-assembler | 10000 runes | `maxBootLeaderRunes` |
| 单文件 brain 摘录 | 6500 bytes | `maxBrainExcerptBytesPerFile` |
| brain 摘录合计 | 20000 bytes | `maxBrainExcerptBytesTotal` |
| memory/index 注入 | 2800 bytes | `maxIndexPromptBytes` |
| 单 skill / 合计 | 8000 / 16000 bytes | `skills_prompt.go` |
| short-term 文件 | 96KB | `session_memory.go` |

### 自主演进

```
触发条件:
    short-term > shortTermTriggerBytes（有足够新内容）
    或 short-term 自上次演进后有变化 + >= shortTermActivityBytes
    或 archive 文件数 >= archiveSummarizeMinFiles

周期:
    默认 600s，由 evolve.cycle_interval 控制

动作:
    observe → LLM 决策 (idle|update|consolidate|crystallize) → 文档补丁 → 索引同步

无手动演进命令。
```

### 保护机制

```
内层循环:
    上下文超 context_window × 85% → 触发 session compress → 历史截断到 40%
    单轮最大 tool 轮次限制（隐式，由 token 预算控制）

工具执行:
    run_command 输出上限 256KB
    命令黑名单 + 用户确认
    路径遍历防护 (safePathUnder)

并发:
    同一产出区只能一个 chat session（output lock）
    同一机只能一个 server（socket 文件锁）
    记忆读写由 evolve 引擎串行化（单 goroutine）

记忆膨胀:
    short/current.md 上限 96KB → 触发 trim
    persona.md 超 6500 bytes → 触发 consolidate
    index.json 超 2800 bytes → 触发 summary 压缩
```

### 多模态（设计，未实现）

**目标**：终端 chat 可向模型附带**图片**（后续可扩 PDF/音频）；**换模型**时只改配置，不改业务代码路径。  
**非目标（v1）**：演进 LLM、Summarize、MCP 默认不走 vision；不做视频流；不在 `~/.cata` 脑子正文里存原图。

#### 现状缺口

| 层 | 今天 | 缺口 |
|----|------|------|
| `llm.Message` | `Content string` | 无法表达 OpenAI 式 `content: [{type,text},{type,image_url}]` |
| `messagesForChatCompletionsWire` | 一律 `content: string` | 出站前需按能力编码 |
| Socket `chat` | 仅 `text` | 无附件字段 |
| TUI | 纯文本输入 | 无粘贴图 / `@文件` |
| Token 估算 | 按字符 | 图片需单独预算 |
| `models` 按角色 | `chat` / `evolution` | 无「有图时用哪模型」策略 |

#### 能力模型（可切换多模态模型）

在 `~/.cata/config.json` 增加 **`llm.capabilities`**（按**模型名**键，与 `llm.model` / `llm.models.*` 解耦）：

```json
{
  "llm": {
    "provider": "openai",
    "api_url": "https://api.openai.com/v1/chat/completions",
    "model": "deepseek-v4-flash",
    "models": {
      "default": "deepseek-v4-flash",
      "chat": "deepseek-v4-flash",
      "chat_vision": "gpt-4o"
    },
    "capabilities": {
      "deepseek-v4-flash": { "modalities": ["text"] },
      "gpt-4o": {
        "modalities": ["text", "image"],
        "max_images_per_message": 4,
        "max_image_bytes": 10485760,
        "image_mime_allow": ["image/png", "image/jpeg", "image/webp", "image/gif"]
      }
    }
  }
}
```

**运行时选模型**（`internal/llm`，新符号 `ResolveChatModel(turn TurnInput)`）：

```
若本 turn 无附件 → models["chat"] ?? model
若本 turn 有图：
  若当前 chat 模型 capabilities 含 image → 继续用 chat 模型
  否则若配置了 models["chat_vision"] 且该模型支持 image → 换用 chat_vision
  否则 → 向客户端返回 error（NDJSON），不静默丢图
```

演进 / 摘要仍用 `RoleEvolution` / `RoleSummarize`，**不**因用户附图切换；`capabilities` 缺省时对未知模型保守视为 **仅 text**。

| 配置键 | 含义 |
|--------|------|
| `modalities` | `text` \| `image`（v2+ 可加 `audio`、`document`） |
| `max_images_per_message` | 单条 user 消息最多几张 |
| `max_image_bytes` | 单张解码后上限 |
| `image_mime_allow` | 白名单 MIME |

换供应商时：只改 `api_url` + `model` + `capabilities` 三条，**不改** server 工具循环与 socket 协议字段名。

#### 内容表示（`internal/llm`）

**原则**：内存 history 与 API wire **解耦**——history 存引用，出站再展开为 provider 格式。

```go
// 会话 history（socket 内存）— 建议新类型，与现有 string Content 并存一段时间
type UserTurn struct {
    Text          string
    AttachmentIDs []string // 指向 AttachmentStore
}

type Message struct {
    Role    string
    Content string // 仍用于 system / 纯文本 user / assistant / tool
    Media   []MediaRef `json:"media,omitempty"` // 仅 role=user 且为「多模态轮次」时使用
}

type MediaRef struct {
    ID   string
    MIME string
    // 可选：宽高等元数据，供 TUI 展示
}
```

**出站编码**（`Provider` 扩展或 `EncodeMessageContent(cap ModelCaps, m Message) any`）：

- **text-only 模型**：`content` 仍为 string；图片轮次在出站前**拒绝**或要求用户改配置（见上）。
- **vision 模型**：user 消息 `content` 为数组：
  - `{ "type": "text", "text": "..." }`
  - `{ "type": "image_url", "image_url": { "url": "data:<mime>;base64,..." } }`  
  兼容网关若要求 `image` 字段，由 `OpenAIProvider` 子策略或 `provider_variant` 配置切换（实现期再定，接口统一进 `Capabilities.WireImagePart`）。

`assistant` / `tool` / `system` **保持字符串**；模型返回的图片（若未来 API 支持）v1 只记文本描述，不进 history 二进制。

#### 附件生命周期

```
产出区路径 / TUI 粘贴 / 未来 MCP 截图
        ↓ ingest（校验 MIME、大小、路径 safePathUnder）
        ↓ AttachmentStore.Write → ~/.cata/cache/attachments/<ws>/<uuid>
        ↓ history 只保留 MediaRef{id,mime}
        ↓ 出站时 Read + base64 data URL（或供应商支持的 URL）
        ↓ chat_reset / 会话结束 → 按 TTL 或随 reset 清理 cache
```

| 规则 | 说明 |
|------|------|
| 可读路径 | 仅 **产出区**内相对/绝对路径 + 可选 `CATA_ATTACH_DIR` 白名单目录 |
| 禁止 | 直接读 `~/.cata/brain/**` 原图进模型（防泄露脑子全文） |
| short-term 记忆 | **只写** `text` + `[attachments: a.png, …]` 一行摘要，**不写** base64 |
| 会话压缩 | 优先裁掉**最早**带 `Media` 的 user 轮中的图片（保留文本）；或压缩任务里让 LLM 用文字描述图意后删 `Media` |
| evolve | 观察 short-term 的文本摘要即可，不读 cache |

#### Socket / Client 协议扩展

**请求**（`server.Request`，向后兼容）：

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
| `attachments[].inline` | TUI 粘贴/拖拽时用，server 落盘后进 Store |
| 上限 | 与 `max_images_per_message` 对齐，超出 → `error` 事件 |

**事件**（可选，便于 TUI）：

| `type` | 字段 | 说明 |
|--------|------|------|
| `attachment_rejected` | `reason`, `path` | 超限 / MIME / 路径非法 |
| `model_switch` | `from`, `to`, `reason` | 本 round 因附图切换 `chat_vision` |

**TUI（`internal/client`）**：

| 操作 | 行为 |
|------|------|
| `/attach <path>` | 加入待发送附件列表，底栏提示 `[1] k.png` |
| 粘贴（Phase 2） | 剪贴板 PNG → `inline` |
| 发送 | `text` + `attachments` 一并提交 |
| 主区展示 | 不嵌大图，一行 `[image: k.png 800×600 42KB]` |

#### 调用链（附图的一轮）

```mermaid
sequenceDiagram
    participant TUI as Client_TUI
    participant S as Server_socket_chat
    participant Store as AttachmentStore
    participant LLM as llm_Client

    TUI->>S: chat text + attachments[]
    S->>Store: ingest paths / inline
    Store-->>S: MediaRef ids
    S->>S: history += user{text, media}
    S->>LLM: ResolveChatModel(hasMedia)
    LLM->>LLM: EncodeMessageContent(caps)
    LLM->>LLM: ChatStreamRound injectBrain=true
    LLM-->>S: tokens / tool_calls
    S-->>TUI: NDJSON token / done
```

#### Token 与保护

- `EstimatedChatInputTokens`：文本仍用字符启发式；每张图加 **`image_token_estimate`**（配置项，默认按 512×512 tile 粗算，或固定 1000/token 占位直至接 tiktoken）。
- 上下文压缩阈值触发时：**先**尝试去掉旧轮 `Media` 再裁消息条数。
- `max_read_bytes` 与附件分离；单张图默认 ≤ 10MB（可配置）。

#### Provider 抽象（便于换模型）

```
Provider (现有)
  + CapabilitiesForModel(model string) ModelCaps
  + EncodeUserContent(m Message, caps ModelCaps) (wire any, err error)
```

DeepSeek / 千问 / OpenAI / 本地 vLLM 差异集中在 **EncodeUserContent**（如是否接受 `image_url`、是否必须 https URL）。业务代码只依赖 `llm.Message` + `MediaRef`。

#### 分阶段实现

| 阶段 | 内容 | 验收 |
|------|------|------|
| **M0** | `capabilities` 配置解析；`ResolveChatModel`；无附件时行为不变 | 改 config 换模型名即可 |
| **M1** | `AttachmentStore` + socket `attachments` + history `MediaRef` + wire 数组 | `chat` + JSON 附件路径可问图 |
| **M2** | TUI `/attach`、发送栏附件提示、`model_switch` 事件 | 交互式附图 |
| **M3** | 粘贴图、压缩策略、token 估算调优 | 长会话不爆窗 |
| **M4** | 文档页（PDF→图或 text extract）、音频 | 按所选模型 capabilities 启用 |

**建议改动的包**（实现时）：`internal/config`、`internal/llm`（Message、wire、tokens）、`internal/server`（ingest、history）、`internal/client`（TUI、session req）、新建 `internal/attachment`（可选）。

**刻意不做（与多模态相关）**：不把图片写入 `memory/short/current.md` 全文；不让 evolve 自动选 vision 模型；不在 v1 支持「管道 `cata chat < img.png`」。

---

### 刻意排除

- 无手动演进命令（纯后台自主运行）
- 无任务队列、无 scheduler
- 无内置 git 操作
- 无 Web UI
- 无多机分布式
- 无 `catacli` 独立二进制（统一 `cata` TUI）
