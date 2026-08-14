package main

import (
	"context"

	"cata/internal/cata/link"
)

// startAgentTunnel 由 `cata agent --link` 调用：agent 进程持有到网关的 WSS 隧道
// （internal/cata/tunnel）。未配置网关时静默跳过（本地模式不受影响）。
func startAgentTunnel(agentID string) error {
	cfg, err := link.LoadConfig()
	if err != nil {
		return err
	}
	if !cfg.TunnelEnabled(agentID) {
		return nil
	}
	return link.RunTunnelWorker(context.Background(), agentID)
}
