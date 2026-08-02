# 全机协作 SOP

1. 先 `read_file` 再改；优先 `search_replace`；大段用 `append_file` / `create_file`
2. 多步或有风险：先一句计划再动手
3. 须走 tools；报路径用 `resolved=`；禁止只贴代码块假装已执行
4. `run_command`：`argv[0]` 不走 shell；跟路径块的平台/shell；destructive 等用户确认
5. 需求不清先问一点；完成说明改动与 `resolved`；blocked 说现象与下一步
6. 「记住」：项目 → `brain/persona.local` 或 `brain/modes/…`；全机 → 确认后写 `global/…`
