# Gateway — 渠道适配与部署

> **模式一（同机）** 与 **remote（云端注册中心 + WSS 隧道）** 均已实现。
> 详见 [`docs/tunnel.md`](tunnel.md)（远程部署的正式设计）。

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
- **cata worker**：每工作空间一个 `cata agent` 进程（per-ws），各自监听
  `~/.cata/sockets/<ws_id>.sock`，与 TUI 共用同一 NDJSON chat 协议。

## 本地 Web UI（多项目，per-ws agent）

启动 `cata-gateway` 后浏览器打开控制台（无需 Telegram/QQ 凭证亦可 **UI-only**）：

| 区域 | 能力 |
|------|------|
| **Projects** | 列出 `~/.cata/brain/workspaces/<id>/`（跳过 `.cata_worker` 渠道沙箱）；会话 `web:<id>`，`<id>` = ws_id = agent_id |
| **Channels** | Telegram/QQ 近期消息只读；**不能**从页面发消息、确认命令或 reset 渠道会话 |
| **设置** | 编辑 `~/.cata/config.json` 与 `~/.cata/gateway.json`；保存时**保留**未知顶层键（如 `llm_previous_qwen` / `llm_xxx`） |

多项目隔离：每个项目一条会话，按 `project.ID`（= ws_id）拨到对应 agent 的 per-ws socket
（`~/.cata/sockets/<ws_id>.sock`），未注册项目由 `EnsureAgent` 按需拉起、空闲回收；
注册且 keep-alive 的项目由 supervisor 守护保活。不同项目 = 独立 agent 进程，可并行对话。

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

gateway 与 cata 之间**转发消息**：会话必须先通过 `/dir` 绑定工作空间，消息才转发到该工作空间的
agent（`cwd` = 该 agent 工作空间根路径）。未绑定前的默认 worker 布局（仅作兜底，不参与转发）：

```
{CATA_WORKER_ROOT}/<channel>/<chat_id>/
```

默认 `CATA_WORKER_ROOT=~/.cata_worker`，例如：

```
~/.cata_worker/telegram/12345/
```

- **未绑定前：不转发**——发消息会提示先 `/dir` 选择工作空间（**无默认转发目标**）
- `/dir` 绑定后：该会话消息转发到选定工作空间的 agent，`cwd` = 该 agent 工作空间根

### 会话切换工作空间（/dir）

**按会话切换**：每个渠道会话独立 `/dir` 选择工作空间，各会话互不影响。

```
/dir                    # 查看/选择本会话的工作空间（须先看列表才能 /dir <序号>）
/dir 1                  # 切换为列表第 1 个（序号按最近使用排序，须先发 /dir 确认列表）
/dir ~/stock            # 也可直接切换为该路径所在工作区
/dir reset              # 恢复默认 worker 目录（此后消息不再转发，需重新 /dir）
```

- **转发模型**：gateway 把消息转发到 `/dir` 选定工作空间的 agent；目标 agent 不在线
  （不在 supervisor）时自动拉起
- **持久化**：切换写入 `~/.cata/gateway_session_cwd.json`，**重启自动恢复**
- 切换后续会话连接重建，历史按新工作空间转发；`/help` 中同样列出
- 远程（remote）模式同样支持：切换后经隧道拨该工作空间 agent

## 发行档位（edition）

同一 `cata-gateway` 二进制，由 `~/.cata/gateway.json` 的 `edition` 决定行为（非不同安装包）：

| edition | 含义 | cata worker |
|---------|------|-------------|
| **base** | 基础版：gateway + 本机 per-ws agent 一体 | 默认 `auto_start: true`，启动时确保 supervisor（保活常驻 agent） |
| **channel** | 渠道版：仅适配器 | 默认不确保 supervisor；agent 按需/外部运行 |
| **remote** | 云端版：注册中心 + 隧道路由 | 不拉起本机进程；worker 由各机器 `cata agent --link` 提供 |

`cata_server` 字段：

| 字段 | 说明 |
|------|------|
| `mode` | `socket`（本机 per-ws agent socket，base 默认）、`external`（仅连接已有 agent）、`remote`（云端注册中心 + WSS 隧道，见 [tunnel.md](tunnel.md)） |
| `auto_start` | 是否在 gateway 启动时确保 supervisor 运行 |

> `binary` / `managed` / `stop_on_exit` 为历史字段（legacy `cata run` 时代），当前不读取，保留仅向后兼容。

示例见仓库根目录 `gateway.example.json`（base）、`gateway.example.channel.json`（channel）。

初始化（推荐）：

```bash
cata-gateway init                    # base 模板 → ~/.cata/gateway.json
cata-gateway init --edition channel  # channel 模板
cata-gateway init --edition remote   # remote（云端隧道）模板
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

## 独立部署（install_gateway.sh）

把 `cata-gateway` 单独跑在一台机器上时，用仓库根目录的 `install_gateway.sh`：
它从 GitHub Releases 下载二进制、写 `~/.cata/gateway.json`、再启动（**编译已在 CI 完成，
脚本不本地编译**）。

**Linux / macOS（一键，从远端拉脚本执行）**

```bash
curl -fsSL https://raw.githubusercontent.com/tangxiaolu0405/cata/main/install_gateway.sh | \
  GATEWAY_UI_PASSWORD=你的口令 bash
