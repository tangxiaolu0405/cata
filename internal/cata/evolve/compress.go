package evolve

import (
	"context"
	"fmt"
	"log"
	"time"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

// sessionCompressMinShortBytes 会话触发压缩时 short-term 至少字节数。
const sessionCompressMinShortBytes = 256

// RunSessionCompress 对话轮次达到阈值后触发一轮演进（consolidate short → persona），跳过周期门控。
// ws 必须显式指定（多 cata 并行勿依赖全局 MustActive，后台 evolve 会改写全局）。
// 复用进程内共享引擎实例（指纹/冷却与后台 ticker 共享，runCycle 互斥串行）。
func RunSessionCompress(ctx context.Context, ws *brain.Workspace) error {
	if config.Config == nil || !config.Config.LLM.Enabled {
		return nil
	}
	if !config.Config.Evolution.Enabled {
		return nil
	}
	if ws == nil {
		return fmt.Errorf("RunSessionCompress: workspace required (parallel-safe: pass explicit ws)")
	}
	e := SharedEngine()
	if err := e.runCycle(ctx, ws, true, false); err != nil {
		return err
	}
	return RunCrystallize(ctx, ws)
}

// TriggerSessionCompressAsync 异步触发会话压缩（不阻塞 chat 轮次）。
// chat 轮次在触发后立即用已裁剪的 history 继续回复；consolidate 在后台排队执行，
// 与后台 ticker 演进经引擎 cycleMu 互斥，避免重复提炼与并发写同一文件。
// ctx 仅用于读取即时状态；实际 consolidate 使用独立的带超时后台 ctx。
func TriggerSessionCompressAsync(ctx context.Context, ws *brain.Workspace) {
	if ws == nil {
		return
	}
	if config.Config == nil || !config.Config.LLM.Enabled || !config.Config.Evolution.Enabled {
		return
	}
	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), sessionCompressAsyncTimeout)
		defer cancel()
		if err := RunSessionCompress(runCtx, ws); err != nil {
			log.Printf("session compress (async) [%s]: %v", ws.ID, err)
		}
	}()
}

// sessionCompressAsyncTimeout 后台压缩整段执行的超时上限。
const sessionCompressAsyncTimeout = 10 * time.Minute
