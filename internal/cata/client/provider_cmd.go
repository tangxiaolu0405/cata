package client

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"cata/internal/cata/config"
	"cata/internal/llm"
)

// handleProviderCmd TUI /provider 命令（在 matchSlash 前拦截）：
//
//	/provider                       打开 provider 列表菜单（选项式：选 provider → 自动探测 → 选模型）
//	/provider probe <name>          文本快捷探测（异步，结果打主区）
//	/provider switch <name> [model] 文本快捷切换（兼容）
//
// 主路径是菜单：/provider 后无需手输名字/模型，全部选项式。
func (m *model) handleProviderCmd(args string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(args)
	sub := ""
	if len(fields) > 0 {
		sub = strings.ToLower(fields[0])
	}
	switch sub {
	case "", "list":
		return m.openProviderPicker()
	case "probe":
		if len(fields) < 2 {
			m.appendLog(styleErr.Render("usage: /provider probe <name>\n"), true)
			return m, nil
		}
		return m.startProbe(fields[1])
	case "switch":
		if len(fields) < 2 {
			m.appendLog(styleErr.Render("usage: /provider switch <name> [model]\n"), true)
			return m, nil
		}
		model := ""
		if len(fields) >= 3 {
			model = fields[2]
		}
		m.providerSwitchNow(fields[1], model)
		return m, nil
	default:
		m.appendLog(styleErr.Render("unknown /provider subcommand: "+sub+"\n"), true)
		return m, nil
	}
}

