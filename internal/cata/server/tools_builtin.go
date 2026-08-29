package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
	"cata/internal/cata/execcmd"
	"cata/internal/cata/protocol"
	"cata/internal/llm"
)

// RegisterBuiltinTools adds all standard tools to the registry.
func (ss *SocketServer) RegisterBuiltinTools(reg *ToolRegistry) {
	if cfg := config.Config; cfg != nil && cfg.WorkspaceFilesEnabled() {
		reg.Register(&readFileTool{})
		reg.Register(&searchReplaceTool{})
		reg.Register(&appendFileTool{})
		reg.Register(&createFileTool{})
		reg.Register(&listFilesTool{})
	}
	if cfg := config.Config; cfg != nil && cfg.Exec.Enabled {
		reg.Register(&runCommandTool{ss: ss})
	}
	reg.Register(&runSkillTool{})
	reg.Register(&readSkillTool{})
	reg.Register(&declareTaskTool{})
	reg.Register(&askUserTool{ss: ss})
	reg.Register(&listModesTool{})
	reg.Register(&delegateModeTool{ss: ss})
	reg.Register(&caseArtifactTool{})
	reg.Register(&delegateTaskTool{ss: ss})
	reg.Register(&delegateWaitTool{ss: ss})
	reg.Register(&manageMCPTool{ss: ss})
	reg.Register(&scheduleTaskTool{})
	reg.Register(&scheduleListTool{})
	reg.Register(&scheduleRemoveTool{})
	reg.Register(&scheduleCancelTool{})
}

// --- read_file ---

type readFileTool struct{}

func (t *readFileTool) Name() string { return "read_file" }

func (t *readFileTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "read_file",
		Description: "Read a text file. Path: default=output cwd; " + brain.ChatBrainToolPathNote + " global/…=~/.cata/global/. Response includes resolved= absolute path.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path: default=output; brain/persona.local|modes|skills → focus_path/.cata/; brain/memory/ → home cell; global/…"},"offset":{"type":"integer","description":"1-based start line (optional)"},"limit":{"type":"integer","description":"Max lines from offset (optional)"}},"required":["path"]}`),
	}}
}

func (t *readFileTool) Execute(ctx context.Context, _ net.Conn, argsJSON string) (string, error) {
	return toolReadFile(ctx, argsJSON)
}

// --- search_replace ---

type searchReplaceTool struct{}

func (t *searchReplaceTool) Name() string { return "search_replace" }

func (t *searchReplaceTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "search_replace",
		Description: "Replace old_string with new_string (first match unless replace_all). " + brain.ChatBrainToolPathNote + " Response includes resolved=.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"},"replace_all":{"type":"boolean"}},"required":["path","old_string","new_string"]}`),
	}}
}

func (t *searchReplaceTool) Execute(ctx context.Context, _ net.Conn, argsJSON string) (string, error) {
	return toolSearchReplace(ctx, argsJSON)
}

// --- append_file ---

type appendFileTool struct{}

func (t *appendFileTool) Name() string { return "append_file" }

func (t *appendFileTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "append_file",
		Description: "Append text to a file (creates if missing). " + brain.ChatBrainToolPathNote + " Response includes resolved=.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`),
	}}
}

func (t *appendFileTool) Execute(ctx context.Context, _ net.Conn, argsJSON string) (string, error) {
	return toolAppendFile(ctx, argsJSON)
}

// --- run_command ---

type runCommandTool struct{ ss *SocketServer }

func (t *runCommandTool) Name() string { return "run_command" }

func (t *runCommandTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "run_command",
		Description: brain.RunCommandToolDescription(),
		Parameters:  json.RawMessage(`{"type":"object","properties":{"argv":{"type":"array","items":{"type":"string"},"minItems":1,"description":"argv[0]=program on PATH; no shell."}},"required":["argv"]}`),
	}}
}

