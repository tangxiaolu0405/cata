package brain

// MCPToolNamesProvider 由 server 注册，返回当前已导出的 MCP 工具名（供路径块列举）。
var MCPToolNamesProvider func() []string
