package brain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"cata/internal/cata/clock"
)

var caseIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

// NormalizeCaseID 校验并规范化 case id。
func NormalizeCaseID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("case_id required")
	}
	if !caseIDRe.MatchString(id) {
		return "", fmt.Errorf("invalid case_id %q (use letters/digits/._-)", id)
	}
	return id, nil
}

// CaseRoot 产出区下 cases/<id>。
func CaseRoot(outputCwd, caseID string) (string, error) {
	id, err := NormalizeCaseID(caseID)
	if err != nil {
		return "", err
	}
	cwd := strings.TrimSpace(outputCwd)
	if cwd == "" {
		cwd = strings.TrimSpace(OutputCwd())
	}
	if cwd == "" {
		return "", fmt.Errorf("output cwd empty")
	}
	return filepath.Join(cwd, DirCases, id), nil
}

// EnsureCase 创建 Case 目录树。
func EnsureCase(outputCwd, caseID string) (root string, err error) {
	root, err = CaseRoot(outputCwd, caseID)
	if err != nil {
		return "", err
	}
	for _, d := range []string{
		root,
		filepath.Join(root, "artifacts"),
		filepath.Join(root, "mode_runs"),
		filepath.Join(root, "runs"),
	} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return "", err
		}
	}
	return root, nil
}

// ArtifactStatus 交接物状态。
type ArtifactStatus string

const (
	ArtifactDraft    ArtifactStatus = "draft"
	ArtifactInReview ArtifactStatus = "in_review"
	ArtifactAccepted ArtifactStatus = "accepted"
	ArtifactRejected ArtifactStatus = "rejected"
)

type artifactMeta struct {
	Name           string            `json:"name"`
	Head           int               `json:"head"`
	Status         ArtifactStatus    `json:"status"`
	UpdatedByMode  string            `json:"updated_by_mode,omitempty"`
	AcceptedByMode string            `json:"accepted_by_mode,omitempty"`
	History        []artifactHistEnt `json:"history,omitempty"`
}

type artifactHistEnt struct {
	V      int            `json:"v"`
	Status ArtifactStatus `json:"status"`
	By     string         `json:"by,omitempty"`
	Reason string         `json:"reason,omitempty"`
}

func artifactDir(caseRoot, name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "/", "_")
	return filepath.Join(caseRoot, "artifacts", name)
}

func loadArtifactMeta(dir string) (artifactMeta, error) {
	p := filepath.Join(dir, "meta.json")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return artifactMeta{Status: ArtifactDraft}, nil
		}
		return artifactMeta{}, err
	}
	var m artifactMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return artifactMeta{}, err
	}
	return m, nil
}

func saveArtifactMeta(dir string, m artifactMeta) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "meta.json"), append(data, '\n'), 0644)
}

// WriteCaseArtifact 写入新版本 artifact（draft）。
func WriteCaseArtifact(outputCwd, caseID, name, content, byMode string) (version int, path string, err error) {
	root, err := EnsureCase(outputCwd, caseID)
	if err != nil {
		return 0, "", err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, "", fmt.Errorf("artifact name required")
	}
	dir := artifactDir(root, name)
	m, err := loadArtifactMeta(dir)
	if err != nil {
		return 0, "", err
	}
	m.Name = name
	m.Head++
	m.Status = ArtifactDraft
	m.UpdatedByMode = strings.TrimSpace(byMode)
	m.History = append(m.History, artifactHistEnt{V: m.Head, Status: ArtifactDraft, By: m.UpdatedByMode})
	path = filepath.Join(dir, fmt.Sprintf("v%d.md", m.Head))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, "", err
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return 0, "", err
	}
	if err := saveArtifactMeta(dir, m); err != nil {
		return 0, "", err
	}
	return m.Head, path, nil
}

// SetCaseArtifactStatus 更新 head 状态。
func SetCaseArtifactStatus(outputCwd, caseID, name string, status ArtifactStatus, byMode, reason string) error {
	root, err := EnsureCase(outputCwd, caseID)
	if err != nil {
		return err
	}
	dir := artifactDir(root, name)
	m, err := loadArtifactMeta(dir)
	if err != nil {
		return err
	}
	if m.Head <= 0 {
		return fmt.Errorf("artifact %q has no versions", name)
	}
	m.Status = status
	if status == ArtifactAccepted {
		m.AcceptedByMode = strings.TrimSpace(byMode)
	}
	m.History = append(m.History, artifactHistEnt{
		V: m.Head, Status: status, By: strings.TrimSpace(byMode), Reason: strings.TrimSpace(reason),
	})
	return saveArtifactMeta(dir, m)
}

