package ui

import (
	"context"
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

	"cata/internal/cata/link"
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
	reg     *tunnel.Registry    // 非 nil = remote 模式：项目列表/路由来自在线 agent
	join    *tunnel.JoinManager // 非 nil = remote 模式：UI 批准机器接入（进程内，免跨域/免 token）
	bans    *ipBan              // 连续登录失败封 IP（仅 ui_password 启用时生效）
	session *sessionStore       // 登录会话（仅 ui_password 启用时生效）
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
	s := &Server{cfg: cfg, hub: hub}
	if reg != nil {
		s.web = NewWebChatWithRegistry(cfg, reg)
		s.reg = reg
	} else {
		s.web = NewWebChat(cfg)
	}
	if strings.TrimSpace(cfg.UIPassword) != "" {
		s.bans = newIPBan(cfg.ResolvedLoginBanMaxAttempts(), cfg.ResolvedLoginBanDuration())
		s.session = newSessionStore(cfg.UIPassword, 24*time.Hour)
	}
	return s
}

// NewServerWithRegistryAndJoin 同 NewServerWithRegistry，并绑定 join 管理器（remote 模式
// UI 批准机器接入用）。本地模式 join 为 nil。
func NewServerWithRegistryAndJoin(cfg gateway.Config, hub *Hub, reg *tunnel.Registry, join *tunnel.JoinManager) *Server {
	s := NewServerWithRegistry(cfg, hub, reg)
	s.join = join
	return s
}

