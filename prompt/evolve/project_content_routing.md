# 项目 .cata 三文件互斥路由（必遵）

同一事实**只写一处**。补丁前对照 decision prompt 里已有的 `persona.local` / `mode persona` / `mode behavior` 节选，**禁止**把已有内容复制到另一文件。

| 写什么 | 唯一归属 | 禁止出现在 |
|--------|----------|------------|
| 项目是什么、数据源 URL、产出目录、技术栈 | `persona.local.md` → `## Project` / `## Tech stack` | persona.md、behavior.md |
| 会变的运行态（最新交易日、当日统计） | `persona.local.md` → `## Current snapshot`（整节 replace） | persona.md |
| 助手自称、语气、身份 | `modes/…/persona.md` → `## Who I am` | persona.local、behavior |
| 用户偏好、禁忌、选股风格 | `modes/…/persona.md` → `## Preferences & taboos` | persona.local、behavior |
| 流水线步骤、输出格式、文件命名、公众号排版 | `modes/…/behavior.md` | persona.local、persona.md |
| 子 agent / 工具踩坑 | `memory/long/sub-agent-failures.md` | 三文件 |
| 可复用细节、历史教训 | `memory/long/learnings.md` 或定向 `memory/long/*.md` | 勿重复进三文件 |

## consolidate 分派

- short-term 里的**身份/偏好** → `modes/<active_mode>/persona.md`（按 ## 节 replace_section）
- short-term 里的**项目事实** → `persona.local.md`
- short-term 里的**流程/格式/SOP** → `modes/<active_mode>/behavior.md`
- **不要**把 `Current goals` 当成 persona 默认节；目标性流程一律进 behavior

## 常见错误（禁止）

- 在 `persona.local` 写「小C自称」或输出格式 → 应在 persona / behavior
- 在 `persona.md` 写数据源 URL、交易日统计 → 应在 persona.local
- 在 persona 与 behavior **两处**写明日预测表头规范 → 只保留 behavior 一处
- 用 append 把同义 bullet 堆进多个文件

## compact 轮次

`compact:*` 时除缩短单文件外，**跨文件去重**：若 behavior 已有格式节，从 persona / persona.local 删除重复表述；若 persona.local 已有 Project，从 persona 删除项目描述类 bullet。
