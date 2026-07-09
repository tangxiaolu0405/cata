package brain

import (
	"fmt"
	"strings"

	"cata/internal/config"
)

// SubagentDelegateGuideBlock 主 Agent 委派 worker 的简明准则（注入路径块）。
func SubagentDelegateGuideBlock() string {
	max := 4
	if cfg := config.Config; cfg != nil && cfg.Subagent.MaxConcurrent > 0 {
		max = cfg.Subagent.MaxConcurrent
	}
	var b strings.Builder
	b.WriteString("### delegate_task / delegate_wait（worker 子 Agent）\n\n")
	b.WriteString("- **用途**：父 Agent 已规划好、边界清晰的**有界执行**（读文件、跑命令、改指定文件、run_skill）；用 **worker 模型**降成本，可并行最多 ")
	b.WriteString(fmt.Sprintf("%d", max))
	b.WriteString(" 个。\n")
	b.WriteString("- **task** 须含：目标、具体路径/输入、完成标准；**context** 传父 Agent 已知事实，避免 worker 重复探索。\n")
	b.WriteString("- **tools** 建议白名单（如 `read_file`,`run_command`）；默认受 `subagent.default_tools` 约束（未配置则全套）。\n")
	b.WriteString("- **禁止**：开放式调研、向用户提问、嵌套 `delegate_task`；**browser/MCP 勿并行**多个 worker（易争用会话），浏览器任务由父 Agent 串行或单 worker 执行。\n")
	b.WriteString("- 委派后务必 **`delegate_wait`**：`ids` 指定 sub-id 可拉**本会话已完成**任务摘要；`all:true` 汇总本会话全部 worker；仅省略 ids 则只等仍在跑的。\n")
	b.WriteString("- 留痕：`")
	b.WriteString(SubagentRunsCSVPathActive())
	b.WriteString("`\n")
	return b.String()
}
