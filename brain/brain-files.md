# 脑子文件说明与演进边界

> 一格脑子：`~/.cata/brain/workspaces/<ws_id>/`。  
> 代码单一事实来源：`internal/brain/evolve_boundary.go`、`internal/brain/chat_paths.go`。  
> 布局总览见 [directory-plan.md](./directory-plan.md)。

## 图例

| 列 | 含义 |
|----|------|
| **写入** | 谁创建/修改正文 |
| **Observe** | 演进 Observe 是否读（元数据或计数） |
| **Input** | 演进决策 prompt 是否可能读全文节选 |
| **Patch** | LLM `updates[]` 是否允许写入 |
| **Inject** | 终端 chat HTTP 出站是否注入 system |
| **Chat 工具** | `read_file` 等是否可读写（路径前缀） |

## 一格脑子内文件

| 路径 | 作用 | 写入 | Observe | Input | Patch | Inject | Chat 工具 |
|------|------|------|---------|-------|-------|--------|-----------|
| `memory/short/current.md` | 对话流水 | server + evolve | ✓ | ✓ | ✓ | 否 | `brain/memory/short/current.md` |
| `modes/<mode>/persona.md` | 身份、偏好、习惯 | evolve | ✓ | ✓ | ✓ | ✓ | `brain/modes/...` |
| `persona.local.md` | focus_path 项目说明 | evolve | | ✓ | ✓ | ✓ | `brain/persona.local.md` |
| `modes/<mode>/behavior.md` | mode 级 SOP | evolve | | | ✓ | ✓* | `brain/modes/...` |
| `modes/<mode>/constraints.md` | mode 级约束 | evolve | | | ✓ | ✓* | `brain/modes/...` |
| `modes/<mode>/capabilities.yaml` | skills / MCP | init + server append | | | ✓‡ | 否 | `brain/modes/...` |
| `memory/long/*.md` | 长期细节 | evolve | ✓ | | ✓ | 经 index | `brain/memory/long/...` |
| `memory/archive/*.md` | 冷存储 | evolve | ✓ | **否** | ✓ | **否** | `brain/memory/archive/...` |
| `memory/index.json` | 记忆索引 | evolve 同步 | | | ✓ | ✓ | `brain/memory/index.json` |
| `skills/<id>/*` | 技能与脚本 | evolve | | | ✓ | ✓ | `brain/skills/...` |
| `meta.json` | ws 元数据 | server + evolve | | | ✓ | 否 | `brain/meta.json` |
| `evolution_log.json` | 演进审计 | evolve | ✓ | | ✓ | 否 | `brain/evolution_log.json` |

\* scaffold 空壳时不出现在 chat 节选，填满后注入。  
‡ evolve 禁止 `append` 改 capabilities；`write`/`overwrite` 须保留 `mcp:`；skill 名由 server `AppendSkillToCapabilities`。

## CATA_HOME 全局（虚拟路径 `global/*`）

| 路径 | 作用 | Patch | Inject | Chat 工具 |
|------|------|-------|--------|-----------|
| `global/constraints.md` | 全机硬规则 | **否** | ✓ | `global/constraints.md` |
| `global/behavior.md` | 全机 SOP | **否** | ✓ | `global/behavior.md` |
| `global/boot-assembler.md` | HTTP boot | **否** | ✓（boot-leader） | `global/boot-assembler.md` |

演进与 chat 均用虚拟路径 `global/...`；resolve 映射到 `~/.cata/global/`。

## Chat 文件工具路径

| 前缀 | 范围 |
|------|------|
| （默认） | 产出区 output_cwd |
| `brain/…` | 当前 workspace 脑子格 `~/.cata/brain/workspaces/<ws>/…` |
| `global/…` | `~/.cata/global/…` |

`run_command` / `run_skill` 仍在产出区执行。

## 演进 patch 模式

| mode | 作用 |
|------|------|
| `write` / `overwrite` | 整文件替换 |
| `append` | 文末追加 |
| `append_section` / `replace_section` | 按 `##` 节追加或替换 |
| `delete` | 删除文件 |
| `delete_section` | 删除指定 `##` 节 |

## 硬边界（代码强制）

1. **workspace 隔离**：per-workspace evolve 仅 patch 本格 `~/.cata/brain/workspaces/<id>/…`；**禁止** `global/*`（`RejectEvolveSharedGlobalPatch`）。
2. **路径穿越**：`..` 拒绝（evolve 与 chat 共用 `PathUnderBase`）。
3. **capabilities.yaml**：禁止 evolve `append`；`write`/`overwrite` 须保留 `mcp:`。
4. **archive**：写入后不再作为 evolve Input，不进入 memory index 注入。
5. **最小 patch 体积**：非 write/overwrite/delete 时 content ≥24 字（防噪声）。

## global/* 与 workspace 格子

| 区域 | 共享范围 | evolve | chat 文件工具 |
|------|----------|--------|---------------|
| workspace 格子 | 一 focus_path 一格 | ✓ 本格内全权限 | `brain/…` |
| `global/*` | **全机所有 workspace** | **禁止** | `global/…` |

若 A 项目 evolve 写入 `global/constraints.md`，B 项目 chat 也会注入该内容 — 故 global 不由 per-workspace evolve 维护。

## 仓库 `brain/` 模板

| 文件 | 运行时对应 |
|------|------------|
| `constraints.md` | `~/.cata/global/constraints.md` |
| `behavior.md` | `~/.cata/global/behavior.md` |
| `boot-assembler.md` | `~/.cata/global/boot-assembler.md` |
| `modes/_default/*` | workspace scaffold 种子 |

模板不参与 chat；`cata init` / `EnsureScaffold` 后按上表运行。
