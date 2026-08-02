package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/llm"
)

type listModesTool struct{}

func (t *listModesTool) Name() string { return "list_modes" }

func (t *listModesTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name:        "list_modes",
		Description: "List project .cata/modes/* role cards (id + one-liner). Use before delegate_task with mode_id (or delegate_mode).",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}}
}

func (t *listModesTool) Execute(ctx context.Context, conn net.Conn, argsJSON string) (string, error) {
	w, err := brain.MustActive()
	if err != nil {
		return "", err
	}
	modes, err := brain.ListProjectModes(w)
	if err != nil {
		return "", err
	}
	return brain.FormatModesList(modes), nil
}

type delegateModeTool struct {
	ss *SocketServer
}

func (t *delegateModeTool) Name() string { return "delegate_mode" }

func (t *delegateModeTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name: "delegate_mode",
		Description: "Alias of delegate_task with mode_id+case_id (specialist modes/<id> on a Case). " +
			"Prefer delegate_task with mode_id for one entry point. Use list_modes first.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"mode_id":{"type":"string","description":"Specialist mode id under .cata/modes/ (from list_modes; project-grown only)"},
				"case_id":{"type":"string","description":"Case id for artifacts/mode_runs under output cases/"},
				"task":{"type":"string","description":"What the mode should do; include acceptance criteria"},
				"context":{"type":"string","description":"Facts/paths for the mode (do not re-discover)"},
				"read_artifacts":{"type":"array","items":{"type":"string"},"description":"Artifact names to load into context (head version)"},
				"write_artifacts":{"type":"array","items":{"type":"string"},"description":"Artifact names the mode may create via case_artifact"},
				"tools":{"type":"array","items":{"type":"string"},"description":"Optional tool whitelist for the worker"},
				"max_rounds":{"type":"integer","description":"Worker tool rounds (default/capped like delegate_task)"},
				"wait":{"type":"boolean","description":"If true, block until done and return summary"}
			},
			"required":["mode_id","case_id","task"]
		}`),
	}}
}

type modeDelegateArgs struct {
	ModeID         string
	CaseID         string
	Task           string
	Context        string
	ReadArtifacts  []string
	WriteArtifacts []string
	Tools          []string
	MaxRounds      int
	Wait           bool
}

func startModeDelegate(ctx context.Context, pool *subagentPool, p modeDelegateArgs) (string, error) {
	modeID := brain.ResolveDelegateModeID(p.ModeID)
	caseID, err := brain.NormalizeCaseID(p.CaseID)
	if err != nil {
		return "", err
	}
	task := strings.TrimSpace(p.Task)
	if task == "" {
		return "", fmt.Errorf("delegate: task required")
	}
	w, err := brain.MustActive()
	if err != nil {
		return "", err
	}
	if !brain.ModeExists(w, modeID) {
		return "", fmt.Errorf("delegate: mode %q not found (list_modes; create focus_path/.cata/modes/%s/)", modeID, modeID)
	}
	if _, err := brain.EnsureCase(brain.OutputCwd(), caseID); err != nil {
		return "", err
	}

	bundle, err := brain.LoadModePromptBundle(w, modeID, 4000)
	if err != nil {
		return "", err
	}
	userPrompt := buildModeWorkerPrompt(modeID, caseID, task, p.Context, bundle, p.ReadArtifacts, p.WriteArtifacts)

	id, started, err := pool.StartMode(ctx, subagentStartOpts{
		Task:          task,
		ParentContext: p.Context,
		ToolFilter:    p.Tools,
		MaxRounds:     clampDelegateRounds(p.MaxRounds),
		ModeID:        modeID,
		CaseID:        caseID,
		ArtifactsIn:   p.ReadArtifacts,
		ArtifactsOut:  p.WriteArtifacts,
		UserPrompt:    userPrompt,
	})
	if err != nil {
		return "", err
	}
	if !p.Wait {
		return started, nil
	}
	return pool.Wait(ctx, []string{id}, false)
}

func (t *delegateModeTool) Execute(ctx context.Context, conn net.Conn, argsJSON string) (string, error) {
	pool := chatSubagentPoolFrom(ctx)
	if pool == nil {
		return "", fmt.Errorf("delegate_mode: internal pool missing")
	}
	var p struct {
		ModeID         string   `json:"mode_id"`
		CaseID         string   `json:"case_id"`
		Task           string   `json:"task"`
		Context        string   `json:"context"`
		ReadArtifacts  []string `json:"read_artifacts"`
		WriteArtifacts []string `json:"write_artifacts"`
		Tools          []string `json:"tools"`
		MaxRounds      int      `json:"max_rounds"`
		Wait           bool     `json:"wait"`
	}
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("delegate_mode args: %w", err)
	}
	return startModeDelegate(ctx, pool, modeDelegateArgs{
		ModeID:         p.ModeID,
		CaseID:         p.CaseID,
		Task:           p.Task,
		Context:        p.Context,
		ReadArtifacts:  p.ReadArtifacts,
		WriteArtifacts: p.WriteArtifacts,
		Tools:          p.Tools,
		MaxRounds:      p.MaxRounds,
		Wait:           p.Wait,
	})
}

func buildModeWorkerPrompt(modeID, caseID, task, parentContext, modeBundle string, readArts, writeArts []string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(brain.LoadWorkerContract()))
	b.WriteString("\n\n")
	b.WriteString("You are running as a **specialist mode**, not the orchestrator. Follow the role card.\n\n")
	if out := strings.TrimSpace(brain.OutputCwd()); out != "" {
		fmt.Fprintf(&b, "## Output cwd\n\n`%s`\n\n", out)
		fmt.Fprintf(&b, "## Case\n\n`cases/%s/` (artifacts/, mode_runs/)\n\n", caseID)
	}
	b.WriteString(modeBundle)
	b.WriteString("\n\n")
	if len(writeArts) > 0 {
		b.WriteString("## Allowed write artifacts\n\n")
		b.WriteString(strings.Join(writeArts, ", "))
		b.WriteString("\nUse `case_artifact` action=write when available; else write markdown under cases/")
		b.WriteString(caseID)
		b.WriteString("/artifacts/<name>/ yourself.\n\n")
	}
	if len(readArts) > 0 {
		b.WriteString("## Input artifacts\n\n")
		for _, name := range readArts {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			body, meta, err := brain.ReadCaseArtifact(brain.OutputCwd(), caseID, name, 0, false)
			if err != nil {
				fmt.Fprintf(&b, "### %s\n\n(error: %v)\n\n", name, err)
				continue
			}
			if len(body) > 6000 {
				body = body[:6000] + "\n…(truncated)"
			}
			fmt.Fprintf(&b, "### %s\n\n%s\n\n%s\n\n", meta, body, "")
		}
	}
	if ctx := strings.TrimSpace(parentContext); ctx != "" {
		b.WriteString("## Parent context\n\n")
		b.WriteString(ctx)
		b.WriteString("\n\n")
	}
	b.WriteString("## Task\n\n")
	b.WriteString(strings.TrimSpace(task))
	b.WriteString("\n\nFinish with a short summary of what you did and artifact paths.\n")
	return b.String()
}

type caseArtifactTool struct{}

func (t *caseArtifactTool) Name() string { return "case_artifact" }

func (t *caseArtifactTool) Schema() llm.Tool {
	return llm.Tool{Type: "function", Function: llm.ToolFunction{
		Name: "case_artifact",
		Description: "Read/write/set status of a Case artifact under output cases/<case_id>/artifacts/<name>/. " +
			"Statuses: draft|in_review|accepted|rejected. Orchestrator accepts/rejects; modes usually write draft.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"action":{"type":"string","enum":["read","write","set_status"]},
				"case_id":{"type":"string"},
				"name":{"type":"string","description":"Artifact name e.g. requirements, spec, impl_notes"},
				"content":{"type":"string","description":"For write"},
				"status":{"type":"string","enum":["draft","in_review","accepted","rejected"],"description":"For set_status"},
				"reason":{"type":"string","description":"Reject reason"},
				"version":{"type":"integer","description":"For read; 0=head"},
				"by_mode":{"type":"string","description":"Author/acceptor mode id"}
			},
			"required":["action","case_id","name"]
		}`),
	}}
}

