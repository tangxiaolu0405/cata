# Gateway — 渠道适配与部署

> **当前实现范围：模式一（同机）**。模式二、三仅保留设计，模式一稳定后再扩展。

## 角色边界

```
[本地 Web UI] ──┐
[终端 TUI]    ──┼──▶ cata-gateway ──▶ cata worker（server）
[Telegram]    ──┤                         │
[QQ 试验]     ──┘                         ├── LLM / 工具 / MCP
                                          └── brain / evolve
```

- **gateway**：渠道消息格式、会话路由、`cwd` 映射、确认按钮 UX；**不**调用 LLM、**不**写脑子。
- **Web UI**（默认 `http://0.0.0.0:8787`）：多项目真实目录对话 + 渠道只读消息面板；手机用 `http://<电脑局域网IP>:8787`。
- **cata worker**：现有 `cata run` + Unix socket chat 循环；与 TUI 共用同一协议。

## 本地 Web UI（多项目）

启动 `cata-gateway` 后浏览器打开控制台（无需 Telegram/QQ 凭证亦可 **UI-only**）：

| 区域 | 能力 |
|------|------|
| **Projects** | 列出 `~/.cata/brain/workspaces/<id>/`（跳过 `.cata_worker` 渠道沙箱）；会话 `web:<id>`，`cwd` = meta/registry 的 `root_path` |
| **Channels** | Telegram/QQ 近期消息只读；**不能**从页面发消息、确认命令或 reset 渠道会话 |
| **设置** | 编辑 `~/.cata/config.json` 与 `~/.cata/gateway.json`；保存时**保留**未知顶层键（如 `llm_previous_qwen` / `llm_xxx`） |

配置：

```json
{
  "ui_listen": "0.0.0.0:8787"
}
```

左侧工作区来自本机已有脑子格（`~/.cata/brain/workspaces` + `registry/workspaces.json`），无需再在 `projects[]` 里手动登记。可在 UI 内点「刷新工作区」。

- `ui_listen`：空/缺省 → 默认 `0.0.0.0:8787`（局域网可访问）；`off` / `0` / `false` 关闭；也可写 `127.0.0.1:8787` 仅本机
- 环境变量 `CATA_GATEWAY_UI`：同 `ui_listen`（设为 `0` 可关 UI）
- 访问控制：本机 + 私网 IP（RFC1918 / 链路本地）；公网来源返回 403
- 绑定仅 loopback；与 TUI 共用产出区锁（同一 `root_path` 不可第二路 web）

配置页入口：控制台左下角 **设置**（运行态读写本机配置）；静态编辑仍可用 [`docs/gateway-config.html`](gateway-config.html)。

API：

| 方法 | 路径 | 说明 |
|------|------|------|
| GET/PUT | `/api/settings/app` | `config.json`：`{ path, config, extras }`；`extras` 为未知顶层键 |
| GET/PUT | `/api/settings/gateway` | `gateway.json`：同上 |

保存策略：已知节覆盖写回，`extras` 原样保留；密钥字段为 `***hidden***` 或空则不覆盖磁盘原值。渠道密钥变更需重启 `cata-gateway`。

## 产出区（worker 目录）

gateway 发给 cata 的 `cwd` 固定布局：

```
{CATA_WORKER_ROOT}/<channel>/<chat_id>/
```

默认 `CATA_WORKER_ROOT=~/.cata_worker`，例如：

```
~/.cata_worker/telegram/12345/
```

- 每个渠道会话一个目录（文件工具、`run_command` 沙箱）
- 脑子解析、`~/.cata/config.json`、evolve 规则与终端 chat **无差别**

## 发行档位（edition）

同一 `cata-gateway` 二进制，由 `~/.cata/gateway.json` 的 `edition` 决定行为（非不同安装包）：

| edition | 含义 | cata server |
|---------|------|-------------|
| **base** | 基础版：gateway + 本机 worker 一体 | 默认 `cata_server.auto_start: true`，启动时拉起 `cata run` |
| **channel** | 渠道版：仅适配器 | 默认不拉起；需单独运行 `cata run` |

`cata_server` 字段（base 版核心）：

| 字段 | 说明 |
|------|------|
| `mode` | `socket`（本机 socket，base 默认）、`external`（仅连接已有 socket）、`remote`（预留 HTTP） |
| `binary` | cata 可执行路径；空则 `CATA_BIN` 或 PATH |
| `auto_start` | 是否在 gateway 启动时确保 server 运行 |
| `managed` | 拉起时是否 `cata run --managed` |
| `stop_on_exit` | gateway 退出时是否结束本进程拉起的 server |

