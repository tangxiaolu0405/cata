package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"cata/internal/cata/config"
)

// SkillManifest 可执行 skill（manifest.yaml）。
type SkillManifest struct {
	Runner      string
	Entry       string
	Description string
}

// RunSkillArgs run_skill 工具参数。
type RunSkillArgs struct {
	Skill  string                 `json:"skill"`
	Params map[string]interface{} `json:"params"`
}

// ResolveSkillDir workspace 脑子优先，其次 ~/.cata/skills/。
func ResolveSkillDir(skillID string) (dir string, err error) {
	skillID = strings.TrimSpace(skillID)
	if skillID == "" {
		return "", fmt.Errorf("skill name required")
	}
	if w := Active(); w != nil {
		p := w.SkillDir(skillID)
		if _, e := os.Stat(filepath.Join(p, FileSkillManifest)); e == nil {
			return p, nil
		}
	}
	g := filepath.Join(CataHome(), DirSkills, skillID)
	if _, e := os.Stat(filepath.Join(g, FileSkillManifest)); e == nil {
		return g, nil
	}
	return "", fmt.Errorf("skill %q: manifest not found in workspace brain or ~/.cata/skills", skillID)
}

// LoadSkillManifest 解析 manifest.yaml（简易 key: value）。
func LoadSkillManifest(dir string) (*SkillManifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, FileSkillManifest))
	if err != nil {
		return nil, err
	}
	m := &SkillManifest{Runner: "python", Entry: "script.py"}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		k := strings.TrimSpace(line[:i])
		v := strings.TrimSpace(strings.Trim(line[i+1:], `"'`))
		switch k {
		case "runner":
			m.Runner = v
		case "entry":
			m.Entry = v
		case "description":
			m.Description = v
		}
	}
	if m.Entry == "" {
		return nil, fmt.Errorf("manifest entry is empty")
	}
	return m, nil
}

// RunSkill 在产出区 cwd 执行脑子内脚本。
func RunSkill(ctx context.Context, args RunSkillArgs) (string, error) {
	dir, err := ResolveSkillDir(args.Skill)
	if err != nil {
		return "", err
	}
	manifest, err := LoadSkillManifest(dir)
	if err != nil {
		return "", fmt.Errorf("load manifest: %w", err)
	}
	entry := filepath.Join(dir, manifest.Entry)
	if _, err := os.Stat(entry); err != nil {
		return "", fmt.Errorf("entry %s: %w", manifest.Entry, err)
	}
	wd, err := ExecWorkingDir()
	if err != nil {
		return "", err
	}
	argv, err := buildSkillArgv(manifest.Runner, entry, args.Params)
	if err != nil {
		return "", err
	}
	to := 120 * time.Second
	if config.Config != nil && config.Config.Exec.TimeoutSeconds > 0 {
		to = time.Duration(config.Config.Exec.TimeoutSeconds) * time.Second
	}
	xctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	cmd := exec.CommandContext(xctx, argv[0], argv[1:]...)
	cmd.Dir = wd
	outb, err := cmd.CombinedOutput()
	maxB := 256 * 1024
	if config.Config != nil && config.Config.Exec.MaxOutputBytes > 0 {
		maxB = config.Config.Exec.MaxOutputBytes
	}
	trunc := false
	if len(outb) > maxB {
		outb = outb[:maxB]
		trunc = true
	}
	text := string(outb)
	if trunc {
		text += "\n…(truncated)"
	}
	if err != nil {
		return text, fmt.Errorf("run_skill %s: %w", args.Skill, err)
	}
	return fmt.Sprintf("run_skill %s ok (cwd=%s)\n%s", args.Skill, wd, text), nil
}

func buildSkillArgv(runner, entry string, params map[string]interface{}) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(runner)) {
	case "python", "python3", "":
		argv := []string{"python", entry}
		if len(params) > 0 {
			if b, e := json.Marshal(params); e == nil && len(b) > 2 && string(b) != "{}" {
				argv = append(argv, string(b))
			}
		}
		return argv, nil
	case "node":
		return []string{"node", entry}, nil
	default:
		return nil, fmt.Errorf("unsupported runner %q", runner)
	}
}

// ParseSkillIDFromRel 从 skills/<id>/... 路径解析 skill id。
func ParseSkillIDFromRel(rel string) string {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if !strings.HasPrefix(rel, DirSkills+"/") {
		return ""
	}
	rest := strings.TrimPrefix(rel, DirSkills+"/")
	if i := strings.Index(rest, "/"); i > 0 {
		return rest[:i]
	}
	return ""
}
