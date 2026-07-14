package ui

import (
	"strings"
	"testing"
	"time"
)

func TestHubPublishAndRecent(t *testing.T) {
	h := NewHub()
	ev := h.Publish("telegram", "telegram:1", "1", "42", "in", "hello")
	if ev.ID != 1 || ev.Text != "hello" || ev.Channel != "telegram" {
		t.Fatalf("unexpected event: %+v", ev)
	}
	got := h.Recent(10)
	if len(got) != 1 || got[0].Text != "hello" {
		t.Fatalf("Recent: %+v", got)
	}
	sess := h.RecentSession("telegram:1", 10)
	if len(sess) != 1 {
		t.Fatalf("RecentSession: %+v", sess)
	}
	keys := h.Sessions()
	if len(keys) != 1 || keys[0] != "telegram:1" {
		t.Fatalf("Sessions: %+v", keys)
	}
}

func TestHubSubscribe(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe()
	defer cancel()
	go h.Publish("qq", "qq:c2c_x", "c2c_x", "u", "out", "bye")
	select {
	case ev := <-ch:
		if ev.Direction != "out" || ev.Text != "bye" {
			t.Fatalf("got %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for subscribe")
	}
}

func TestHubTextTruncate(t *testing.T) {
	h := NewHub()
	long := make([]byte, maxTextStore+50)
	for i := range long {
		long[i] = 'a'
	}
	ev := h.Publish("telegram", "telegram:2", "2", "", "in", string(long))
	if len(ev.Text) <= maxTextStore {
		t.Fatalf("expected truncate marker, len=%d", len(ev.Text))
	}
	if !strings.HasSuffix(ev.Text, "…") {
		t.Fatalf("expected ellipsis: %q", ev.Text[len(ev.Text)-3:])
	}
}
