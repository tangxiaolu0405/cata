package telegram

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"cata/internal/gateway"
	"cata/internal/gateway/ui"
)

// Bot Telegram ↔ cata gateway。
type Bot struct {
	cfg      gateway.Config
	tg       *Client
	sessions *gateway.SessionManager
	locks    *gateway.ProcessLock

	mu          sync.Mutex
	pendingExec map[string]chan bool
	pendingPick map[string]chan []string
}

// NewBot 创建 bot。
func NewBot(cfg gateway.Config) *Bot {
	return &Bot{
		cfg:         cfg,
		tg:          NewClient(cfg.TelegramBotToken),
		sessions:    gateway.NewSessionManager(cfg.SocketPath, cfg.WorkerRoot),
		locks:       gateway.NewProcessLock(),
		pendingExec: make(map[string]chan bool),
		pendingPick: make(map[string]chan []string),
	}
}

// Run 长轮询运行直到 ctx 取消。
func (b *Bot) Run(ctx context.Context) error {
	if strings.TrimSpace(b.cfg.TelegramBotToken) == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN is required (env or ~/.cata/gateway.json)")
	}
	username, err := b.tg.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("telegram token check failed: %w (check token, network/proxy to api.telegram.org)", err)
	}
	root := gateway.WorkerRoot(b.cfg.WorkerRoot)
	log.Printf("cata-gateway: telegram bot started @%s (worker_root=%s socket=%s)", username, root, b.cfg.SocketPath)
	log.Printf("telegram: long polling; open Telegram and message @%s (try /start)", username)

	var offset int64
	var pollOK bool
	for {
		select {
		case <-ctx.Done():
			b.sessions.CloseAll()
			return ctx.Err()
		default:
		}
		updates, err := b.tg.GetUpdates(ctx, offset, 30)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("getUpdates: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if !pollOK {
			pollOK = true
			log.Printf("telegram: API ok, waiting for messages (poll timeout=30s)")
		}
		if len(updates) > 0 {
			log.Printf("telegram: received %d update(s)", len(updates))
		}
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			if u.CallbackQuery != nil {
				go b.handleCallback(ctx, u.CallbackQuery)
				continue
			}
			if u.Message != nil && u.Message.Text != "" {
				go b.handleMessage(ctx, u.Message)
			}
		}
	}
}

