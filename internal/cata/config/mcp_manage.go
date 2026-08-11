package config

import "strings"

// FindMCPServer 按 name（大小写不敏感）查找 server 定义；未找到返回 nil。
func FindMCPServer(c *AppConfig, name string) *MCPServerEntry {
	if c == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	for i := range c.MCP.Servers {
		if strings.EqualFold(strings.TrimSpace(c.MCP.Servers[i].Name), name) {
			return &c.MCP.Servers[i]
		}
	}
	return nil
}

// UpsertMCPServer 按 name 合并 server 定义（manage_mcp install 用，避免重复写）。
// 已存在：更新 Enabled（及非空 Command/Args/Env），返回 false（未新增）；
// 不存在：追加，返回 true（新增）。
func UpsertMCPServer(c *AppConfig, s MCPServerEntry) bool {
	if c == nil {
		return false
	}
	if existing := FindMCPServer(c, s.Name); existing != nil {
		existing.Enabled = s.Enabled
		if strings.TrimSpace(s.Command) != "" {
			existing.Command = s.Command
		}
		if s.Args != nil {
			existing.Args = s.Args
		}
		if s.Env != nil {
			existing.Env = s.Env
		}
		return false
	}
	c.MCP.Servers = append(c.MCP.Servers, s)
	return true
}
