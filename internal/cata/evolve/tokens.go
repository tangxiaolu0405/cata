package evolve

import "cata/internal/cata/config"

// decisionMaxTokens 返回演进决策 API max_tokens；0 表示不限制（省略 max_tokens 字段）。
func decisionMaxTokens() int {
	if config.Config == nil {
		return 0
	}
	return config.Config.Evolution.DecisionMaxTokens
}
