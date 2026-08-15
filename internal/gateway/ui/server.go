package ui

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/gateway"
	"cata/internal/gateway/tunnel"
)

//go:embed static/*
var staticFS embed.FS

// Server 本地控制台 HTTP。
type Server struct {
	cfg     gateway.Config
	cfgMu   sync.RWMutex
	web     *WebChat
	hub     *Hub
	httpSrv *http.Server
	reg     *tunnel.Registry // 非 nil = remote 模式：项目列表/路由来自在线 agent
}

// NewServer 创建 UI 服务器（本地模式）。
func NewServer(cfg gateway.Config, hub *Hub) *Server {
	return NewServerWithRegistry(cfg, hub, nil)
}

// NewServerWithRegistry 创建 UI 服务器；reg 非 nil 时运行在 remote 模式：
// 项目 = 在线 agent，聊天经隧道拨到远端 per-ws socket。
func NewServerWithRegistry(cfg gateway.Config, hub *Hub, reg *tunnel.Registry) *Server {
	if hub == nil {
		hub = DefaultHub
	}
	if reg != nil {
		return &Server{cfg: cfg, web: NewWebChatWithRegistry(cfg, reg), hub: hub, reg: reg}
	}
	return &Server{cfg: cfg, web: NewWebChat(cfg), hub: hub}
}

