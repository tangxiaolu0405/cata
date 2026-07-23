# Global constraints（全机硬规则）

> 本文件在 **每个 chat** 注入。只写 **与具体仓库无关** 的底线；项目事实见 **`focus_path/.cata/`**（`brain/persona.local.md` 工具路径）。架构见仓库 `brain/brain-files.md`。

## 三层分工

| 层 | 位置 | 写什么 |
|----|------|--------|
| **Global（引导）** | `~/.cata/global/` | 全机安全与协作底线（本文件 + behavior + boot-assembler） |
| **项目主要内容** | `<focus_path>/.cata/` | persona.local、modes/、skills/（工具路径 `brain/persona.local.md` 等） |
| **运行时记忆** | `~/.cata/brain/workspaces/<id>/` | short/long/archive、index（工具路径 `brain/memory/…`） |
| **产出区** | chat 的 cwd / `--dir` | 代码、构建产物、`run_command` 结果 |

**persona.local 不在 `~/.cata/brain/workspaces/<id>/`**，而在 **focus_path 下的 `.cata/persona.local.md`**。system 路径块会给出当前绝对路径。

## 路径与工具

- 文件工具 **默认** = 产出区相对路径
- `brain/persona.local.md`、`brain/modes/…`、`brain/skills/…` → **项目** `<focus_path>/.cata/`
- `brain/memory/…`、`brain/meta.json` → **home** `~/.cata/brain/workspaces/<id>/`
- `global/…` = `~/.cata/global/`（全机共享引导）
- `run_command` / 交付物 **只在产出区**

## 写入边界

| 谁 | global | 项目 .cata | home 记忆 | 产出区 |
|----|--------|------------|-----------|--------|
| server（每轮 chat） | — | — | 追加 short-term | — |
| evolve（后台） | **禁止** | 本 workspace 主要内容 | 本格 memory | **禁止** |
| chat 文件工具 | 用户明确要改全机规则时 | `brain/persona.local`、`brain/skills/…` 等 | `brain/memory/…` | 默认路径 |

**禁止** 把某项目的栈、任务、仓库细节写进 global（会污染所有 workspace）。
**禁止** 把 skills 写到 `~/.cata/brain/workspaces/<id>/skills`（应用 `brain/skills/<id>/` → 项目 `.cata/skills/`）。
