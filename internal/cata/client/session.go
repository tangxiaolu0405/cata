package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"sync"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

type req struct {
	Command      string            `json:"command"`
	Text         string            `json:"text,omitempty"`
	Stream       bool              `json:"stream,omitempty"`
	ConfirmID    string            `json:"confirm_id,omitempty"`
	Approved     bool              `json:"approved,omitempty"`
	Cwd          string            `json:"cwd,omitempty"`
	Runtime      *brain.RuntimeEnv `json:"runtime,omitempty"`
	ShowThinking bool              `json:"show_thinking,omitempty"`
	Attachments  []attachReq       `json:"attachments,omitempty"`
}

// attachReq 单个附件请求（与 server.AttachmentReq 的 JSON 对齐）：path 与 inline 二选一。
type attachReq struct {
	Path   string        `json:"path,omitempty"`
	Inline *inlineAttach `json:"inline,omitempty"`
}

// inlineAttach 客户端已编码的附件内容（TUI 粘贴/拖拽等场景）。
type inlineAttach struct {
	MIME   string `json:"mime,omitempty"`
	Base64 string `json:"base64,omitempty"`
}

type resp struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type session struct {
	conn        net.Conn
	br          *bufio.Reader
	writeMu     sync.Mutex // 串行化客户端→服务器写入（chat / chat_cancel / exec_confirm / user_choice）
	readMu      sync.Mutex // 串行化读取；与 writeMu 分离，避免流式读阻塞时 chat_cancel 写不出去
	lastExecCmd string
	lastExecCwd string
}

// dialAgent 拨某工作空间的 per-ws agent socket（~/.cata/sockets/<ws_id>.sock）。
// 一个工作空间 = 一个 agent 进程 = 一个 LLM loop。
func dialAgent(wsID string) (*session, error) {
	return dialPath(config.ResolvedAgentSocketPath(wsID))
}

func dialPath(socketPath string) (*session, error) {
	if err := config.InitBrainPath(); err != nil {
		return nil, err
	}
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return &session{conn: conn, br: bufio.NewReader(conn)}, nil
}

func (s *session) write(v any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.conn.Write(append(b, '\n'))
	return err
}

func (s *session) readLine() ([]byte, error) {
	s.readMu.Lock()
	defer s.readMu.Unlock()
	line, err := s.br.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	return sanitizeSocketLine(line), nil
}

func sanitizeSocketLine(line []byte) []byte {
	line = bytes.ReplaceAll(line, []byte{0}, nil)
	return bytes.TrimSpace(line)
}

func (s *session) call(r req) (resp, error) {
	if err := s.write(r); err != nil {
		return resp{}, err
	}
	line, err := s.readLine()
	if err != nil {
		return resp{}, err
	}
	var out resp
	return out, json.Unmarshal(line, &out)
}

func (s *session) writeChoice(choiceID string, selected []string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	b, err := json.Marshal(map[string]any{
		"command":   "user_choice",
		"choice_id": choiceID,
		"selected":  selected,
	})
	if err != nil {
		return err
	}
	_, err = s.conn.Write(append(b, '\n'))
	return err
}