// Run 监听直到 ctx 取消。
func (s *Server) Run(ctx context.Context) error {
	addr := s.cfg.ResolvedUIListen()
	if addr == "" {
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/projects", s.handleProjects)
	mux.HandleFunc("/api/projects/", s.handleProjectAction)
	mux.HandleFunc("/api/channels", s.handleChannels)
	mux.HandleFunc("/api/channels/", s.handleChannelMessages)
	mux.HandleFunc("/api/events", s.handleEventsSSE)
	mux.HandleFunc("/api/settings/app", s.handleSettingsApp)
	mux.HandleFunc("/api/settings/gateway", s.handleSettingsGateway)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("ui listen %s: %w", addr, err)
	}
	s.httpSrv = &http.Server{
		Handler:           s.lanOrLocalOnly(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("cata-gateway: UI http://%s/ (LAN: use this machine's IP:port)", ln.Addr().String())

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.httpSrv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shCtx)
		s.web.Close()
		return ctx.Err()
	case err := <-errCh:
		s.web.Close()
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// lanOrLocalOnly 允许本机与 RFC1918/链路本地局域网；拒绝公网直连作轻量防护。
// 配置 UIPassword 时叠加 HTTP Basic 口令：LAN-only 只是「够得着」限制，不是授权。
func (s *Server) lanOrLocalOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if !isAllowedUIClient(ip) {
			http.Error(w, "LAN or localhost only", http.StatusForbidden)
			return
		}
		if s.uiPasswordRequired() && !s.checkUIPassword(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="cata-gateway"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// uiPasswordRequired 是否配置了 UI 访问口令。
func (s *Server) uiPasswordRequired() bool {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return strings.TrimSpace(s.cfg.UIPassword) != ""
}

// checkUIPassword 校验 HTTP Basic 口令（常量时间比较）。
func (s *Server) checkUIPassword(r *http.Request) bool {
	s.cfgMu.RLock()
	want := s.cfg.UIPassword
	s.cfgMu.RUnlock()
	if want == "" {
		return true
	}
	_, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(pass), []byte(want)) == 1
}

func isAllowedUIClient(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func (s *Server) loadCfg() gateway.Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

func (s *Server) setCfg(cfg gateway.Config) {
	s.cfgMu.Lock()
	s.cfg = cfg
	s.web.cfg = cfg
	s.cfgMu.Unlock()
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(staticFS, "static/index.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	if s.reg != nil {
		// remote 模式：项目 = 当前在线 agent。
		type arow struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Path        string `json:"path"`
			Kind        string `json:"kind,omitempty"`
			HomeDir     string `json:"home_dir,omitempty"`
			LastSeen    string `json:"last_seen_at,omitempty"`
			ConnectedAt string `json:"connected_at,omitempty"`
			RemoteAddr  string `json:"remote_addr,omitempty"`
		}
		agents := s.reg.OnlineAgents()
		out := make([]arow, 0, len(agents))
		for _, a := range agents {
			name := a.Name
			if name == "" {
				name = a.AgentID
			}
			out = append(out, arow{
				ID:          a.AgentID,
				Name:        name,
				Path:        a.RootPath,
				Kind:        "agent",
				HomeDir:     a.RootPath,
				ConnectedAt: a.ConnectedAt,
				RemoteAddr:  a.RemoteAddr,
			})
		}
		writeJSON(w, out)
		return
	}
	list, err := brain.ListHomeWorkspaces()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type row struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Path     string `json:"path"`
		Kind     string `json:"kind,omitempty"`
		HomeDir  string `json:"home_dir,omitempty"`
		LastSeen string `json:"last_seen_at,omitempty"`
	}
	out := make([]row, 0, len(list))
	for _, ws := range list {
		out = append(out, row{
			ID:       ws.ID,
			Name:     ws.Name,
			Path:     ws.RootPath,
			Kind:     string(ws.Kind),
			HomeDir:  ws.HomeDir,
			LastSeen: ws.LastSeenAt,
		})
	}
	writeJSON(w, out)
}

func (s *Server) resolveProject(id string) (gateway.Project, bool) {
	if s.reg != nil {
		a, ok := s.reg.FindAgent(id)
		if !ok {
			return gateway.Project{}, false
		}
		name := a.Name
		if name == "" {
			name = a.AgentID
		}
		return gateway.Project{ID: a.AgentID, Name: name, Path: a.RootPath}, true
	}
	ws, ok := brain.FindHomeWorkspace(id)
	if !ok {
		return gateway.Project{}, false
	}
	name := ws.Name
	if name == "" {
		name = ws.ID
	}
	return gateway.Project{ID: ws.ID, Name: name, Path: ws.RootPath}, true
}

func (s *Server) handleProjectAction(w http.ResponseWriter, r *http.Request) {
	// /api/projects/:id[/chat|/reset|/confirm|/choice]
	path := strings.TrimPrefix(r.URL.Path, "/api/projects/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	proj, ok := s.resolveProject(id)
	if !ok && action != "" {
		http.Error(w, "project not found", 404)
		return
	}

	switch {
	case action == "chat" && r.Method == http.MethodPost:
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		text := strings.TrimSpace(body.Text)
		if text == "" {
			http.Error(w, "empty text", 400)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		emit := func(ev StreamEvent) error {
			b, err := json.Marshal(ev)
			if err != nil {
				return err
			}
			if _, err := w.Write(append(b, '\n')); err != nil {
				return err
			}
			if flusher != nil {
				flusher.Flush()
			}
			return nil
		}
		result, err := s.web.Chat(r.Context(), proj, text, emit)
		if err != nil {
			_ = emit(StreamEvent{"type": "error", "message": err.Error()})
			_ = emit(StreamEvent{"type": "done", "success": false})
			return
		}
		_ = emit(StreamEvent{
			"type":      "done",
			"success":   result.Success && result.ErrMsg == "",
			"cancelled": result.Cancelled,
			"error":     result.ErrMsg,
		})
	case action == "reset" && r.Method == http.MethodPost:
		if err := s.web.Reset(proj); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	case action == "confirm" && r.Method == http.MethodPost:
		var body struct {
			ConfirmID string `json:"confirm_id"`
			Approved  bool   `json:"approved"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := s.web.ResolveConfirm(body.ConfirmID, body.Approved); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	case action == "choice" && r.Method == http.MethodPost:
		var body struct {
			ChoiceID string   `json:"choice_id"`
			Selected []string `json:"selected"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := s.web.ResolveChoice(body.ChoiceID, body.Selected); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (s *Server) handleChannels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	keys := s.hub.Sessions()
	type row struct {
		Session string `json:"session"`
		Last    any    `json:"last,omitempty"`
	}
	out := make([]row, 0, len(keys))
	for _, k := range keys {
		evs := s.hub.RecentSession(k, 1)
		var last any
		if len(evs) > 0 {
			last = evs[len(evs)-1]
		}
		out = append(out, row{Session: k, Last: last})
	}
	writeJSON(w, out)
}

func (s *Server) handleChannelMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/api/channels/")
	key = strings.Trim(key, "/")
	if i := strings.Index(key, "/"); i >= 0 {
		key = key[:i]
	}
	if key == "" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, s.hub.RecentSession(key, 100))
}

func (s *Server) handleEventsSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "sse unsupported", 500)
		return
	}
	// snapshot
	for _, ev := range s.hub.Recent(50) {
		b, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", b)
	}
	flusher.Flush()

	ch, cancel := s.hub.Subscribe()
	defer cancel()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(ev)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
