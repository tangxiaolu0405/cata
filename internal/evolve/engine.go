package evolve

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cata/internal/brain"
	"cata/internal/config"
	"cata/internal/llm"
	"cata/prompt"
)

// Engine 后台自主演进。
type Engine struct {
	interval time.Duration

	mu              sync.Mutex
	lastFingerprint map[string]string
	cooldownUntil   map[string]time.Time
}

// NewEngine 创建演进引擎。
func NewEngine(interval time.Duration) *Engine {
	if interval <= 0 {
		interval = DefaultCycleSeconds * time.Second
	}
	return &Engine{
		interval:        interval,
		lastFingerprint: make(map[string]string),
		cooldownUntil:   make(map[string]time.Time),
	}
}

// Start 周期执行；对每个已注册 workspace 分别门控与演进。
func (e *Engine) Start(ctx context.Context) {
	if config.Config == nil || !config.Config.LLM.Enabled {
		log.Println("Autonomous evolution: skipped (LLM not enabled)")
		return
	}
	if config.Config != nil && !config.Config.Evolution.Enabled {
		log.Println("Autonomous evolution: disabled in config")
		return
	}

	log.Printf("Autonomous evolution: started (interval %s, per-workspace)", e.interval)

	go func() {
		ticker := time.NewTicker(e.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Println("Autonomous evolution: stopped")
				return
			case <-ticker.C:
				e.runAll(ctx)
			}
		}
	}()
}

func (e *Engine) runAll(ctx context.Context) {
	_ = brain.EnsureCataLayout()
	list, err := brain.ListWorkspaces()
	if err != nil {
		log.Printf("Autonomous evolution: list workspaces: %v", err)
		return
	}
	if len(list) == 0 {
		return
	}
	prev := brain.Active()
	defer brain.SetActive(prev)
	for _, ws := range list {
		brain.SetActive(ws)
		if err := e.runCycle(ctx, ws, false, false); err != nil {
			log.Printf("Autonomous evolution [%s]: %v", ws.ID, err)
		}
	}
}

