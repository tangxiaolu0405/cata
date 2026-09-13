package ui

import (
	"net/http"
	"strings"
	"testing"

	"cata/internal/cata/client"
)

// TestWebChatOutputLockIsPerTurn 回归：Web 控制台的产出区锁必须**按轮持有**。
//
// 旧实现按会话缓存（`pathLock` map 里的 unlocker 正常路径下永不调用），网关常驻 ⇒
// 只要在 Web 控制台对某工作区聊过一次，`cata chat` 在该目录就永久报
// "output area busy (…, pid <网关>)"。这里断言：一轮结束后锁已释放，TUI 可以打开该目录。
func TestWebChatOutputLockIsPerTurn(t *testing.T) {
	env := newWebChatTestEnv(t)

	rec := env.chat(t, `{"text":"你好"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 轮次结束后：TUI（同一 acquire 入口）应能拿到该产出区的锁。
	release, err := client.AcquireOutputLock(env.wsDir)
	if err != nil {
		t.Fatalf("产出区锁未被释放（TUI 会被永久挡住）: %v", err)
	}
	release()

	// 再来一轮，仍然能正常开始（不会因为自己上一轮的锁而 busy）。
	rec2 := env.chat(t, `{"text":"再来一条"}`)
	if rec2.Code != http.StatusOK || !strings.Contains(rec2.Body.String(), `"type":"done"`) {
		t.Fatalf("second turn failed: status=%d body=%s", rec2.Code, rec2.Body.String())
	}
}

// TestWebChatTurnBlockedByTUIHolder TUI 正持有产出区锁时，Web 轮次应明确报 busy
// （互斥仍然生效），并且失败的那一轮不得留下锁文件。
func TestWebChatTurnBlockedByTUIHolder(t *testing.T) {
	env := newWebChatTestEnv(t)

	release, err := client.AcquireOutputLock(env.wsDir) // 模拟 TUI 会话期间持有
	if err != nil {
		t.Fatal(err)
	}

	rec := env.chat(t, `{"text":"你好"}`)
	out := rec.Body.String()
	if !strings.Contains(out, "output area busy") {
		t.Fatalf("expected busy error, got status=%d body=%s", rec.Code, out)
	}
	// agent 不应收到请求。
	select {
	case req := <-env.chatReq:
		t.Fatalf("agent received a request while output area busy: %+v", req)
	default:
	}

	// 释放后正常可用，且此时锁归本轮所有 → 结束后释放。
	release()
	rec2 := env.chat(t, `{"text":"你好"}`)
	if rec2.Code != http.StatusOK {
		t.Fatalf("chat after release status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	if _, err := client.AcquireOutputLock(env.wsDir); err != nil {
		t.Fatalf("lock not released after turn: %v", err)
	}
}
