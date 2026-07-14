package ui

import (
	"sync"
	"time"
)

const (
	maxEventsGlobal   = 500
	maxEventsPerKey   = 100
	maxTextStore      = 2000
)

// ChannelEvent 渠道只读事件（不可由 UI 回写）。
type ChannelEvent struct {
	ID        int64  `json:"id"`
	At        string `json:"at"`
	Channel   string `json:"channel"`
	Session   string `json:"session"`
	ChatID    string `json:"chat_id"`
	UserID    string `json:"user_id,omitempty"`
	Direction string `json:"direction"` // in | out
	Text      string `json:"text"`
}

// Hub 内存环形缓冲 + 订阅者广播。
type Hub struct {
	mu      sync.RWMutex
	seq     int64
	events  []ChannelEvent
	byKey   map[string][]ChannelEvent
	subs    map[chan ChannelEvent]struct{}
}

// DefaultHub 全局渠道只读事件中心。
var DefaultHub = NewHub()

// NewHub 创建 Hub。
func NewHub() *Hub {
	return &Hub{
		events: make([]ChannelEvent, 0, 64),
		byKey:  make(map[string][]ChannelEvent),
		subs:   make(map[chan ChannelEvent]struct{}),
	}
}

// Publish 写入事件并通知订阅者。
func (h *Hub) Publish(channel, session, chatID, userID, direction, text string) ChannelEvent {
	if len(text) > maxTextStore {
		text = text[:maxTextStore] + "…"
	}
	h.mu.Lock()
	h.seq++
	ev := ChannelEvent{
		ID:        h.seq,
		At:        time.Now().UTC().Format(time.RFC3339),
		Channel:   channel,
		Session:   session,
		ChatID:    chatID,
		UserID:    userID,
		Direction: direction,
		Text:      text,
	}
	h.events = append(h.events, ev)
	if len(h.events) > maxEventsGlobal {
		h.events = h.events[len(h.events)-maxEventsGlobal:]
	}
	key := session
	if key == "" {
		key = channel + ":" + chatID
	}
	lst := append(h.byKey[key], ev)
	if len(lst) > maxEventsPerKey {
		lst = lst[len(lst)-maxEventsPerKey:]
	}
	h.byKey[key] = lst
	subs := make([]chan ChannelEvent, 0, len(h.subs))
	for ch := range h.subs {
		subs = append(subs, ch)
	}
	h.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
		}
	}
	return ev
}

// Recent 返回最近全局事件（旧→新）。
func (h *Hub) Recent(limit int) []ChannelEvent {
	if limit <= 0 {
		limit = 100
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := len(h.events)
	if n == 0 {
		return nil
	}
	if limit > n {
		limit = n
	}
	out := make([]ChannelEvent, limit)
	copy(out, h.events[n-limit:])
	return out
}

// RecentSession 返回某会话最近事件。
func (h *Hub) RecentSession(session string, limit int) []ChannelEvent {
	if limit <= 0 {
		limit = 100
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	lst := h.byKey[session]
	n := len(lst)
	if n == 0 {
		return nil
	}
	if limit > n {
		limit = n
	}
	out := make([]ChannelEvent, limit)
	copy(out, lst[n-limit:])
	return out
}

// Sessions 列出已知渠道会话键。
func (h *Hub) Sessions() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.byKey))
	for k := range h.byKey {
		out = append(out, k)
	}
	return out
}

// Subscribe 订阅新事件；返回取消函数。
func (h *Hub) Subscribe() (<-chan ChannelEvent, func()) {
	ch := make(chan ChannelEvent, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	cancel := func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}
