package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"cata/internal/cata/brain"
	"cata/internal/cata/clock"
	"cata/internal/cata/config"
	"cata/internal/llm"
	"cata/internal/mcp"
)

type chatSubagentPoolKey struct{}

func withChatSubagentPool(ctx context.Context, p *subagentPool) context.Context {
	return context.WithValue(ctx, chatSubagentPoolKey{}, p)
}

func chatSubagentPoolFrom(ctx context.Context) *subagentPool {
	p, _ := ctx.Value(chatSubagentPoolKey{}).(*subagentPool)
	return p
}

func subagentRunningFrom(ctx context.Context) int {
	pool := chatSubagentPoolFrom(ctx)
	if pool == nil {
		return 0
	}
	return pool.RunningCount()
}

type subagentResult struct {
	success bool
	summary string
	rounds  int
	err     error
}

type subagentTask struct {
	id            string
	task          string
	context       string
	model         string
	maxRounds     int
	tools         []llm.Tool
	toolNames     string
	startedAt     string
	outputCwd     string
	sessionID     string
	delegateIndex uint64

	// mode 委托（空 = 普通 delegate_task）
	modeID       string
	caseID       string
	artifactsIn  []string
	artifactsOut []string
	userPrompt   string // 若非空，替代 buildWorkerSystemPrompt

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	result subagentResult
	done   chan struct{}
}

func (t *subagentTask) finish(r subagentResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	select {
	case <-t.done:
		return
	default:
	}
	t.result = r
	close(t.done)
}

func (t *subagentTask) snapshot() subagentResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.result
}

func (t *subagentTask) isRunning() bool {
	select {
	case <-t.done:
		return false
	default:
		return true
	}
}

