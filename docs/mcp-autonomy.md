# MCP 自主管理（全局定义 + 项目启用）— 实现与验收

> 目标：让 cata 自己接手 MCP server 的安装/启用，同时**保持现状双层模型不变**：
> - **定义层（怎么启动）**：`~/.cata/config.json → mcp.servers`（`MCPServerEntry`：command/args/env），全局唯一
> - **启用层（是否启动）**：`focus_path/.cata/modes/<mode>/capabilities.yaml → mcp: [名]`（`AllowsMCPServer` 判定）
>
> **明确不做**：项目级 `.cata/mcp.yaml` 定义 + 全局/项目合并机制（已确认保持现状）。
> **明确禁止**：后台 evolve 写 `~/.cata/config.json`（`evolve_boundary.go` 中 `global/*` `EvolvePatch: false`，evolve 对 config 只读）。

## 决策规则（chat 内 LLM 安装时遵循）

| # | 情况 | 动作 |
|---|------|------|
| 1 | 全局已有同名 server 定义 | 只启用：项目 `capabilities.yaml` 加名字；**不**重复写全局 |
| 2 | 跨项目通用 / 绑用户本机（browser、全局 CLI、用户级认证） | **全局**定义 + 项目启用 |
| 3 | 绑项目数据源 / 项目内服务 / 项目内凭据 | 项目启用声明；定义仍走全局兜底（无项目定义机制），记入 memory 备注 |
| 4 | 无法判断 / 用户未表态 | 默认全局定义 + 项目启用；先可用，后迁移 |

规则已写入 `internal/cata/brain/guidance/constraints.md`（模板）+ `~/.cata/global/constraints.md`（运行时，T4）并内嵌进 `manage_mcp` 工具描述。

## capabilities.yaml 的 mcp 语义（disable 关键）

- **显式含 `mcp:` 节**（`mcp: []` / 空节 / `mcp: [a, b]`，支持多行 `- x` 与内联 `[a, b]`）→ **以列表为准**；空列表 = 不启用任何 MCP（`manage_mcp disable` 的落点）
- **没有 `mcp:` 节 / 文件缺失** → 兼容默认 `browser`（老项目，`ParseCapabilitiesYAML` / `LoadCapabilitiesFor` 补齐）
- `AllowsMCPServer` 空列表返回 false（不再“空 = 全放行”）；默认 browser 由 Load/Parse 层在“无 mcp 节”时补齐

> ⚠️ 行为变化：现有项目若 capabilities.yaml 是 `mcp: []`，将不再默认启用 browser（与 AGENTS.md“浏览器默认不在 capabilities 启用”一致）。
> 想要 browser 的项目执行 `manage_mcp enable browser` 即可。

## 实现状态（T1–T5）

### T1 — chat 内 `manage_mcp` 工具 ✅
- **位置**：`internal/cata/server/tools_mcp.go`（handler）；`tools_builtin.go` 注册；`chat_tools_tier.go` `contextTierStdExtra` 加入（Standard/Full 档，主 chat 才有；worker 白名单天然排除）
- **动作**：
  - `install`：复用 `exec_confirm_required` 让用户 Run/Cancel 确认 → 写全局 `mcp.servers`（`UpsertMCPServer` 同名去重）→ `SaveConfig` + `LoadConfig` → 项目启用 → `mcp.Reload`（免重启）
  - `enable` / `disable`：只写项目 `capabilities.yaml` mcp 段（cata 正常写入区，不需确认）；disable 写空 mcp 节 = 禁用全部
  - `list`：全局定义 + 当前项目已启用
- **边界**：写全局前**必须**用户确认；同名去重；串行执行（`chatToolParallelSafe`）
- **审计**：`[mcp-manage] action=… server=… by=chat` 日志；工具结果进入对话 history/short-term memory

### T2 — config 热重载 ✅
- **实现**：`internal/mcp/manager.go` 新增 `Reload(caps)`（先 `shutdownLocked` 再 `Init`，自持 `initMu`）与 `ForceInit()`；`manage_mcp` 写完后 `config.LoadConfig()` + `Reload`
- **验收**：安装后无需重启即生效（工具返回 `MCP reloaded`）

### T3 — capabilities mcp 段 server 端写 ✅
- **实现**：`internal/cata/brain/capabilities_merge.go` 新增 `AppendMCPToCapabilities` / `RemoveMCPFromCapabilities`（复用 `FormatCapabilitiesYAML` + `maxCapabilitiesFileBytes` 上限；去重）
- **语义**：显式空 mcp 节 = 禁用全部（见上）；evolve 仍被 `RejectCapabilitiesPatch` 拦在 mcp 段外

### T4 — 判定规则注入提示词 ✅
- **实现**：`internal/cata/brain/guidance/constraints.md`（模板）+ `~/.cata/global/constraints.md`（运行时）追加决策规则；`manage_mcp` Schema 描述内嵌规则

### T5 — evolve 边界保持 + 记忆 ✅
- **保持**：evolve 不写全局 config.json（`global/*` `EvolvePatch:false`，evolve 代码无 config 写入）；evolve 不 append capabilities 的 mcp 段（`RejectCapabilitiesPatch` 不变）
- **记忆**：安装/启用在对话 history + short-term memory 可见；审计日志落 server log

## 测试

- `internal/cata/brain/capabilities_merge_test.go`：Append/Remove 保留 mcp 段、去重、显式空 mcp 禁用全部、无 mcp 节默认 browser
- `internal/cata/config/mcp_manage_test.go`：`FindMCPServer` 大小写不敏感、`UpsertMCPServer` 同名不重复写
- `internal/mcp/manager_test.go`：`Reload`/`ForceInit` 禁用配置 → 空 manager
- `internal/cata/server/tools_mcp_test.go`：install（确认后写全局+启用、取消不写）、enable/disable/list、同名只启用项目
- `internal/cata/server/socket_chat_tools_test.go`：`manage_mcp` 必须串行

## 验收（对照）

1. chat 里说「装 browser MCP」→ 确认后：全局 `config.json` 出现 server 定义、当前项目 `capabilities.yaml` 出现 `mcp: [browser]`、**无需重启即生效** ✅（组件+集成测试覆盖 install 新 server / 同名只启用项目）
2. 重复安装不重复写全局 ✅（`UpsertMCPServer` 同名合并）
3. 同名已存在时只启用项目，不改全局 ✅（`TestManageMCPInstallExistingOnlyEnablesProject`）
4. 后台 evolve 永不触碰 `config.json` ✅（evolve 代码无 config 写入；`global/*` `EvolvePatch:false`）
