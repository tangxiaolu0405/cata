package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxCapabilitiesFileBytes = 2048

// CapabilitiesPath 当前 mode capabilities.yaml。
func (w *Workspace) CapabilitiesPath() string {
	return filepath.Join(w.ModeDir(w.modeID()), FileCapabilities)
}

// AppendSkillToCapabilities 追加 skill 名（不修改 mcp 段）。
func AppendSkillToCapabilities(w *Workspace, skillID string) error {
	if w == nil {
		return fmt.Errorf("no workspace")
	}
	skillID = strings.TrimSpace(skillID)
	if skillID == "" {
		return fmt.Errorf("empty skill id")
	}
	path := w.CapabilitiesPath()
	data, _ := os.ReadFile(path)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") && strings.EqualFold(strings.TrimSpace(line[2:]), skillID) {
			return nil
		}
	}
	caps := ParseCapabilitiesYAML(data)
	for _, s := range caps.Skills {
		if strings.EqualFold(s, skillID) {
			return nil
		}
	}
	if strings.Contains(string(data), "skills: []") {
		data = []byte(strings.Replace(string(data), "skills: []", "skills:", 1))
	}
	var b strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	text := b.String()
	if !strings.Contains(text, "skills:") {
		if len(text) > 0 && !strings.HasSuffix(text, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString("skills:\n")
	}
	b.WriteString("  - ")
	b.WriteString(skillID)
	b.WriteByte('\n')
	out := b.String()
	if len(out) > maxCapabilitiesFileBytes {
		return fmt.Errorf("capabilities.yaml too large after append")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(out), 0644)
}

// RejectCapabilitiesPatch 禁止演进 patch 破坏 mcp。
func RejectCapabilitiesPatch(rel, mode, content string) error {
	if !strings.HasSuffix(filepath.ToSlash(rel), FileCapabilities) {
		return nil
	}
	if strings.EqualFold(mode, "append") {
		return fmt.Errorf("capabilities.yaml: use server-side skill append only")
	}
	if strings.EqualFold(mode, "write") || strings.EqualFold(mode, "overwrite") {
		return brainRejectCapabilitiesOverwrite(content)
	}
	return fmt.Errorf("capabilities.yaml cannot be patched by evolution")
}

func brainRejectCapabilitiesOverwrite(content string) error {
	c := strings.ToLower(content)
	if strings.Contains(c, "mcp: []") || strings.Contains(c, "mcp:[]") {
		return fmt.Errorf("must not clear mcp")
	}
	if !strings.Contains(c, "mcp:") {
		return fmt.Errorf("must retain mcp section")
	}
	return nil
}
