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

// ParseCapabilitiesYAML 解析简易 YAML（仅 skills:/mcp: 列表，支持多行 `- x` 与内联 `[a, b]`）。
// 语义（manage_mcp disable 依赖）：
//   - 显式含 `mcp:` 节（含 `mcp: []` / 空节）= 以列表为准，空列表 = 不启用任何 MCP；
//   - 没有 `mcp:` 节 / 文件缺失 = 兼容默认 browser（老项目），由 Load/Parse 层补齐。
func ParseCapabilitiesYAML(data []byte) Capabilities {
	var out Capabilities
	section := ""
	mcpSectionPresent := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
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
			continue
		}
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		rest := strings.TrimSpace(line[idx+1:])
		if rest != "" && !strings.HasPrefix(rest, "[") {
			continue // 非列表内联值（如 version: 1）不当作节头
		}
		section = key
		if key == "mcp" {
			mcpSectionPresent = true
		}
		if strings.HasPrefix(rest, "[") && strings.HasSuffix(rest, "]") {
			inner := strings.TrimSpace(rest[1 : len(rest)-1])
			if inner == "" {
				continue
			}
			for _, item := range strings.Split(inner, ",") {
				item = strings.Trim(strings.TrimSpace(item), `"'`)
				if item == "" {
					continue
				}
				switch key {
				case "skills":
					out.Skills = append(out.Skills, item)
				case "mcp":
					out.MCP = append(out.MCP, item)
				}
			}
		}
	}
	if !mcpSectionPresent && len(out.MCP) == 0 {
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
// 注意：MCP 空列表 = 显式禁用全部（capabilities.yaml 写 `mcp:` 空节）；
// 默认 browser 由 Load/Parse 层在「没有 mcp 节」时补齐，这里不兜底。
func (c Capabilities) AllowsMCPServer(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, m := range c.MCP {
		if strings.EqualFold(strings.TrimSpace(m), name) {
			return true
		}
	}
	return false
}
