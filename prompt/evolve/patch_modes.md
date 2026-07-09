# patch 模式选用（updates[].mode）

按**意图**选模式，不要默认一律 append。

## replace_section（项目 persona / persona.local / behavior / constraints 的首选）

**何时用**
- 更新**已有** `##` 节（如 Who I am、Preferences、Current focus、Current goals）
- 修正、合并、替换该节内的旧表述
- **consolidate**：把 short-term 提炼进 persona 时，按节写入，避免全文重复

**字段**：`section` = 节标题（不含 `##`）；`content` = 该节**完整新正文**（非增量补丁）

## append_section

**何时用**
- 需要**新增一个 ## 节**，且文件中**尚不存在**同名节
- 若节已存在，server 行为等同 `replace_section`

## overwrite / write

**何时用**
- `fill:*`：空壳文档**首次填满**
- `compact:*`：全文过长，需**结构性精简**（删节、重排、合并）
- 多处同时大改，逐节 replace 条数不够时

**慎用**：不要为单条新事实 overwrite 整文件。

## append（全文文末追加）

**何时用**
- `memory/long/*.md`、`memory/short/current.md`：**流水/日志**天然适合 append
- 项目主要内容：**仅当** (1) 纯新增信息、(2) 与现有各节**不重复**、(3) 不适合归入任何已有 `##` 节时，才可文末 append
- 若新事实属于某节 → 用 `replace_section`，不要用 append

**不要 append 的情况**
- 重复已有 bullet / 同义表述
- 要改/删/合并旧内容（用 replace_section 或 overwrite）
- `compact:*` 轮次（须缩短，用 overwrite / replace_section）

## delete_section

**何时用**
- 删除已过时的 `##` 节（任务结束、偏好已变）

## 路径与模式对照（简表）

| 路径 | 常规增量 | 更新已有事实 | 首次填充 / 全文精简 |
|------|----------|--------------|---------------------|
| modes/…/persona.md | replace_section | replace_section | overwrite（fill/compact） |
| persona.local.md | replace_section | replace_section | overwrite（fill/compact） |
| modes/…/behavior.md | replace_section | replace_section | overwrite |
| memory/long/*.md | **append** 或新文件 write | replace_section（单文件内） | overwrite |
| memory/short/current.md | append / write | overwrite（归档后） | — |

**原则**：项目主要内容 = **结构化文档**（按 ## 节维护）；memory = **日志型**（可 append）。
