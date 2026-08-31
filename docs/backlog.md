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

- **状态**: `todo`
- **问题**: audio wire（`input_audio`）已落地；PDF 出站会报「需先转图/文本」（`internal/llm/capability.go`）。
- **修法**: 探测 `pdftotext` 系统命令做文本提取（缺失时仍报错提示），让 document modality 模型可用；
  或在 server 侧 ingest PDF 时预转文本。需确认模型 capabilities 语义后再动。

## D. 协议预留事件：file_written / diff（agents.md 标注预留）

- **状态**: `todo`
- **问题**: server 从未发 `file_written` / `diff` 事件，TUI 也未接。
- **修法**: 定义事件格式 → server 在 `create_file`/`append_file` 后发 `file_written`（path/bytes），
  可选 `diff`；TUI 在主区/侧栏展示。中价值，需定协议。

## E. gateway 模式二/三 与 per-agent token（v2 架构级）

- **状态**: `todo`
- **问题**: `docs/gateway.md` 模式二/三预留；v2 逐 agent token、按项目路由（web 会话已按项目）。
- **修法**: 架构级，工作量大，暂不动，留档。

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