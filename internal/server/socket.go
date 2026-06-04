package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"cata/internal/brain"
	"cata/internal/client"
	"cata/internal/config"
	"cata/internal/llm"
)

// SocketServer 处理客户端连接
type SocketServer struct {
	server       *Server
	ln           net.Listener
	chatSessions int32        // 仅统计 cata chat 长连接；ping 探测不计入
	tools        *ToolRegistry // built-in tool registry
}

// ChatSessions 返回当前交互式 chat 会话数（不含 ping 探活连接）。
func (ss *SocketServer) ChatSessions() int32 {
	return atomic.LoadInt32(&ss.chatSessions)
}

// Request 客户端请求
type Request struct {
	Command string `json:"command"`
	// Text 用于 chat 的完整用户输入
	Text string `json:"text,omitempty"`
	// Stream 为 true 时 chat 走 NDJSON 流式事件（token / tool_* / done 等）
	Stream bool `json:"stream,omitempty"`
	// ExecConfirm：流式 chat 中收到 exec_confirm_required 后由客户端发送（非 LLM）
	ConfirmID string `json:"confirm_id,omitempty"`
	Approved  bool   `json:"approved,omitempty"`
	// Cwd 产出区：当前工作目录（命令与交付物）；用于选脑子分区 + exec.cwd
	Cwd string `json:"cwd,omitempty"`
	// Runtime 客户端所在 OS/终端（注入 LLM，避免生成需多轮纠正的命令）
	Runtime *brain.RuntimeEnv `json:"runtime,omitempty"`
}

// Response 服务器响应
type Response struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// NewSocketServer 创建 socket 服务器
func NewSocketServer(srv *Server) (*SocketServer, error) {
	socketPath := getSocketPath()
	
	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(socketPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create socket directory: %w", err)
	}

	if err := client.PingServer(); err == nil {
		return nil, fmt.Errorf("cata server already running (socket: %s)", socketPath)
	}
	// 删除陈旧 socket 文件
	if _, err := os.Stat(socketPath); err == nil {
		_ = os.Remove(socketPath)
	}

	// 创建 Unix socket 监听器
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on socket: %w", err)
	}

	reg := NewToolRegistry()
	ss := &SocketServer{
		server: srv,
		ln:     ln,
		tools:  reg,
	}
	ss.RegisterBuiltinTools(reg)
	return ss, nil
}

// getSocketPath 获取 socket 文件路径（默认 CATA_HOME/cata.sock，见 internal/config）。
func getSocketPath() string {
	return config.ResolvedSocketPath()
}

// Start 启动 socket 服务器
func (ss *SocketServer) Start() {
	log.Printf("Socket server listening on: %s", ss.ln.Addr().String())
	
	go func() {
		for {
			conn, err := ss.ln.Accept()
			if err != nil {
				// 检查是否因为关闭而错误
				select {
				case <-ss.server.ctx.Done():
					return
				default:
					log.Printf("Error accepting connection: %v", err)
					continue
				}
			}
			
			// 处理每个连接
			go ss.handleConnection(conn)
		}
	}()
}

// Stop 停止 socket 服务器
func (ss *SocketServer) Stop() {
	if ss.ln != nil {
		ss.ln.Close()
		socketPath := getSocketPath()
		os.Remove(socketPath)
		log.Println("Socket server stopped")
	}
}

// handleConnection 处理客户端连接
func (ss *SocketServer) handleConnection(conn net.Conn) {
	var chatSession bool
	defer func() {
		if r := recover(); r != nil {
			log.Printf("connection handler panic: %v", r)
		}
		conn.Close()
		if chatSession {
			if atomic.AddInt32(&ss.chatSessions, -1) == 0 {
				ss.server.ClientDisconnected()
			}
		}
	}()

	var chatHistory []llm.Message

	br := bufio.NewReaderSize(conn, 64*1024)

	for {
		line, err := readClientLine(br)
		if err != nil {
			if err != io.EOF {
				log.Printf("Error reading from connection: %v", err)
			}
			break
		}

		// 解析请求
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			ss.sendResponse(conn, Response{
				Success: false,
				Message: fmt.Sprintf("Invalid request: %v", err),
			})
			continue
		}

		switch req.Command {
		case "chat":
			ss.markChatSession(&chatSession)
			if !req.Stream {
				ss.sendResponse(conn, Response{
					Success: false,
					Message: "chat requires stream:true",
				})
				continue
			}
			cwd := strings.TrimSpace(req.Cwd)
			if cwd == "" {
				cwd = config.GetBrainBaseDir()
			}
			if req.Runtime != nil {
				brain.SetRuntimeEnv(req.Runtime)
			} else {
				e := brain.DetectRuntimeEnvFromProcess()
				brain.SetRuntimeEnv(&e)
			}
			if _, err := brain.ResolveWorkspace(cwd); err != nil {
				log.Printf("resolve brain: %v", err)
			}
			if err := ss.handleTerminalChatStream(conn, br, &chatHistory, req.Text); err != nil {
				log.Printf("terminal chat stream: %v", err)
			}
			continue
		case "chat_reset":
			ss.markChatSession(&chatSession)
			chatHistory = nil
			if err := brain.AppendSessionBoundary(); err != nil {
				log.Printf("short-term session boundary: %v", err)
			}
			ss.sendResponse(conn, Response{Success: true, Message: "Conversation cleared."})
			continue
		case "chat_cancel":
			ss.sendResponse(conn, Response{Success: true, Message: "no active stream"})
			continue
		default:
			resp := ss.handleCommand(req)
			ss.sendResponse(conn, resp)
		}
	}
}

func readClientLine(br *bufio.Reader) ([]byte, error) {
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			return line, nil
		}
	}
}

func (ss *SocketServer) markChatSession(chatSession *bool) {
	if *chatSession {
		return
	}
	*chatSession = true
	atomic.AddInt32(&ss.chatSessions, 1)
}

// handleCommand 处理非 chat 类 socket 命令（终端客户端仅需 ping）。
func (ss *SocketServer) handleCommand(req Request) Response {
	switch req.Command {
	case "ping":
		return Response{Success: true, Message: "pong"}
	default:
		return Response{
			Success: false,
			Message: fmt.Sprintf("Unknown command: %s", req.Command),
		}
	}
}

// sendResponse 发送响应
func (ss *SocketServer) sendResponse(conn net.Conn, resp Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("Error marshaling response: %v", err)
		return
	}

	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		log.Printf("Error writing response: %v", err)
	}
}
