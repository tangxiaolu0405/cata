package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"sync"

	"cata/internal/brain"
	"cata/internal/config"
)

type req struct {
	Command   string            `json:"command"`
	Text      string            `json:"text,omitempty"`
	Stream    bool              `json:"stream,omitempty"`
	ConfirmID string            `json:"confirm_id,omitempty"`
	Approved  bool              `json:"approved,omitempty"`
	Cwd       string            `json:"cwd,omitempty"`
	Runtime   *brain.RuntimeEnv `json:"runtime,omitempty"`
}

type resp struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type session struct {
	conn        net.Conn
	br          *bufio.Reader
	mu          sync.Mutex
	lastExecCmd string
	lastExecCwd string
}

func dial() (*session, error) {
	if err := config.InitBrainPath(); err != nil {
		return nil, err
	}
	conn, err := net.Dial("unix", config.ResolvedSocketPath())
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	return &session{conn: conn, br: bufio.NewReader(conn)}, nil
}

func (s *session) write(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.conn.Write(append(b, '\n'))
	return err
}

func (s *session) readLine() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
