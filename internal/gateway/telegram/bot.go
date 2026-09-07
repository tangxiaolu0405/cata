package telegram

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
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
	pending  *gateway.PendingManager
}

// NewBot 创建 bot（本地模式：拨本机 socket，按默认 agent 转发）。
func NewBot(cfg gateway.Config) *Bot {
	return NewBotWithSessions(cfg, gateway.NewSessionManager(cfg.SocketPath, cfg.WorkerRoot))
}

// NewBotWithSessions 创建 bot 并使用显式会话管理器（remote 模式由 gateway 传入
// 经隧道拨远端 agent 的 SessionManager）。
func NewBotWithSessions(cfg gateway.Config, sessions *gateway.SessionManager) *Bot {
	return &Bot{
		cfg:      cfg,
		tg:       NewClient(cfg.TelegramBotToken),
		sessions: sessions,
		locks:    gateway.NewProcessLock(),
		pending:  gateway.NewPendingManager(),
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
			if u.Message != nil {
				// 纯附件消息（无 caption）Text 为空，但 photo/document/voice 仍要处理。
				if u.Message.Text != "" || len(u.Message.Photo) > 0 || u.Message.Document != nil || u.Message.Voice != nil {
					go b.handleMessage(ctx, u.Message)
				}
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
	// 附件消息：caption 作为文本（纯附件无 caption 时给默认提示）。
	if text == "" && (len(msg.Photo) > 0 || msg.Document != nil || msg.Voice != nil) {
		text = strings.TrimSpace(msg.Caption)
		if text == "" {
			text = "（附件消息，请查看）"
		}
	}
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
	case text == "/status":
		_, _ = b.tg.SendMessage(ctx, msg.Chat.ID, gateway.ChannelStatus(b.sessions, b.cfg, "telegram", key), nil)
		return
	case strings.HasPrefix(text, "/dir"):
		reply := gateway.ReplyForWorkdir(b.sessions, "telegram", key, strings.TrimPrefix(text, "/dir"))
		_, _ = b.tg.SendMessage(ctx, msg.Chat.ID, reply, nil)
		ui.DefaultHub.Publish("telegram", string(key), fmt.Sprintf("%d", msg.Chat.ID), fmt.Sprintf("%d", userID), "out", reply)
		return
	}

	unlock := b.locks.Lock(key)
	defer unlock()
	conn, err := b.sessions.ConnForMessage(b.cfg, "telegram", key)
	if err != nil {
		_, _ = b.tg.SendMessage(ctx, msg.Chat.ID, err.Error(), nil)
		return
	}
	handler := &tgStreamHandler{bot: b, ctx: ctx, chatID: msg.Chat.ID}

	// 附件：photo/document/voice → 下载到 worker 产出区 → 作为附件传给 cata。
	attachments, attErr := b.downloadAttachments(ctx, key, msg)
	if attErr != nil {
		log.Printf("telegram: attachment download error chat_id=%d: %v", msg.Chat.ID, attErr)
		_, _ = b.tg.SendMessage(ctx, msg.Chat.ID, "⚠️ 附件下载失败: "+attErr.Error(), nil)
	}

	log.Printf("telegram: cata chat start chat_id=%d", msg.Chat.ID)
	result, err := conn.ChatAsWithAttachments(ctx, text, "", attachments, handler)
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

// downloadAttachments 把 Telegram 消息中的 photo/document/voice 下载到该会话的
// worker 产出区，返回 cata AttachmentReq 列表（空 = 无附件）。
// 下载目录 = {worker_root}/telegram/<chat_id>/，在 cata server 的产出区白名单内，
// 无需额外配置 llm.attachment_dir。
func (b *Bot) downloadAttachments(ctx context.Context, key gateway.SessionKey, msg *Message) ([]gateway.AttachmentReq, error) {
	// 无附件直接返回。
	if len(msg.Photo) == 0 && msg.Document == nil && msg.Voice == nil {
		return nil, nil
	}
	cwd, err := gateway.WorkerCwdForSession(b.cfg.WorkerRoot, key)
	if err != nil {
		return nil, err
	}
	var reqs []gateway.AttachmentReq
	// photo：取最大尺寸（数组最后一个）。
	if len(msg.Photo) > 0 {
		p := msg.Photo[len(msg.Photo)-1]
		dest := filepath.Join(cwd, fmt.Sprintf("photo_%d_%s.jpg", msg.MessageID, p.FileID))
		if _, err := b.tg.DownloadFile(ctx, p.FileID, dest); err != nil {
			return nil, fmt.Errorf("photo: %w", err)
		}
		reqs = append(reqs, gateway.AttachmentReq{Path: dest})
	}
	// document：文件名优先，含扩展名。
	if msg.Document != nil {
		name := msg.Document.FileName
		if name == "" {
			name = fmt.Sprintf("doc_%d", msg.MessageID)
		}
		dest := filepath.Join(cwd, gateway.SanitizeFilename(name))
		if _, err := b.tg.DownloadFile(ctx, msg.Document.FileID, dest); err != nil {
			return nil, fmt.Errorf("document: %w", err)
		}
		reqs = append(reqs, gateway.AttachmentReq{Path: dest})
	}
	// voice：OGG 语音。
	if msg.Voice != nil {
		dest := filepath.Join(cwd, fmt.Sprintf("voice_%d.ogg", msg.MessageID))
		if _, err := b.tg.DownloadFile(ctx, msg.Voice.FileID, dest); err != nil {
			return nil, fmt.Errorf("voice: %w", err)
		}
		reqs = append(reqs, gateway.AttachmentReq{Path: dest})
	}
	return reqs, nil
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
		b.pending.ResolveExec(confirmID, approved)
	case strings.HasPrefix(data, "uc:"):
		parts := strings.Split(data, ":")
		if len(parts) != 3 {
			return
		}
		choiceID := parts[1]
		optID := parts[2]
		b.pending.ResolveChoice(choiceID, optID)
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
	ch, cleanup := h.bot.pending.RegisterExec(p.ConfirmID)

	text := fmt.Sprintf("确认执行命令？\n\n`%s`\n\ncwd: %s", p.CommandLine, p.Cwd)
	kb := [][]InlineKeyboardButton{
		{
			{Text: "Run", CallbackData: "ec:" + p.ConfirmID + ":1"},
			{Text: "Cancel", CallbackData: "ec:" + p.ConfirmID + ":0"},
		},
	}
	if _, err := h.bot.tg.SendMessage(ctx, h.chatID, text, kb); err != nil {
		cleanup()
		return false, err
	}
	return gateway.WaitExec(ctx, ch, cleanup)
}

func (h *tgStreamHandler) Choose(ctx context.Context, p gateway.UserChoicePrompt) ([]string, error) {
	if len(p.Options) == 0 {
		return nil, fmt.Errorf("no options")
	}
	ch, cleanup := h.bot.pending.RegisterChoice(p.ChoiceID, p.Options)

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
		cleanup()
		return nil, err
	}
	return gateway.WaitChoice(ctx, ch, cleanup)
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
/status — 查看当前转发 agent 与 LLM 状态
/clear — 清空 cata 会话历史
/dir — 本会话切换工作空间（agent）：/dir 查看列表；/dir <序号或路径> 切换；/dir reset 恢复默认

说明:
- gateway 启动不依赖 cata；发消息时连接 worker 侧 socket
- 消息转发到 /dir 选定工作空间的 agent（不在线会自动拉起）；未切换时用默认 agent
- 支持发送图片/文档/语音作为附件（下载到会话产出区后交给 cata）
- 危险命令会弹出 Run/Cancel 按钮确认`)
}

func truncLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
