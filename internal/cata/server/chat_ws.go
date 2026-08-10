package server

import (
	"context"

	"cata/internal/cata/brain"
)

// chatWorkspaceKey 把本轮 chat 解析出的脑子分区放进 ctx，工具执行时从 ctx 取用，
// 避免依赖全局 brain.Active()（后台 evolve 会临时改写全局 Active，多 workspace 会串）。
type chatWorkspaceKey struct{}

func withChatWorkspace(ctx context.Context, ws *brain.Workspace) context.Context {
	return context.WithValue(ctx, chatWorkspaceKey{}, ws)
}

// chatWorkspaceFrom 返回本轮 chat 的脑子分区；未注入时返回 nil。
func chatWorkspaceFrom(ctx context.Context) *brain.Workspace {
	ws, _ := ctx.Value(chatWorkspaceKey{}).(*brain.Workspace)
	return ws
}
