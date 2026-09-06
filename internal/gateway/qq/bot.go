package qq

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"cata/internal/gateway"
	"cata/internal/gateway/ui"
)

// Bot QQ WebSocket ↔ cata gateway。
type Bot struct {
	cfg      gateway.Config
	api      *Client
	sessions *gateway.SessionManager
	binding  *gateway.AgentBinding
	locks    *gateway.ProcessLock

	mu           sync.Mutex
	pendingExec  map[string]chan bool
	pendingPick  map[string]chan []string
	progressOnce map[string]bool // eventMsgID
}

// NewBot 创建 QQ bot（本地模式：拨本机 socket，按绑定 agent 转发）。
func NewBot(cfg gateway.Config) *Bot {
	return NewBotWithBinding(cfg, gateway.NewSessionManager(cfg.SocketPath, cfg.WorkerRoot), gateway.DefaultAgentBinding())
}

// NewBotWithSessions 创建 bot 并使用显式会话管理器（remote 模式由 gateway 传入）。
func NewBotWithSessions(cfg gateway.Config, sessions *gateway.SessionManager) *Bot {
	return NewBotWithBinding(cfg, sessions, gateway.DefaultAgentBinding())
}

// NewBotWithBinding 使用显式会话管理器 + 绑定存储创建 bot。
func NewBotWithBinding(cfg gateway.Config, sessions *gateway.SessionManager, binding *gateway.AgentBinding) *Bot {
	return &Bot{
		cfg:          cfg,
		sessions:     sessions,
		binding:      binding,
		locks:        gateway.NewProcessLock(),
		pendingExec:  make(map[string]chan bool),
		pendingPick:  make(map[string]chan []string),
		progressOnce: make(map[string]bool),
	}
}

// Run 启动 WebSocket 直到 ctx 取消。
func (b *Bot) Run(ctx context.Context) error {
	if !b.cfg.QQEnabled() {
		return fmt.Errorf("QQ_APP_ID and QQ_APP_SECRET are required")
	}
	defer b.sessions.CloseAll()
	root := gateway.WorkerRoot(b.cfg.WorkerRoot)
	log.Printf("cata-gateway: qq bot starting (sandbox=%v worker_root=%s) — WebSocket is experimental / may be disabled by QQ",
		b.cfg.QQSandbox, root)

	tokens := NewTokenSource(b.cfg.QQAppID, b.cfg.QQAppSecret)
	if err := tokens.Refresh(ctx); err != nil {
		return fmt.Errorf("qq token: %w", err)
	}
	b.api = NewClient(tokens, b.cfg.QQSandbox)
	gw := NewGateway(b.api, tokens, b.handleIncoming)
	log.Printf("qq: connecting WebSocket (C2C + group @); if this fails, QQ channel is unavailable")
	return gw.Run(ctx)
}

