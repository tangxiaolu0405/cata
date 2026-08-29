package server

import (
	"cata/internal/cata/config"
	"cata/internal/cata/link"
	"cata/internal/cata/secrets"
)

// serverRedactor 进程级敏感值脱敏器：登记运行时已知 secret
// （环境变量疑似 secret + config.APIKey + link.json machine_token/gateway_token），
// 工具结果进 history（→ LLM）与 llm.log 前统一掩盖。
var serverRedactor = secrets.New(8)

// initServerRedactor 收集本机已知 secret 填入脱敏器。调用方在启动时执行一次。
func initServerRedactor() {
	red := secrets.New(8)
	// 1. 环境变量里的疑似 secret（API_KEY / TOKEN / SECRET / PASSWORD …）。
	for _, v := range secrets.CollectFromEnv() {
		red.Add(v)
	}
	// 2. config.json 的 LLM api_key。
	if cfg := config.Config; cfg != nil {
		red.Add(cfg.LLM.APIKey)
	}
	// 3. link.json 的逐机器 token（若本机已 join 网关）。
	if lc, err := link.LoadConfig(); err == nil {
		red.Add(lc.MachineToken)
		red.Add(lc.GatewayToken)
	}
	serverRedactor = red
}
