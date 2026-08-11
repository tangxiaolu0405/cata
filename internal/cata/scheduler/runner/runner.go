// Package runner 实现调度框架的「客户端自发起」执行：到点后作为真实 socket 客户端
// 连接 cata server，发起一轮 chat（run_as=scheduled，server 强制 full 工具档并跳过任务状态机），
// 自动应答 exec_confirm / user_choice，收集 token 流，写审计 JSONL 与 markdown 报告。
package runner

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/cata/clock"
	"cata/internal/cata/scheduler"
	"cata/internal/cata/socketclient"
)

// autoHandler 无人值守的流式事件应答：run_command 按 allow_exec 批准，ask_user 全空跳过。
type autoHandler struct {
	allowExec bool
}

func (h *autoHandler) OnProgress(string)  {}
func (h *autoHandler) OnToolStart(string) {}
func (h *autoHandler) OnToken(string)     {}
func (h *autoHandler) ConfirmExec(ctx context.Context, p socketclient.ExecConfirmPrompt) (bool, error) {
	if h.allowExec {
		log.Printf("scheduler: auto-approved exec: %s", p.CommandLine)
	}
	return h.allowExec, nil
}
func (h *autoHandler) Choose(ctx context.Context, p socketclient.UserChoicePrompt) ([]string, error) {
	log.Printf("scheduler: auto-skipped ask_user: %s", p.Prompt)
	return []string{}, nil
}

// Run 到点执行一条排程：真实客户端拨号 server socket 发起 chat，落盘审计与报告。
// socketPath 为 cata server 的 Unix socket 路径。
func Run(ctx context.Context, sched *scheduler.Schedule, socketPath string) (scheduler.RunResult, error) {
	if sched == nil {
		return scheduler.RunResult{}, fmt.Errorf("runner: nil schedule")
	}
	if err := sched.Validate(); err != nil {
		return scheduler.RunResult{}, fmt.Errorf("runner: invalid schedule: %w", err)
	}
	if strings.TrimSpace(socketPath) == "" {
		return scheduler.RunResult{}, fmt.Errorf("runner: empty socket path")
	}

	// 审计 JSONL：<存储目录>/runs/<id>/<ts>.jsonl，逐行记录收到的原始 NDJSON 事件。
	ts := clock.Now().Format("20060102-150405")
	auditDir := scheduler.RunsDirFor(sched)
	auditPath := filepath.Join(auditDir, ts+".jsonl")
	if err := os.MkdirAll(auditDir, 0755); err != nil {
		log.Printf("scheduler: audit dir: %v", err)
	}
	var audit *os.File
	if f, err := os.OpenFile(auditPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		audit = f
		defer audit.Close()
	} else {
		log.Printf("scheduler: open audit %s: %v", auditPath, err)
	}

	conn := socketclient.NewConn(socketPath, sched.Cwd)
	if audit != nil {
		conn.SetAuditWriter(audit)
	}
	defer conn.Close()

	res, err := conn.ChatAs(ctx, sched.Prompt, "scheduled", &autoHandler{allowExec: sched.AllowExec})
	if err != nil {
		return scheduler.RunResult{Success: false, Summary: res.Text}, err
	}
	if res.Cancelled {
		return scheduler.RunResult{Success: false, Summary: res.Text}, fmt.Errorf("scheduler: run cancelled")
	}

	summary := strings.TrimSpace(res.Text)
	reportPath := ""
	if summary != "" && res.Success {
		reportPath, err = WriteReport(sched, ts, summary, res.Success, nil)
		if err != nil {
			log.Printf("scheduler: write report for %s: %v", sched.ID, err)
		}
	}
	return scheduler.RunResult{Success: res.Success, Summary: summary, ReportPath: reportPath}, nil
}

// WriteReport 把一次运行的最终答复写成 markdown 报告。
// 默认目录 <项目>/.cata/schedule-runs/<id>/，可用 schedule.output_dir 覆盖为绝对目录。
func WriteReport(sched *scheduler.Schedule, ts, summary string, success bool, runErr error) (string, error) {
	dir := strings.TrimSpace(sched.OutputDir)
	if dir == "" {
		ws, err := brain.ResolveWorkspace(sched.Cwd)
		if err == nil && ws != nil {
			dir = filepath.Join(ws.ProjectCataRoot(), "schedule-runs", sched.ID)
		} else {
			dir = filepath.Join(sched.Cwd, ".cata", "schedule-runs", sched.ID)
		}
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, ts+".md")
	var b strings.Builder
	b.WriteString("# Schedule Run: ")
	b.WriteString(sched.Name)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "- id: `%s`\n", sched.ID)
	fmt.Fprintf(&b, "- at: %s\n", clock.RFC3339())
	fmt.Fprintf(&b, "- success: %t\n", success)
	if runErr != nil {
		fmt.Fprintf(&b, "- error: %s\n", runErr.Error())
	}
	b.WriteString("\n## Prompt\n\n")
	b.WriteString(sched.Prompt)
	b.WriteString("\n\n## Result\n\n")
	b.WriteString(summary)
	b.WriteString("\n")
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return "", err
	}
	return path, nil
}