func (b *Bot) handleIncoming(ctx context.Context, msg IncomingMessage) {
	if !b.cfg.QQOpenIDAllowed(msg.UserOpenID) {
		log.Printf("qq: deny openid=%s", trunc(msg.UserOpenID, 24))
		_ = b.reply(ctx, msg, "未授权使用此 bot。")
		return
	}

	text := strings.TrimSpace(msg.Content)
	log.Printf("qq: kind=%s user=%s text=%q", msg.Kind, trunc(msg.UserOpenID, 16), trunc(text, 120))
	key := sessionKey(msg)
	ui.DefaultHub.Publish("qq", string(key), SessionIDFor(msg), msg.UserOpenID, "in", text)

	if b.tryTextConfirm(text) {
		return
	}

	switch {
	case text == "/start", text == "/help":
		_ = b.reply(ctx, msg, helpText())
		return
	case text == "/clear", text == "/reset":
		unlock := b.locks.Lock(key)
		defer unlock()
		if err := b.sessions.Reset(key); err != nil {
			_ = b.reply(ctx, msg, "清空失败: "+err.Error())
			return
		}
		_ = b.reply(ctx, msg, "会话已清空。")
		return
	case strings.HasPrefix(text, "/dir"):
		reply := gateway.ReplyForWorkdir(b.binding, "qq", key, strings.TrimPrefix(text, "/dir"))
		_ = b.reply(ctx, msg, reply)
		ui.DefaultHub.Publish("qq", string(key), SessionIDFor(msg), msg.UserOpenID, "out", reply)
		return
	}

	unlock := b.locks.Lock(key)
	defer unlock()

	conn, err := b.sessions.ConnForMessage(b.binding, "qq", key)
	if err != nil {
		_ = b.reply(ctx, msg, err.Error())
		return
	}
	handler := &qqStreamHandler{bot: b, ctx: ctx, msg: msg}
	log.Printf("qq: cata chat start key=%s", key)
	result, err := conn.Chat(ctx, text, handler)
	if err != nil {
		log.Printf("qq: cata chat error: %v", err)
		_ = b.reply(ctx, msg, "cata 不可用（确认 cata run / base 自动拉起）。\n"+err.Error())
		return
	}
	reply := strings.TrimSpace(result.Text)
	if result.ErrMsg != "" {
		reply = "⚠️ " + result.ErrMsg
		if strings.TrimSpace(result.Text) != "" {
			reply += "\n\n" + result.Text
		}
	}
	if reply == "" {
		if result.Cancelled {
			reply = "已取消。"
		} else {
			reply = "(无文本回复)"
		}
	}
	if err := b.reply(ctx, msg, reply); err != nil {
		log.Printf("qq: send failed: %v", err)
	}
	ui.DefaultHub.Publish("qq", string(key), SessionIDFor(msg), msg.UserOpenID, "out", reply)
	log.Printf("qq: cata chat done key=%s chars=%d", key, len(reply))
}

func (b *Bot) reply(ctx context.Context, msg IncomingMessage, content string) error {
	return b.api.SendText(ctx, replyTarget(msg), content)
}

func replyTarget(msg IncomingMessage) ReplyTarget {
	return ReplyTarget{
		Kind:       msg.Kind,
		UserID:     msg.UserOpenID,
		GroupID:    msg.GroupOpenID,
		EventMsgID: msg.MsgID,
	}
}

func sessionKey(msg IncomingMessage) gateway.SessionKey {
	return gateway.SessionKeyFor("qq", SessionIDFor(msg))
}

