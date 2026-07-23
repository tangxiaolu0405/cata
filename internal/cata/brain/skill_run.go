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
	// VerifyEntry 可选：固化后自动验证脚本（相对 skill 目录）。
	VerifyEntry string
}

// RunSkillArgs run_skill 工具参数。
type RunSkillArgs struct {
	Skill  string                 `json:"skill"`
	Params map[string]interface{} `json:"params"`
}

// ResolveSkillDir 项目 .cata/skills 优先，其次 ~/.cata/skills/（全局回退）。
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
		// SKILL.md-only skills (no manifest yet) still resolve for read_skill.
		if _, e := os.Stat(filepath.Join(p, FileSkillMD)); e == nil {
			return p, nil
		}
	}
	g := filepath.Join(CataHome(), DirSkills, skillID)
	if _, e := os.Stat(filepath.Join(g, FileSkillManifest)); e == nil {
		return g, nil
	}
	if _, e := os.Stat(filepath.Join(g, FileSkillMD)); e == nil {
		return g, nil
	}
	return "", fmt.Errorf("skill %q: not found under focus_path/.cata/skills/ or ~/.cata/skills/", skillID)
}

// LoadSkillManifest 解析 manifest.yaml（简易 key: value）。
// runner / entry 必须显式写出；不默认任何语言（尤其不默认 python）。
func LoadSkillManifest(dir string) (*SkillManifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, FileSkillManifest))
	if err != nil {
		return nil, err
	}
	m := &SkillManifest{}
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
		case "verify_entry", "verify":
			m.VerifyEntry = v
		}
	}
	if strings.TrimSpace(m.Runner) == "" {
		return nil, fmt.Errorf("manifest runner is required (do not assume a language)")
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
	text, err := runSkillCmd(ctx, wd, argv)
	if err != nil {
		return text, fmt.Errorf("run_skill %s: %w", args.Skill, err)
	}
	return fmt.Sprintf("run_skill %s ok (cwd=%s)\n%s", args.Skill, wd, text), nil
}

func runSkillCmd(ctx context.Context, wd string, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("empty argv")
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
	return text, err
}

func buildSkillArgv(runner, entry string, params map[string]interface{}) ([]string, error) {
	r := strings.ToLower(strings.TrimSpace(runner))
	if r == "" {
		return nil, fmt.Errorf("runner required (do not assume a language)")
	}
	switch r {
	case "node":
		argv := []string{"node", entry}
		if len(params) > 0 {
			if b, e := json.Marshal(params); e == nil && len(b) > 2 && string(b) != "{}" {
				argv = append(argv, string(b))
			}
		}
		return argv, nil
	case "bash", "sh":
		return []string{"bash", entry}, nil
	case "go", "go-run":
		return []string{"go", "run", entry}, nil
	case "python", "python3":
		// 仅当 manifest 显式声明 runner 时才用；系统不默认选 python。
		argv := []string{r, entry}
		if len(params) > 0 {
			if b, e := json.Marshal(params); e == nil && len(b) > 2 && string(b) != "{}" {
				argv = append(argv, string(b))
			}
		}
		return argv, nil
	default:
		// 通用：runner 即 PATH 中的可执行文件名
		argv := []string{runner, entry}
		if len(params) > 0 {
			if b, e := json.Marshal(params); e == nil && len(b) > 2 && string(b) != "{}" {
				argv = append(argv, string(b))
			}
		}
		return argv, nil
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