// Run 监听直到 ctx 取消。
func (s *Server) Run(ctx context.Context) error {
	addr := s.cfg.ResolvedUIListen()
	if addr == "" {
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc(LoginPath, s.handleLoginPage)
	mux.HandleFunc(LoginAPIPath, s.handleLoginAPI)
	mux.HandleFunc("/api/logout", s.handleLogout)
	mux.HandleFunc("/api/projects", s.handleProjects)
	mux.HandleFunc("/api/projects/", s.handleProjectAction)
	mux.HandleFunc("/api/machines", s.handleMachines)
	mux.HandleFunc("/api/machines/", s.handleMachineAction)
	mux.HandleFunc("/api/join/pending", s.handleJoinPending)
	mux.HandleFunc("/api/join/approve", s.handleJoinApprove)
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
		Handler:           s.authHandler(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

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

// authHandler 统一鉴权中间件：
//   - 未配置 ui_password：保持 LAN-only（公网直连拒绝），不弹登录页（向后兼容）。
//   - 已配置 ui_password：放开公网，但需登录会话 cookie；未登录跳 /login。
//     登录接口 /login、/api/login、/api/logout 免鉴权。连续失败达阈值封该 IP LoginBanDuration。
func (s *Server) authHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.uiPasswordRequired() {
			// 无口令：仅本机与 RFC1918/链路本地局域网（公网直连拒绝）。
			ip := net.ParseIP(stripPort(r.RemoteAddr))
			if !isAllowedUIClient(ip) {
				http.Error(w, "LAN or localhost only", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		// 已启用登录：IP 封禁检查。
		clientIP := clientIPFromRequest(r)
		if rem := s.bans.bannedRemaining(clientIP); rem > 0 {
			http.Error(w, "too many failed attempts; try again later", http.StatusForbidden)
			return
		}
		// 登录页/接口免鉴权。
		if isLoginRoute(r.URL.Path) || r.URL.Path == "/api/logout" {
			next.ServeHTTP(w, r)
			return
		}
		// 其余需有效会话。
		if !s.validSession(r) {
			serveLoginResult(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// uiPasswordRequired 是否配置了 UI 访问口令（启用登录页 + 封 IP）。
func (s *Server) uiPasswordRequired() bool {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return strings.TrimSpace(s.cfg.UIPassword) != ""
}

// validSession 校验请求是否携带有效登录会话 cookie。
func (s *Server) validSession(r *http.Request) bool {
	if s.session == nil {
		return false
	}
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return false
	}
	return s.session.valid(c.Value)
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

// handleLoginPage 返回登录页（仅口令已配置时有意义；未配置时仍返回页面但提交会 400）。
func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	if !s.uiPasswordRequired() {
		http.Error(w, "login disabled: set ui_password in gateway.json", http.StatusBadRequest)
		return
	}
	data, err := fs.ReadFile(staticFS, "static/login.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// handleLoginAPI 校验口令并签发会话 cookie；连续失败达阈值封该 IP。
func (s *Server) handleLoginAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if !s.uiPasswordRequired() || s.session == nil {
		http.Error(w, `{"error":"login disabled"}`, http.StatusBadRequest)
		return
	}
	ip := clientIPFromRequest(r)
	if rem := s.bans.bannedRemaining(ip); rem > 0 {
		http.Error(w, `{"error":"too many failed attempts; try again later"}`, http.StatusForbidden)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	token, ok := s.session.create(strings.TrimSpace(body.Password))
	if !ok {
		banned, _ := s.bans.recordFailure(ip)
		if banned {
			log.Printf("cata-gateway: IP %s banned after %d failed logins", ip, s.bans.max)
			http.Error(w, `{"error":"too many failed attempts; try again later"}`, http.StatusForbidden)
			return
		}
		http.Error(w, `{"error":"invalid password"}`, http.StatusUnauthorized)
		return
	}
	s.bans.recordSuccess(ip)
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(24 * time.Hour.Seconds()),
	})
	writeJSON(w, map[string]any{"ok": true})
}

// handleLogout 注销当前会话。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if c, err := r.Cookie(SessionCookieName); err == nil {
		s.session.destroy(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
	writeJSON(w, map[string]any{"ok": true})
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
			MachineID   string `json:"machine_id,omitempty"`
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
				MachineID:   a.MachineID,
				HomeDir:     a.RootPath,
				ConnectedAt: a.ConnectedAt,
				RemoteAddr:  a.RemoteAddr,
			})
		}
		writeJSON(w, out)
		return
	}
	// 本地模式：项目 = link.json 里注册的工作空间（agent 注册表），
	// 与 remote 的「在线 agent」同构——不再扫描 ~/.cata/brain/workspaces（legacy 读取已废弃）。
	type row struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Path      string `json:"path"`
		Kind      string `json:"kind,omitempty"`
		MachineID string `json:"machine_id,omitempty"`
		Enabled   bool   `json:"enabled,omitempty"`
		KeepAlive bool   `json:"keep_alive,omitempty"`
	}
	entries, err := link.List()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	machine := link.MachineID()
	out := make([]row, 0, len(entries))
	for _, e := range entries {
		name := e.Name
		if name == "" {
			name = e.AgentID
		}
		out = append(out, row{
			ID:        e.AgentID,
			Name:      name,
			Path:      e.RootPath,
			Kind:      "agent",
			MachineID: machine,
			Enabled:   e.Enabled,
			KeepAlive: e.KeepAlive,
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
	// 本地模式：从 agent 注册表（link.json）按 id 查，与 remote 对称。
	entries, err := link.List()
	if err != nil {
		return gateway.Project{}, false
	}
	for _, e := range entries {
		if e.AgentID == id {
			name := e.Name
			if name == "" {
				name = e.AgentID
			}
			return gateway.Project{ID: e.AgentID, Name: name, Path: e.RootPath}, true
		}
	}
	return gateway.Project{}, false
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

// handleMachines GET /api/machines → 在线机器列表（remote 模式；本地模式返回空）。
// 机器 = 分组维度：register 控制帧按 machine_id 路由到该机器任一在线 agent。
func (s *Server) handleMachines(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	if s.reg == nil {
		writeJSON(w, []string{})
		return
	}
	writeJSON(w, s.reg.Machines())
}

// handleMachineAction POST /api/machines/:id/register → 向该机器下发注册工作空间控制帧。
// body: {"subpath": "相对该机 workspace_root 的子路径"}。校验与越界防护在 worker 侧完成。
func (s *Server) handleMachineAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/machines/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	machineID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	if action != "register" || r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if s.reg == nil {
		http.Error(w, "remote mode only", 400)
		return
	}
	var body struct {
		Subpath string `json:"subpath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := s.reg.SendRegister(machineID, body.Subpath); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleJoinPending GET /api/join/pending → 待批准机器接入列表（remote 模式）。
// UI 展示后一键批准，管理员无需复制机器打印的 code。
func (s *Server) handleJoinPending(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", 405)
		return
	}
	if s.join == nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, s.join.Pending())
}

// handleJoinApprove POST /api/join/approve → UI 批准机器接入（remote 模式）。
// 走 UI 端口、进程内调 JoinManager，无需 gateway_token、无跨域；防护靠登录会话（authHandler）。
func (s *Server) handleJoinApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if s.join == nil {
		http.Error(w, "remote mode only", 400)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	machineID, err := s.join.ApproveJoin(body.Code)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "machine_id": machineID})
}
