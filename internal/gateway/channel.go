package gateway

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Channel 渠道插件接口：Telegram / QQ 等渠道 Bot 需实现的收发能力。
// 网关核心（会话管理、绑定、转发）在 gateway 包，渠道只负责
// 「平台消息 ⇄ 网关统一事件」的适配。
type Channel interface {
	// Name 渠道名（telegram / qq / …），用于会话键与绑定存储。
	Name() string
	// Run 运行渠道直到 ctx 取消（长轮询 / WebSocket 等）。
	Run(ctx context.Context) error
	// SendText 发送文本到目标会话。
	SendText(ctx context.Context, chatID string, text string) error
	// SendPrompt 发送交互提示（确认/选择），返回渠道侧事件 id（用于后续回调）。
	SendPrompt(ctx context.Context, chatID, prompt string, options []ChoiceOption) (string, error)
	// ResolveChoice 把渠道回调数据解析为选项 id（nil = 未匹配）。
	ResolveChoice(callbackData string) (choiceID, optID string, ok bool)
}

// PendingManager 共享的 exec/choice 等待管理器。
// 渠道 Bot 在 ConfirmExec / Choose 时注册等待通道，用户应答后由渠道回调
// （按钮点击 / 文本回复）resolve 到对应通道。消除 TG/QQ 重复的 pending map。
type PendingManager struct {
	mu           sync.Mutex
	pendingExec  map[string]chan bool
	pendingPick  map[string]chan []string
	choiceLabels map[string][]ChoiceOption // choiceID → 选项（文本渠道显示用）
}

// NewPendingManager 创建等待管理器。
func NewPendingManager() *PendingManager {
	return &PendingManager{
		pendingExec:  make(map[string]chan bool),
		pendingPick:  make(map[string]chan []string),
		choiceLabels: make(map[string][]ChoiceOption),
	}
}

// RegisterExec 注册一次 exec 确认等待，返回确认通道与清理函数。
func (p *PendingManager) RegisterExec(confirmID string) (chan bool, func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch := make(chan bool, 1)
	p.pendingExec[confirmID] = ch
	return ch, func() {
		p.mu.Lock()
		delete(p.pendingExec, confirmID)
		p.mu.Unlock()
	}
}

// RegisterChoice 注册一次选择等待。options 供文本渠道渲染。
func (p *PendingManager) RegisterChoice(choiceID string, options []ChoiceOption) (chan []string, func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch := make(chan []string, 1)
	p.pendingPick[choiceID] = ch
	p.choiceLabels[choiceID] = options
	return ch, func() {
		p.mu.Lock()
		delete(p.pendingPick, choiceID)
		delete(p.choiceLabels, choiceID)
		p.mu.Unlock()
	}
}

// ResolveExec 处理 exec 回调（按钮/文本 yes-no）。
func (p *PendingManager) ResolveExec(confirmID string, approved bool) {
	p.mu.Lock()
	ch := p.pendingExec[confirmID]
	delete(p.pendingExec, confirmID)
	p.mu.Unlock()
	if ch != nil {
		select {
		case ch <- approved:
		default:
		}
	}
}

// ResolveChoice 处理选择回调（按钮/文本选项）。
func (p *PendingManager) ResolveChoice(choiceID, optID string) {
	p.mu.Lock()
	ch := p.pendingPick[choiceID]
	delete(p.pendingPick, choiceID)
	delete(p.choiceLabels, choiceID)
	p.mu.Unlock()
	if ch != nil {
		select {
		case ch <- []string{optID}:
		default:
		}
	}
}

// HasPendingExec 是否有待确认的 exec（文本渠道用：回复 yes/no 时匹配）。
func (p *PendingManager) HasPendingExec() (confirmID string, ch chan bool, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, c := range p.pendingExec {
		return id, c, true
	}
	return "", nil, false
}

// HasPendingChoice 是否有待选择（文本渠道用：回复编号时匹配）。
func (p *PendingManager) HasPendingChoice() (choiceID string, ch chan []string, options []ChoiceOption, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, c := range p.pendingPick {
		return id, c, p.choiceLabels[id], true
	}
	return "", nil, nil, false
}

// PendingTimeout 交互等待超时。
var PendingTimeout = 10 * time.Minute

// WaitExec 等待 exec 确认（带 ctx 取消与超时）。
func WaitExec(ctx context.Context, ch chan bool, cleanup func()) (bool, error) {
	select {
	case <-ctx.Done():
		cleanup()
		return false, ctx.Err()
	case ok := <-ch:
		return ok, nil
	case <-time.After(PendingTimeout):
		cleanup()
		return false, fmt.Errorf("exec confirm timeout")
	}
}

// WaitChoice 等待选择（带 ctx 取消与超时）。
func WaitChoice(ctx context.Context, ch chan []string, cleanup func()) ([]string, error) {
	select {
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	case sel := <-ch:
		return sel, nil
	case <-time.After(PendingTimeout):
		cleanup()
		return nil, fmt.Errorf("user choice timeout")
	}
}
