package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/cata/scheduler"
	"cata/internal/cata/scheduler/runner"
	"cata/internal/cata/server"
)

// fakeLLM 返回 OpenAI 兼容 SSE 端点：每次 POST 回一段固定文本（无 tool call）。
func fakeLLM(t *testing.T, reply string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		payload := map[string]interface{}{
			"id":     "chatcmpl-test",
			"object": "chat.completion.chunk",
			"model":  "test-model",
			"choices": []map[string]interface{}{
				{
					"index":         0,
					"delta":         map[string]interface{}{"role": "assistant", "content": reply},
					"finish_reason": "stop",
				},
			},
		}
		b, _ := json.Marshal(payload)
		_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	return ts
}

// startServer 在临时 CATA_HOME 上启动一个真实 socket server（LLM 指向 fake），返回 socket 路径与清理函数。
func startServer(t *testing.T, apiURL string) (socketPath string, cleanup func()) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvCataHome, home)
	t.Setenv(config.EnvConfigFile, filepath.Join(home, "config.json"))

	sock := filepath.Join(os.TempDir(), fmt.Sprintf("cata-%d.sock", os.Getpid()))
	cfg := fmt.Sprintf(`{
		"llm":{"enabled":true,"provider":"test","api_format":"openai","api_key":"test","api_url":%q,"model":"test-model","max_tokens":1024,"timeout":30},
		"server":{"socket_path":%q},
		"mcp":{"enabled":false},
		"exec":{"enabled":true},
		"workspace_files":{"enabled":true},
		"evolution":{"enabled":false},
		"schedules":{"enabled":true,"tick_seconds":30}
	}`, apiURL, sock)
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadConfig(); err != nil {
		t.Fatal(err)
	}

	srv, err := server.NewServer(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	return config.ResolvedSocketPath(), func() {
		srv.Stop()
	}
}

func TestRunnerFiresRealClientChatWritesReportAndAudit(t *testing.T) {
	llm := fakeLLM(t, "已整理 5 个候选商品：A、B、C、D、E")
	defer llm.Close()
	socketPath, stop := startServer(t, llm.URL+"/chat/completions")
	defer stop()

	root := t.TempDir()
	sched := &scheduler.Schedule{
		ID:       "selpin",
		Name:     "每日选品",
		Prompt:   "去电商平台看今日热门商品并整理候选清单",
		Cwd:      root,
		WSID:     "ws",
		Interval: "24h",
		Enabled:  true,
		Project:  root, // 项目级排程
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := runner.Run(ctx, sched, socketPath)
	if err != nil {
		t.Fatalf("runner.Run: %v", err)
	}
	if !res.Success {
		t.Fatalf("run not successful: %+v", res)
	}
	if !strings.Contains(res.Summary, "候选商品") {
		t.Fatalf("summary = %q, want candidate products mention", res.Summary)
	}
	if res.ReportPath == "" {
		t.Fatalf("report path empty")
	}
	report, err := os.ReadFile(res.ReportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(report), "每日选品") || !strings.Contains(string(report), "候选商品") {
		t.Fatalf("report should include name and result:\n%s", report)
	}
	// 项目级排程默认报告落在 <project>/.cata/schedule-runs/<id>/。
	if !strings.HasPrefix(res.ReportPath, filepath.Join(root, ".cata", "schedule-runs", "selpin")) {
		t.Fatalf("report path = %q, want under project .cata/schedule-runs", res.ReportPath)
	}

	// 审计 JSONL：项目 <root>/.cata/schedules/runs/<id>/<ts>.jsonl 含 done/token 事件。
	entries, err := os.ReadDir(scheduler.RunsDirFor(sched))
	if err != nil {
		t.Fatalf("read audit dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit dir has %d files, want 1", len(entries))
	}
	audit, err := os.ReadFile(filepath.Join(scheduler.RunsDirFor(sched), entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(audit), `"type":"done"`) && !strings.Contains(string(audit), `"type": "done"`) {
		t.Fatalf("audit should contain done event:\n%s", audit)
	}
	if !strings.Contains(string(audit), "候选商品") {
		t.Fatalf("audit should contain token stream:\n%s", audit)
	}
}

func TestRunnerEngineEndToEnd(t *testing.T) {
	llm := fakeLLM(t, "完成")
	defer llm.Close()
	socketPath, stop := startServer(t, llm.URL+"/chat/completions")
	defer stop()

	root := t.TempDir()
	// 注册工作区：项目级排程发现依赖工作区 registry（ListAll 遍历已注册根）。
	if _, err := brain.ResolveWorkspace(root); err != nil {
		t.Fatal(err)
	}
	sched := &scheduler.Schedule{
		Name:     "task",
		Prompt:   "跑一下",
		Cwd:      root,
		Interval: "24h",
		Enabled:  true,
		Project:  root,
		// 已到点：引擎扫描时直接触发。
		NextRun: time.Now().Add(-time.Minute).Format(time.RFC3339),
	}
	if err := scheduler.Save(sched); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng := scheduler.NewEngine(50*time.Millisecond, func(ctx context.Context, s *scheduler.Schedule) (scheduler.RunResult, error) {
		return runner.Run(ctx, s, socketPath)
	})
	eng.Start(ctx)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		loaded, _, err := scheduler.Find(sched.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded == nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if loaded.LastRun != nil && loaded.LastRun.Success {
			if loaded.LastRun.Report == "" {
				t.Fatal("last_run.report should be set")
			}
			if loaded.NextRun == "" {
				t.Fatal("next_run should be recomputed")
			}
			if loaded.Project != root {
				t.Fatalf("project = %q, want %q", loaded.Project, root)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("engine never persisted a successful last_run")
}

var _ = brain.KindGit
