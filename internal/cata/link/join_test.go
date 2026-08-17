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

func TestJoinRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cata/v1/join/request" {
			t.Errorf("path = %s, want /cata/v1/join/request", r.URL.Path)
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
