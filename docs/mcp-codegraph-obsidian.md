# Codegraph + Obsidian MCP（Codex 侧）

本项目的 codegraph / obsidian 两个 MCP 是给 **Codex**（以及其它支持 MCP 的客户端）用的，注册在 `~/.codex/config.toml` 的 `[mcp_servers]`；**不**写入 `~/.cata/config.json` 或项目 `.cata/modes/_default/capabilities.yaml`。

## 服务器

| 名称 | 启动命令 | 说明 |
|------|----------|------|
| `codegraph` | `/Users/lucas/.bun/bin/codegraph serve --mcp`（v1.5.0） | 代码知识图谱；本项目已索引 `.codegraph/`（239 files / 3,336 nodes / 8,434 edges） |
| `obsidian` | `npx -y @istrejo/obsidian-mcp` | 免插件直接读写 vault 文件系统；vault=`/Users/lucas/work/cata_obsidian/cata_obsidian` |

obsidian 走**纯文件系统**，不需要 Obsidian 客户端或 Local REST API 插件（应用未开也能用）。

## Codex 配置片段（`~/.codex/config.toml`）

```toml
[mcp_servers.codegraph]
type = "stdio"
command = "/Users/lucas/.bun/bin/codegraph"
args = ["serve", "--mcp"]

[mcp_servers.obsidian]
type = "stdio"
command = "npx"
args = ["-y", "@istrejo/obsidian-mcp"]
startup_timeout_sec = 120

[mcp_servers.obsidian.env]
OBSIDIAN_VAULT_PATH = "/Users/lucas/work/cata_obsidian/cata_obsidian"
```

改配置后**新开会话**才会加载 MCP（当前会话不能热加载）。

## 验证

- codegraph：`codegraph status` / `codegraph query <符号>` / `codegraph explore <问题>`（CLI 直用）；MCP 侧工具为 `codegraph_explore`。
- obsidian：`npx -y @istrejo/obsidian-mcp` 握手返回 `serverInfo: obsidian-mcp`；MCP 工具为 `read_note` / `list_notes` / `search_content` / `create_note` 等 13 个（工具名无 `obsidian_` 前缀）。

## 与 cata 的关系

cata 的 MCP 客户端（`internal/mcp`）是通用 stdio 客户端：只要在 `~/.cata/config.json` 的 `mcp.servers` 注册、且项目 `capabilities.yaml` 的 `mcp:` 列表包含该 server 名，就能接任意 MCP。codegraph/obsidian 同样可以这样给 cata 用，但目前**未启用**（本项目仅给 Codex 用）。
