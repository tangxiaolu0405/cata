# 目录规划：脑子 vs 产出区

## 两个世界

```
┌─ 脑子 CATA_HOME（默认 ~/.cata/）──┐   ┌─ 产出区 cwd ──────────────────┐
│ 记忆、persona、演进、config        │   │ 源码、文件、构建产物、run_command │
│ 不进用户 git，本机私有             │   │ 进用户 git                      │
└────────────────────────────────────┘   └────────────────────────────────┘
              ▲
              │ focus_path（git root / workspace.yaml / cwd）决定用哪格脑子
              │
```

## ~/.cata 完整布局

```text
~/.cata/                            # CATA_HOME
├── config.json                     # LLM、exec、evolution 配置
├── cata.sock                       # Unix socket
├── registry/
│   └── workspaces.json             # focus_path → workspace_id 索引
├── global/                         # 全机级（种子来自仓库 brain/）
│   ├── constraints.md              # ← brain/constraints.md
│   ├── behavior.md                 # ← brain/behavior.md
│   └── boot-assembler.md           # ← brain/boot-assembler.md
├── locks/                          # 产出区锁（同目录只允许一个 chat）
└── brain/workspaces/<ws_id>/       # 一格脑子
    ├── meta.json                   # focus_path、kind、active_mode
    ├── persona.local.md            # 对当前关注对象的说明
    ├── evolution_log.json          # 本 workspace 演进审计
    ├── memory/
    │   ├── index.json              # 摘要索引（常驻 context）
    │   ├── short/current.md        # 对话流水（每轮追加）
    │   ├── long/                   # 长期记忆（evolve 读写，参与 context index）
    │   └── archive/                # 冷存储（summarize 移入，不参与 evolve 和 context）
    ├── modes/
    │   ├── _default/               # 默认 mode（新建 workspace 必有）
    │   │   ├── persona.md          # mode persona（evolve 维护）
    │   │   ├── behavior.md         # mode 行为覆盖
    │   │   ├── constraints.md      # mode 约束覆盖
    │   │   └── capabilities.yaml   # skills: [...] + mcp: [...]
    │   └── <mode-id>/              # 由 evolve 分叉或用户创建
    └── skills/<id>/
        ├── SKILL.md                # 技能说明（注入 context）
        ├── manifest.yaml           # 执行声明
        └── script.{py,sh,...}      # 脚本（cwd=产出区执行）
```

## focus_path 解析

1. 从 chat 传入的 cwd 向上找 `.git` → `KindGit`
2. 否则找 `.cata/workspace.yaml` → `KindMarked`
3. 否则 cwd → `KindEphemeral`

`focus_path` 只决定用哪格脑子，不改变产出区位置。monorepo 子目录干活时脑子仍绑 git 根。

## 项目内 `.cata/`

```text
<项目>/.cata/
├── workspace.yaml    # 可选：name、active_mode
└── workspace.link    # 可选：id: ws_xxx → 指向 ~/.cata/brain/<id>
```

- 可提交 git：`workspace.yaml`、`workspace.link`
- 不可提交：persona、short-term 正文（始终在 `~/.cata`）

## 仓库 `brain/` vs 运行时 `~/.cata/`

| 位置 | 角色 |
|------|------|
| `mybot/brain/constraints.md` | 约束模板 → `cata init` 拷到 `global/constraints.md` |
| `mybot/brain/behavior.md` | 行为模板 → `cata init` 拷到 `global/behavior.md` |
| `mybot/brain/boot-assembler.md` | 引导模板 → `cata init` 拷到 `global/boot-assembler.md` |
| `~/.cata/` | 运行时脑子（live，由 server + evolve 维护） |
| cwd | 产出区 |

各文件作用与 **evolve 可 patch / 只读 / 注入** 边界见 **[brain-files.md](./brain-files.md)**（与 `internal/brain/evolve_boundary.go` 对齐）。

## 命名约定

| 避免 | 改用 |
|------|------|
| "工作区 = ~/.cata" | 脑子 = ~/.cata，产出区 = cwd |
| "workspace 在 home 里" | 脑子分区 `<ws_id>` 在 `~/.cata/brain/workspaces/` |
| "brain 在项目里" | 项目里只有 `.cata` 声明；脑子在 home |

## 环境变量与配置

| 项 | 作用 |
|----|------|
| `CATA_HOME` | 脑子根（默认 `~/.cata`） |
| `brain.base_dir` | 产出区根（exec、文件工具） |
| `llm.api_key` / `DEEPSEEK_API_KEY` | LLM 密钥 |

## 数据流

```
cata chat --dir <产出区>
    │
    ├─ focus_path 解析 → 选中 ~/.cata/brain/workspaces/<id>/
    ├─ LLM 注入：global + mode persona + persona.local + memory index + skills
    ├─ run_command / 文件工具 → 产出区
    ├─ 每轮成功 → AppendChatTurn → memory/short/current.md
    └─ 上下文 ≥ 85% → evolve 压缩 → persona / long
```
