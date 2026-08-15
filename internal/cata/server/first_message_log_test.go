package server

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"cata/internal/cata/brain"
)

func TestFirstMessageSnapshotRender(t *testing.T) {
	snap := firstMessageSnapshot{
		serverStart:   "2026-08-10 10:00:00",
		workspaceID:   "ws-1",
		focusPath:     "/tmp/proj",
		activeMode:    "default",
		outputCwd:     "/tmp/proj",
		model:         "deepseek-v4-flash",
		apiFormat:     "openai",
		apiURL:        "https://api.deepseek.com/chat/completions",
		keyPresent:    true,
		timeoutSec:    120,
		execEnabled:   true,
		filesEnabled:  true,
		mcpEnabled:    false,
		evolveEnabled: true,
		tools:         14,
		message:       "  hello world  ",
	}
	got := snap.render()
	for _, want := range []string{
		"server_start=2026-08-10 10:00:00",
		"ws=ws-1 focus=/tmp/proj mode=default",
		"cwd=/tmp/proj",
		"llm=deepseek-v4-flash format=openai url=https://api.deepseek.com/chat/completions key=✓ timeout=120s",
		"exec=true files=true mcp=false evolve=true tools=14",
		`msg="hello world"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("render() missing %q in:\n%s", want, got)
		}
	}
}

func TestFirstMessageSnapshotRenderNoKeyNoWS(t *testing.T) {
	snap := firstMessageSnapshot{
		serverStart: "?",
		message:     "",
	}
	got := snap.render()
	if !strings.Contains(got, "ws=<未解析到工作区>") {
		t.Fatalf("expected unresolved-ws line, got:\n%s", got)
	}
	if !strings.Contains(got, "key=✗") {
		t.Fatalf("expected key=✗ when no key, got:\n%s", got)
	}
	if strings.Contains(got, "msg=") {
		t.Fatalf("expected no msg line for empty message, got:\n%s", got)
	}
}

func TestFirstMessageSnapshotRenderTruncatesLongMsg(t *testing.T) {
	long := strings.Repeat("x", 300)
	snap := firstMessageSnapshot{message: long}
	got := snap.render()
	if !strings.Contains(got, "…") {
		t.Fatalf("expected truncation marker, got:\n%s", got)
	}
	if len(got) > 500 {
		t.Fatalf("render output too long: %d", len(got))
	}
}

func TestBuildFirstMessageDiagnostics(t *testing.T) {
	ss := &SocketServer{tools: NewToolRegistry()}
	ws := &brain.Workspace{ID: "ws-1", RootPath: "/tmp/proj", ActiveMode: "default"}
	out := ss.buildFirstMessageDiagnosticsWithOutCwd(nil, ws, "/tmp/proj", "hi")
	if !strings.Contains(out, "ws=ws-1 focus=/tmp/proj mode=default") {
		t.Fatalf("buildFirstMessageDiagnostics missing ws block:\n%s", out)
	}
	if !strings.Contains(out, "tools=0") {
		t.Fatalf("buildFirstMessageDiagnostics missing tools count:\n%s", out)
	}
}

func TestEmitFirstMessageDiagnosticsWritesLogEvent(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	ss := &SocketServer{tools: NewToolRegistry()}
	go ss.emitFirstMessageDiagnosticsWithOutCwd(serverConn, nil, &brain.Workspace{ID: "ws-1", RootPath: "/tmp/proj", ActiveMode: "default"}, "/tmp/proj", "hello")

	_ = clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(clientConn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read line: %v", err)
	}
	var ev map[string]interface{}
	if err := json.Unmarshal(line, &ev); err != nil {
		t.Fatalf("invalid NDJSON: %v\n%s", err, line)
	}
	if ev["type"] != "log" {
		t.Fatalf("expected type=log, got %v", ev["type"])
	}
	msg, _ := ev["message"].(string)
	if !strings.Contains(msg, "首次消息诊断") || !strings.Contains(msg, "ws=ws-1") {
		t.Fatalf("unexpected message:\n%s", msg)
	}
}
