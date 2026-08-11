# 全机硬规则

- 产出区（默认相对路径 / `run_command`）：代码与交付物
- `brain/persona.local` · `brain/modes/…` · `brain/skills/…` → `<focus_path>/.cata/`
- `brain/memory/…` → `~/.cata/brain/workspaces/<id>/`
- `global/…` → `~/.cata/global/`（改前须用户明确同意；evolve 禁写）

禁止：把项目栈/任务写进 global；把 skills 写进 home 格。

## MCP（manage_mcp 决策规则）

- 全局已有同名定义 → 只启用当前项目 `capabilities.yaml` 的 mcp 段，**不重复写全局**
- 跨项目通用 / 绑用户本机（browser、全局 CLI）→ 全局 `~/.cata/config.json` `mcp.servers` 定义一次 + 项目启用
- 绑项目数据源/内部服务 → 仍走全局定义（无项目级定义机制）+ 项目启用，注明项目专属
- 写全局 config.json 前**必须**用户确认（Run/Cancel）；项目 `.cata` 属 cata 正常写入区，无需确认
- 后台 evolve **永不写** config.json；evolve 也不 append capabilities 的 mcp 段（保留既有边界）
