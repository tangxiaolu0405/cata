package server

import (
	"fmt"
	"log"
	"net"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/cata/clock"
	"cata/internal/cata/config"
	"cata/internal/llm"
	"cata/internal/mcp"
)

// emitFirstMessageDiagnostics 在每条连接的首条 chat 消息时输出诊断，便于定位
// 「cata 没处理 / 无响应」的问题来源：
//  1. 写入 server 日志（managed 模式落在 cata-server.log）；
//  2. 向客户端发 type=log 事件，TUI 直接展示。
func (ss *SocketServer) emitFirstMessageDiagnostics(conn net.Conn, client *llm.Client, ws *brain.Workspace, userText string) {
	ss.emitFirstMessageDiagnosticsWithOutCwd(conn, client, ws, brain.OutputCwd(), userText)
}

// emitFirstMessageDiagnosticsWithOutCwd 显式指定产出区（多 cata 并行勿依赖全局 OutputCwd）。
func (ss *SocketServer) emitFirstMessageDiagnosticsWithOutCwd(conn net.Conn, client *llm.Client, ws *brain.Workspace, outCwd, userText string) {
	diag := ss.buildFirstMessageDiagnosticsWithOutCwd(client, ws, outCwd, userText)
	log.Printf("first message diagnostics:\n%s", diag)
	_ = ss.emitStreamLine(conn, map[string]interface{}{
		"type":    "log",
		"level":   "info",
		"summary": "[boot] 诊断 ws=" + wsIDLabel(ws) + " m=" + modelLabel(client) + " key=" + keyLabel(client),
		"message": "[boot] 首次消息诊断（排障用，真实日志见 cata-server.log）\n" + diag,
	})
}

// wsIDLabel 诊断概要用工作区短标签。
func wsIDLabel(ws *brain.Workspace) string {
	if ws == nil {
		return "?"
	}
	if ws.ID != "" {
		return ws.ID
	}
	return "?"
}

// modelLabel 诊断概要用模型名（截断避免侧栏换行）。
func modelLabel(client *llm.Client) string {
	if client == nil {
		return "?"
	}
	m := strings.TrimSpace(client.ModelName())
	if len(m) > 18 {
		m = m[:16] + "…"
	}
	if m == "" {
		return "?"
	}
	return m
}

// keyLabel 诊断概要用密钥状态。
func keyLabel(client *llm.Client) string {
	if client == nil {
		return "?"
	}
	if client.APIKeyPresent() {
		return "✓"
	}
	return "✗"
}

// firstMessageSnapshot 是诊断所需的全部快照字段（纯数据，便于单测 render）。
type firstMessageSnapshot struct {
	serverStart   string // 已格式化
	workspaceID   string
	focusPath     string
	activeMode    string
	outputCwd     string
	model         string
	apiFormat     string
	apiURL        string
	keyPresent    bool
	timeoutSec    int
	execEnabled   bool
	filesEnabled  bool
	mcpEnabled    bool
	evolveEnabled bool
	tools         int
	message       string
}

// render 生成多行可读文本。
func (s firstMessageSnapshot) render() string {
	var b strings.Builder
	b.WriteString("server_start=" + s.serverStart + "\n")
	if s.workspaceID != "" {
		fmt.Fprintf(&b, "ws=%s focus=%s mode=%s\n", s.workspaceID, s.focusPath, s.activeMode)
	} else {
		b.WriteString("ws=<未解析到工作区>\n")
	}
	if s.outputCwd != "" {
		fmt.Fprintf(&b, "cwd=%s\n", s.outputCwd)
	}
	key := "✗"
	if s.keyPresent {
		key = "✓"
	}
	fmt.Fprintf(&b, "llm=%s format=%s url=%s key=%s timeout=%ds\n",
		s.model, s.apiFormat, s.apiURL, key, s.timeoutSec)
	fmt.Fprintf(&b, "exec=%v files=%v mcp=%v evolve=%v tools=%d\n",
		s.execEnabled, s.filesEnabled, s.mcpEnabled, s.evolveEnabled, s.tools)
	if msg := strings.TrimSpace(s.message); msg != "" {
		if len(msg) > 120 {
			msg = msg[:120] + "…"
		}
		fmt.Fprintf(&b, "msg=%q", msg)
	}
	return b.String()
}

// buildFirstMessageDiagnostics 收集首条消息处理链路的状态快照并渲染。
func (ss *SocketServer) buildFirstMessageDiagnostics(client *llm.Client, ws *brain.Workspace, userText string) string {
	return ss.buildFirstMessageDiagnosticsWithOutCwd(client, ws, brain.OutputCwd(), userText)
}

// buildFirstMessageDiagnosticsWithOutCwd 显式指定产出区（多 cata 并行勿依赖全局 OutputCwd）。
func (ss *SocketServer) buildFirstMessageDiagnosticsWithOutCwd(client *llm.Client, ws *brain.Workspace, outCwd, userText string) string {
	snap := firstMessageSnapshot{}
	if ss.server != nil && !ss.server.startedAt.IsZero() {
		snap.serverStart = ss.server.startedAt.In(clock.Location()).Format("2006-01-02 15:04:05")
	} else {
		snap.serverStart = "?"
	}
	if ws != nil {
		snap.workspaceID = ws.ID
		snap.focusPath = ws.RootPath
		snap.activeMode = ws.ActiveMode
	}
	if outCwd != "" {
		snap.outputCwd = outCwd
	}
	if client != nil {
		snap.model = client.ModelName()
		snap.apiFormat = client.APIFormatLabel()
		snap.apiURL = client.APIURL()
		snap.keyPresent = client.APIKeyPresent()
		snap.timeoutSec = client.TimeoutSeconds()
	}
	if cfg := config.Config; cfg != nil {
		snap.execEnabled = cfg.Exec.Enabled
		snap.filesEnabled = cfg.WorkspaceFilesEnabled()
		snap.mcpEnabled = cfg.MCP.Enabled
		snap.evolveEnabled = cfg.Evolution.Enabled
	}
	if ss != nil {
		snap.tools = len(ss.tools.Schemas())
		if mgr := mcp.Global(); mgr != nil {
			snap.tools += len(mgr.Tools())
		}
	}
	snap.message = userText
	return snap.render()
}
