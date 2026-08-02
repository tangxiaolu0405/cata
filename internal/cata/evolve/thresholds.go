package evolve

import "cata/internal/cata/config"

// 默认周期与触发阈值；short-term 字节阈值可由 config.evolution 覆盖。
const (
	DefaultCycleSeconds = 600 // 10 分钟
	// defaultShortTermTriggerBytes short-term 达到此大小应 consolidate（config 未设时）。
	defaultShortTermTriggerBytes = 16384
	// defaultShortTermActivityBytes short-term 有一定内容即可尝试演进（config 未设时）。
	defaultShortTermActivityBytes = 512
	longTermSummarizeMinFiles     = 25 // 触发 summarize：long-term 文件过多时归档到 archive（冷存储）
	maxShortExcerptBytes          = 2400
	maxUpdatesPerCycle            = 6
	maxCrystallizeUpdatesPerCycle = 8
	minPatchContentRunes          = 24
	maxLogEntries                 = 80
)

// ShortTermTriggerBytes 短期记忆「够大、应 consolidate」阈值（字节）。
// 来自 evolution.short_term_trigger_bytes；≤0 时用默认 16KiB。
func ShortTermTriggerBytes() int {
	if config.Config != nil && config.Config.Evolution.ShortTermTriggerBytes > 0 {
		return config.Config.Evolution.ShortTermTriggerBytes
	}
	return defaultShortTermTriggerBytes
}

// ShortTermActivityBytes 短期记忆「有活动、可尝试演进」阈值（字节）。
// 来自 evolution.short_term_activity_bytes；≤0 时用默认 512。
func ShortTermActivityBytes() int {
	if config.Config != nil && config.Config.Evolution.ShortTermActivityBytes > 0 {
		return config.Config.Evolution.ShortTermActivityBytes
	}
	return defaultShortTermActivityBytes
}