func (e *Engine) runCycle(ctx context.Context, ws *brain.Workspace, sessionCompress, crystallize bool) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	brain.SetActive(ws)

	snap, err := Observe(ws)
	if err != nil {
		return fmt.Errorf("observe: %w", err)
	}

	e.mu.Lock()
	cooldown := e.cooldownUntil[ws.ID]
	lastFP := e.lastFingerprint[ws.ID]
	e.mu.Unlock()

	if crystallize {
		if snap.ShortTermBytes < crystallizeMinShortBytes {
			log.Printf("Autonomous evolution [%s]: crystallize skipped (short-term too small)", ws.ID)
			return nil
		}
		if excerpt, err := readFileCap(ws.ShortTermPath(), maxShortExcerptBytes); err == nil {
			appendCrystallizeTriggers(snap, excerpt)
		}
		snap.Triggers = append(snap.Triggers, "high_token_session")
		if !shouldInvokeCrystallize(snap) {
			log.Printf("Autonomous evolution [%s]: crystallize skipped (no triggers)", ws.ID)
			return nil
		}
		log.Printf("Autonomous evolution [%s]: crystallize_skill (%s)", ws.ID, strings.Join(snap.Triggers, ","))
	} else if sessionCompress {
		if snap.ShortTermBytes < sessionCompressMinShortBytes {
			log.Printf("Autonomous evolution [%s]: session compress skipped (short-term too small)", ws.ID)
			return nil
		}
		snap.Triggers = append([]string{"session_turn_threshold"}, snap.Triggers...)
		log.Printf("Autonomous evolution [%s]: session compress (turn threshold)", ws.ID)
	} else {
		ok, reason := shouldInvokeLLM(snap, cooldown, lastFP)
		if !ok {
			log.Printf("Autonomous evolution [%s]: skip LLM (%s)", ws.ID, reason)
			return nil
		}
	}

	client, err := llm.NewClientForRole(llm.RoleEvolution)
	if err != nil {
		return fmt.Errorf("LLM: %w", err)
	}

	decisionPrompt := buildDecisionPrompt(ws, snap, sessionCompress, crystallize)
	sys := prompt.EvolveSystemPrompt()
	if sessionCompress {
		sys = prompt.EvolveSessionCompressPrompt()
	} else if crystallize {
		sys = prompt.EvolveCrystallizePrompt()
	}
	messages := []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: decisionPrompt},
	}

	reply, err := client.ChatEvolution(messages, decisionMaxTokens())
	if err != nil {
		return fmt.Errorf("decide: %w", err)
	}

	dec, err := parseDecision(reply)
	if err != nil {
		log.Printf("Autonomous evolution [%s]: parse failed (%v), retry once; raw prefix: %.120q", ws.ID, err, reply)
		retryMsgs := append(append([]llm.Message(nil), messages...),
			llm.Message{Role: "assistant", Content: reply},
			llm.Message{Role: "user", Content: "Response truncated or invalid. Output ONLY one compact JSON object: action, reason (≤120 chars), learning (≤120 chars), updates (≤3 items; keep each content ≤800 chars). No markdown."},
		)
		if reply2, err2 := client.ChatEvolution(retryMsgs, decisionMaxTokens()); err2 == nil {
			reply = reply2
			dec, err = parseDecision(reply2)
		}
	}
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if crystallize {
		dec.Updates = filterUpdatesCrystallize(dec.Updates)
	} else {
		dec.Updates = filterUpdates(dec.Updates)
	}

	var touched []string
	action := strings.ToLower(strings.TrimSpace(dec.Action))
	if action != "idle" && len(dec.Updates) > 0 {
		touched, err = ApplyUpdates(ws, dec.Updates)
		if err != nil {
			return fmt.Errorf("apply: %w", err)
		}
		if extra := compactTouchedProjectContent(ws, touched); len(extra) > 0 {
			touched = append(touched, extra...)
		}
	}
	if crystallize && (action == "crystallize_skill" || len(touched) > 0) {
		ingestCrystallizedSkills(ws, touched)
	}

	// Deterministic long-term → archive when file count exceeds threshold.
	if snap.LongTermFileCount >= longTermSummarizeMinFiles {
		if moved, err := summarizeLongTerm(ws); err != nil {
			log.Printf("Autonomous evolution [%s]: summarize: %v", ws.ID, err)
		} else if len(moved) > 0 {
			touched = append(touched, moved...)
		}
	}

	if !isMeaningfulDecision(dec, touched) {
		log.Printf("Autonomous evolution [%s]: no-op (action=%s)", ws.ID, dec.Action)
		e.mu.Lock()
		e.lastFingerprint[ws.ID] = snap.Fingerprint()
		e.mu.Unlock()
		return nil
	}

	learning := strings.TrimSpace(dec.Learning)
	if learning == "" {
		learning = dec.Reason
	}
	entry := LogEntry{
		WorkspaceID: ws.ID,
		ModeID:      ws.ActiveMode,
		Action:      dec.Action,
		Reason:      dec.Reason,
		Learning:    learning,
		DocTouched:  touched,
	}
	if shouldFinalizeShortTerm(dec, touched, snap, sessionCompress) {
		if arch, err := brain.FinalizeShortTermAfterConsolidate(brain.DefaultKeepRecentAfterConsolidate); err != nil {
			log.Printf("Autonomous evolution [%s]: short-term finalize: %v", ws.ID, err)
		} else if arch != "" {
			entry.DocTouched = append(entry.DocTouched, arch)
			log.Printf("Autonomous evolution [%s]: short-term archived to %s", ws.ID, arch)
			if fresh, err := Observe(ws); err == nil {
				snap = fresh
			}
		}
	}
	if err := brain.SyncMemoryIndexAfterEvolution(entry.DocTouched, learning, archRel(entry.DocTouched)); err != nil {
		log.Printf("Autonomous evolution [%s]: memory index: %v", ws.ID, err)
	}
	if err := AppendLog(ws, entry); err != nil {
		return err
	}

	e.mu.Lock()
	e.lastFingerprint[ws.ID] = snap.Fingerprint()
	if len(touched) > 0 {
		e.cooldownUntil[ws.ID] = time.Now().Add(e.interval)
	}
	e.mu.Unlock()

	log.Printf("Autonomous evolution [%s]: action=%s files=%v", ws.ID, dec.Action, touched)
	return nil
}

