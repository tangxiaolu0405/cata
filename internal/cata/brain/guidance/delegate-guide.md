### delegate_task / delegate_wait

Worker 无 persona/记忆/skills，只靠 **task + context** + output_cwd。

**task**：① 目标一句 ② 输入路径/命令（数据先落盘）③ 输出路径·格式·验收 ④ 可改/禁改范围。  
**context**：数据路径与 schema、已定决策、平台路径（Windows 用原生或相对路径）。

并行 ≤{{max_concurrent}}；建议 `tools` 白名单；事后 `delegate_wait`。禁：开放调研、ask_user、嵌套 delegate、多 worker 并行 browser。留痕：`{{csv_path}}`
