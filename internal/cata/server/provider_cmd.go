package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cata/internal/cata/config"
	"cata/internal/llm"
)

// handleProviderSwitch socket 命令「provider_switch」：切换激活 provider。
//   - Text 形如 "<name>" 或 "<name> <model>"
//   - 该 provider 无探测/探测过期 → 自动补探（探测失败保留既有配置，仍可切换连接）
//   - 可选指定主模型
//   - 切换后重载全局 config：下一轮 chat 的 llm.NewClientForRole 立即使用新 provider
func (ss *SocketServer) handleProviderSwitch(req Request) Response {
	name := strings.TrimSpace(req.Text)
	model := ""
	if fields := strings.Fields(name); len(fields) > 1 {
		model = fields[1]
		name = fields[0]
	}
	if name == "" {
		return Response{Success: false, Message: "provider_switch requires <name> [model]"}
	}
	providers, err := config.LoadLLMProviders()
	if err != nil {
		return Response{Success: false, Message: fmt.Sprintf("load providers: %v", err)}
	}
	prov, ok := providers.Providers[name]
	if !ok {
		return Response{Success: false, Message: fmt.Sprintf("provider %q not found (use `cata config provider list`)", name)}
	}
	if config.ProviderProbeExpired(prov.Probe.ProbedAt, 0) || prov.Probe.ProbedError != "" {
		// 缺失 / 过期 / 上次失败 → 自动重探（失败则保留既有配置，仍可切换连接）。
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		llm.ProbeAndPersist(ctx, name, true)
	}
	if model != "" {
		if err := config.SetProviderModel(name, model); err != nil {
			return Response{Success: false, Message: fmt.Sprintf("set model: %v", err)}
		}
	}
	if err := config.ActivateProvider(name); err != nil {
		return Response{Success: false, Message: fmt.Sprintf("activate: %v", err)}
	}
	// ActivateProvider 已把 cfg 写回全局 Config；再 LoadConfig 归一默认值/环境覆盖。
	if _, err := config.LoadConfig(); err != nil {
		return Response{Success: false, Message: fmt.Sprintf("reload config: %v", err)}
	}
	msg := fmt.Sprintf("switched to provider %q (model %s)", name, config.Config.LLM.Model)
	if v := config.Config.LLM.Models["chat_vision"]; v != "" {
		msg += fmt.Sprintf(", chat_vision %s", v)
	}
	return Response{Success: true, Message: msg}
}
