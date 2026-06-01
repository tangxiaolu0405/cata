package evolve

// 默认周期与触发阈值（可在后续迁入 config.Evolution）。
const (
	DefaultCycleSeconds = 600 // 10 分钟
	// shortTermTriggerBytes 短期记忆足够大，应 consolidate 进 hot / long
	shortTermTriggerBytes = 4096
	// shortTermActivityBytes 短期有新增内容（相对上次演进），即可尝试更新 hot
	shortTermActivityBytes = 512
	longTermSummarizeMinFiles = 25 // 触发 summarize：long-term 文件过多时归档到 archive（冷存储）
	maxShortExcerptBytes    = 2400
	maxUpdatesPerCycle            = 6
	maxCrystallizeUpdatesPerCycle = 8
	minPatchContentRunes    = 24
	maxLogEntries           = 80
)
