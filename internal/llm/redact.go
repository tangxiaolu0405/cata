package llm

import (
	"cata/internal/cata/config"
	"cata/internal/cata/secrets"
)

// llmRedactor llm 侧敏感值脱敏器：登记环境变量疑似 secret + config.json 的 api_key。
// appendLLMLog 写盘前对已序列化的 JSON 字节做掩盖，防止密钥进入 llm.log。
var llmRedactor = secrets.New(8)

func init() {
	red := secrets.New(8)
	for _, v := range secrets.CollectFromEnv() {
		red.Add(v)
	}
	if cfg := config.Config; cfg != nil {
		red.Add(cfg.LLM.APIKey)
	}
	llmRedactor = red
}

// redactLogBytes 对 llm.log 的 JSON 行做脱敏（掩盖已知 secret 的明文出现）。
func redactLogBytes(data []byte) []byte {
	out := llmRedactor.Redact(string(data))
	return []byte(out)
}