func (t *subagentTask) wait(ctx context.Context) error {
	select {
	case <-t.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// complete closes the done channel (for delegate_wait) and emits subagent_done once.
func (t *subagentTask) complete(ss *SocketServer, conn net.Conn, r subagentResult) {
	t.finish(r)
	t.persistRunCSV(r)
	if t.modeID != "" && t.caseID != "" {
		status := "ok"
		if !r.success {
			status = "failed"
		}
		if path, err := brain.AppendModeRunLog(t.outputCwd, brain.ModeRunLog{
			ModeID: t.modeID, CaseID: t.caseID, SubagentID: t.id,
			Task: t.task, Status: status, Summary: r.summary,
			ArtifactsIn: t.artifactsIn, ArtifactsOut: t.artifactsOut, Rounds: r.rounds,
		}); err != nil {
			log.Printf("mode run log: %v", err)
		} else {
			_ = path
		}
		if err := brain.AppendDelegateModeNote(t.modeID, t.caseID, t.id, status, r.summary); err != nil {
			log.Printf("delegate_mode short-term: %v", err)
		}
	}
	ev := map[string]interface{}{
		"type": "subagent_done", "id": t.id, "success": r.success, "summary": r.summary,
		"log_path": brain.SubagentRunsCSVPath(t.outputCwd),
	}
	if t.modeID != "" {
		ev["mode"] = t.modeID
		ev["case_id"] = t.caseID
	}
	if r.rounds > 0 {
		ev["rounds"] = r.rounds
	}
	_ = ss.emitStreamLine(conn, ev)
}

func (t *subagentTask) abortIfCancelled(ss *SocketServer, conn net.Conn, rounds int) error {
	if t.ctx.Err() == nil {
		return nil
	}
	t.complete(ss, conn, subagentResult{
		success: false, summary: "cancelled", rounds: rounds, err: t.ctx.Err(),
	})
	return t.ctx.Err()
}

type subagentPool struct {
	ss   *SocketServer
	conn net.Conn

	sessionID   string
	delegateSeq uint64

	mu    sync.Mutex
	tasks map[string]*subagentTask
}

func newSubagentPool(parentCtx context.Context, ss *SocketServer, conn net.Conn) *subagentPool {
	p := &subagentPool{
		ss:        ss,
		conn:      conn,
		sessionID: fmt.Sprintf("cs-%d", clock.Now().UnixNano()),
		tasks:     make(map[string]*subagentTask),
	}
	go func() {
		<-parentCtx.Done()
		p.CancelAll()
	}()
	return p
}

func (p *subagentPool) CancelAll() {
	p.mu.Lock()
	tasks := make([]*subagentTask, 0, len(p.tasks))
	for _, t := range p.tasks {
		tasks = append(tasks, t)
	}
	p.mu.Unlock()
	for _, t := range tasks {
		if t.cancel != nil {
			t.cancel()
		}
	}
}

// Start launches a worker goroutine. Returns task id and a short status line for the parent LLM.
func (p *subagentPool) Start(parentCtx context.Context, task, parentContext string, toolFilter []string, maxRounds int) (id string, msg string, err error) {
	return p.start(parentCtx, subagentStartOpts{
		Task: task, ParentContext: parentContext, ToolFilter: toolFilter, MaxRounds: maxRounds,
	})
}

// StartMode 启动注入指定 mode 角色卡的子会话。
func (p *subagentPool) StartMode(parentCtx context.Context, opts subagentStartOpts) (id string, msg string, err error) {
	return p.start(parentCtx, opts)
}

type subagentStartOpts struct {
	Task          string
	ParentContext string
	ToolFilter    []string
	MaxRounds     int
	ModeID        string
	CaseID        string
	ArtifactsIn   []string
	ArtifactsOut  []string
	UserPrompt    string
}

func (p *subagentPool) start(parentCtx context.Context, opts subagentStartOpts) (id string, msg string, err error) {
	toolFilter := opts.ToolFilter
	if len(toolFilter) == 0 {
		toolFilter = config.DefaultSubagentTools()
	}
	allTools := p.ss.workerTools()
	tools, err := filterWorkerTools(allTools, toolFilter)
	if err != nil {
		return "", "", err
	}
	if len(tools) == 0 {
		return "", "", fmt.Errorf("delegate: no worker tools configured")
	}
	client, err := llm.NewClientForRole(llm.RoleWorker)
	if err != nil {
		return "", "", err
	}

	id = nextSubagentID()
	if p.ss.subagentSem.isFull() {
		ev := map[string]interface{}{
			"type": "subagent_queued", "id": id, "task": opts.Task,
			"running": p.ss.subagentSem.runningCount(), "max": p.ss.subagentSem.capacity(),
		}
		if opts.ModeID != "" {
			ev["mode"] = opts.ModeID
		}
		_ = p.ss.emitStreamLine(p.conn, ev)
	}
	if err := p.ss.subagentSem.acquire(parentCtx); err != nil {
		return "", "", err
	}

	slotHeld := true
	defer func() {
		if slotHeld {
			p.ss.subagentSem.release()
		}
	}()

	idx := atomic.AddUint64(&p.delegateSeq, 1)
	taskCtx, cancel := context.WithCancel(parentCtx)
	st := &subagentTask{
		id:            id,
		task:          opts.Task,
		context:       brain.EnrichWorkerDelegateContext(opts.ParentContext),
		model:         client.ModelName(),
		maxRounds:     opts.MaxRounds,
		tools:         tools,
		toolNames:     formatWorkerToolNames(tools),
		startedAt:     clock.RFC3339(),
		outputCwd:     brain.OutputCwd(),
		sessionID:     p.sessionID,
		delegateIndex: idx,
		modeID:        opts.ModeID,
		caseID:        opts.CaseID,
		artifactsIn:   append([]string(nil), opts.ArtifactsIn...),
		artifactsOut:  append([]string(nil), opts.ArtifactsOut...),
		userPrompt:    opts.UserPrompt,
		ctx:           taskCtx,
		cancel:        cancel,
		done:          make(chan struct{}),
	}

	p.mu.Lock()
	p.tasks[id] = st
	p.mu.Unlock()

	profile := "minimal"
	if opts.ModeID != "" {
		profile = "mode:" + opts.ModeID
	}
	ev := map[string]interface{}{
		"type":           "subagent_start",
		"id":             id,
		"task":           opts.Task,
		"model":          client.ModelName(),
		"prompt_profile": profile,
	}
	if opts.ModeID != "" {
		ev["mode"] = opts.ModeID
		ev["case_id"] = opts.CaseID
	}
	_ = p.ss.emitStreamLine(p.conn, ev)

	slotHeld = false
	go p.run(st, client)
	if opts.ModeID != "" {
		return id, formatDelegateModeStarted(id, opts.ModeID, opts.CaseID, client.ModelName(), p.ss.subagentSem.capacity(), len(tools), st.outputCwd), nil
	}
	return id, formatDelegateStarted(id, client.ModelName(), p.ss.subagentSem.capacity(), len(tools), st.outputCwd), nil
}

func (p *subagentPool) Wait(ctx context.Context, ids []string, all bool) (string, error) {
	waitList, err := p.resolveWaitList(ids, all)
	if err != nil {
		return "", err
	}
	if len(waitList) == 0 {
		return "[delegate_wait] nothing to wait for (no running sub-agents)", nil
	}
	if err := p.waitMany(ctx, waitList); err != nil {
		return "", err
	}
	return formatDelegateWaitResults(waitList), nil
}

func (p *subagentPool) resolveWaitList(ids []string, all bool) ([]*subagentTask, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if all && len(ids) == 0 {
		out := make([]*subagentTask, 0, len(p.tasks))
		for _, t := range p.tasks {
			out = append(out, t)
		}
		return out, nil
	}

	if len(ids) == 0 {
		running := make([]*subagentTask, 0, len(p.tasks))
		for _, t := range p.tasks {
			if t.isRunning() {
				running = append(running, t)
			}
		}
		return running, nil
	}

	out := make([]*subagentTask, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		t, ok := p.tasks[id]
		if !ok {
			return nil, fmt.Errorf("delegate_wait: unknown sub-agent %q", id)
		}
		out = append(out, t)
	}
	return out, nil
}

func (p *subagentPool) waitMany(ctx context.Context, tasks []*subagentTask) error {
	if len(tasks) == 0 {
		return nil
	}
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(len(tasks))
	for _, task := range tasks {
		t := task
		go func() {
			defer wg.Done()
			if err := t.wait(ctx); err != nil {
				select {
				case errCh <- err:
				default:
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	case <-done:
		return nil
	}
}

func (p *subagentPool) run(st *subagentTask, client *llm.Client) {
	defer p.ss.subagentSem.release()
	runSubagentLoop(st, p.conn, p.ss, client)
}

func runSubagentLoop(st *subagentTask, conn net.Conn, ss *SocketServer, client *llm.Client) {
	userContent := st.userPrompt
	if strings.TrimSpace(userContent) == "" {
		userContent = buildWorkerSystemPrompt(st.task, st.context)
	}
	messages := []llm.Message{{Role: "user", Content: userContent}}
	tools := st.tools
	toolCap := workerToolResultMaxBytes()
	maxOut := config.SubagentMaxOutputTokens()

	profile := "minimal"
	if st.modeID != "" {
		profile = "mode:" + st.modeID
	}

	for round := 1; round <= st.maxRounds; round++ {
		if err := st.abortIfCancelled(ss, conn, round-1); err != nil {
			return
		}
		_ = ss.emitStreamLine(conn, map[string]interface{}{
			"type": "subagent_progress", "id": st.id, "message": fmt.Sprintf("round %d", round),
			"prompt_profile": profile,
		})

		asst, _, toolCalls, _, _, err := client.ChatWorkerStreamRound(st.ctx, messages, tools, maxOut, llm.WorkerRoundMeta{
			SubagentID: st.id,
			SessionID:  st.sessionID,
		}, nil)
		toolCalls = llm.NormalizeToolCalls(toolCalls)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				_ = st.abortIfCancelled(ss, conn, round)
			} else {
				st.complete(ss, conn, subagentResult{success: false, summary: err.Error(), rounds: round, err: err})
			}
			return
		}

		if len(toolCalls) == 0 {
			summary := strings.TrimSpace(asst)
			if summary == "" {
				summary = "(sub-agent finished with no text)"
			}
			st.complete(ss, conn, subagentResult{success: true, summary: summary, rounds: round})
			return
		}

		messages = append(messages, llm.Message{Role: "assistant", Content: asst, ToolCalls: toolCalls})
		fatalBrowser := false
		for _, tc := range toolCalls {
			if err := st.abortIfCancelled(ss, conn, round); err != nil {
				return
			}
			name := tc.Function.Name
			_ = ss.emitStreamLine(conn, map[string]interface{}{
				"type": "subagent_tool", "id": st.id, "phase": "start", "name": name,
			})
			var out string
			var terr error
			if fatalBrowser && mcp.IsBrowserTool(name) {
				out = "[browser error] skipped: browser crashed (see previous error)"
			} else {
				out, terr = ss.runTerminalTool(st.ctx, conn, tc)
			}
			if !fatalBrowser && isFatalBrowserError(terr, out) {
				fatalBrowser = true
			}
			if terr != nil {
				if errors.Is(terr, context.Canceled) {
					_ = st.abortIfCancelled(ss, conn, round)
					return
				}
				if out != "" {
					out = out + "\n[error] " + terr.Error()
				} else {
					out = "[error] " + terr.Error()
				}
			}
			if err := st.abortIfCancelled(ss, conn, round); err != nil {
				return
			}
			preview := out
			if len(preview) > 1200 {
				preview = preview[:1200] + "\n…(truncated)"
			}
			_ = ss.emitStreamLine(conn, map[string]interface{}{
				"type": "subagent_tool", "id": st.id, "phase": "result", "name": name, "output": preview,
			})
			messages = append(messages, llm.Message{
				Role: "tool", ToolCallID: tc.ID, Name: name, Content: truncateWorkerToolResult(out, toolCap),
			})
		}
	}

	summary := fmt.Sprintf("stopped after %d rounds (max); partial work in sub-agent log", st.maxRounds)
	st.complete(ss, conn, subagentResult{success: false, summary: summary, rounds: st.maxRounds})
}

func (p *subagentPool) RunningCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, t := range p.tasks {
		if t.isRunning() {
			n++
		}
	}
	return n
}

func formatDelegateStarted(id, model string, maxConcurrent, toolCount int, outputCwd string) string {
	return fmt.Sprintf("[delegate_task %s] worker started (model=%s, tools=%d, max_concurrent=%d). "+
		"log=%s — call delegate_wait with ids=[%q] or omit ids to collect summaries.",
		id, model, toolCount, maxConcurrent, brain.SubagentRunsCSVPath(outputCwd), id)
}

func formatDelegateModeStarted(id, modeID, caseID, model string, maxConcurrent, toolCount int, outputCwd string) string {
	return fmt.Sprintf("[delegate_mode %s] mode=%s case=%s started (model=%s, tools=%d, max_concurrent=%d). "+
		"cases under %s/%s/ — call delegate_wait with ids=[%q].",
		id, modeID, caseID, model, toolCount, maxConcurrent, brain.DirCases, caseID, id)
}

func formatDelegateWaitResults(tasks []*subagentTask) string {
	var b strings.Builder
	b.WriteString("[delegate_wait]\n")
	for _, t := range tasks {
		r := t.snapshot()
		status := "ok"
		if !r.success {
			status = "failed"
		}
		b.WriteString(fmt.Sprintf("--- %s (%s) rounds=%d ---\n", t.id, status, r.rounds))
		body := r.summary
		if body == "" && r.err != nil {
			body = r.err.Error()
		}
		if body != "" {
			b.WriteString(body)
			if !strings.HasSuffix(body, "\n") {
				b.WriteString("\n")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatWorkerToolNames(tools []llm.Tool) string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if n := strings.TrimSpace(t.Function.Name); n != "" {
			names = append(names, n)
		}
	}
	return strings.Join(names, "|")
}

func (t *subagentTask) persistRunCSV(r subagentResult) {
	rec := brain.SubagentRunRecord{
		SessionID:     t.sessionID,
		DelegateIndex: int(t.delegateIndex),
		StartedAt:     t.startedAt,
		FinishedAt:    brain.SubagentRunFinishedAt(),
		ID:            t.id,
		Workspace:     brain.SubagentWorkspaceLabel(),
		OutputCwd:     t.outputCwd,
		Model:         t.model,
		Status:        subagentRunStatus(r),
		Rounds:        r.rounds,
		Tools:         t.toolNames,
		Task:          t.task,
		Context:       t.context,
		Summary:       r.summary,
	}
	if err := brain.AppendSubagentRunCSV(rec); err != nil {
		log.Printf("subagent csv: %v", err)
	}
}

func subagentRunStatus(r subagentResult) string {
	if r.success {
		return "ok"
	}
	summary := strings.ToLower(strings.TrimSpace(r.summary))
	if summary == "cancelled" || (r.err != nil && errors.Is(r.err, context.Canceled)) {
		return "cancelled"
	}
	if strings.Contains(summary, "stopped after") {
		return "partial"
	}
	return "failed"
}
