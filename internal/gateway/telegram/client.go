package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const apiBase = "https://api.telegram.org"

type apiError struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

func (e apiError) err(prefix string) error {
	if e.Description != "" {
		return fmt.Errorf("%s: %s (error_code=%d)", prefix, e.Description, e.ErrorCode)
	}
	return fmt.Errorf("%s: telegram API returned ok=false", prefix)
}

// GetMe 启动时校验 token，返回 bot 用户名。
func (c *Client) GetMe(ctx context.Context) (username string, err error) {
	var out struct {
		apiError
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := c.getJSON(ctx, "/getMe", &out); err != nil {
		return "", fmt.Errorf("getMe: %w", err)
	}
	if !out.OK {
		return "", out.err("getMe")
	}
	return out.Result.Username, nil
}

// Client 最小 Telegram Bot API 客户端（stdlib only）。
type Client struct {
	token string
	http  *http.Client
	base  string
}

// NewClient 创建客户端。
func NewClient(token string) *Client {
	return &Client{
		token: token,
		http:  &http.Client{Timeout: 90 * time.Second},
		base:  apiBase + "/bot" + token,
	}
}

// Update Telegram update。
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// Message 入站消息。
type Message struct {
	MessageID int64  `json:"message_id"`
	Chat      Chat   `json:"chat"`
	From      *User  `json:"from"`
	Text      string `json:"text"`
}

// CallbackQuery 按钮回调。
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

// Chat 会话（Telegram API 字段为 id，不是 chat_id）。
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type,omitempty"`
}

// User 用户。
type User struct {
	ID int64 `json:"id"`
}

// InlineKeyboardButton 内联按钮。
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
}

// GetUpdates 长轮询拉取更新。
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	q := url.Values{}
	if offset > 0 {
		q.Set("offset", strconv.FormatInt(offset, 10))
	}
	if timeout <= 0 {
		timeout = 30
	}
	q.Set("timeout", strconv.Itoa(timeout))
	var out struct {
		apiError
		Result []Update `json:"result"`
	}
	if err := c.getJSON(ctx, "/getUpdates?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, out.err("getUpdates")
	}
	return out.Result, nil
}

// SendMessage 发送文本。
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, keyboard [][]InlineKeyboardButton) (int64, error) {
	body := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if len(keyboard) > 0 {
		body["reply_markup"] = map[string]any{"inline_keyboard": keyboard}
	}
	var out struct {
		apiError
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := c.postJSON(ctx, "/sendMessage", body, &out); err != nil {
		return 0, err
	}
	if !out.OK {
		return 0, out.err("sendMessage")
	}
	return out.Result.MessageID, nil
}

// AnswerCallbackQuery 应答按钮点击。
func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error {
	body := map[string]any{"callback_query_id": callbackID}
	if text != "" {
		body["text"] = text
	}
	var out struct {
		OK bool `json:"ok"`
	}
	return c.postJSON(ctx, "/answerCallbackQuery", body, &out)
}

// SendChatAction 发送 typing 等状态。
func (c *Client) SendChatAction(ctx context.Context, chatID int64, action string) error {
	body := map[string]any{"chat_id": chatID, "action": action}
	var out struct {
		OK bool `json:"ok"`
	}
	return c.postJSON(ctx, "/sendChatAction", body, &out)
}

func (c *Client) getJSON(ctx context.Context, path string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeJSON(resp.Body, dest)
}

func (c *Client) postJSON(ctx context.Context, path string, body any, dest any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("telegram HTTP %d: %s", resp.StatusCode, string(slurp))
	}
	return decodeJSON(resp.Body, dest)
}

func decodeJSON(r io.Reader, dest any) error {
	dec := json.NewDecoder(r)
	if err := dec.Decode(dest); err != nil {
		return err
	}
	return nil
}