func (t *runCommandTool) Execute(ctx context.Context, conn net.Conn, argsJSON string) (string, error) {
	var p struct {
		Argv []string `json:"argv"`
	}
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("run_command args: %w", err)
	}
	if len(p.Argv) == 0 {
		return "", fmt.Errorf("run_command: argv required")
	}
	if config.Config == nil {
		return "", fmt.Errorf("config not loaded")
	}
	if err := config.CheckExecArgv(p.Argv); err != nil {
		return "", err
	}
	if err := checkRunCommandArgv(p.Argv); err != nil {
		return "", err
	}
	ec := &config.Config.Exec
	wd, err := resolveExecCwd(ctx)
	if err != nil {
		return "", err
	}
	cmdLine := execcmd.FormatLine(p.Argv)
	if config.ExecNeedsConfirm(p.Argv) {
		id := protocol.NewConfirmID()
		_ = t.ss.emitStreamLine(conn, map[string]interface{}{
			"type":         "exec_confirm_required",
			"confirm_id":   id,
			"argv":         p.Argv,
			"command_line": cmdLine,
			"cwd":          wd,
			"options": []map[string]string{
				{"id": "run", "label": "Run"},
				{"id": "cancel", "label": "Cancel"},
			},
		})
		approved, err := protocol.WaitExecClientConfirm(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return "[run_command] cancelled", nil
			}
			return "", err
		}
		if !approved {
			_ = t.ss.emitStreamLine(conn, map[string]interface{}{
				"type": "exec_denied", "confirm_id": id,
				"command_line": cmdLine, "cwd": wd,
			})
			return "[run_command] cancelled by user", nil
		}
	}

	to := time.Duration(ec.TimeoutSeconds) * time.Second
	if to <= 0 {
		to = 120 * time.Second
	}
	_ = t.ss.emitStreamLine(conn, map[string]interface{}{
		"type":    "progress",
		"message": fmt.Sprintf("run_command 执行中（最长 %ds）…", int(to.Seconds())),
	})
	xctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

	// 不用 CommandContext：Windows 上仅 Kill wsl.exe 时 Linux 侧子进程常残留，Wait 会挂死整轮 chat。
	cmd := exec.Command(p.Argv[0], p.Argv[1:]...)
	cmd.Dir = wd
	cmd.Stdin = nil

	var stdOut, stdErr bytes.Buffer
	cmd.Stdout = &stdOut
	cmd.Stderr = &stdErr

	runErr := cmd.Start()
	timedOut := false
	if runErr == nil {
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case runErr = <-done:
		case <-xctx.Done():
			killCmdTree(cmd)
			select {
			case runErr = <-done:
			case <-time.After(5 * time.Second):
				runErr = xctx.Err()
			}
			if errors.Is(xctx.Err(), context.DeadlineExceeded) {
				timedOut = true
			} else if ctx.Err() != nil {
				runErr = ctx.Err()
			}
		}
	}

	maxB := ec.MaxOutputBytes
	if maxB <= 0 {
		maxB = 256 * 1024
	}

	exitCode := 0
	var exitErr *exec.ExitError
	if runErr != nil {
		if timedOut || errors.Is(runErr, context.DeadlineExceeded) {
			timedOut = true
			exitCode = -1
		} else if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if errors.Is(runErr, context.Canceled) {
			exitCode = -1
		} else {
			exitCode = -1
		}
	}

	stdoutStr := stdOut.String()
	stderrStr := stdErr.String()
	totalLen := len(stdoutStr) + len(stderrStr)
	truncated := false
	if totalLen > maxB {
		stdoutStr, stderrStr = truncateCmdOutput(stdoutStr, stderrStr, maxB)
		truncated = true
	}

	result := formatCommandResult(wd, cmdLine, exitCode, timedOut, truncated, stdoutStr, stderrStr)

	_ = t.ss.emitStreamLine(conn, map[string]interface{}{
		"type":         "exec_done",
		"argv":         p.Argv,
		"command_line": cmdLine,
		"cwd":          wd,
		"exit_code":    exitCode,
		"timed_out":    timedOut,
		"truncated":    truncated,
	})

	if runErr != nil && !timedOut && exitCode < 0 {
		log.Printf("run_command failed: argv=%v cwd=%s: %v", p.Argv, wd, runErr)
	} else {
		log.Printf("run_command: exit=%d argv=%v cwd=%s bytes=%d", exitCode, p.Argv, wd, totalLen)
	}
	return result, nil
}

// --- declare_task ---

type declareTaskTool struct{}

func (t *declareTaskTool) Name() string { return "declare_task" }

