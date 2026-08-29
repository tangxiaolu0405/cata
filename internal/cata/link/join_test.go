package link

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestJoinBaseURL(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"https://gw.example.com", "https://gw.example.com", false},
		{"ws://127.0.0.1:8799", "http://127.0.0.1:8799", false},
		{"wss://gw.example.com", "https://gw.example.com", false},
		{"http://gw:8080/", "http://gw:8080", false},
		// 无 scheme：自动补 http://，避免 localhost 被 url.Parse 误当 scheme。
		{"localhost:8799", "http://localhost:8799", false},
		{"127.0.0.1:8788", "http://127.0.0.1:8788", false},
		{"gw.example.com", "http://gw.example.com", false},
		{"192.168.1.5:8800", "http://192.168.1.5:8800", false},
		{"ftp://x", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := joinBaseURL(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("joinBaseURL(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("joinBaseURL(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("joinBaseURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestTunnelWSURLNoScheme 隧道连接同样要接受无 scheme 的网关地址（自动补 http:// 再转 ws://）。
func TestTunnelWSURLNoScheme(t *testing.T) {
	got, err := tunnelWSURL("localhost:8799", "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ws://localhost:8799/cata/v1/tunnel?agent=agent-1" {
		t.Fatalf("got %q", got)
	}
	got, err = tunnelWSURL("https://gw.example.com", "a2")
	if err != nil {
		t.Fatal(err)
	}
	if got != "wss://gw.example.com/cata/v1/tunnel?agent=a2" {
		t.Fatalf("got %q", got)
	}
	if _, err := tunnelWSURL("ftp://x", "a"); err == nil {
		t.Fatal("ftp should be rejected")
	}
}

func TestJoinRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cata/v1/join/challenge":
			fmt.Fprint(w, `{"challenge":"nonce-1","sig":"sig-1"}`)
			return
		case "/cata/v1/join/request":
			// 校验协议头 + 挑战头被正确携带。
			if r.Header.Get(JoinProtoHeaderName) != JoinProtoHeaderValue {
				t.Errorf("missing %s header", JoinProtoHeaderName)
			}
			if r.Header.Get(JoinChallengeHeaderName) != "nonce-1" {
				t.Errorf("challenge header = %q, want nonce-1", r.Header.Get(JoinChallengeHeaderName))
			}
			if r.Header.Get(JoinChallengeSigHeaderName) != "sig-1" {
				t.Errorf("sig header = %q, want sig-1", r.Header.Get(JoinChallengeSigHeaderName))
			}
			var body struct {
				MachineID string `json:"machine_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode: %v", err)
			}
			if body.MachineID != "test-machine" {
				t.Errorf("machine_id = %q, want test-machine", body.MachineID)
			}
			fmt.Fprint(w, `{"join_code":"abc123"}`)
			return
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	code, err := joinRequest(srv.URL, "test-machine")
	if err != nil {
		t.Fatal(err)
	}
	if code != "abc123" {
		t.Fatalf("code = %q, want abc123", code)
	}
}

func TestPollJoinStatus(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if !strings.Contains(r.URL.Path, "/cata/v1/join/status") {
			t.Errorf("path = %s", r.URL.Path)
		}
		if requests == 1 {
			fmt.Fprint(w, `{"approved":false}`)
		} else {
			fmt.Fprint(w, `{"approved":true,"machine_token":"tok-1"}`)
		}
	}))
	defer srv.Close()

	token, err := pollJoinStatus(srv.URL, "code", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if token != "tok-1" {
		t.Fatalf("token = %q, want tok-1", token)
	}
	if requests < 2 {
		t.Fatalf("should poll until approved, requests=%d", requests)
	}
}

func TestJoinRequestServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := joinRequest(srv.URL, "m"); err == nil {
		t.Fatal("expected error on 500")
	}
}