// ReadCaseArtifact 读指定版本；version<=0 读 head。acceptedOnly 时拒绝非 accepted。
func ReadCaseArtifact(outputCwd, caseID, name string, version int, acceptedOnly bool) (body string, metaSummary string, err error) {
	root, err := CaseRoot(outputCwd, caseID)
	if err != nil {
		return "", "", err
	}
	dir := artifactDir(root, name)
	m, err := loadArtifactMeta(dir)
	if err != nil {
		return "", "", err
	}
	if m.Head <= 0 {
		return "", "", fmt.Errorf("artifact %q not found", name)
	}
	if version <= 0 {
		version = m.Head
	}
	if acceptedOnly && m.Status != ArtifactAccepted {
		return "", "", fmt.Errorf("artifact %q status=%s (need accepted)", name, m.Status)
	}
	path := filepath.Join(dir, fmt.Sprintf("v%d.md", version))
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	metaSummary = fmt.Sprintf("%s@v%d status=%s", name, version, m.Status)
	return string(data), metaSummary, nil
}

// ModeRunLog 一条专职 mode 委托记录。
type ModeRunLog struct {
	ModeID       string
	CaseID       string
	SubagentID   string
	Task         string
	Status       string
	Summary      string
	ArtifactsIn  []string
	ArtifactsOut []string
	Rounds       int
}

// AppendModeRunLog 写入 cases/.../mode_runs/<mode>/<ts>.md
func AppendModeRunLog(outputCwd string, rec ModeRunLog) (string, error) {
	root, err := EnsureCase(outputCwd, rec.CaseID)
	if err != nil {
		return "", err
	}
	mode := NormalizeModeID(rec.ModeID)
	dir := filepath.Join(root, "mode_runs", mode)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	ts := clock.Now().UTC().Format("20060102T150405")
	if ts == "" {
		ts = time.Now().UTC().Format("20060102T150405")
	}
	path := filepath.Join(dir, ts+".md")
	var b strings.Builder
	b.WriteString("# Mode run\n\n")
	fmt.Fprintf(&b, "- mode: %s\n- case: %s\n- subagent: %s\n- status: %s\n- rounds: %d\n", mode, rec.CaseID, rec.SubagentID, rec.Status, rec.Rounds)
	if len(rec.ArtifactsIn) > 0 {
		fmt.Fprintf(&b, "- artifacts_in: %s\n", strings.Join(rec.ArtifactsIn, ", "))
	}
	if len(rec.ArtifactsOut) > 0 {
		fmt.Fprintf(&b, "- artifacts_out: %s\n", strings.Join(rec.ArtifactsOut, ", "))
	}
	b.WriteString("\n## Task\n\n")
	b.WriteString(strings.TrimSpace(rec.Task))
	b.WriteString("\n\n## Summary\n\n")
	b.WriteString(strings.TrimSpace(rec.Summary))
	b.WriteByte('\n')
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return "", err
	}
	return path, nil
}

// AppendDelegateModeNote 主 short-term 留痕（全局 Active）。
func AppendDelegateModeNote(modeID, caseID, subID, status, summary string) error {
	w, err := MustActive()
	if err != nil {
		return err
	}
	return AppendDelegateModeNoteFor(w, modeID, caseID, subID, status, summary)
}

// AppendDelegateModeNoteFor 显式指定 workspace 的主 short-term 留痕（多 chat 并行勿依赖全局 Active）。
func AppendDelegateModeNoteFor(w *Workspace, modeID, caseID, subID, status, summary string) error {
	if w == nil {
		var err error
		w, err = MustActive()
		if err != nil {
			return err
		}
	}
	summary = truncateRunes(strings.TrimSpace(summary), 240)
	line := fmt.Sprintf("[delegate_mode mode=%s case=%s id=%s status=%s] %s",
		ResolveDelegateModeID(modeID), caseID, subID, status, summary)
	block := fmt.Sprintf("\n\n## %s delegate_mode\n\n%s\n", clock.RFC3339(), line)
	return appendToShortTerm(w.ShortTermPath(), block)
}
