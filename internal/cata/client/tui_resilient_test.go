package client

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
)

// TestWriteChatResilientWakesAndResends agent 已死（写连接报 broken pipe / closed pipe）时，
// 发消息应自动唤醒（重连）并把**同一条消息**重发成功——用户不必重敲。
// 这正是「kill 掉 supervisor 后 agent 被心跳掐掉 → TUI 每轮 broken pipe」的回归。
func TestWriteChatResilientWakesAndResends(t *testing.T) {
	// 旧会话：对端已关闭 → 写入必然失败。
	deadServer, deadClient := net.Pipe()
	_ = deadServer.Close()
	m := &model{
		input:     newChatTextarea(),
		vp:        viewport.New(80, 20),
		sidebarVP: newSidebarViewport(),
		wsID:      "ws-x",
		sess:      &session{conn: deadClient, br: bufio.NewReader(deadClient)},
	}
	if err := deadClient.Close(); err != nil {
		t.Fatal(err)
	}

	// 唤醒后拿到的新会话：接到一个假 agent，记录收到的请求行。
	got := make(chan map[string]any, 1)
	agentSide, clientSide := net.Pipe()
	t.Cleanup(func() { _ = agentSide.Close(); _ = clientSide.Close() })
	go func() {
		br := bufio.NewReader(agentSide)
		line, err := br.ReadBytes('\n')
		if err != nil {
			return
		}
		var req map[string]any
		if json.Unmarshal(line, &req) == nil {
			got <- req
		}
	}()

	woke := 0
	m.dialFn = func(wsID string) (*session, error) {
		woke++
		return &session{conn: clientSide, br: bufio.NewReader(clientSide)}, nil
	}

	r := req{Command: "chat", Text: "个人复盘整理", Stream: true}
	if err := m.writeChatResilient(r); err != nil {
		t.Fatalf("writeChatResilient: %v（应自动唤醒并重发）", err)
	}
	if woke != 1 {
		t.Fatalf("dial 次数=%d want 1（应唤醒一次）", woke)
	}
	select {
	case req := <-got:
		if req["command"] != "chat" || req["text"] != "个人复盘整理" {
			t.Fatalf("重发的请求不对: %+v", req)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("唤醒后的 agent 没收到重发消息")
	}
}

// TestWriteChatResilientWakeFailureIsReported 唤醒失败（agent 起不来）时返回可读错误，
// 不 panic、不静默吞掉原写入错误。
func TestWriteChatResilientWakeFailureIsReported(t *testing.T) {
	deadServer, deadClient := net.Pipe()
	_ = deadServer.Close()
	_ = deadClient.Close()
	m := &model{
		input:     newChatTextarea(),
		vp:        viewport.New(80, 20),
		sidebarVP: newSidebarViewport(),
		wsID:      "ws-x",
		sess:      &session{conn: deadClient, br: bufio.NewReader(deadClient)},
	}
	called := 0
	m.dialFn = func(string) (*session, error) {
		called++
		return nil, errors.New("spawn failed")
	}

	err := m.writeChatResilient(req{Command: "chat", Text: "x"})
	if err == nil {
		t.Fatal("唤醒失败时不应返回 nil")
	}
	if called != 1 {
		t.Fatalf("dial 次数=%d want 1", called)
	}
	if !strings.Contains(err.Error(), "spawn failed") {
		t.Fatalf("错误应包含唤醒失败原因: %v", err)
	}
}
