package qq

import (
	"encoding/json"
	"testing"
)

func TestParseC2C(t *testing.T) {
	raw := []byte(`{"id":"m1","content":"hello","author":{"user_openid":"u1"}}`)
	msg, ok := ParseC2C(raw)
	if !ok {
		t.Fatal("parse")
	}
	if msg.Kind != "c2c" || msg.UserOpenID != "u1" || msg.Content != "hello" {
		t.Fatalf("%+v", msg)
	}
	if SessionIDFor(msg) != "c2c_u1" {
		t.Fatal(SessionIDFor(msg))
	}
}

func TestParseGroupAT(t *testing.T) {
	raw := []byte(`{"id":"m2","group_openid":"g1","content":"<@!bot> hi","author":{"member_openid":"m1"}}`)
	msg, ok := ParseGroupAT(raw)
	if !ok {
		t.Fatal("parse")
	}
	if msg.Content != "hi" || msg.GroupOpenID != "g1" {
		t.Fatalf("%+v", msg)
	}
	if SessionIDFor(msg) != "group_g1" {
		t.Fatal(SessionIDFor(msg))
	}
}

func TestStripAtMention(t *testing.T) {
	if got := StripAtMention("<@!123>  foo"); got != "foo" {
		t.Fatal(got)
	}
}

func TestSplitMessage(t *testing.T) {
	parts := SplitMessage(string(make([]byte, 2500)), 2000)
	if len(parts) < 2 {
		t.Fatalf("parts=%d", len(parts))
	}
}

func TestParseExpiresInJSON(t *testing.T) {
	var v any
	_ = json.Unmarshal([]byte(`"7200"`), &v)
	if parseExpiresIn(v) != 7200 {
		t.Fatal(parseExpiresIn(v))
	}
}
