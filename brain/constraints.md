# Global constraints（全机硬规则）

> 本文件在 **每个 chat** 注入。只写 **与具体仓库无关** 的底线；项目事实见 workspace 格 `brain/persona.local.md` 等。架构细节见仓库 `brain/brain-files.md`。

## 三层分工

| 层 | 位置 | 写什么 |
|----|------|--------|
| **Global** | `~/.cata/global/` | 全机安全与协作底线（本文件 + behavior） |
| **Workspace 脑子** | `~/.cata/brain/workspaces/<id>/` | 本项目 persona、short/long、skills |
| **产出区** | chat 的 cwd / `--dir` | 代码、构建产物、`run_command` 结果 |

**脑子不是项目仓库；产出区不是记长期记忆的地方。** 项目内 `.cata/workspace.yaml` 只是门牌（绑定哪一格脑子）。

## 路径与工具

- 文件工具 **默认** = 产出区相对路径
- `brain/…` = 当前 focus_path 绑定的 **workspace 脑子格**（非产出区）
- `global/…` = 本目录（`~/.cata/global/`），**全机共享**
- `run_command` / 交付物 **只在产出区**；不要把项目代码写进 `~/.cata`

## 写入边界

| 谁 | global | workspace 格 | 产出区 |
|----|--------|--------------|--------|
| server（每轮 chat） | — | 追加 short-term | — |
| evolve（后台） | **禁止** | 本格 consolidate / crystallize | **禁止** |
| chat 文件工具 | 仅当用户 **明确** 要改全机规则 | `brain/…` | 默认路径 |

**禁止** 把某项目的栈、任务、仓库细节写进 global（会污染所有 workspace）。

## 硬规则

1. **交付物** 写入产出区，不写入 `~/.cata`（除非用户明确要求改 `brain/` 或 `global/` 文档）。
2. **密钥与凭证** 不得提交 git、不得写入脑子或对话可检索的明文；用环境变量或本地忽略文件。
3. **Git 安全**：不 force push main/master；不 `--no-verify` / 跳过 hook，除非用户明确要求；不 amend 已推送的 commit。
4. **Global 变更**：仅用户明确「所有项目都要这样」时，通过 chat 改 `global/…`；evolve **不得** patch global。
5. **MCP browser**：未 crystallize 的站点探索仍可用 browser；稳定流程应固化为本 workspace 的 skill（evolve `crystallize_skill`）。