func helpText() string {
	return strings.TrimSpace(`Cata QQ Gateway（WebSocket 试验）

命令:
/help — 本帮助
/clear — 清空会话
/dir — 本渠道首次先选要绑定的工作空间（agent），之后消息转发给它；/dir <序号或路径> 换绑；/dir reset 解绑本渠道；各渠道独立绑定

说明:
- QQ 渠道的消息按绑定转发到指定工作空间的 agent（本渠道单独绑定；不在线会自动拉起）
- /dir 第一次使用会列出本机工作区供选择，重启后绑定保持
- 危险命令请回复 yes / no
- 官方已逐步下线 WebSocket；连不上则本渠道不可用`)
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (b *Bot) tryTextConfirm(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	b.mu.Lock()
	defer b.mu.Unlock()

	// exec: yes/no / 是/否 / y/n
	if len(b.pendingExec) == 1 {
		var id string
		var ch chan bool
		for k, v := range b.pendingExec {
			id, ch = k, v
		}
		switch lower {
		case "yes", "y", "是", "同意", "run", "ok":
			delete(b.pendingExec, id)
			select {
			case ch <- true:
			default:
			}
			return true
		case "no", "n", "否", "取消", "cancel":
			delete(b.pendingExec, id)
			select {
			case ch <- false:
			default:
			}
			return true
		}
	}

	// choice: reply with 1-based index or option id
	if len(b.pendingPick) == 1 {
		var id string
		var ch chan []string
		for k, v := range b.pendingPick {
			id, ch = k, v
		}
		if lower == "" {
			return false
		}
		delete(b.pendingPick, id)
		select {
		case ch <- []string{text}:
		default:
		}
		return true
	}
	return false
}

type qqStreamHandler struct {
	bot *Bot
	ctx context.Context
	msg IncomingMessage
}

func (h *qqStreamHandler) OnProgress(message string) {
	if message == "" {
		return
	}
	h.bot.mu.Lock()
	if h.bot.progressOnce[h.msg.MsgID] {
		h.bot.mu.Unlock()
		return
	}
	h.bot.progressOnce[h.msg.MsgID] = true
	h.bot.mu.Unlock()
	_ = h.bot.reply(h.ctx, h.msg, "⏳ "+message)
}

func (h *qqStreamHandler) OnToolStart(name string) {}

func (h *qqStreamHandler) ConfirmExec(ctx context.Context, p gateway.ExecConfirmPrompt) (bool, error) {
	ch := make(chan bool, 1)
	h.bot.mu.Lock()
	h.bot.pendingExec[p.ConfirmID] = ch
	h.bot.mu.Unlock()

	text := fmt.Sprintf("确认执行命令？\n\n%s\n\ncwd: %s\n\n请回复 yes 或 no", p.CommandLine, p.Cwd)
	if err := h.bot.reply(ctx, h.msg, text); err != nil {
		h.bot.mu.Lock()
		delete(h.bot.pendingExec, p.ConfirmID)
		h.bot.mu.Unlock()
		return false, err
	}
	select {
	case <-ctx.Done():
		h.bot.mu.Lock()
		delete(h.bot.pendingExec, p.ConfirmID)
		h.bot.mu.Unlock()
		return false, ctx.Err()
	case ok := <-ch:
		return ok, nil
	case <-time.After(10 * time.Minute):
		h.bot.mu.Lock()
		delete(h.bot.pendingExec, p.ConfirmID)
		h.bot.mu.Unlock()
		return false, fmt.Errorf("exec confirm timeout")
	}
}

func (h *qqStreamHandler) Choose(ctx context.Context, p gateway.UserChoicePrompt) ([]string, error) {
	if len(p.Options) == 0 {
		return nil, fmt.Errorf("no options")
	}
	ch := make(chan []string, 1)
	h.bot.mu.Lock()
	h.bot.pendingPick[p.ChoiceID] = ch
	h.bot.mu.Unlock()

	var b strings.Builder
	prompt := p.Prompt
	if prompt == "" {
		prompt = "请选择："
	}
	b.WriteString(prompt)
	b.WriteString("\n")
	for i, o := range p.Options {
		label := o.Label
		if label == "" {
			label = o.ID
		}
		b.WriteString(fmt.Sprintf("\n%d) %s", i+1, label))
		if o.ID != "" && o.ID != label {
			b.WriteString(" [" + o.ID + "]")
		}
	}
	b.WriteString("\n\n请回复选项编号或 id")
	if err := h.bot.reply(ctx, h.msg, b.String()); err != nil {
		h.bot.mu.Lock()
		delete(h.bot.pendingPick, p.ChoiceID)
		h.bot.mu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		h.bot.mu.Lock()
		delete(h.bot.pendingPick, p.ChoiceID)
		h.bot.mu.Unlock()
		return nil, ctx.Err()
	case sel := <-ch:
		if len(sel) == 1 {
			if n, err := strconv.Atoi(strings.TrimSpace(sel[0])); err == nil && n >= 1 && n <= len(p.Options) {
				return []string{p.Options[n-1].ID}, nil
			}
		}
		return sel, nil
	case <-time.After(10 * time.Minute):
		h.bot.mu.Lock()
		delete(h.bot.pendingPick, p.ChoiceID)
		h.bot.mu.Unlock()
		return nil, fmt.Errorf("user choice timeout")
	}
}
