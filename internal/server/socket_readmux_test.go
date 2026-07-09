package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestConnLineReaderSurvivesIdlePast30s(t *testing.T) {
	client, server := net.Pipe()
	br := bufio.NewReader(server)
	lr := newConnLineReader(br, server, nil)
	defer lr.Stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(35 * time.Millisecond)
		_, _ = client.Write([]byte(`{"command":"exec_confirm","confirm_id":"x","approved":true}` + "\n"))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raw, err := lr.waitLine(ctx, time.Now().Add(2*time.Second))
	if err != nil {
		t.Fatalf("waitLine: %v", err)
	}
	var req struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Command != "exec_confirm" {
		t.Fatalf("command=%q", req.Command)
	}
	<-done
	_ = client.Close()
}
