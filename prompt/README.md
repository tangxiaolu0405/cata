# Cata 提示词（prompt/）

HTTP 出站前组装的 **LLM 专用提示词**（与 `brain/` 下注入 chat 的 global 文档不同）。

## 目录

| 路径 | 用途 |
|------|------|
| `evolve/system.md` | 常规自主演进 system |
| `evolve/session_compress_extra.md` | 会话压缩时追加在 system 后 |
| `evolve/crystallize.md` | 固化 skill 专用 system |
| `evolve/decision_scope.md` | 决策 user prompt 中的 workspace 隔离提醒 |
| `evolve/decision_footer.md` | 决策 user prompt 结尾 |

`buildDecisionPrompt` 的动态部分（triggers、state JSON、short-term 节选等）仍在 `internal/evolve/engine.go` 组装。

## 修改方式

1. 编辑本目录下 `.md` 文件
2. 重新编译：`go build -o cata ./cmd/cata`

提示词在 **编译时 embed** 进二进制；改文件后不 build 不会生效。

## 代码入口

```go
import "cata/prompt"

prompt.EvolveSystemPrompt()
prompt.EvolveSessionCompressPrompt()
prompt.EvolveCrystallizePrompt()
prompt.Load("evolve/system.md")
```

## 其它提示词（未在此目录）

| 部分 | 位置 |
|------|------|
| boot-leader | `~/.cata/global/boot-assembler.md` 或 `brain/boot-assembler.md` |
| global 约束/行为（chat 注入） | `~/.cata/global/constraints.md`、`behavior.md` |
| brain 节选 | workspace 格子内 markdown，由 `internal/brain/terminal_context.go` 读取 |
