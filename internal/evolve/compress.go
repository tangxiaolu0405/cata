package evolve

import (
	"context"
	"fmt"

	"cata/internal/brain"
	"cata/internal/config"
)

// sessionCompressMinShortBytes 会话触发压缩时 short-term 至少字节数。
const sessionCompressMinShortBytes = 256

// RunSessionCompress 对话轮次达到阈值后触发一轮演进（consolidate short → persona），跳过周期门控。
func RunSessionCompress(ctx context.Context) error {
	if config.Config == nil || !config.Config.LLM.Enabled {
		return nil
	}
	if !config.Config.Evolution.Enabled {
		return nil
	}
	ws, err := brain.MustActive()
	if err != nil {
		return fmt.Errorf("active workspace: %w", err)
	}
	e := NewEngine(cycleInterval())
	if err := e.runCycle(ctx, ws, true, false); err != nil {
		return err
	}
	return RunCrystallize(ctx)
}
