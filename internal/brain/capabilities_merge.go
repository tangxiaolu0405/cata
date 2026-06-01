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
	caps := ParseCapabilitiesYAML(data)
	for _, s := range caps.Skills {
		if strings.EqualFold(s, skillID) {
			return nil
		}
	}
	caps.Skills = append(caps.Skills, skillID)
	out := FormatCapabilitiesYAML(caps)
	if len(out) > maxCapabilitiesFileBytes {
		return fmt.Errorf("capabilities.yaml too large after append")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(out), 0644)
}

// RejectCapabilitiesPatch 演进 patch capabilities 时保留 mcp 段（skill 名仍优先 server append）。
func RejectCapabilitiesPatch(rel, mode, content string) error {
	if !strings.HasSuffix(filepath.ToSlash(rel), FileCapabilities) {
		return nil
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "append" || mode == "" {
		return fmt.Errorf("capabilities.yaml: use server-side skill append only")
	}
	if mode == "write" || mode == "overwrite" {
		return brainRejectCapabilitiesOverwrite(content)
	}
	return nil
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