func (t *declareTaskTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name: "declare_task",
		Description: "Persist recoverable task contract: goal, acceptance, steps, and THIS task's loop limits. " +
			"Termination limits are task-specific (not global): set max_tool_rounds / max_consecutive_failures / max_stale_rounds for this job. " +
			"Round limits should be generous: complex multi-step jobs (data fetch→transform→analysis→report) routinely exceed 20 tool rounds; " +
			"default max_tool_rounds to 0 (hard ceiling only) unless you know the job is short, and raise it if a long job gets 'budget_exhausted'. " +
			"Call early on multi-step work.",
		Parameters: json.RawMessage(`{"type":"object","properties":{"goal":{"type":"string"},"acceptance":{"type":"array","items":{"type":"string"},"description":"Done criteria for THIS task"},"steps":{"type":"array","items":{"type":"string"}},"max_tool_rounds":{"type":"integer","description":"Task tool-round budget; 0=use hard ceiling only"},"max_consecutive_failures":{"type":"integer","description":"Stop after N all-fail tool rounds; 0=off"},"max_stale_rounds":{"type":"integer","description":"Stop after N identical-outcome rounds; 0=off"}},"required":["goal"]}`),
	}}
}

func (t *declareTaskTool) Execute(ctx context.Context, _ net.Conn, argsJSON string) (string, error) {
	var p struct {
		Goal                   string   `json:"goal"`
		Acceptance             []string `json:"acceptance"`
		Steps                  []string `json:"steps"`
		MaxToolRounds          *int     `json:"max_tool_rounds"`
		MaxConsecutiveFailures *int     `json:"max_consecutive_failures"`
		MaxStaleRounds         *int     `json:"max_stale_rounds"`
	}
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("declare_task args: %w", err)
	}
	w := chatWorkspaceFrom(ctx)
	if w == nil {
		return "", fmt.Errorf("declare_task: no active workspace")
	}
	c := brain.TaskContract{
		Goal:                   p.Goal,
		Acceptance:             p.Acceptance,
		Steps:                  p.Steps,
		SetAcceptance:          p.Acceptance != nil,
		SetSteps:               p.Steps != nil,
		MaxToolRounds:          p.MaxToolRounds,
		MaxConsecutiveFailures: p.MaxConsecutiveFailures,
		MaxStaleRounds:         p.MaxStaleRounds,
	}
	st, err := brain.UpdateTaskContract(w, c)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("task %s status=%s goal=%q acceptance=%d steps=%d limits=rounds:%d fail:%d stale:%d",
		st.ID, st.Status, st.Goal, len(st.Acceptance), len(st.Steps),
		st.MaxToolRounds, st.MaxConsecutiveFailures, st.MaxStaleRounds), nil
}

// --- run_skill ---

type runSkillTool struct{}

func (t *runSkillTool) Name() string { return "run_skill" }

func (t *runSkillTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name: "run_skill",
		Description: "Run a crystallized skill from focus_path/.cata/skills/<id>/ (tool path brain/skills/<id>/). " +
			"NOT under ~/.cata/brain/workspaces/. Outputs go to output cwd. Prefer this for known tasks; use browser_* for new sites.",
		Parameters: json.RawMessage(`{"type":"object","properties":{"skill":{"type":"string","description":"Skill id listed in modes/<mode>/capabilities.yaml (files live at brain/skills/<id>/ → focus_path/.cata/skills/<id>/)"},"params":{"type":"object","description":"Optional JSON params passed to the skill script"}},"required":["skill"]}`),
	}}
}

func (t *runSkillTool) Execute(ctx context.Context, _ net.Conn, argsJSON string) (string, error) {
	var p brain.RunSkillArgs
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("run_skill args: %w", err)
	}
	return brain.RunSkill(ctx, p)
}

// --- read_skill ---

type readSkillTool struct{}

func (t *readSkillTool) Name() string { return "read_skill" }

func (t *readSkillTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name: "read_skill",
		Description: "Load full SKILL.md for a skill id (project focus_path/.cata/skills/<id>/, tool path brain/skills/<id>/SKILL.md). " +
			"Use before run_skill when you need complete instructions.",
		Parameters: json.RawMessage(`{"type":"object","properties":{"skill":{"type":"string","description":"Skill id from modes/<mode>/capabilities.yaml skills list"}},"required":["skill"]}`),
	}}
}

