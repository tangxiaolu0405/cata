# CLAW / MyBot — 项目级 AI 上下文（`agents.md`）

本文档定义**终端优先个人 Agent** 的目标与实现边界，与当前 Go 代码对齐。

# 最重要的事情：严格遵守第一性原理
---

## 愿景

**终端原生 AI 助手**：编排与记忆可审计、可 fork；推理外置为可配置 HTTP LLM；主入口为 `cata`（默认 `chat`）流式对话。

---

## 目录边界（重要）

见 **`brain/directory-plan.md`**（布局）与 **`brain/brain-files.md`**（各文件作用与 evolve 边界，代码：`internal/cata/brain/evolve_boundary.go`）。

| 位置 | 角色 |
|------|------|
| **`~/.cata/`** | **CATA_HOME**：引导型提示词（`global/`）、运行时记忆（`brain/workspaces/<id>/memory`）、config、socket、registry |
| **`focus_path/.cata/`** | **项目主要内容**：`persona.local`、`modes/<mode>/*`、`skills/<id>/*`、`workspace.yaml` |
| **当前工作目录 cwd** | **产出区**：代码与命令结果；`run_command` 与默认文件工具在此 |
| **仓库 `brain/`** | 模板种子（`cata init` → `~/.cata/global/`；`cata initconfig` 种子 config.json） |

`focus_path`（git 根 / `.cata/workspace.yaml` / cwd）决定绑定哪一格脑子（`ws_id`），**不把产出存进 `~/.cata`**。

**双根写入**：
- **引导**（`~/.cata/global/`）：constraints、behavior、boot-assembler — 用户或 `cata init` 维护；**evolve 不写**
- **主要内容**（`focus_path/.cata/`）：persona、modes、skills — **server + 后台 evolve** 维护
- **运行时记忆**（`~/.cata/brain/workspaces/<id>/`）：short/long/archive、index、evolution_log — server 追加 + evolve 提炼

首次 `ResolveWorkspace` → `EnsureScaffold` 会把旧版 home 格内的 persona/modes/skills **迁移**到项目 `.cata/`。

---

## 代码布局（internal）

| 包 | 角色 |
|----|------|
| **`internal/cata/`** | 核心 agent：`server`、`client`（TUI）、`brain`、`evolve`、`config`、`clock`、`execcmd` |
| **`internal/gateway/`** | 渠道适配子模块（Telegram / QQ）→ 同一 Unix socket worker |
| **`internal/llm/`** | OpenAI 兼容 Chat；出站前注入 boot-assembler + brain 节选 |
| **`internal/mcp/`** | MCP 客户端（browser 等） |

## 当前实现

| 区域 | 作用 |
|------|------|
| **`cmd/cata`** | `init`（初始化 ~/.cata）、`initconfig`（种子 config.json）、`config`、`run`（socket + 后台演进） |
| **`cmd/cata`（`chat`）** | 默认 Bubble Tea TUI；协议：`chat`（`stream:true`）、`chat_reset`、`ping` |
| **`cmd/cata-gateway`** | 渠道入口；凭证驱动并发启渠道 |
| **`cmd/cata-pet`** | 可选桌宠客户端（Wails + `cmd/cata-pet/pet`）；同一 socket，不改 server |
| **`internal/cata/server`** | Unix socket、终端 chat 工具循环 |
| **`internal/llm`** | OpenAI 兼容 Chat；出站前注入 **boot-assembler** + **brain 节选**（见下文「提示词组装」） |
| **`internal/cata/brain`** | 双根路径（`project_paths.go`）、工作区解析、终端节选 |
| **`internal/cata/evolve`** | **仅后台**自主演进：Observe → LLM → 文档补丁 → `evolution_log.json`（无手动 CLI） |
| **`internal/cata/scheduler`** | 自托管调度框架：排程（机器 `~/.cata/schedules/` + 项目 `<root>/.cata/schedules/`）、cron / interval、环境发现、到点触发（`cata schedule` 守护 + `--once`）；执行走 `scheduler/runner` 真实客户端自发起（见 `docs/schedules.md`） |
| **`internal/cata/socketclient`** | 共享 socket 客户端协议（gateway 与调度框架共用；`ChatAs(…, runAs="scheduled")`） |
| **`internal/cata/config`** | `~/.cata/config.json`：LLM、exec、`evolution.enabled` / `cycle_interval` / `short_term_trigger_bytes`、`schedules.enabled` / `tick_seconds` |

**已移除**：`internal/memory`、`internal/evolution`（旧任务引擎）、`internal/git`、`skills/` 服务端加载。

---

## 记忆流（摘要）

