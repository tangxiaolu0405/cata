# 定时任务（自托管调度框架）

cata 支持「到点自动跑一轮完整 chat」的**自托管**入口：调度框架（`cata schedule` 守护进程）
**发现环境里有哪些任务、什么时候执行**，到点后**作为真实 socket 客户端自行发起**一轮 chat
（与客户自己发起的行为一致）。典型场景：每天 09:00 让 cata 打开电商平台（browser MCP）
收集热门商品、整理候选清单，报告落盘，结果写进工作区短期记忆供后续 evolve 提炼。

## 入口（chat 内工具）

| 工具 | 作用 |
|------|------|
| `schedule_task` | 创建/更新一条排程：`name` + `prompt`，`cron`（5 字段）与 `interval`（如 `24h`/`30m`）二选一；可选 `output_dir` / `allow_exec` / `enabled` |
| `schedule_list` | 列出全部排程（id、cron/interval、enabled、next_run、last_run） |
| `schedule_remove` | 按 id 删除排程 |

示例：

```
schedule_task name=每日选品 prompt="去跨境电商平台看今日热门商品，整理候选清单写报告" cron="0 9 * * *"
schedule_task name=每小时巡检 prompt="检查本地服务状态并汇报" interval="1h" allow_exec=true
schedule_list
```

## 存储与发现（机器级 + 项目级）

排程分两级存放，调度框架扫描两者：

- **机器级** `~/.cata/schedules/<id>.json`：临时目录/未注册工作区创建的任务落这里。
- **项目级** `<project>/.cata/schedules/<id>.json`：在 git / workspace.yaml 标记的工作区里，
  `schedule_task` 会写项目级，**随项目 `.cata` 分发**（与 MCP/modes/skills 同源），不污染 `~/.cata`。

`cata schedule` 的环境发现：

1. 机器级 `~/.cata/schedules/`；
2. 每个**已注册工作区**（registry）的 `<root>/.cata/schedules/`（`--dir` 可额外注册项目根）。

同 id 出现在多处时机器级优先（每任务一个定义）。`schedule_list` 也走同一发现（`ListAll`）。

## 调度框架（`cata schedule`）

```
cata schedule              # 守护进程：发现任务，到点触发
cata schedule --once       # 扫描到点任务同步执行一轮后退出（可挂系统 cron）
cata schedule --dir ~/proj # 额外注册项目根（供项目级排程发现）
cata schedule --tick 15    # 扫描周期（秒；默认 config.schedules.tick_seconds=30）
```

- 守护进程按 tick 周期扫描；到点（`next_run <= now`）且未在运行（防重入）即触发。
- **触发即「客户端自发起」**：通过 `internal/cata/scheduler/runner` 拨号 server Unix socket，
  以真实 chat 客户端身份发一轮 chat（`run_as=scheduled`）；无 server 时本进程内嵌一个（`cata run` 语义），
  已有则复用（外部 managed server 退出后到点前会自动再拉起）。
- cron 支持 `*`、`*/n`、`a-b`、`a,b`，日/周为 OR 语义；interval 最小 1m。

## 执行行为（与真实客户一致）

- **完整工具档**：full 档，含 browser 等 MCP；**跳过任务状态机**（不进入 `declare_task` 的
  BeginOrResumeTask / LoadCurrentTask），避免污染前台任务。
- **无人值守应答**：`ask_user` 自动跳过、`user_choice` 全空、`run_command` 需 `allow_exec=true`
  否则自动拒绝（`exec_confirm` 直接批准）。
- **产出**：
  - 最终答复 → 报告 `<project>/.cata/schedule-runs/<id>/<ts>.md`（可用 `output_dir` 改到绝对目录）
  - 全程 NDJSON 事件 → 审计 `<存储目录>/runs/<id>/<ts>.jsonl`（机器级 `~/.cata/schedules/runs/…`，项目级 `<project>/.cata/schedules/runs/…`）
  - chat 循环照常写工作区短期记忆（`~/.cata/brain/workspaces/<id>/memory/short/current.md`）
  - `last_run` / `next_run` 回写到排程 JSON，`schedule_list` 可查

## 配置（`~/.cata/config.json`）

```json
"schedules": {
  "enabled": true,     // 是否允许 cata schedule 调度框架运行（缺省 true；false 时 --once/守护直接退出）
  "tick_seconds": 30   // 扫描周期
}
```

server 自身不再内嵌调度引擎（无 keep_alive 等保活配置）；排程执行完全由 `cata schedule` 守护进程承担。

## 已知限制

- 定时任务**跳过任务状态机**（不进入 `declare_task` 的 BeginOrResumeTask / LoadCurrentTask），避免污染前台任务。
- 与前台 chat 并行时，`ResolveWorkspace` / MCP `EnsureInit` 复用 server 既有的全局 Active 模式（与多 chat 并行行为一致）；
  极端并发下 MCP capabilities 缓存可能串到另一工作区，属已知边界。
- 错过的时间点（如机器休眠）只补触发一次，不做历史补跑队列。
- 时区统一走 `internal/cata/clock`（默认 Asia/Shanghai）。
- 机器级排程在 `~/.cata/schedules/` 不随 git 分发；项目级排程在 `<project>/.cata/schedules/` 可随项目走；
  报告在项目 `.cata/schedule-runs/`，可随项目走。

## 代码位置

- `internal/cata/scheduler/`：`Schedule` 定义/校验/存储（机器 + 项目）、cron 解析、触发引擎、环境发现（`ListAll`/`Find`）（纯逻辑，不依赖 server）
- `internal/cata/scheduler/runner/`：**客户端自发起** runner（真实 socket 拨号 + 自动应答 + 审计/报告）
- `internal/cata/socketclient/`：共享 socket 客户端协议（gateway 与调度框架共用；`ChatAs` 支持 `run_as=scheduled`）
- `cmd/cata/schedule.go`：`cata schedule` 守护命令（发现 + 到点触发 + 内嵌 server）
- `internal/cata/server/tools_schedule.go`：`schedule_task` / `schedule_list` / `schedule_remove`
- `internal/cata/server/socket.go` + `scheduled_ctx.go`：`run_as=scheduled` 请求 → full 档 + 跳过任务状态机
