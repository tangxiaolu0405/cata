package llm

// WorkerRoundMeta 子 Agent LLM 轮次写入 llm.log 的关联字段。
type WorkerRoundMeta struct {
	SubagentID string
	SessionID  string
}
