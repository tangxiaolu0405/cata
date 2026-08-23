# 远程网关隧道（cata-tunnel.v1）— 设计

> 让 `cata-gateway` 部署到任意服务器，各机器上的 `cata` 通过 WSS 隧道注册上来。
> 底层 NDJSON chat 协议**零改动**——隧道只是逐字节透传。

## 一句话架构（扁平化三层）

```
           云端（任意位置）                      各机器
┌────────────────────────────┐      WSS       ┌─────────────────────────────┐
│ cata-gateway (remote 模式)  │ ◀────────────▶ │ cata supervisor             │
│  · 无状态注册中心 + 路由      │   隧道         │  · 每机器一个，只管进程生命周期 │
│  · Web UI / 渠道 都是会话    │                └───────────┬─────────────────┘
│  · 不跑 LLM、不写脑子        │                            │ 拉起 / 保活 / 停止
└────────────────────────────┘                            ▼
                                             ┌─────────────────────────────┐
                                             │ cata agent --workspace <id> │
                                             │  · 一个工作空间 = 一个 LLM loop │
                                             │  · 自持到网关的 WSS 隧道        │
                                             └─────────────────────────────┘
```

- **gateway（云端）**：一个对外渠道。部署在任意位置，注册上来的 worker 都能用。
- **supervisor（每机器一个）**：只负责本机注册工作空间的 agent 进程生命周期；不转发对话、不持隧道。
- **agent（每工作空间一个）**：`cata agent --workspace <id>`，绑定单一工作空间（一个实际上的 LLM loop），
  服务本机 `~/.cata/sockets/<ws_id>.sock`；注册且配了网关时自持 WSS 隧道。

**隔离键 = agent_id = ws_id（工作空间 id）**。网关视角下，`workerid + worker 的工作空间` 就构成实际隔离。

## 为什么这样设计

- 网关简单化：就是一个对外渠道，无状态；重启后 agent 重连即恢复。
- 协议简单：`agent_id` 唯一标识 worker 的工作空间；会话 = 网关把一条「逻辑 socket 连接」经隧道
  反向拨到对应 agent 的本地 per-ws socket。
- 结构上消除跨工作空间并行串扰：一个 agent 进程只服务一个工作空间，全局状态不再跨空间共享。
- 本地无网关时退化：`cata chat` 按需拉起 per-ws agent、空闲回收；注册（`cata link add`）的常驻。

## 注册与常驻（cata link）

`~/.cata/link.json`（机器级）：

```json
{
  "gateway_url": "wss://gw.example.com",
  "machine_id": "本机稳定标识（join 生成）",
  "machine_token": "本机逐机器凭证（join 签发）",
  "workspace_root": "/home/user/projects",
  "default_agent_id": "",
  "agents": {
    "ws-xxxx": { "agent_id": "ws-xxxx", "root_path": "/home/user/projects/proj-a", "name": "proj-a", "keep_alive": true, "enabled": true }
  }
}
```

| 命令 | 作用 |
|------|------|
| `cata link join <gateway_url>` | 机器首次接入：join 拿逐机器 token（一次性 code 经网关 UI 批准；无需任何固定口令） |
| `cata link add --dir <path>` | 注册工作空间（join 后）；写 link.json 并拉起 agent + supervisor |
| `cata link remove <agent_id>` | 注销并停止 agent |
| `cata link list` / `cata link status` | 查看注册与运行状态 |
| `cata supervisor` | 每机器守护：启动时确保全部注册 agent 在跑，每 30s 复查补拉；控制 socket `~/.cata/supervisor.sock` |

agent 启动参数：

| 参数 | 说明 |
|------|------|
| `--workspace <ws_id>` | 绑定工作空间（必需） |
| `--idle-timeout <s>` | 无会话空闲回收（默认 300；0 关闭） |
| `--keep-alive` | 常驻（注册到网关的项目，不因空闲退出） |
| `--link` | 额外持有到网关的 WSS 隧道 |

## 隧道协议（cata-tunnel.v1）

- 传输：WebSocket（wss），端点 `GET /cata/v1/tunnel?agent=<agent_id>`。不再要求 `gateway_token`；
  鉴权在 **hello 帧**用逐机器 `machine_token` 完成（网关存 hash，可单台吊销）。
