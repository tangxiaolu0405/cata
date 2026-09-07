package qq

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"

	"cata/internal/gateway"
	"cata/internal/gateway/ui"
)

// Bot QQ WebSocket ↔ cata gateway。
type Bot struct {
	cfg      gateway.Config
	api      *Client
	sessions *gateway.SessionManager
	locks    *gateway.ProcessLock
	pending  *gateway.PendingManager

	progressMu   sync.Mutex
	progressOnce map[string]bool // eventMsgID
}

// NewBot 创建 QQ bot（本地模式：拨本机 socket，按默认 agent 转发）。
func NewBot(cfg gateway.Config) *Bot {
	return NewBotWithSessions(cfg, gateway.NewSessionManager(cfg.SocketPath, cfg.WorkerRoot))
}

// NewBotWithSessions 创建 bot 并使用显式会话管理器（remote 模式由 gateway 传入）。
func NewBotWithSessions(cfg gateway.Config, sessions *gateway.SessionManager) *Bot {
	return &Bot{
		cfg:          cfg,
		sessions:     sessions,
		locks:        gateway.NewProcessLock(),
		pending:      gateway.NewPendingManager(),
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
	case text == "/status":
		_ = b.reply(ctx, msg, gateway.ChannelStatus(b.sessions, b.cfg, "qq", key))
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
		reply := gateway.ReplyForWorkdir(b.sessions, "qq", key, strings.TrimPrefix(text, "/dir"))
		_ = b.reply(ctx, msg, reply)
		ui.DefaultHub.Publish("qq", string(key), SessionIDFor(msg), msg.UserOpenID, "out", reply)
		return
	}

	unlock := b.locks.Lock(key)
	defer unlock()

	conn, err := b.sessions.ConnForMessage(b.cfg, "qq", key)
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
/status — 查看当前转发 agent 与 LLM 状态
/clear — 清空会话
/dir — 本会话切换工作空间（agent）：/dir 查看列表；/dir <序号或路径> 切换；/dir reset 恢复默认

说明:
- 消息转发到 /dir 选定工作空间的 agent（不在线会自动拉起）；未切换时用默认 agent
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

	// exec: yes/no / 是/否 / y/n
	if id, _, ok := b.pending.HasPendingExec(); ok {
		switch lower {
		case "yes", "y", "是", "同意", "run", "ok":
			b.pending.ResolveExec(id, true)
			return true
		case "no", "n", "否", "取消", "cancel":
			b.pending.ResolveExec(id, false)
			return true
		}
	}

	// choice: reply with 1-based index or option id
	if id, _, _, ok := b.pending.HasPendingChoice(); ok {
		if lower == "" {
			return false
		}
		b.pending.ResolveChoice(id, text)
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
	h.bot.progressMu.Lock()
	if h.bot.progressOnce[h.msg.MsgID] {
		h.bot.progressMu.Unlock()
		return
	}
	h.bot.progressOnce[h.msg.MsgID] = true
	h.bot.progressMu.Unlock()
	_ = h.bot.reply(h.ctx, h.msg, "⏳ "+message)
}

func (h *qqStreamHandler) OnToolStart(name string) {
	if name == "" {
		return
	}
	// QQ 无 typing action；工具开始时提示一次（与 OnProgress 共享 progressOnce 去重）。
	h.bot.progressMu.Lock()
	if h.bot.progressOnce[h.msg.MsgID] {
		h.bot.progressMu.Unlock()
		return
	}
	h.bot.progressOnce[h.msg.MsgID] = true
	h.bot.progressMu.Unlock()
	_ = h.bot.reply(h.ctx, h.msg, "🔧 正在执行: "+name)
}

func (h *qqStreamHandler) ConfirmExec(ctx context.Context, p gateway.ExecConfirmPrompt) (bool, error) {
	ch, cleanup := h.bot.pending.RegisterExec(p.ConfirmID)

	text := fmt.Sprintf("确认执行命令？\n\n%s\n\ncwd: %s\n\n请回复 yes 或 no", p.CommandLine, p.Cwd)
	if err := h.bot.reply(ctx, h.msg, text); err != nil {
		cleanup()
		return false, err
	}
	return gateway.WaitExec(ctx, ch, cleanup)
}

func (h *qqStreamHandler) Choose(ctx context.Context, p gateway.UserChoicePrompt) ([]string, error) {
	if len(p.Options) == 0 {
		return nil, fmt.Errorf("no options")
	}
	ch, cleanup := h.bot.pending.RegisterChoice(p.ChoiceID, p.Options)

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
		cleanup()
		return nil, err
	}

	sel, err := gateway.WaitChoice(ctx, ch, cleanup)
	if err != nil {
		return nil, err
	}
	if len(sel) == 1 {
		if n, err := strconv.Atoi(strings.TrimSpace(sel[0])); err == nil && n >= 1 && n <= len(p.Options) {
			return []string{p.Options[n-1].ID}, nil
		}
	}
	return sel, nil
}
