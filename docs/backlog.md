# Cata 优化清单（Backlog）

> 记录 2026-08-29 代码审查发现的可优化点。状态：`todo` / `doing` / `done`。
> 完成项会附提交 hash；未完成项保持可执行描述，后续迭代直接照此实施。

## A. Server.Stop() 并发安全（真实 bug 风险）

- **状态**: `done`（提交 `c448aef` 引入 sync.Once；`096b603` 后的 `supervisor heartbeat` 修正见下）
- **问题**: `Server.Stop()` 有 5 个并发调用点，无 `sync.Once` 保护：
  1. idle 空闲回收（`internal/cata/server/server.go`）
  2. managed 空连接回收（`go s.Stop()`）
  3. agent 收到 SIGTERM/SIGINT（`setupSignalHandling`）
  4. keep-alive agent 的 supervisor 心跳兜底（`cmd/cata/main.go`）
  5. `s.socketSrv.Stop()`
- `mcp.Shutdown()` 有 `initMu` 锁、`s.cancel()` 幂等，但 `SocketServer.Stop()` 里 `ln.Close()` /
  `ln.Addr()` 无保护，并发触发可能重复 close / 异常或日志刷屏。
- **修法**: `Server` 增加 `stopOnce sync.Once`，`Stop()` 包 `once.Do`；补并发测试
  （`server_stop_test.go`：并发 Stop 幂等 + Stop 后 Wait 返回）。
- **后续修正（EOF 根因）**: 心跳兜底 `WatchSupervisorAndStop` 曾无条件在失联 30s 后
  stop，导致**正在进行的 chat/任务被误杀**（TUI EOF）。现在 `Busy` 配置（agent 传
  `srv.HasActiveChat`）使有活跃会话时推迟退出，空闲或 supervisor 恢复后才判；
  `Server.HasActiveChat()` 暴露会话状态。回归测试 `TestWatchSupervisorBusyDefersShutdown`。

## B. supervisor 心跳参数可配

- **状态**: `done`
- **问题**: `SupervisorWatchConfig` 默认 `Interval=5s` / `Deadline=30s` 硬编码
  （`internal/cata/link/supervisor_agent.go`），对延迟敏感或想更快收敛的场景不可调。
- **修法**: 环境变量 `CATA_SUPERVISOR_INTERVAL` / `CATA_SUPERVISOR_DEADLINE`（秒）覆盖，
  未设置回落默认；显式传入 cfg 优先于环境变量；保留测试注入 `AliveFn`。
  测试：环境覆盖 / 非法回落 / 显式优先。

## C. M4 遗留：PDF 文档页（agents.md §多模态 标 🟡）

- **状态**: `done`
- **方案**: server 侧 ingest 识别 `application/pdf`，用系统 `pdftotext`（poppler-utils）提取文本，
  作为 `attachmentDoc` 拼入 user 消息正文（`[pdf:name]\n<text>`），**不依赖模型 document
  modality**——任何文本模型都能读 PDF 内容。逐份截断 `pdfMaxExtractBytes`（256KB）防爆；
  `pdftotext` 缺失时拒绝并提示安装。记忆摘要只记文件名 `[pdf: …]`。
  测试：假 pdftotext 提取成功 / 缺失拒绝提示。

## D. 协议预留事件：file_written / diff（agents.md 标注预留）

- **状态**: `done`
- **方案**: `file_written` + `diff` 均已落地。
  - `maybeEmitFileWritten`：写入工具（create_file / append_file / search_replace）成功时
    解析 `resolved=` 路径发 `file_written{name,path,bytes,id}`；TUI 主区一行
    `✎ create_file → /abs/path (N bytes)`（`--quiet` 隐正文，侧栏详情常记）。
  - `diff`：每次工具执行派生独立 ctx + diff sink（`withFileDiffSink`，并行不串扰），
    写入工具成功后用 go-difflib 生成 unified diff（`emitFileDiff`，幂等）填入；
    `file_written` 事件附 diff 字段，TUI verbose 模式展示全文、auto 记入侧栏详情。
  - 解析器与 diff 生成均有单测。

## E. gateway 模式二/三 与 per-agent token（v2 架构级）

- **状态**: `per-agent token` 已完成；模式二/三被 remote（模式四）取代，设计归档不再实施
- **per-agent token（done）**：
  - 协议：hello 帧加 `agent_token`，新增 `hello_ack` 帧（网关下发 token）
  - 网关 `MachinesStore`：`machines`（机器）+ `agents`（agent→hash+machine_id）双表；
    `IssueAgentToken`（首次签发、已存在幂等）、`ValidateAgent`、`RevokeAgent`（单 agent 吊销）
  - 引导：机器首次注册 agent → 网关签发 token → hello_ack 下发 → worker 落盘 link.json
    （AgentEntry.AgentToken）→ 带 token 重连建正式隧道；旧 worker 回退 machine token 兼容
  - 测试：per-agent 签发/校验/吊销/持久化；handler 端到端 bootstrap（
    TestHandlerAgentTokenBootstrap）；link 落盘幂等
  - 遗留：网关 UI 按 agent 吊销入口（当前 API 有 RevokeAgent，UI 未接，可用 machines.json 手动删）

## F. 小型清理

- **状态**: `done`
- **子项**:
  - F1: `cmd/cata-desktop/terminal/render.go` 与 `internal/widget/termgrid.go` 的 `fyne.Do`
    注释改为准确说明——Fyne 要求 UI 刷新在主线程，这是**有意的调度**而非待修 bug，
    移除会造成跨线程 panic；补全注释不再误导。
  - F2: `cmd/cata-desktop/terminal/dcs.go` 的 DCS 应答按 DECRQSS 标准明确
    「以 0+r not-recognised 应答」的行为注释（避免程序等待应答挂起），移除 TODO。
  - F3: `internal/llm/tokens.go` 图片 token 估算边界复查：确认 `assembleSystemForRole`
    浅拷贝保留 `Media`（估算正确计入图片额度）；新增测试
    `TestEstimateMessagesTokensMedia` 固化「每图 +image_token_estimate」边界。