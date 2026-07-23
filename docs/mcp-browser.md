# Browser MCP（Playwright）

cata 通过 `~/.cata/config.json` → `mcp.servers` 启动 **Playwright MCP**（stdio），在 `capabilities.yaml` 里用 `mcp: [browser]` 启用。

## 默认配置（v0.1.4+）

| 项 | 值 |
|----|-----|
| 包 | `@playwright/mcp@0.0.75`（pin，不用 `@latest`） |
| 启动 | `npx -y @playwright/mcp@0.0.75` |
| Console | **默认关闭**（旧版带 `--console` 会把页面 console 附在每次工具结果里，占 token 且易误导模型） |
| 工具超时 | `tool_timeout_seconds`: **300**（发帖、大页加载别卡 120s，否则 cata 会 Kill MCP 重连 → 浏览器窗口消失） |

> **注意（已修）**：旧版用 `exec.CommandContext` 绑在 Init 的 60s 超时上，初始化一结束就会 Kill MCP，有界面时像「浏览器闪一下就关」。请用含该修复的构建。

旧配置 `["-y", "@playwright/mcp@latest", "--console"]` 或 `["-y", "@playwright/mcp@latest"]` 在 **LoadConfig 时自动迁移** 为新默认。

## 两种浏览器模式

### 1. 内置 Chromium（默认）

Playwright 自带 Chromium，profile 在：

```text
# Windows
%USERPROFILE%\AppData\Local\ms-playwright\mcp-{channel}-{workspace-hash}
```

适合通用自动化、测试。对 **小红书创作服务平台** 等依赖 Chrome 扩展的站点，页面常报：

```text
Failed to load resource: net::ERR_FAILED @ chrome-extension://invalid/
```

这是页面 SDK 在找扩展，**不是 cata 关浏览器**。功能可能卡住，模型容易反复 navigate，体感像「浏览器老重启」。

### 2. Extension 模式（推荐：小红书 / 已登录发布）

连到你 **已打开的 Chrome**（含登录态、扩展），需：

1. 在 Chrome 安装 [Playwright MCP Bridge](https://chromewebstore.google.com/detail/playwright-mcp-bridge/mmlmfjhmonkocbjadbfplnigmagldckm)（非 Default profile 用户请用 0.0.72+）。
2. 用该 profile 打开 Chrome，登录目标站点（如 creator.xiaohongshu.com）。
3. 修改 `~/.cata/config.json`：

```json
{
  "mcp": {
    "enabled": true,
    "tool_timeout_seconds": 300,
    "servers": [
      {
        "name": "browser",
        "enabled": true,
        "command": "npx",
        "args": ["-y", "@playwright/mcp@0.0.75", "--extension"]
      }
    ]
  }
}
```

4. 重启 `cata run` 或重新开 `cata chat`（MCP 进程在 server 生命周期内复用）。

**注意**：同一 workspace profile 同时只能被一个 Playwright MCP 实例占用；不要 cata + Cursor browser MCP 抢同一 profile。

## 可选：调试 console

需要看页面报错时再开，建议只收 error：

```json
"args": ["-y", "@playwright/mcp@0.0.75", "--console-level=error"]
```

或用 CLI（`args` 为 JSON 数组字符串）：

```bash
cata config set mcp.browser.args '["-y","@playwright/mcp@0.0.75","--console-level=error"]'
cata config set mcp.tool_timeout_seconds 300
```

不要用旧版 `--console`（等价于 info 级，噪音大）。

## 产出区里的 `.playwright-mcp/`

在产出区（如 `d:\cata_project\xiaohongshu`）会出现 `console-*.log`、`page-*.yml`，是 Playwright MCP 的调试输出，**每次 browser 工具调用可能各写一份**；开头 `[289ms]` 是**页面加载时间轴**，不一定表示 Chromium 进程重启。

## 故障排查

| 现象 | 查什么 |
|------|--------|
| 窗口闪灭再开 | server log：`MCP transient error ... reconnecting browser` → 超时或 pipe 断；加大 `tool_timeout_seconds` |
| 满屏 console + `chrome-extension://invalid` | 换 `--extension` 或接受内置 Chromium 限制 |
| 登录态丢 | 用 extension 连已登录 Chrome，或 `--user-data-dir` 指固定目录（见 [Playwright MCP 文档](https://github.com/microsoft/playwright-mcp)） |

## 相关代码

- 默认与迁移：`internal/cata/config/config.go` → `normalizeMCPConfig`
- MCP 生命周期：`internal/mcp/manager.go`（`reconnectServer` 会 Kill 子进程）
- 示例：`config.example.json`
