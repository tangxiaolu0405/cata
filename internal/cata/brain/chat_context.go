package brain

import "context"

// ChatContext 一次 chat 请求的显式上下文：脑子分区、产出区、运行环境、prompt 档位。
// 多 cata 并行时勿依赖全局 SetActive/SetOutputCwd/SetRuntimeEnv/SetPromptProfile；
// server 在每轮 chat 入口构造 ChatContext 并注入 ctx，工具与 LLM 注入链从 ctx 取用。
type ChatContext struct {
	// WS 本轮 chat 解析出的脑子分区（勿用 brain.Active()，后台 evolve 会临时改写全局 Active）。
	WS *Workspace
	// OutputCwd 本轮产出区（run_command / 文件工具基准）。
	OutputCwd string
	// Runtime 客户端所在 OS/终端（注入 LLM 生成命令）。
	Runtime *RuntimeEnv
	// Profile 本轮 system 注入档位（主 chat 首轮从 task 起）。
	Profile PromptProfile
}

type chatContextKey struct{}

// WithChatContext 将 ChatContext 注入 ctx。
func WithChatContext(ctx context.Context, cc *ChatContext) context.Context {
	if ctx == nil || cc == nil {
		return ctx
	}
	return context.WithValue(ctx, chatContextKey{}, cc)
}

// ChatContextFrom 返回 ctx 中的 ChatContext；未注入时回退全局（兼容非 chat / 测试调用）。
func ChatContextFrom(ctx context.Context) *ChatContext {
	if ctx != nil {
		if cc, ok := ctx.Value(chatContextKey{}).(*ChatContext); ok && cc != nil {
			return cc
		}
	}
	return &ChatContext{
		WS:        Active(),
		OutputCwd: OutputCwd(),
		Runtime:   ActiveRuntimeEnv(),
		Profile:   ActivePromptProfile(),
	}
}
