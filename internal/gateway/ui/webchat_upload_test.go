package ui

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/cata/link"
	"cata/internal/gateway"
)

// webChatTestEnv 一次性搭好：短路径 CATA_HOME + 注册一个工作空间 + fake per-ws agent
// （ping 回 pong；chat 记录请求并回事件流）。
type webChatTestEnv struct {
	server  *Server
	agentID string
	wsDir   string
	chatReq chan map[string]any
}

func newWebChatTestEnv(t *testing.T) *webChatTestEnv {
	t.Helper()
	// Unix socket 有长度上限（macOS ~104B）：CATA_HOME 与项目目录都要短，
	// 否则 ws_id → socket 路径会超限（/tmp 而不是 t.TempDir()）。
	home, err := os.MkdirTemp("/tmp", "ch")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("CATA_HOME", home)
	if err := brain.EnsureCataLayout(); err != nil {
		t.Fatal(err)
	}
	wsDir := filepath.Join(home, "proj")
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		t.Fatal(err)
	}
	entry, err := link.Add(wsDir, true)
	if err != nil {
		t.Fatal(err)
	}

	env := &webChatTestEnv{
		agentID: entry.AgentID,
		wsDir:   wsDir,
		chatReq: make(chan map[string]any, 8),
	}
	sockPath := config.ResolvedAgentSocketPath(entry.AgentID)
	if err := os.MkdirAll(filepath.Dir(sockPath), 0755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				for {
					line, err := br.ReadBytes('\n')
					if err != nil {
						return
					}
					var req map[string]any
					if json.Unmarshal(line, &req) != nil {
						continue
					}
					switch req["command"] {
					case "ping":
						_, _ = c.Write([]byte("{\"success\":true,\"message\":\"pong\"}\n"))
					case "chat":
						select {
						case env.chatReq <- req:
						default:
						}
						for _, ev := range []string{
							`{"type":"token","content":"图片收到了"}`,
							`{"type":"attachment_rejected","path":"x.png","reason":"too big"}`,
							`{"type":"model_switch","from":"deepseek-v4-flash","to":"deepseek-flash"}`,
							`{"type":"done","success":true}`,
						} {
							_, _ = c.Write([]byte(ev + "\n"))
						}
					}
				}
			}(c)
		}
	}()

	cfg := gateway.Config{WorkerRoot: home}
	env.server = &Server{cfg: cfg, web: NewWebChat(cfg)}
	return env
}

// chat 走一次 HTTP chat 请求，返回响应记录器。
func (e *webChatTestEnv) chat(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	e.server.handleProjectAction(rec, httptest.NewRequest(http.MethodPost,
		"/api/projects/"+e.agentID+"/chat", strings.NewReader(body)))
	return rec
}

// TestProjectChatUploadsInlineImage Web 控制台图片上传链路（base64 内联）：
// POST /api/projects/<id>/chat 带 attachments[].inline →
//   - agent（fake per-ws socket）收到原样 base64 的 inline 附件；
//   - agent 回的 attachment_rejected / model_switch 事件被转发给浏览器。
func TestProjectChatUploadsInlineImage(t *testing.T) {
	env := newWebChatTestEnv(t)

	// 1) 带内联图片（无文字）→ 允许，附件原样到达 agent。
	pngB64 := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nfake-png-bytes"))
	body := `{"text":"","attachments":[{"inline":{"mime":"image/png","base64":"` + pngB64 + `"}}]}`
	rec := env.chat(t, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	for _, want := range []string{`"type":"token"`, `"type":"attachment_rejected"`, `"type":"model_switch"`, `"type":"done"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("stream missing %s:\n%s", want, out)
		}
	}
	select {
	case req := <-env.chatReq:
		atts, _ := req["attachments"].([]any)
		if len(atts) != 1 {
			t.Fatalf("agent got attachments=%v", req["attachments"])
		}
		a0, _ := atts[0].(map[string]any)
		in, _ := a0["inline"].(map[string]any)
		if in["mime"] != "image/png" || in["base64"] != pngB64 {
			t.Fatalf("inline attachment not forwarded verbatim: %+v", a0)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent never received the chat request")
	}

	// 2) 文字与附件都为空 → 400（不再接受空请求）。
	rec2 := env.chat(t, `{"text":""}`)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("empty request status=%d want 400", rec2.Code)
	}

	// 3) 纯文本仍照旧可用。
	rec3 := env.chat(t, `{"text":"你好"}`)
	if rec3.Code != http.StatusOK || !strings.Contains(rec3.Body.String(), `"type":"done"`) {
		t.Fatalf("text-only chat failed: status=%d body=%s", rec3.Code, rec3.Body.String())
	}
}
