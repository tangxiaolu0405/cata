package evolve

import (
	"context"
	"fmt"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

// sessionCompressMinShortBytes 会话触发压缩时 short-term 至少字节数。
const sessionCompressMinShortBytes = 256

// RunSessionCompress 对话轮次达到阈值后触发一轮演进（consolidate short → persona），跳过周期门控。
// ws 可显式指定脑子分区（多 cata 并行勿依赖全局 MustActive）；nil 时回退全局。
func RunSessionCompress(ctx context.Context, ws *brain.Workspace) error {
	if config.Config == nil || !config.Config.LLM.Enabled {
		return nil
	}
	if !config.Config.Evolution.Enabled {
		return nil
	}
	var err error
	if ws == nil {
		ws, err = brain.MustActive()
		if err != nil {
			return fmt.Errorf("active workspace: %w", err)
		}
	}
	e := NewEngine(cycleInterval())
	if err := e.runCycle(ctx, ws, true, false); err != nil {
		return err
	}
	return RunCrystallize(ctx, ws)
}
