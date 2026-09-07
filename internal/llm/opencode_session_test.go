package llm

import (
	"net/http"
	"testing"
)

// TestApplyOutboundHeadersOpenCodeSession 验证 opencode 网关自动附加 x-opencode-session：
// per-会话 id 优先、headers 显式值次之、二者皆无则用进程回退值；非 opencode host 不加。
func TestApplyOutboundHeadersOpenCodeSession(t *testing.T) {
	mk := func() *http.Request {
		r, _ := http.NewRequest(http.MethodPost, "https://opencode.ai/zen/go/v1/chat/completions", nil)
		return r
	}

	// 有 server per-session id：用它。
	c1 := &Client{opencodeSessionID: "session-abc", apiURL: "https://opencode.ai/zen/go/v1/chat/completions"}
	req := mk()
	applyOutboundHeaders(req, c1)
	if got := req.Header.Get("x-opencode-session"); got != "session-abc" {
		t.Fatalf("per-session 头=%q want session-abc", got)
	}

	// 无 per-session id 但 headers 显式配置：用配置值。
	c2 := &Client{apiURL: "https://opencode.ai/v1/chat/completions",
		extraHeaders: map[string]string{"x-opencode-session": "cfg-v"}}
	req = mk()
	applyOutboundHeaders(req, c2)
	if got := req.Header.Get("x-opencode-session"); got != "cfg-v" {
		t.Fatalf("headers 值=%q want cfg-v", got)
	}

	// 都无：回退生成进程稳定值（非空且稳定）。
	c3 := &Client{apiURL: "https://opencode.ai/v1/chat/completions"}
	req = mk()
	applyOutboundHeaders(req, c3)
	a := req.Header.Get("x-opencode-session")
	if a == "" {
		t.Fatal("回退值不应为空")
	}
	// 其它普通 host 不加。
	c4 := &Client{apiURL: "https://api.deepseek.com/chat/completions"}
	req2, _ := http.NewRequest(http.MethodPost, "https://api.deepseek.com/chat/completions", nil)
	applyOutboundHeaders(req2, c4)
	if h := req2.Header.Get("x-opencode-session"); h != "" {
		t.Fatalf("非 opencode 不应加头，got %q", h)
	}

	// config extraHeaders 里的其它自定义头应附加到非 opencode 也生效。
	c5 := &Client{apiURL: "https://api.deepseek.com/chat/completions",
		extraHeaders: map[string]string{"x-my-hdr": "v1"}}
	req3, _ := http.NewRequest(http.MethodPost, "https://api.deepseek.com/chat/completions", nil)
	applyOutboundHeaders(req3, c5)
	if req3.Header.Get("x-my-hdr") != "v1" {
		t.Fatalf("自定义头未附加 x-my-hdr=%q", req3.Header.Get("x-my-hdr"))
	}
}

// TestIsOpenCodeHost 域名判定。
func TestIsOpenCodeHost(t *testing.T) {
	yes := []string{
		"https://opencode.ai/zen/go/v1/chat/completions",
		"https://a.opencode.ai/v1/chat/completions",
		"http://opencode.ai/foo",
	}
	no := []string{
		"https://api.deepseek.com/chat/completions",
		"https://openai.com/v1",
		"https://notopencode.ai/",
		"https://x/opencode.ai", // 非域名后缀
	}
	for _, u := range yes {
		if !isOpenCodeHost(u) {
			t.Errorf("isOpenCodeHost(%q)=false want true", u)
		}
	}
	for _, u := range no {
		if isOpenCodeHost(u) {
			t.Errorf("isOpenCodeHost(%q)=true want false", u)
		}
	}
}