| 层 | 物理位置 | 写入方 |
|----|----------|--------|
| Socket 会话历史 | server 内存 | 每轮对话（`chat_reset` 清空） |
| `memory/short/current.md` | home 脑子格 | **每轮 cata chat 成功后 server 规则追加**（`session_memory.go`） |
| `modes/<active_mode>/persona.md` 等 | 项目 `.cata/` | **仅** `internal/cata/evolve` 从 short-term 提炼 |
| `long-term/`、`archive/` | home 脑子格 | evolve |

详见 **`brain/constraints.md` §记忆分层**。

## 自主演进（摘要）

- **触发**：short-term 有新内容等门控（见 `internal/cata/evolve`）；默认周期 600s。
- **patch 路由**：主要内容 → `focus_path/.cata/`（**active_mode**）；记忆/审计 → home 格；**禁止** `global/*`
- **防膨胀**：按场景选 patch 模式（`replace_section` / `append` / `overwrite`，见 `prompt/evolve/patch_modes.md`）；超 3.5KB 触发 `compact:*`；补丁后自动去重。
- **无** 手动 `cata evolve` 命令。

---

## 多模态（设计，未实现）

终端 chat 计划支持**图片附件**（路径 / 粘贴），并按 **`llm.capabilities` + `models.chat_vision`** 在纯文本模型与 vision 模型间切换；history 存附件引用，出站前编码为 OpenAI 式 `content[]`。详见 **`design.md` §多模态**。

## LLM（DeepSeek）

