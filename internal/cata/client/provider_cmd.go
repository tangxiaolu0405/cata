package client

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"cata/internal/cata/config"
	"cata/internal/llm"
)

// handleProviderCmd TUI /provider 命令（在 matchSlash 前拦截，支持子命令参数）：
//
//	/provider                       列出已注册 provider 与探测状态
//	/provider probe <name>          自动探测（成功写回；失败保留既有配置）
//	/provider switch <name> [model] 切换激活 provider（走 server socket，热生效）
func (m *model) handleProviderCmd(args string) {
	fields := strings.Fields(args)
	sub := ""
	if len(fields) > 0 {
		sub = strings.ToLower(fields[0])
	}
	switch sub {
	case "", "list":
		m.providerList()
	case "probe":
		if len(fields) < 2 {
			m.appendLog(styleErr.Render("usage: /provider probe <name>\n"), true)
			return
		}
		m.providerProbe(fields[1])
	case "switch":
		if len(fields) < 2 {
			m.appendLog(styleErr.Render("usage: /provider switch <name> [model]\n"), true)
			return
		}
		model := ""
		if len(fields) >= 3 {
			model = fields[2]
		}
		m.providerSwitch(fields[1], model)
	default:
		m.appendLog(styleErr.Render("unknown /provider subcommand: "+sub+"\n"), true)
	}
}

func (m *model) providerList() {
	providers, err := config.LoadLLMProviders()
	if err != nil {
		m.appendLog(styleErr.Render("! "+err.Error()+"\n"), true)
		return
	}
	names := make([]string, 0, len(providers.Providers))
	for n := range providers.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("providers:\n")
	for _, n := range names {
		p := providers.Providers[n]
		mark := " "
		if n == providers.Active {
			mark = "*"
		}
		probe := "unprobed"
		if p.Probe.ProbedAt != "" {
			if p.Probe.ProbedError != "" {
				probe = "failed"
			} else {
				probe = fmt.Sprintf("%d models", len(p.Probe.Models))
			}
		}
		model := p.Model
		if model == "" {
			model = "-"
		}
		fmt.Fprintf(&b, "  %s %-14s %-10s %s\n", mark, n, model, probe)
	}
	b.WriteString("  /provider switch <name> [model] — probe & activate\n")
	m.appendLog(styleDim.Render(b.String())+"\n", true)
}

func (m *model) providerProbe(name string) {
	providers, err := config.LoadLLMProviders()
	if err != nil {
		m.appendLog(styleErr.Render("! "+err.Error()+"\n"), true)
		return
	}
	prov, ok := providers.Providers[name]
	if !ok {
		m.appendLog(styleErr.Render(fmt.Sprintf("! provider %q not found\n", name)), true)
		return
	}
	m.appendLog(styleDim.Render(fmt.Sprintf("probing %s (%s)…\n", name, prov.APIURL)), true)
	rep, ok := llm.ProbeAndPersist(context.Background(), name, true)
	if !ok {
		m.appendLog(styleErr.Render("! probe failed; existing config kept\n"), true)
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "probe %s: %d models\n", name, len(rep.Models))
	for _, mm := range rep.Models {
		mods := ""
		if c, ok := rep.Capabilities[mm]; ok {
			mods = strings.Join(c.Modalities, ",")
		}
		fmt.Fprintf(&b, "  %-36s [%s]\n", mm, mods)
	}
	m.appendLog(b.String()+"\n", true)
}

func (m *model) providerSwitch(name, model string) {
	text := name
	if model != "" {
		text += " " + model
	}
	m.appendLog(styleDim.Render(fmt.Sprintf("switching to %s…\n", name)), true)
	r, err := m.sess.call(req{Command: "provider_switch", Text: text})
	if err != nil {
		m.appendLog(styleErr.Render("! "+err.Error()+"\n"), true)
		return
	}
	if !r.Success {
		m.appendLog(styleErr.Render("! "+r.Message+"\n"), true)
		return
	}
	m.appendLog(r.Message+"\n", true)
	if m.stats.effectiveModel != "" {
		m.stats.effectiveModel = "" // 下轮 stats 事件会更新
	}
}
