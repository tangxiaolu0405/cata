# 目录规划：引导 vs 主要内容 vs 产出区

## 三个世界

```
┌─ CATA_HOME ~/.cata/ ─────────────┐   ┌─ 项目 focus_path/.cata/ ─────┐   ┌─ 产出区 cwd ─────────┐
│ 引导 global/、运行时记忆、config   │   │ persona、modes、skills（主要内容）│   │ 源码、构建、命令结果   │
│ 不进用户 git（除用户自管 global）  │   │ 可随 git 提交（按需 .gitignore）  │   │ 进用户 git           │
└──────────────────────────────────┘   └──────────────────────────────────┘   └──────────────────────┘
              ▲                                      ▲
              │         focus_path 绑定 ws_id           │
              └────────────────────────────────────────┘
```

## ~/.cata 布局（CATA_HOME）

```text
~/.cata/
├── config.json
├── cata.sock
├── registry/workspaces.json
├── global/                         # 引导型提示词（evolve 禁止 patch）
│   ├── constraints.md
│   ├── behavior.md
│   └── delegate-guide.md
├── locks/
├── skills/                         # 全局 skill 回退目录
└── brain/workspaces/<ws_id>/       # home 脑子格（运行时记忆）
    ├── meta.json
    ├── evolution_log.json
    └── memory/
        ├── index.json
        ├── short/current.md
        ├── long/
        └── archive/
```

## 项目 focus_path/.cata/（主要内容）

```text
<focus_path>/.cata/
├── workspace.yaml                  # 可选：name、active_mode
├── workspace.link                  # 可选：id → home 格
├── persona.local.md                # 项目说明（evolve 维护）
├── modes/
│   ├── _default/                   # 默认前台 mode
│   └── <mode-id>/                  # 仅项目演化/结晶或用户自建，不预种演示岗
└── skills/<id>/
    ├── SKILL.md
    ├── manifest.yaml
    └── script.*
```

首次 `EnsureScaffold` 会将旧版 home 格内的 persona/modes/skills **迁移**到此目录（`workspace_migrate_project.go`）。

## focus_path 解析

1. 从 chat 传入的 cwd 向上找 `.git` → `KindGit`
2. 否则找 `.cata/workspace.yaml` → `KindMarked`
3. 否则 cwd → `KindEphemeral`

`focus_path` 只决定绑定哪格脑子（`ws_id`），不改变产出区位置。

## 仓库 `brain/` vs 运行时

| 位置 | 角色 |
|------|------|
| `internal/cata/brain/guidance/` | 引导层模板（constraints / behavior / delegate-guide）→ `cata init` → `~/.cata/global/` |
| `internal/llm/rolecards/` | 角色卡片（chat / worker / evolve 身份 + 协议，编译期 embed） |
| `~/.cata/` | 引导 + 运行时记忆 + config |
| `focus_path/.cata/` | 项目主要内容（evolve 迭代 **active_mode**） |
| cwd | 产出区 |

各文件演进边界见 **[brain-files.md](./brain-files.md)**（`internal/cata/brain/evolve_boundary.go`）。

## 命名约定

| 避免 | 改用 |
|------|------|
| "脑子 = 全部在 ~/.cata" | 引导+记忆在 ~/.cata；主要内容在 `focus_path/.cata/` |
| "workspace 在 home 里" | home 格 `<ws_id>` 在 `brain/workspaces/` |
| "brain 在项目里" | 项目 `.cata` 是主要内容，不是 CATA_HOME |

## 数据流

```
cata chat --dir <产出区>
    │
    ├─ focus_path → ws_id + 项目 .cata/
    ├─ LLM 注入：global 引导 + 项目内容 + memory index + skills
    ├─ 文件工具 / run_command → 产出区
    ├─ 每轮成功 → home memory/short/current.md
    └─ evolve → 提炼进项目 .cata；细节进 home memory/long；过大时 compact
```