func (t *caseArtifactTool) Execute(ctx context.Context, conn net.Conn, argsJSON string) (string, error) {
	var p struct {
		Action  string `json:"action"`
		CaseID  string `json:"case_id"`
		Name    string `json:"name"`
		Content string `json:"content"`
		Status  string `json:"status"`
		Reason  string `json:"reason"`
		Version int    `json:"version"`
		ByMode  string `json:"by_mode"`
	}
	if err := llm.ParseToolArguments(argsJSON, &p); err != nil {
		return "", fmt.Errorf("case_artifact args: %w", err)
	}
	caseID, err := brain.NormalizeCaseID(p.CaseID)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return "", fmt.Errorf("case_artifact: name required")
	}
	cwd := brain.OutputCwd()
	by := strings.TrimSpace(p.ByMode)
	if by == "" {
		if w := brain.Active(); w != nil {
			by = brain.NormalizeModeID(w.ActiveMode)
		}
	}
	switch strings.ToLower(strings.TrimSpace(p.Action)) {
	case "read":
		body, meta, err := brain.ReadCaseArtifact(cwd, caseID, name, p.Version, false)
		if err != nil {
			return "", err
		}
		return meta + "\n\n" + body, nil
	case "write":
		v, path, err := brain.WriteCaseArtifact(cwd, caseID, name, p.Content, by)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("wrote %s@v%d path=%s status=draft", name, v, path), nil
	case "set_status":
		st := brain.ArtifactStatus(strings.TrimSpace(p.Status))
		switch st {
		case brain.ArtifactDraft, brain.ArtifactInReview, brain.ArtifactAccepted, brain.ArtifactRejected:
		default:
			return "", fmt.Errorf("case_artifact: invalid status %q", p.Status)
		}
		if err := brain.SetCaseArtifactStatus(cwd, caseID, name, st, by, p.Reason); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s status=%s by=%s", name, st, by), nil
	default:
		return "", fmt.Errorf("case_artifact: action must be read|write|set_status")
	}
}