示例见仓库根目录 `gateway.example.json`（base）、`gateway.example.channel.json`（channel）。

初始化（推荐）：

```bash
cata-gateway init                    # base 模板 → ~/.cata/gateway.json
cata-gateway init --edition channel  # channel 模板
cata-gateway init --force            # 覆盖已有文件
```

或用浏览器打开可视化配置页（单文件、无需服务器）：

- [`docs/gateway-config.html`](gateway-config.html) — 开关渠道、编辑字段、导入/下载 `gateway.json`，复制到 `~/.cata/gateway.json`

### QQ（WebSocket 试验）

- 凭证：`qq_app_id` + `qq_app_secret`（或 `QQ_APP_ID` / `QQ_APP_SECRET`）
- 传输：官方 WebSocket 长连接（与 Telegram 一样无需公网回调）
- 场景：单聊 C2C + 群 @（intent `GROUP_AND_C2C_EVENT`）
- 产出区：`~/.cata_worker/qq/c2c_<openid>/`、`~/.cata_worker/qq/group_<openid>/`
- 说明：官方已声明 WS 逐步下线；若连接失败则本渠道不可用（Telegram 不受影响）
- 调试：`cata-gateway qq` 仅启 QQ

环境变量可覆盖：`CATA_GATEWAY_EDITION`、`CATA_SERVER_MODE`、`CATA_SERVER_AUTO_START`、`CATA_BIN`。

## 三种部署模式

| 模式 | gateway | cata worker | 传输 | 状态 |
|------|---------|-------------|------|------|
| **一、同机** | 本机 | 本机 | Unix socket `~/.cata/cata.sock` | **当前实现** |
| **二、安全隔离** | 公网/云端 | 内网或用户本机 | HTTPS → worker HTTP API（`CATA_URL`） | 设计预留 |
| **三、全云端** | 云端 A | 云端 B（或同机） | HTTP API 或 VPC 内 socket | 设计预留 |

### 模式一（当前）

- **base 版（推荐单进程）**：`edition: base`，一条命令 `cata-gateway` 即可（自动 `cata run`）
- **channel 版**：`edition: channel` 或省略 edition；worker 需 `cata run` 独立进程
- 配置：`TELEGRAM_BOT_TOKEN`、`CATA_SOCKET`（可选）、`CATA_WORKER_ROOT`（可选）
- 首渠道：Telegram 长轮询（`cmd/cata-gateway`）

### 模式二（预留）

场景：Telegram/飞书 webhook 在公网，cata 在内网，API Key 与产出区不出内网。

```
Internet ──▶ gateway (cloud) ──TLS──▶ cata serve-api (intranet)
                                         cwd = ~/.cata_worker/...
```

待实现（模式一完成后）：

- `cata serve-api` 或 `cata run --listen`：复用 `socket_chat` 语义，NDJSON over SSE/WebSocket
- gateway `CATA_URL` + 认证（token/mTLS）
- `exec_confirm` / `user_choice` 经 HTTP 回传（与 Telegram 按钮映射）

### 模式三（预留）

场景：gateway 与 worker 均在云上，可能同 VM 或 K8s 两 Pod。

- 同 VM：可继续 Unix socket（等同模式一）
- 跨 VM：同模式二 HTTP API
- 运维：worker 镜像 + gateway 镜像分离；`CATA_WORKER_ROOT` 挂卷

## 配置字段

| 字段 | 模式一 | 模式二/三 |
|------|--------|-----------|
| `edition` | `base` \| `channel` | 同左 |
| `cata_server` | base 版自动拉起 | channel 或 remote |
| `ui_listen` / `CATA_GATEWAY_UI` | Web 控制台（默认 0.0.0.0:8787） | 通常关闭 |
| `projects` | （可选遗留；UI 列表改读 brain/workspaces） | — |
| `socket_path` / `CATA_SOCKET` | ✓ | 同机时 ✓ |
| `cata_url` / `CATA_URL` | 忽略 | worker HTTP 基址 |
| `worker_root` / `CATA_WORKER_ROOT` | ✓（渠道沙箱） | ✓（worker 侧路径） |

见 `gateway.example.json`、`gateway.example.channel.json`、`README.md` Gateway 节。

## 扩展顺序（建议）

1. ✅ 模式一：Telegram + socket + per-chat worker 目录
2. 模式一完善：更多 Telegram 能力（附件、/status）、渠道插件结构
3. 模式二/三：cata HTTP API 契约 → gateway 远端客户端 → 认证与部署文档
