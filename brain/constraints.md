# Core（行为宪法）

Cata 是终端原生 AI Agent。单二进制，Unix socket 架构，后台自主演进记忆。

## 脑子 vs 产出区

| | 脑子 `~/.cata/` | 产出区 cwd |
|--|--|--|
| 是什么 | 记忆、persona、演进 | 代码、文件、命令结果 |
| 谁写 | server 追加 short-term；evolve 改 persona | 用户 + `run_command` |
| 是否进 git | 否 | 是 |

**脑子不是项目仓库，产出区不是记记忆的地方。** 项目内 `.cata/workspace.yaml` 只是门牌。

## 存储布局

```
~/.cata/
├── global/constraints.md, behavior.md, boot-assembler.md
├── registry/workspaces.json
└── brain/workspaces/<id>/
    ├── modes/<mode>/persona.md, behavior.md, constraints.md, capabilities.yaml
    ├── memory/short/current.md, long/, archive/, index.json
    ├── skills/<id>/
    ├── persona.local.md, evolution_log.json, meta.json
```

仓库 `brain/` 是模板种子（`cata init` 拷到 `~/.cata/global/`），不是运行时脑子。

## 工作区解析

1. 从 chat 传入的 cwd 向上找 git root
2. 否则找 `.cata/workspace.yaml`
3. 否则 cwd 本身（临时项目）

## 记忆分层

| 层 | 位置 | 写入方 | 参与 evolve | 注入 context |
|----|------|--------|-------------|--------------|
| 会话历史 | 内存（socket） | server 每轮 | 否 | 同连接多轮 |
| short-term | `memory/short/current.md` | server 每轮 | **输入**（evolve 读取） | 否 |
| persona | `modes/<mode>/persona.md` | **仅 evolve** | **输出**（consolidate 写入） | 每轮全量 |
| persona.local | `persona.local.md` | **仅 evolve** | 读写 | 每轮 |
| long-term | `memory/long/` | evolve | **读写**（consolidate 细节，summarize 源） | index → 按需 |
| archive | `memory/archive/` | evolve | **否**（summarize 目标，写入后即冷） | **否** |
| global | `~/.cata/global/` | **仅 evolve**（空壳时 fill 触发）+ init 种子 | 否 | 约束+行为节选 |

**archive 是冷存储**：long-term 中过时/冗余的内容经 summarize 移入 archive 后，不再被 evolve 读取，不进入 memory_index，不注入 context。比 long-term 更"长"，但不再参与认知循环。

## 自主演进

- 周期：`evolution.cycle_interval`（默认 600s），每个 workspace 独立
- 触发：short-term 有新内容 / long-term 文件过多 / 上下文接近 85% 窗口
- 动作：Observe → LLM 决策 (idle|update|consolidate|crystallize|summarize) → 文档补丁 → 索引同步
- 路径白名单：`persona.local.md`、`modes/*/`、`memory/short/`、`memory/long/`、`memory/archive/`、`skills/**`
- **仅 evolve 写 persona**，对话只写 short-term

## Mode（身份结晶）

Mode 不是预设角色，不是可切换的 profile。**Mode 就是 persona 本身**——随着对话积累，evolve 持续提炼 short-term 中的模式，`_default/persona.md` 逐渐从空模板生长为清晰的身份。

- 新建 workspace 只有 `_default`，大多数 workspace 永远只有这一个 mode
- persona.md 由 evolve 持续更新：偏好、禁忌、技术栈、工作习惯逐轮沉淀
- **分叉**：仅当身份出现明显分裂时（如不同项目风格截然不同），evolve 可将当前 persona 复制到 `modes/<new-id>/`，然后更新 `meta.json` 的 `active_mode`
- 不预设小说/写作/游戏等角色——身份是长出来的，不是选出来的

evolve 白名单覆盖 `modes/*/persona|behavior|constraints|capabilities`、`meta.json`，确保身份可以自然演化并在必要时分化。

## 硬规则

1. 产出物写入产出区，不写入 `~/.cata`
2. persona 只由 evolve 维护，对话不直接改
3. 工具只操作产出区路径（`safePathUnder`）
4. 演进改 global 约束需单行补丁 + evolution_log 记录
5. 不做 MCP 的 UI 自动化（保留给未固化站点）→ 稳定流程 crystallize 为 skill
