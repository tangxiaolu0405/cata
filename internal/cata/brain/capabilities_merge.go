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

// RemoveSkillFromCapabilities 从 capabilities.yaml 移除 skill（验证失败回退）。
func RemoveSkillFromCapabilities(w *Workspace, skillID string) error {
	if w == nil {
		return fmt.Errorf("no workspace")
	}
	skillID = strings.TrimSpace(skillID)
	if skillID == "" {
		return fmt.Errorf("empty skill id")
	}
	path := w.CapabilitiesPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	caps := ParseCapabilitiesYAML(data)
	filtered := caps.Skills[:0]
	for _, s := range caps.Skills {
		if strings.EqualFold(s, skillID) {
			continue
		}
		filtered = append(filtered, s)
	}
	caps.Skills = filtered
	return os.WriteFile(path, []byte(FormatCapabilitiesYAML(caps)), 0644)
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

// AppendMCPToCapabilities 追加 MCP server 名到当前 workspace 活跃 mode capabilities
// （server 端专用：manage_mcp 工具启用；evolve 仍走 RejectCapabilitiesPatch 边界）。
func AppendMCPToCapabilities(w *Workspace, name string) error {
	if w == nil {
		return fmt.Errorf("no workspace")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("empty mcp server name")
	}
	path := w.CapabilitiesPath()
	data, _ := os.ReadFile(path)
	caps := ParseCapabilitiesYAML(data)
	if caps.AllowsMCPServer(name) {
		return nil
	}
	caps.MCP = append(caps.MCP, name)
	out := FormatCapabilitiesYAML(caps)
	if len(out) > maxCapabilitiesFileBytes {
		return fmt.Errorf("capabilities.yaml too large after append")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(out), 0644)
}

// RemoveMCPFromCapabilities 从 capabilities.yaml 移除 MCP server 名（保留 skills 段）。
func RemoveMCPFromCapabilities(w *Workspace, name string) error {
	if w == nil {
		return fmt.Errorf("no workspace")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("empty mcp server name")
	}
	path := w.CapabilitiesPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	caps := ParseCapabilitiesYAML(data)
	filtered := caps.MCP[:0]
	removed := false
	for _, m := range caps.MCP {
		if strings.EqualFold(strings.TrimSpace(m), name) {
			removed = true
			continue
		}
		filtered = append(filtered, m)
	}
	if !removed {
		return nil
	}
	caps.MCP = filtered
	return os.WriteFile(path, []byte(FormatCapabilitiesYAML(caps)), 0644)
}