func (b *Bot) handleMessage(ctx context.Context, msg *Message) {
	userID := int64(0)
	if msg.From != nil {
		userID = msg.From.ID
	}
	if !b.cfg.UserAllowed(userID) {
		log.Printf("telegram: deny user_id=%d chat_id=%d (not in TELEGRAM_ALLOWED_USERS)", userID, msg.Chat.ID)
		_, _ = b.tg.SendMessage(ctx, msg.Chat.ID, "未授权使用此 bot。", nil)
		return
	}

	text := strings.TrimSpace(msg.Text)
	log.Printf("telegram: chat_id=%d user_id=%d text=%q", msg.Chat.ID, userID, truncLog(text, 120))
	key := sessionKey(msg.Chat.ID)
	ui.DefaultHub.Publish("telegram", string(key), fmt.Sprintf("%d", msg.Chat.ID), fmt.Sprintf("%d", userID), "in", text)
	switch {
	case text == "/start":
		_, _ = b.tg.SendMessage(ctx, msg.Chat.ID, welcomeText(gateway.WorkerRoot(b.cfg.WorkerRoot)), nil)
		return
	case text == "/clear", text == "/reset":
		unlock := b.locks.Lock(key)
		defer unlock()
		if err := b.sessions.Reset(key); err != nil {
			_, _ = b.tg.SendMessage(ctx, msg.Chat.ID, "清空失败: "+err.Error(), nil)
			return
		}
		_, _ = b.tg.SendMessage(ctx, msg.Chat.ID, "会话已清空。", nil)
		ui.DefaultHub.Publish("telegram", string(key), fmt.Sprintf("%d", msg.Chat.ID), fmt.Sprintf("%d", userID), "out", "会话已清空。")
		return
	case text == "/help":
		_, _ = b.tg.SendMessage(ctx, msg.Chat.ID, helpText(), nil)
		return
	}

	unlock := b.locks.Lock(key)
	defer unlock()

	_ = b.tg.SendChatAction(ctx, msg.Chat.ID, "typing")
	conn, err := b.sessions.Get(key)
	if err != nil {
		log.Printf("telegram: worker cwd error chat_id=%d: %v", msg.Chat.ID, err)
		_, _ = b.tg.SendMessage(ctx, msg.Chat.ID, "工作区错误: "+err.Error(), nil)
		return
	}
	handler := &tgStreamHandler{bot: b, ctx: ctx, chatID: msg.Chat.ID}

	log.Printf("telegram: cata chat start chat_id=%d", msg.Chat.ID)
	result, err := conn.Chat(ctx, text, handler)
	if err != nil {
		log.Printf("telegram: cata chat error chat_id=%d: %v", msg.Chat.ID, err)
		_, _ = b.tg.SendMessage(ctx, msg.Chat.ID, cataUnavailableHint(err), nil)
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
	for _, part := range gateway.SplitTelegramMessage(reply, 4096) {
		if _, err := b.tg.SendMessage(ctx, msg.Chat.ID, part, nil); err != nil {
			log.Printf("telegram: sendMessage chat_id=%d failed: %v", msg.Chat.ID, err)
			return
		}
	}
	ui.DefaultHub.Publish("telegram", string(key), fmt.Sprintf("%d", msg.Chat.ID), fmt.Sprintf("%d", userID), "out", reply)
	log.Printf("telegram: cata chat done chat_id=%d success=%v chars=%d", msg.Chat.ID, result.Success && result.ErrMsg == "", len(reply))
}

func (b *Bot) handleCallback(ctx context.Context, cb *CallbackQuery) {
	if cb.Message == nil {
		return
	}
	userID := cb.From.ID
	if !b.cfg.UserAllowed(userID) {
		_ = b.tg.AnswerCallbackQuery(ctx, cb.ID, "未授权")
		return
	}
	data := strings.TrimSpace(cb.Data)
	_ = b.tg.AnswerCallbackQuery(ctx, cb.ID, "")

	switch {
	case strings.HasPrefix(data, "ec:"):
		parts := strings.Split(data, ":")
		if len(parts) != 3 {
			return
		}
		confirmID := parts[1]
		approved := parts[2] == "1"
		b.mu.Lock()
		ch := b.pendingExec[confirmID]
		delete(b.pendingExec, confirmID)
		b.mu.Unlock()
		if ch != nil {
			select {
			case ch <- approved:
			default:
			}
		}
	case strings.HasPrefix(data, "uc:"):
		parts := strings.Split(data, ":")
		if len(parts) != 3 {
			return
		}
		choiceID := parts[1]
		optID := parts[2]
		b.mu.Lock()
		ch := b.pendingPick[choiceID]
		delete(b.pendingPick, choiceID)
		b.mu.Unlock()
		if ch != nil {
			select {
			case ch <- []string{optID}:
			default:
			}
		}
	}
}

type tgStreamHandler struct {
	bot    *Bot
	ctx    context.Context
	chatID int64
}

func (h *tgStreamHandler) OnProgress(message string) {
	if message == "" {
		return
	}
	_, _ = h.bot.tg.SendMessage(h.ctx, h.chatID, "⏳ "+message, nil)
}

func (h *tgStreamHandler) OnToolStart(name string) {
	if name == "" {
		return
	}
	_ = h.bot.tg.SendChatAction(h.ctx, h.chatID, "typing")
}

func (h *tgStreamHandler) ConfirmExec(ctx context.Context, p gateway.ExecConfirmPrompt) (bool, error) {
	ch := make(chan bool, 1)
	h.bot.mu.Lock()
	h.bot.pendingExec[p.ConfirmID] = ch
	h.bot.mu.Unlock()

	text := fmt.Sprintf("确认执行命令？\n\n`%s`\n\ncwd: %s", p.CommandLine, p.Cwd)
	kb := [][]InlineKeyboardButton{
		{
			{Text: "Run", CallbackData: "ec:" + p.ConfirmID + ":1"},
			{Text: "Cancel", CallbackData: "ec:" + p.ConfirmID + ":0"},
		},
	}
	if _, err := h.bot.tg.SendMessage(ctx, h.chatID, text, kb); err != nil {
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

func (h *tgStreamHandler) Choose(ctx context.Context, p gateway.UserChoicePrompt) ([]string, error) {
	if len(p.Options) == 0 {
		return nil, fmt.Errorf("no options")
	}
	ch := make(chan []string, 1)
	h.bot.mu.Lock()
	h.bot.pendingPick[p.ChoiceID] = ch
	h.bot.mu.Unlock()

	var rows [][]InlineKeyboardButton
	for _, o := range p.Options {
		label := o.Label
		if label == "" {
			label = o.ID
		}
		rows = append(rows, []InlineKeyboardButton{{
			Text:         label,
			CallbackData: "uc:" + p.ChoiceID + ":" + o.ID,
		}})
	}
	prompt := p.Prompt
	if prompt == "" {
		prompt = "请选择："
	}
	if _, err := h.bot.tg.SendMessage(ctx, h.chatID, prompt, rows); err != nil {
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
		return sel, nil
	case <-time.After(10 * time.Minute):
		h.bot.mu.Lock()
		delete(h.bot.pendingPick, p.ChoiceID)
		h.bot.mu.Unlock()
		return nil, fmt.Errorf("user choice timeout")
	}
}

func sessionKey(chatID int64) gateway.SessionKey {
	return gateway.SessionKeyFor("telegram", fmt.Sprintf("%d", chatID))
}

func cataUnavailableHint(err error) string {
	return "cata 不可用（请确认 worker 侧 `cata run` 已启动且 socket 可达）。\n\n" + err.Error()
}

func welcomeText(workerRoot string) string {
	return fmt.Sprintf("Cata Telegram Gateway\n\n产出区根目录: %s/<channel>/<chat_id>/\n\n发送消息即可对话；/clear 清空会话；/help 帮助。", workerRoot)
}

func helpText() string {
	return strings.TrimSpace(`命令:
/start — 欢迎
/help — 本帮助
/clear — 清空 cata 会话历史

说明:
- gateway 启动不依赖 cata；发消息时连接 worker 侧 socket
- 每个 Telegram chat 产出区: ~/.cata_worker/telegram/<chat_id>/
- 危险命令会弹出 Run/Cancel 按钮确认`)
}

func truncLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