- **provider**：`deepseek`（[OpenAI 兼容](https://api-docs.deepseek.com/zh-cn/)，代码走 `OpenAIProvider`）
- **默认**：`https://api.deepseek.com/chat/completions`，模型 `deepseek-v4-flash`（更强用 `deepseek-v4-pro`）
- **密钥**：`llm.api_key` 或 `DEEPSEEK_API_KEY`
- **`llm.thinking`**：`auto`（默认，有 tools 时 `disabled`，避免 tool 轮次 400）、`enabled`、`disabled`
- 思考模式 + tool 调用时须回传 `reasoning_content`（已实现）；见 [Thinking Mode](https://api-docs.deepseek.com/guides/thinking_mode)
- **仅 DeepSeek / MiMo 类网关**会下发非标准 `thinking` / `reasoning_content`；OpenAI、Gemini OpenAI 兼容层等通用端点不发这些字段，换模型只需改 `api_url` / `model` / `api_key`（`api_format=openai`）。`api_url` 可写 base；缺路径时运行时探测并记住。
- 原千问配置备份在 `~/.cata/config.json` → `llm_previous_qwen`（不参与加载）

## MCP 与 Skill（已接入）

- **MCP browser**：默认**不**在 capabilities 启用；`~/.cata/config.json` → `mcp.servers` 可配 Playwright，仅当 `modes/*/capabilities.yaml` 含 `mcp: [browser]` 时才会连接。未安装时失败只记日志，对话继续。小红书见 **`docs/mcp-browser.md`**。
- **双模型**：`llm.models.chat`（主对话）/ `evolution` / `worker`（`delegate_task`，可选 `mode_id`）；未配置时回退 `llm.model`
- **委派**：`delegate_task` 无 `mode_id` = 廉价 worker；带 `mode_id`+`case_id` = 专职 mode（`delegate_mode` 为其别名）。主 chat 会注入【专职 Modes】目录；有专职时首轮至少 standard（含委派工具）。`crystallize_mode` 会在 `_default/behavior.md` 登记委派路由。
- **长期记忆**：`memory/long/learnings.md` 等除 index 外注入【长期记忆节选】近条；需要全文时 `read_file brain/memory/long/…`。
- **evolve**：对外主行动 `consolidate` / `crystallize`（+ `crystallize_mode`）；`mode_evolve`/`orch_evolve` 仍为别名，Observe 读 `mode_buckets` 内部路由
- **run_skill**：执行项目 `.cata/skills/<id>/` 的 `manifest.yaml` + 脚本（cwd=产出区）；由演进 `crystallize_skill` 固化。
- **crystallize_skill**：高 token / 重复 browser 任务后，evolve 写 `skills/<id>/` 到**项目 `.cata`** 并自动 append capabilities；下次 chat 生效。
- **api_url**：可写 base 或完整路径；运行时会试「原样 / +默认路径」，成功后写入 `~/.cata/api_url_resolved.json` 记住。

## 定时任务（自托管调度框架）

- **入口**：chat 内工具 `schedule_task` / `schedule_list` / `schedule_cancel` / `schedule_remove`（Standard/Full 档）；排程定义落在**机器级** `~/.cata/schedules/<id>.json` 或**项目级** `<project>/.cata/schedules/<id>.json`（git/workspace.yaml 工作区写项目级，随项目 `.cata` 分发），id 由名称稳定生成（保留中文/字母/数字）。
- **调度框架**：`cata schedule` 守护进程**发现环境里的任务**（机器级 + 所有已注册工作区的项目级，`ListAll`），按 `schedules.tick_seconds`（默认 30s）扫描；到点且未在运行即触发（错过只补一次，无历史补跑队列）。`schedule_task` 创建/启用任务时自动拉起守护（`EnsureDaemonRunning`，`setsid` + `~/.cata/schedules/daemon.sock` 单例锁，日志 `daemon.log`）——**chat 里指定后不用管，后台自动执行**；`cata schedule --once` 可挂系统 cron。
- **执行**：到点后由 `internal/cata/scheduler/runner` **作为真实 socket 客户端自发起**一轮 chat（`run_as=scheduled`，与客户自己发起一致）；`ask_user` 自动跳过、`user_choice` 全空、`run_command` 需 `allow_exec=true`。产出：报告 `<project>/.cata/schedule-runs/<id>/<ts>.md`（可 `output_dir` 改绝对目录）+ 审计 `<存储目录>/runs/<id>/<ts>.jsonl`；chat 循环照常写短期记忆。
- **server 不内嵌调度**：managed server 不再因排程保活；执行由独立 `cata schedule` 守护承担（无 server 时内嵌一个，已有则复用）。
- **工作区边界**：排程绑定创建它的工作区；`schedule_list` / `schedule_cancel` / `schedule_remove` 只作用于当前工作区（项目级=当前项目根；机器级=创建它的 `ws_id`），不跨工作区管理；执行也在任务自己的 `cwd`/`ws_id` 下进行。守护的环境发现仍全环境（机器级 + 所有已注册工作区）。
- **边界**：定时任务跳过任务状态机（不污染前台 `declare_task`）；与前台并行时复用 server 全局 Active 模式（同多 chat 并行行为）。详见 **`docs/schedules.md`**。

## 卫星客户端（非核心环）

| 入口 | 角色 |
|------|------|
| `cata chat` TUI | **核心**交互 |
| `cata-gateway`（Telegram / QQ / Web UI） | 渠道适配 → 同一 socket |
| `cata-pet` | 桌面宠物 UI → 同一 socket |

改核心优先 `internal/cata/{server,client,evolve,brain}` + `internal/llm`；渠道与 pet 默认不进必读路径。

## 产出区（Output Area / Workspace）

**原则**：程序不一定运行在产出区。用户可在任意目录启动 `cata`，通过 `--dir` 指定干活的目标目录。

```
cata chat                         # 产出区 = 当前目录（默认）
cata chat --dir ~/project         # 产出区 = ~/project
cata chat --dir ~/a --dir ~/b     # 多产出区，第一个是主产出区
```

**参考 Claude Code**：
- Claude 的 launch dir → cata 的 cwd 或第一个 `--dir`
- Claude 的 `--add-dir` → cata 的多个 `--dir`
- Claude 不允许 `--cwd` 改变工作目录；cata 走自己的 `--dir` 方案

**脑子绑定**：
- 产出区用于文件工具和 `run_command` 的操作范围
- home 脑子格（`~/.cata/brain/workspaces/<id>/`）由 `focus_path` 决定；**主要内容**在 `focus_path/.cata/`
- 多产出区共享同一个脑子（若属于同一 git 项目）
- 不同产出区可并行开多个 chat（各自独立 output lock）

**实现要点**：
- Client 将 `--dir` 解析后的路径作为 `cwd` 发给 Server
- Server 的 `ResolveWorkspace(cwd)` 逻辑不变
- 文件工具 `safePathUnder` 检查每个产出区都在允许范围内

---

## 提示词组装（与代码对齐）

终端 **socket history** 只存 `user` / `assistant` / `tool`；**系统提示在 HTTP 出站前**由 `internal/llm` 注入（`buildHTTPChatRequest(..., injectBrain=true)` → `withBootLeaderSystemMessage`）。详见 **`design.md` §Context 组装**。

| 顺序 | API `messages[]` | 来源（代码） |
|------|------------------|--------------|
| ① | `system` boot-assembler | `loadBootLeaderPrompt()` ← `brain.BootLeaderPath()` → 优先 `~/.cata/global/boot-assembler.md`，否则 `brain/boot-assembler.md`；≤10000 码点；**只含身份/优先级/交互，不复述路径表** |
| ② | `system` brain 节选 | `brain.TerminalBrainSystemExtension()`（单条 system，内部分段） |
| ③+ | `user` / `assistant` / `tool` | `internal/cata/server/socket_chat.go` 内存 history |
| 并行 | `tools[]` | 内置工具 + MCP（不经 messages 拼接正文） |

**② brain 节选**（`internal/cata/brain/terminal_context.go`）自上而下：

1. **路径块** — `TerminalPathsSystemBlock()`：**仅**本轮绝对路径与本机环境（勿与 constraints/behavior 重复 SOP）
2. **Skills** — `SkillsPromptBlock()`：读项目 `capabilities.yaml` 的 `skills`，查找 `SKILL.md`（项目 → `~/.cata/skills/` → `~/.cursor/skills-cursor/`）
3. **记忆索引** — `MemoryIndexPromptBlock()` ← home 格 `memory/index.json`（≤2800 bytes）
4. **【Cata 引导 · ~/.cata/global】** — `constraints.md`（写入边界）、`behavior.md`（协作 SOP）；evolve 不写
5. **【Cata 项目内容 · focus_path/.cata】** — `modes/<active_mode>/persona|behavior|constraints`、`persona.local.md`

**演进 LLM**（`internal/cata/evolve`）：自带 `system` + `user`；经 `ChatEvolution` 且 **`NoBrainInject`**，**不**再前置 ①②。

**其它 LLM 调用**：`Summarize` / 查询预处理等走 `chat()` 时仍会带上 ①②。

**日志拆解**：`internal/llm/prompt_log.go`（`LLM_LOG_FILE`）；组件 id：`boot-leader`、`brain-excerpt`、`conversation`、`tools`。

---

## 交互层：Bubble Tea TUI

`cata chat` 使用 **Bubble Tea** 全屏 TUI（`internal/cata/client/tui.go`）：主区滚动对话、底栏 `›` 输入、宽屏 **右侧状态栏**（`tui_stats.go`，≥96 列；`CATA_NO_SIDEBAR=1` 关闭）。**不再**使用 stdout/stderr 分流，故不支持 `cata chat "…" > file` 管道模式。

### Server → Client（NDJSON）

`internal/cata/server/socket_chat.go` → `emitStreamLine`；TUI 在 `stream.go` 消费。

| `type` | TUI |
|--------|-----|
| `token` | 主区流式正文 |
| `tool_*` / `progress` / `error` | 主区或侧栏 |
| `exec_confirm_required` | 列表菜单 Run/Cancel |
| `user_choice` | 列表菜单（↑↓/j/k，Enter） |
| `stats` | 刷新右侧栏 |
| `done` | 结束本轮；`cancelled`=Ctrl+C |

斜杠：`/help` `/status` `/clear` `/exit` `/retry` `/config`。

**预留**（server 未发或未接）：`file_written`、`diff`。已落地：`display` 分级（tool_start/tool_result 带 `level`；TUI `--quiet`/`--verbose`）、`thinking` 事件（`cata chat --show-thinking` 展示模型推理）。

### 推理/思考内容

- Server 在流式轮次收集 `reasoning_content` 并写入 **history**（供 DeepSeek tool 轮次回传）；客户端带 `--show-thinking` 时实时下发 `thinking` 事件，默认不展示
- DeepSeek `llm.thinking`：`auto` / `enabled` / `disabled`（`internal/llm/provider.go`）

---

## 刻意排除

- **旧 `skills/` 服务端调度、`scripts/` 主线**：已废弃；仅保留 MD 提示词加载。
- **手动演进命令、任务队列、MemoryManager 索引**：已废弃（注意：`internal/cata/scheduler` 是**定时触发引擎**，不是任务队列；其执行由 `cata schedule` 守护进程驱动，见上文「定时任务」）。

---

## 给 AI 的约束

1. 改核心先看 **`internal/cata/`**（`server` / `client` / `evolve`）与 **`cmd/cata`**；改渠道看 **`internal/gateway/`**；桌宠看 **`cmd/cata-pet`**（含 `pet/` 子包与 `frontend/`）。
2. 路径以 **`internal/cata/brain/project_paths.go`**、`paths.go`、`context_paths.go` 为准；产出区 = chat 请求的 `cwd`（`--dir` 时 client 会 `chdir` 到主产出区）。
3. **同机一个 server**（`cata` 自动 `run --managed` 或手动 `cata run`）；**同一产出区目录只能开一个 chat**；**最后一个 chat 断开**后 managed server 自动退出。
4. 勿虚构路径；勿把仓库 `brain/`（模板）与 `~/.cata`（运行时）混为一谈；勿把 focus_path 当成产出区；**主要内容**在 `focus_path/.cata/`，不在 home 脑子格。

---

## 建议阅读顺序

1. 本文件  
2. **`design.md`**（架构、Context 组装、NDJSON 协议）  
3. `~/.cata/global/constraints.md`（或仓库模板 `brain/constraints.md`）  
4. 提示词代码：`internal/llm/client.go`、`internal/cata/brain/terminal_context.go`、`internal/llm/prompt_log.go`  
5. 对话循环：`internal/cata/server/socket_chat.go`、`internal/cata/client/tui.go`  
6. 演进：`internal/cata/evolve/engine.go`  