- join 握手：机器侧 `cata link join` 的请求带自定义协议头 `X-Cata-Join: cata-tunnel.v1`，
  网关端在最外层校验——未携带该头的请求（随机扫描/爆破）直接 400 丢弃并记录告警，不进状态机。
- 帧：JSON text message。`stream` 是网关侧分配的递增 id，标识一条「逻辑 socket 连接」。
- `line` 帧的 `data` 为 base64，逐字节透传（不含行尾约定），因此 NDJSON chat 协议无需任何改动。

| 帧 | 方向 | 含义 |
|----|------|------|
| `hello` | worker → gateway | 注册：agent_id / name / root_path / **machine_id** / **machine_token** / protocol / version（必须第一帧） |
| `open` | gateway → worker | 打开一条新 stream |
| `opened` | worker → gateway | stream 已建立（本地 per-ws socket 已拨通） |
| `line` | 双向 | stream 上的原始字节（base64） |
| `close` | 双向 | 关闭 stream |
| `error` | 双向 | stream 错误 |
| `register` | gateway → worker | 命令本机注册新工作空间（`root_path` = 相对该机 `workspace_root` 的子路径） |
| `ping` / `pong` | 双向 | 保活 |
| `detach` | 预留 | — |

单帧上限 8 MiB（`MaxFrameBytes`），超过断开。

### 逐机器 token（两层鉴权）

- **HTTP 握手层**：`Authorization: Bearer <gateway_token>`（网关准入口令，共享），挡掉互联网乱扫。
- **hello 帧层**：`machine_id + machine_token`，网关按 machine_id 查表比对 sha256 hash——**每机器独立 token**，
  单机泄露可单独吊销，不影响其它机器（替代 v1 全网共享 token）。
- token 落盘 `~/.cata/machines.json`（网关侧，0600），**只存 hash 不存明文**。

### join 流程（机器首次接入）

机器侧 `cata link join <gateway_url>`（无需任何固定口令）：

1. 本地发 POST `/cata/v1/join/request {machine_id}`，**携带自定义协议头 `X-Cata-Join: cata-tunnel.v1`**
   → 网关最外层校验该头后发一次性 join code（10 分钟有效，内存态）；
2. 机器进入待批准状态，**已在登录的网关 UI 自动弹出待批准提示**（无需复制 code）；
3. 管理员在 UI 点「批准」→ 网关签发 machine_token（machines.json 存 hash），状态改 approved；
4. 机器轮询 `/cata/v1/join/status?code=xxx` 领取明文 token，写回 link.json；
5. 之后 agent 隧道 hello 带 machine_id + machine_token，网关 hello 层校验通过才注册。

**join 端点防爆破（两层）**：
- **协议头拦截**：`request`/`status` 最外层校验 `X-Cata-Join: cata-tunnel.v1`，未携带/不符的请求
  （随机扫描器、爆破器）**直接 400 丢弃**并记录 IP 告警，连限流/状态机都进不去。
- **IP 限流**：通过协议头校验后套内存态 RateLimiter（60s 窗口最多 10 次，**超限拉黑 10 分钟**，
  返回 429 + Retry-After）。拉黑池为临时态，网关重启清空（攻击者继续刷会再次被拉黑，故不持久化）。

**首次引导**：一台机器首次接入需本机 `cata link join`（保证"有人在这台机器上、且同意接入"）；之后新增工作空间即可远程 register。

### 动态注册工作空间（register 控制帧）

gateway 可经隧道向某机器下发 `register` 帧，让该机器**动态注册一个新工作空间**，无需 ssh 回机器手动 `cata link add`：

1. 机器侧在 `link.json` 配置 `workspace_root`（固定前缀，机器级）；
2. gateway UI 按 `machine_id` 分组，选择目标机器 + 填**项目名**（如 `abc`）或**绝对路径**；
3. gateway 经该机器任一在线 agent 下发 `register{root_path=输入}`；
4. worker 侧解析路径（见下），确保目录存在（不存在则创建），经 supervisor.sock 转交 supervisor 执行 `Add + EnsureAgent`；
5. 新 agent 进程自带 `--link` 回连网关，网关 registry 出现新 agent。

**路径语义**：