func buildDecisionPrompt(ws *brain.Workspace, snap *Snapshot, sessionCompress, crystallize bool) string {
	var b strings.Builder
	b.WriteString("workspace_scope: id=")
	b.WriteString(ws.ID)
	b.WriteString(" focus_path=")
	b.WriteString(ws.RootPath)
	b.WriteString(" active_mode=")
	b.WriteString(brain.NormalizeModeID(ws.ActiveMode))
	b.WriteString(" project_cata=")
	b.WriteString(ws.ProjectCataRoot())
	b.WriteString(" home_brain=")
	b.WriteString(ws.Dir())
	if ws.Name != "" {
		b.WriteString(" name=")
		b.WriteString(ws.Name)
	}
	b.WriteString("\n")
	if notice := prompt.EvolveDecisionScopeNotice(); notice != "" {
		b.WriteString(notice)
		b.WriteByte('\n')
	}
	b.WriteString("triggers: ")
	b.WriteString(strings.Join(snap.Triggers, ", "))
	if sessionCompress {
		b.WriteString(" (session-driven compress)")
	}
	if crystallize {
		b.WriteString(" (crystallize_skill)")
	}
	if len(snap.SkillIDs) > 0 {
		b.WriteString("\nexisting_skills: ")
		b.WriteString(strings.Join(snap.SkillIDs, ", "))
	}
	b.WriteString("\nstate: ")
	compact, _ := json.Marshal(snap)
	b.Write(compact)

	if snap.ShortTermBytes >= shortTermActivityBytes {
		includeExcerpt := true
		if snap.LastEvolutionAt != "" && snap.ShortTermModTime != "" &&
			snap.ShortTermModTime <= snap.LastEvolutionAt && snap.ShortTermBytes < shortTermTriggerBytes {
			includeExcerpt = false
		}
		if includeExcerpt {
			if excerpt, err := readFileCap(ws.ShortTermPath(), maxShortExcerptBytes); err == nil && excerpt != "" {
				b.WriteString("\n\nshort_term excerpt (this workspace only):\n")
				b.WriteString(excerpt)
			}
		} else {
			b.WriteString("\n\n(short_term unchanged since last evolution; excerpt omitted)\n")
		}
		if hot, err := readFileCap(ws.PersonaPath(), 1200); err == nil && hot != "" {
			b.WriteString("\n\ncurrent mode persona (project content — modes/")
			b.WriteString(brain.NormalizeModeID(ws.ActiveMode))
			b.WriteString("/persona.md):\n")
			b.WriteString(hot)
		}
		modeDir := ws.ModeDir(brain.NormalizeModeID(ws.ActiveMode))
		if beh, err := readFileCap(filepath.Join(modeDir, brain.FileBehavior), 800); err == nil && beh != "" && !brain.FileNeedsEvolveFill(filepath.Join(modeDir, brain.FileBehavior)) {
			b.WriteString("\n\ncurrent mode behavior (project content):\n")
			b.WriteString(beh)
		}
		if local, err := readFileCap(ws.PersonaLocalPath(), 1200); err == nil {
			b.WriteString("\n\ncurrent persona.local (project content):\n")
			b.WriteString(local)
		}
	}
	if mustFill := brainDocsNeedingFill(ws); len(mustFill) > 0 {
		b.WriteString("\n\nMUST fill scaffold brain docs this cycle (this workspace only; from short_term): ")
		b.WriteString(strings.Join(mustFill, ", "))
	}
	if hasCompactTrigger(snap) {
		b.WriteString("\n\nMUST compact project content this cycle: remove duplicates and stale bullets; merge overlapping ## sections with replace_section or rewrite with overwrite. Do not append redundant text. Target each file under ~3500 bytes. action should be consolidate.")
	}
	if snap.RecentLogSummary != "" {
		b.WriteString("\n\nrecent evolution: ")
		b.WriteString(snap.RecentLogSummary)
	}
	if footer := prompt.EvolveDecisionFooter(); footer != "" {
		b.WriteString("\n\n")
		b.WriteString(footer)
	}
	return b.String()
}

func readFileCap(path string, max int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := string(data)
	if len(s) > max {
		return s[:max] + "\n…(truncated)", nil
	}
	return s, nil
}

// RunCycle 对当前活跃 workspace 执行一轮（测试用）。
func RunCycle(ctx context.Context) error {
	ws, err := brain.MustActive()
	if err != nil {
		return err
	}
	return NewEngine(cycleInterval()).runCycle(ctx, ws, false, false)
}
