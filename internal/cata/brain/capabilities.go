package brain

import (
	"os"
	"path/filepath"
	"strings"
)

// Capabilities 当前 mode 启用的 MCP / Skill（capabilities.yaml）。
type Capabilities struct {
	Skills []string
	MCP    []string
}

// LoadActiveCapabilities 读取当前 workspace 活跃 mode 的 capabilities.yaml。
func LoadActiveCapabilities() Capabilities {
	return LoadCapabilitiesFor(Active())
}

// LoadCapabilitiesFor 显式指定 workspace 读取 capabilities.yaml（多 chat 并行勿依赖全局 Active）。
func LoadCapabilitiesFor(w *Workspace) Capabilities {
	if w == nil {
		return Capabilities{MCP: []string{"browser"}}
	}
	path := filepath.Join(w.ModeDir(w.modeID()), FileCapabilities)
	data, err := os.ReadFile(path)
	if err != nil {
		return Capabilities{MCP: []string{"browser"}}
	}
	return ParseCapabilitiesYAML(data)
}

// ParseCapabilitiesYAML 解析简易 YAML（仅 skills:/mcp: 列表）。
func ParseCapabilitiesYAML(data []byte) Capabilities {
	var out Capabilities
	section := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasSuffix(line, ":") && !strings.HasPrefix(line, "-") {
			section = strings.TrimSuffix(line, ":")
			continue
		}
		if strings.HasPrefix(line, "- ") {
			item := strings.TrimSpace(strings.TrimPrefix(line, "- "))
			switch section {
			case "skills":
				out.Skills = append(out.Skills, item)
			case "mcp":
				out.MCP = append(out.MCP, item)
			}
		}
	}
	if len(out.MCP) == 0 {
		out.MCP = []string{"browser"}
	}
	return out
}

// FormatCapabilitiesYAML 序列化 capabilities（skills + mcp 列表）。
func FormatCapabilitiesYAML(c Capabilities) string {
	var b strings.Builder
	b.WriteString("skills:\n")
	for _, s := range c.Skills {
		b.WriteString("  - ")
		b.WriteString(s)
		b.WriteByte('\n')
	}
	b.WriteString("mcp:\n")
	for _, m := range c.MCP {
		b.WriteString("  - ")
		b.WriteString(m)
		b.WriteByte('\n')
	}
	return b.String()
}

// AllowsMCPServer 是否启用该 MCP server 名。
func (c Capabilities) AllowsMCPServer(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	if len(c.MCP) == 0 {
		return true
	}
	for _, m := range c.MCP {
		if strings.EqualFold(strings.TrimSpace(m), name) {
			return true
		}
	}
	return false
}
