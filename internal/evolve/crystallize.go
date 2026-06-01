package evolve

import (
	"context"
	"fmt"
	"log"

	"cata/internal/brain"
	"cata/internal/config"
)

// RunCrystallize 高 token / 重复任务后尝试将探索固化为脑子内 skill（不修改 mcp）。
func RunCrystallize(ctx context.Context) error {
	if config.Config == nil || !config.Config.LLM.Enabled || !config.Config.Evolution.Enabled {
		return nil
	}
	ws, err := brain.MustActive()
	if err != nil {
		return fmt.Errorf("active workspace: %w", err)
	}
	return NewEngine(cycleInterval()).runCycle(ctx, ws, false, true)
}

func ingestCrystallizedSkills(ws *brain.Workspace, touched []string) {
	seen := make(map[string]bool)
	for _, rel := range touched {
		id := brain.ParseSkillIDFromRel(rel)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if err := brain.AppendSkillToCapabilities(ws, id); err != nil {
			log.Printf("crystallize: append skill %q to capabilities: %v", id, err)
		} else {
			log.Printf("crystallize: enabled skill %q in capabilities.yaml", id)
		}
	}
}
