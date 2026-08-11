package server

import "context"

type scheduledRunKey struct{}

// WithScheduledRun 标记 ctx 为定时任务运行（强制 full 工具档、跳过任务状态机）。
// 由调度框架以真实客户端协议发起（Request.RunAs="scheduled"）时设置。
func WithScheduledRun(ctx context.Context) context.Context {
	return context.WithValue(ctx, scheduledRunKey{}, true)
}

// IsScheduledRun 返回 ctx 是否标记为定时任务运行。
func IsScheduledRun(ctx context.Context) bool {
	v, _ := ctx.Value(scheduledRunKey{}).(bool)
	return v
}
