### delegate_task / delegate_wait（worker · minimal 脑子）

Worker **没有** persona/constraints/记忆/skills 节选，只靠你写的 **task + context** + output_cwd。委派须自洽、可执行。

**task**（必填，执行单）须含：
1. 目标（一句话）
2. 输入：文件路径或命令（**禁止**粘贴大段数据/网页正文；数据应先落盘再给路径）
3. 输出：相对 output_cwd 的路径、格式、验收标准（行数/列/编码等）
4. 边界：只改哪些文件、禁止动什么

**context**（强烈建议）传父 Agent 已掌握的事实：
- 原始数据文件路径、schema/列名、分隔符
- 已做决策（语言/库/命令模板）
- 平台路径约定（Windows 用 `D:\...` 或相对路径，勿写 `/mnt/d/...` 除非 WSL shell）

**示例**
```
task: 读取 context 中的 raw.json，按列顺序 … 写出 zhangtingban_analysis/2026-07-10/today_zt_data.csv；验收：UTF-8、表头一致、92 行。
context: raw.json 路径=…；父 agent 已 browser 落盘；勿再打开网页。
tools: ["read_file","create_file","run_command"]
```

- 并行上限 {{max_concurrent}}；**tools** 建议白名单；委派后 **`delegate_wait`** 收摘要。
- **禁止**：开放式调研、ask_user、嵌套 delegate；多 worker 勿并行 browser。
- 留痕：`{{csv_path}}`