```

**下载到本地再执行**（便于审查 / 离线）

```bash
curl -fsSL https://raw.githubusercontent.com/tangxiaolu0405/cata/main/install_gateway.sh -o install_gateway.sh
chmod +x install_gateway.sh
GATEWAY_UI_PASSWORD=你的口令 ./install_gateway.sh
```

启动方式与环境变量：

| 参数 / 变量 | 说明 |
|------|------|
| `GATEWAY_UI_PASSWORD` | 控制台访问口令；**设了才启用登录页**，否则仅本机/局域网可访问 |
| `GATEWAY_UI_LISTEN` | 监听地址，默认 `0.0.0.0:8787` |
| `GATEWAY_EDITION` | 默认 `channel`（仅 gateway）；`base` 会额外拉起本机 cata server |
| `INSTALL_DIR` | 二进制安装目录，默认 `~/.local/bin` |
| `GATEWAY_REPO` | 自定义仓库，默认 `tangxiaolu0405/cata` |
| `--version=v1.2.3` | 指定 release tag，默认 latest |
| `--run=systemd` | 生成并启用 systemd unit（需 root）；默认 nohup 后台 + pidfile |

登录安全：设了口令后，UI 所有请求需登录会话 cookie；**连续 5 次登录失败封该 IP 10 分钟**
（阈值/时长可改 `gateway.json` 的 `login_ban_max_attempts` / `login_ban_duration_seconds`）。

示例：指定版本 + systemd 托管

```bash
GATEWAY_UI_PASSWORD=你的口令 ./install_gateway.sh --version=v1.2.3 --run=systemd
```

## 三种部署模式

| 模式 | gateway | cata worker | 传输 | 状态 |
|------|---------|-------------|------|------|
| **一、同机（per-ws）** | 本机 | 本机每项目一个 `cata agent` | Unix socket `~/.cata/sockets/<ws_id>.sock` | **当前实现** |
| **四、remote（云端隧道）** | 任意位置（云端） | 各机器 `cata agent --link` | WSS 隧道（cata-tunnel.v1） | **当前实现** |
| **二、安全隔离** | 公网/云端 | 内网或用户本机 | HTTPS → worker HTTP API（`CATA_URL`） | 由模式四取代（设计归档） |
| **三、全云端** | 云端 A | 云端 B（或同机） | HTTP API 或 VPC 内 socket | 由模式四取代（设计归档） |

### 模式一（当前，per-ws agent）

- **base 版（推荐单进程）**：`edition: base`，一条命令 `cata-gateway` 即可（自动确保 supervisor 保活常驻 agent）
- **channel 版**：`edition: channel` 或省略 edition；agent 按需拉起（`EnsureAgent`）或外部自管
- 配置：`TELEGRAM_BOT_TOKEN`、`CATA_WORKER_ROOT`（可选；`CATA_SOCKET` 已无实际用途，本地走 per-ws socket）
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

| 字段 | 模式一 | remote（模式四） |
|------|--------|-----------|
| `edition` | `base` \| `channel` | `remote` |
| `cata_server` | base 版自动拉起 | `mode: remote`（不拉起本机 server） |
| `gateway_token` / `CATA_GATEWAY_TOKEN` | — | 已移除（join 靠 X-Cata-Join 协议头 + UI 批准；隧道用逐机 machine_token） |
| `tunnel_listen` / `CATA_TUNNEL_LISTEN` | — | 隧道端点，默认 `0.0.0.0:8799` |
| `allow_agent_ids` / `CATA_GATEWAY_ALLOW_AGENTS` | — | agent 白名单（空 = 放行所有） |
| `default_agent_id` / `CATA_GATEWAY_DEFAULT_AGENT` | — | 通道会话默认路由 agent |
| `ui_listen` / `CATA_GATEWAY_UI` | Web 控制台（默认 0.0.0.0:8787） | 项目 = 在线 agent |
| `projects` | （可选遗留；UI 列表改读 brain/workspaces） | — |
| `socket_path` / `CATA_SOCKET` | ✓ | 本地模式专用 |
| `cata_url` / `CATA_URL` | 忽略 | 设置即进入 remote 模式 |
| `worker_root` / `CATA_WORKER_ROOT` | ✓（渠道沙箱） | ✓（远端 cwd 基址） |

见 `gateway.example.json`、`gateway.example.channel.json`、本文件「独立部署」节。

## 扩展顺序（建议）

1. ✅ 模式一：Telegram + socket + per-chat worker 目录
2. ✅ 模式四（remote）：WSS 隧道注册中心 + 路由（`cata agent --link` / `cata link` / `cata supervisor`），见 [tunnel.md](tunnel.md)
3. ✅ 模式一完善：Telegram 附件（photo/document/voice → worker 产出区 → cata attachments）、/status（当前工作空间 + LLM 状态）、渠道插件结构（`gateway.Channel` 接口 + `PendingManager` 共享 exec/choice 等待）、QQ /status + 工具执行提示、渠道转发改为「会话 /dir 切换工作空间」（移除 per-channel agent 绑定）
4. v2：逐 agent token、按项目路由（web 会话已按项目；渠道可扩展 per-channel agent）