| 输入 | 行为 |
|------|------|
| 项目名（相对，如 `abc`） | 拼到 `workspace_root/abc`；目录不存在则 `MkdirAll` 创建 |
| 绝对路径，已存在 | 直接绑定（允许在 workspace_root 之外，用于接入本机已有项目） |
| 绝对路径，不存在 | 必须严格落在 workspace_root 下才创建，否则拒绝 |

**幂等**：目录/工作空间已存在或已注册时，不重复创建、不重复拉 agent。

**自动接入**：supervisor 启动/复查时，扫描 registry 里 kind 为 git/marked 的工作空间，
把尚未注册到 link.json 的自动 Add（keep-alive），使本机已有项目自动接入 gateway，避免逐个手动接入。

**安全边界**：不配置 `workspace_root` 时，worker 拒绝相对名 register（绝对路径仅当目录已存在才可绑定，
不允许创建）。gateway 即使被攻破/误操作，也只能在机器声明的前缀下**新建**工作空间，无法读 `/etc`、
`~/.ssh` 等机器内部内容——agent 的 LLM loop 只在该前缀内跑。

## 网关 remote 模式

`~/.cata/gateway.json`：

```json
{
  "edition": "remote",
  "cata_server": { "mode": "remote" },
  "tunnel_listen": "0.0.0.0:8799",
  "allow_agent_ids": [],
  "default_agent_id": ""
}
```

| 字段 / 环境变量 | 说明 |
|-----------------|------|
| `tunnel_listen` / `CATA_TUNNEL_LISTEN` | 隧道 + agents API 监听，默认 `0.0.0.0:8799` |
| `allow_agent_ids` / `CATA_GATEWAY_ALLOW_AGENTS` | 白名单；空 = 放行所有（仍要求机器 join 后逐机 token） |
| `default_agent_id` / `CATA_GATEWAY_DEFAULT_AGENT` | 通道类会话（telegram/qq）默认路由 agent；空 = 第一个在线 |

- `edition: remote` 或 `cata_server.mode: remote`（或设了 `cata_url`）进入 remote 模式。
- remote 模式**不**拉起本机 cata server；worker 在各机器由 `cata agent --link` 自持隧道。
- 端点：`/cata/v1/tunnel`（WSS 注册）、`/cata/v1/join/*`（机器首次接入，协议头拦截 + IP 限流）。
  `/cata/v1/agents` 与批准（approve）已改为 UI 进程内调用，不再暴露为远端无鉴权 API。

## 会话路由

- **Web UI**：项目 = 当前在线 agent（`/cata/v1/agents` / 注册表）；会话经隧道 `DialAgent(project_id)` 拨到对应 agent 的 per-ws socket。
- **Telegram / QQ**：v1 全部会话拨到 `default_agent_id`（或第一个在线 agent），cwd 用该 agent 的 `root_path`（history 仍 per-会话）。
- **本地模式不变**：仍走本机 Unix socket（`socket_path` / `CATA_SOCKET`）。

## 安全

- 鉴权：hello 层 `machine_token`（逐机器，hash 存储、可单独吊销）；join 授权靠一次性 code + 管理员 UI 批准。
- join 端点最外层校验自定义协议头 `X-Cata-Join: cata-tunnel.v1`，未携带（扫描/爆破）直接 400 + 告警；通过后 IP 限流拉黑 10 分钟。
- token 只存 sha256 hash（`~/.cata/machines.json`，0600），不落明文。
- `allow_agent_ids` 白名单（可选）。
- 隧道端点 `CheckOrigin` 放行（可能经 nginx/caddy 反代）；建议网关部署走 HTTPS。

## 与旧模式的关系

| | 旧（模式一，同机） | 新（remote） |
|--|--------------------|--------------|
| 连接 | 本机 Unix socket `~/.cata/cata.sock` | WSS 隧道 + 每工作空间 per-ws socket |
| worker | 单 server 多空间（曾有串扰） | 每工作空间一个 agent 进程（结构隔离） |
| gateway | 本机渠道适配 | 云端注册中心 + 路由 |
| 协议 | NDJSON over Unix socket | 同 NDJSON，经隧道逐字节透传 |

本地 `cata chat` 现按工作空间拉起 per-ws agent（`~/.cata/sockets/<ws_id>.sock`）；未注册工作空间按需拉起、空闲回收，
注册（`cata link add`）的常驻并持有隧道。