func (t *readSkillTool) Execute(ctx context.Context, _ net.Conn, argsJSON string) (string, error) {
	var p brain.ReadSkillArgs
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("read_skill args: %w", err)
	}
	return brain.ReadSkill(ctx, p)
}

// --- ask_user ---

type askUserTool struct{ ss *SocketServer }

func (t *askUserTool) Name() string { return "ask_user" }

func (t *askUserTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "ask_user",
		Description: "Present a choice to the user. Use when you need the user to decide between approaches, pick from alternatives, or confirm a multi-option selection. The user sees an interactive selector (arrow keys, Enter to confirm).",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string","description":"Question to present to the user"},"options":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"label":{"type":"string"},"desc":{"type":"string"}},"required":["id","label"]},"minItems":2},"multi":{"type":"boolean","description":"Allow user to select multiple options (default false)"}},"required":["prompt","options"]}`),
	}}
}

func (t *askUserTool) Execute(ctx context.Context, conn net.Conn, argsJSON string) (string, error) {
	var p struct {
		Prompt  string `json:"prompt"`
		Options []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
			Desc  string `json:"desc"`
		} `json:"options"`
		Multi bool `json:"multi"`
	}
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("ask_user args: %w", err)
	}
	if len(p.Options) < 2 {
		return "", fmt.Errorf("ask_user: at least 2 options required")
	}
	choiceID := protocol.NewConfirmID()
	_ = t.ss.emitStreamLine(conn, map[string]interface{}{
		"type":    "user_choice",
		"id":      choiceID,
		"prompt":  p.Prompt,
		"detail":  "",
		"multi":   p.Multi,
		"options": p.Options,
	})
	selected, err := protocol.WaitUserChoice(ctx, choiceID)
	if err != nil {
		return "", err
	}
	if len(selected) == 0 {
		return "[ask_user] user cancelled", nil
	}
	var labels []string
	for _, s := range selected {
		for _, o := range p.Options {
			if o.ID == s {
				labels = append(labels, o.Label)
				break
			}
		}
	}
	if len(labels) == 0 {
		labels = selected
	}
	if p.Multi {
		return fmt.Sprintf("[ask_user] user selected: %s", strings.Join(labels, ", ")), nil
	}
	return fmt.Sprintf("[ask_user] user selected: %s", labels[0]), nil
}

// --- create_file ---

type createFileTool struct{}

func (t *createFileTool) Name() string { return "create_file" }

func (t *createFileTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "create_file",
		Description: "Create a new file. " + brain.ChatBrainToolPathNote + " Response includes resolved=.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path (default output; brain/… or global/…)"},"content":{"type":"string","description":"File content"},"overwrite":{"type":"boolean","description":"If true, overwrite existing file (default false)"}},"required":["path","content"]}`),
	}}
}

func (t *createFileTool) Execute(ctx context.Context, _ net.Conn, argsJSON string) (string, error) {
	var p struct {
		Path      string `json:"path"`
		Content   string `json:"content"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("create_file args: %w", err)
	}
	if strings.TrimSpace(p.Path) == "" {
		return "", fmt.Errorf("create_file: path required")
	}
	full, err := resolveWorkspaceFile(ctx, p.Path)
	if err != nil {
		return "", err
	}
	if !p.Overwrite {
		if _, err := os.Stat(full); err == nil {
			return "", fmt.Errorf("create_file: %s already exists (use overwrite:true to replace)", p.Path)
		}
	}
	_, maxWrite := workspaceFileLimits()
	if len(p.Content) > maxWrite {
		return "", fmt.Errorf("create_file: content exceeds max_write_bytes (%d)", maxWrite)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, []byte(p.Content), 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("create_file %s resolved=%s: wrote %d bytes", p.Path, full, len(p.Content)), nil
}

// --- list_files ---

type listFilesTool struct{}

func (t *listFilesTool) Name() string { return "list_files" }

func (t *listFilesTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "list_files",
		Description: "List files and directories. default=output; brain/ routes per " + brain.ChatBrainToolPathNote,
		Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Directory: default=output; brain/ or global/ (optional, default root)"}},"required":[]}`),
	}}
}

func (t *listFilesTool) Execute(ctx context.Context, _ net.Conn, argsJSON string) (string, error) {
	return toolListFiles(ctx, argsJSON)
}