// openProviderPicker 打开 provider 列表菜单（● = 当前激活）。
func (m *model) openProviderPicker() (tea.Model, tea.Cmd) {
	providers, err := config.LoadLLMProviders()
	if err != nil {
		m.appendLog(styleErr.Render("! "+err.Error()+"\n"), true)
		return m, nil
	}
	names := make([]string, 0, len(providers.Providers))
	for n := range providers.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 {
		m.appendLog(styleDim.Render("no providers registered — 首次启动会自动迁移 & 探测\n"), true)
		return m, nil
	}
	var items []list.Item
	for _, n := range names {
		p := providers.Providers[n]
		mark := " "
		if n == providers.Active {
			mark = "●"
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
		items = append(items, pickItem{
			id:    n,
			title: fmt.Sprintf("%s %s", mark, n),
			desc:  fmt.Sprintf("%s · model %s · %s", p.APIURL, model, probe),
		})
	}
	l := list.New(items, list.NewDefaultDelegate(), 64, min(12, len(items)+2))
	l.SetShowTitle(true)
	l.Title = "LLM providers (↑↓ select · Enter auto-probe & switch · Esc cancel)"
	l.SetFilteringEnabled(false)
	m.overlay = &overlayState{mode: overlayProvider, list: l}
	return m, nil
}

// enterProviderPick 选中 provider：缺探测/过期/失败 → 后台自动探测；否则直接出模型菜单。
func (m *model) enterProviderPick() (tea.Model, tea.Cmd) {
	it, ok := m.overlay.list.SelectedItem().(pickItem)
	if !ok {
		return m, nil
	}
	name := it.id
	providers, err := config.LoadLLMProviders()
	if err != nil {
		m.appendLog(styleErr.Render("! "+err.Error()+"\n"), true)
		m.overlay = nil
		return m, nil
	}
	prov, ok := providers.Providers[name]
	if !ok {
		m.appendLog(styleErr.Render(fmt.Sprintf("! provider %q not found\n", name)), true)
		m.overlay = nil
		return m, nil
	}
	if config.ProviderProbeExpired(prov.Probe.ProbedAt, 0) || prov.Probe.ProbedError != "" {
		m.overlay.providerName = name
		m.overlay.probing = true
		m.appendLog(styleDim.Render(fmt.Sprintf("auto-probing %s (%s)…\n", name, prov.APIURL)), true)
		return m, probeProviderCmd(name)
	}
	return m.openProviderModelMenu(name, prov.Probe.Models, prov.Probe.Capabilities, prov.Model)
}

// openProviderModelMenu 进入模型选择菜单（探测结果；● = provider 当前主模型）。
// 模型清单/能力由调用方直接传入：探测刚完成时写盘可能有竞态，内存结果更准。
func (m *model) openProviderModelMenu(name string, models []string, caps map[string]config.ModelCapCfg, current string) (tea.Model, tea.Cmd) {
	if len(models) == 0 {
		// 无探测结果：直接按 provider 默认模型切换（server 端会再自动补探）。
		m.providerSwitchNow(name, current)
		m.overlay = nil
		return m, nil
	}
	var items []list.Item
	for _, mm := range models {
		mods := ""
		if c, ok := caps[mm]; ok {
			mods = strings.Join(c.Modalities, ",")
		}
		mark := " "
		if mm == current {
			mark = "●"
		}
		items = append(items, pickItem{
			id:    mm,
			title: fmt.Sprintf("%s %s", mark, mm),
			desc:  "[" + mods + "]",
		})
	}
	l := list.New(items, list.NewDefaultDelegate(), 64, min(12, len(items)+2))
	l.SetShowTitle(true)
	l.Title = fmt.Sprintf("provider %s — pick model (● current · Enter switch · Esc back)", name)
	l.SetFilteringEnabled(false)
	m.overlay = &overlayState{mode: overlayProviderModel, list: l, providerName: name}
	return m, nil
}

// enterProviderModelPick 选中模型 → 切换（socket provider_switch，server 热生效）。
func (m *model) enterProviderModelPick() (tea.Model, tea.Cmd) {
	it, ok := m.overlay.list.SelectedItem().(pickItem)
	if !ok {
		return m, nil
	}
	name := m.overlay.providerName
	model := it.id
	m.overlay = nil
	m.providerSwitchNow(name, model)
	return m, nil
}

// providerSwitchNow 文本/菜单共用：切到 provider + 指定模型（阻塞等 server 回执）。
func (m *model) providerSwitchNow(name, model string) {
	text := name
	if model != "" {
		text += " " + model
	}
	m.appendLog(styleDim.Render(fmt.Sprintf("switching to %s (%s)…\n", name, model)), true)
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

// providerProbeDoneMsg 后台探测完成（probeProviderCmd 返回）。
type providerProbeDoneMsg struct {
	name string
	rep  llm.ProbeReport
	ok   bool
}

// startProbe 后台探测某 provider：结果统一由 handleProviderProbeDone 分发
// （菜单路径 → 模型菜单；文本路径/菜单已关 → 主区打印）。
func (m *model) startProbe(name string) (tea.Model, tea.Cmd) {
	providers, err := config.LoadLLMProviders()
	if err != nil {
		m.appendLog(styleErr.Render("! "+err.Error()+"\n"), true)
		return m, nil
	}
	prov, ok := providers.Providers[name]
	if !ok {
		m.appendLog(styleErr.Render(fmt.Sprintf("! provider %q not found\n", name)), true)
		return m, nil
	}
	m.appendLog(styleDim.Render(fmt.Sprintf("probing %s (%s)…\n", name, prov.APIURL)), true)
	return m, probeProviderCmd(name)
}

func probeProviderCmd(name string) tea.Cmd {
	return func() tea.Msg {
		rep, ok := llm.ProbeAndPersist(context.Background(), name, true)
		return providerProbeDoneMsg{name: name, rep: rep, ok: ok}
	}
}

// handleProviderProbeDone 探测完成：
//   - 菜单路径（overlay 还开着且对应此 provider）→ 出模型菜单
//   - 文本路径 / 菜单已被 Esc 关闭 → 结果打主区
func (m *model) handleProviderProbeDone(msg providerProbeDoneMsg) (tea.Model, tea.Cmd) {
	if m.overlay != nil && m.overlay.mode == overlayProvider && m.overlay.providerName == msg.name {
		if !msg.ok {
			m.appendLog(styleErr.Render(fmt.Sprintf("! probe %s failed; existing config kept\n", msg.name)), true)
			m.overlay = nil
			return m, nil
		}
		// 探测成功：读 provider.Model（SetProbeResult 成功时已补第一个模型），用内存结果出模型菜单。
		current := ""
		if providers, err := config.LoadLLMProviders(); err == nil {
			if prov, ok := providers.Providers[msg.name]; ok {
				current = prov.Model
			}
		}
		return m.openProviderModelMenu(msg.name, msg.rep.Models, msg.rep.Capabilities, current)
	}
	if !msg.ok {
		m.appendLog(styleErr.Render(fmt.Sprintf("! probe %s failed; existing config kept\n", msg.name)), true)
		return m, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "probe %s: %d models\n", msg.name, len(msg.rep.Models))
	for _, mm := range msg.rep.Models {
		mods := ""
		if c, ok := msg.rep.Capabilities[mm]; ok {
			mods = strings.Join(c.Modalities, ",")
		}
		fmt.Fprintf(&b, "  %-36s [%s]\n", mm, mods)
	}
	m.appendLog(b.String()+"\n", true)
	return m, nil
}
