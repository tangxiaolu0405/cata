# 脑子文件说明与演进边界

> **双根**：home 格 `~/.cata/brain/workspaces/<ws_id>/`（运行时记忆）+ 项目 `focus_path/.cata/`（主要内容）。  
> 代码单一事实来源：`internal/brain/evolve_boundary.go`、`project_paths.go`、`chat_paths.go`。  
> 布局总览见 [directory-plan.md](./directory-plan.md)。

## 图例

| 列 | 含义 |
|----|------|
| **写入** | 谁创建/修改正文 |
| **Observe** | 演进 Observe 是否读（元数据或计数） |
| **Input** | 演进决策 prompt 是否可能读全文节选 |
| **Patch** | LLM `updates[]` 是否允许写入 |
| **Inject** | 终端 chat HTTP 出站是否注入 system |
| **Chat 工具** | `read_file` 等路径前缀 |

## 项目主要内容（`focus_path/.cata/`）

| 路径 | 作用 | 写入 | Observe | Input | Patch | Inject | Chat 工具 |
|------|------|------|---------|-------|-------|--------|-----------|
| `persona.local.md` | 项目说明 | evolve | | ✓ | ✓† | ✓ | `brain/persona.local.md` |
| `modes/<mode>/persona.md` | 身份、偏好 | evolve | ✓ | ✓ | ✓† | ✓ | `brain/modes/...` |
| `modes/<mode>/behavior.md` | 项目 SOP | evolve | | | ✓† | ✓* | `brain/modes/...` |
| `modes/<mode>/constraints.md` | 项目约束 | evolve | | | ✓† | ✓* | `brain/modes/...` |
| `modes/<mode>/capabilities.yaml` | skills / MCP | init + server | | | ✓‡ | 否 | `brain/modes/...` |
| `skills/<id>/*` | 技能与脚本 | evolve | | | ✓ | ✓ | `brain/skills/...` |

\* scaffold 空壳时不出现在 chat 节选，填满后注入。  
† 按场景选 patch 模式（见 `prompt/evolve/patch_modes.md`）：更新已有节 → `replace_section`；memory 流水 → `append`；fill/compact → `overwrite`。补丁后 server 自动 **compact** 去重。  
‡ evolve 禁止 `append` capabilities；`write`/`overwrite` 须保留 `mcp:`。

## home 脑子格（`~/.cata/brain/workspaces/<ws_id>/`）

| 路径 | 作用 | 写入 | Observe | Input | Patch | Inject | Chat 工具 |
|------|------|------|---------|-------|-------|--------|-----------|
| `memory/short/current.md` | 对话流水 | server + evolve | ✓ | ✓ | ✓ | 否 | `brain/memory/short/...` |
| `memory/long/*.md` | 长期细节 | evolve | ✓ | | ✓ | 经 index | `brain/memory/long/...` |
| `memory/archive/*.md` | 冷存储 | evolve | ✓ | **否** | ✓ | **否** | `brain/memory/archive/...` |
| `memory/index.json` | 记忆索引 | evolve 同步 | | | ✓ | ✓ | `brain/memory/index.json` |
| `meta.json` | ws 元数据 | server + evolve | | | ✓ | 否 | `brain/meta.json` |
| `evolution_log.json` | 演进审计 | evolve | ✓ | | ✓ | 否 | `brain/evolution_log.json` |

## CATA_HOME 全局（`global/*`）

| 路径 | 类型 | Patch | Inject | Chat 工具 |
|------|------|-------|--------|-----------|
| `global/constraints.md` | **引导** | **否** | ✓ | `global/constraints.md` |
| `global/behavior.md` | **引导** | **否** | ✓ | `global/behavior.md` |
| `global/boot-assembler.md` | **引导** | **否** | ✓（boot-leader） | `global/boot-assembler.md` |

## Chat 文件工具路径

| 前缀 | 解析 |
|------|------|
| （默认） | 产出区 output_cwd |
| `brain/memory/…`、`brain/meta.json` | home 脑子格 |
| `brain/modes/…`、`brain/persona.local.md`、`brain/skills/…` | 项目 `.cata/` |
| `global/…` | `~/.cata/global/` |

## 演进 patch 模式

| mode | 作用 |
|------|------|
| `replace_section` | **首选**：更新已有 `##` 节（项目 persona 等） |
| `append_section` | 新增尚不存在的 `##` 节 |
| `overwrite` / `write` | fill 空壳、compact 全文精简、多处大改 |
| `append` | **memory/** 流水；项目内容仅用于无法归入任何节的新增 |
| `delete` / `delete_section` | 删除文件或过时节 |

选用规则见 **`prompt/evolve/patch_modes.md`**（编入 evolve system prompt）。

## consolidate 与 compact

- **consolidate**：short-term → 项目 persona（按节 `replace_section`）；细节 → `memory/long/`（可 `append`）。
- **compact**（`compact:persona>=3500B` 等）：`overwrite` / `replace_section` 缩短；补丁后 `CompactMarkdown` 去重。

## 硬边界（代码强制）

1. **双根路由**：`ResolveBrainDocAbs` / `ApplyUpdates`。
2. **禁止** per-workspace evolve patch `global/*`。
3. **项目主要内容**：按 `prompt/evolve/patch_modes.md` 选模式；非一律禁止 append。
4. **capabilities.yaml**：禁止 evolve `append`；须保留 `mcp:`。
5. **archive**：写入后不再作为 evolve Input。

## 仓库 `brain/` 模板

| 文件 | 运行时对应 |
|------|------------|
| `constraints.md` | `~/.cata/global/constraints.md` |
| `behavior.md` | `~/.cata/global/behavior.md` |
| `boot-assembler.md` | `~/.cata/global/boot-assembler.md` |
| `modes/_default/*` | 项目 `.cata` scaffold 种子 |
