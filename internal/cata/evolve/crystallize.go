package evolve

import (
	"context"
	"fmt"
	"log"
	"strings"

	"cata/internal/cata/brain"
	"cata/internal/cata/config"
)

// RunCrystallize 高 token / 重复任务后尝试将探索固化为脑子内 skill（不修改 mcp）。
// ws 必须显式指定（多 cata 并行勿依赖全局 MustActive，后台 evolve 会改写全局）。
func RunCrystallize(ctx context.Context, ws *brain.Workspace) error {
	if config.Config == nil || !config.Config.LLM.Enabled || !config.Config.Evolution.Enabled {
		return nil
	}
	if ws == nil {
		return fmt.Errorf("RunCrystallize: workspace required (parallel-safe: pass explicit ws)")
	}
	return SharedEngine().runCycle(ctx, ws, false, true)
}

func ingestCrystallizedSkills(ws *brain.Workspace, touched []string) {
	seen := make(map[string]bool)
	for _, rel := range touched {
		id := brain.ParseSkillIDFromRel(rel)
		if id == "" || id == ".failed" || strings.HasPrefix(id, ".") || seen[id] {
			continue
		}
		seen[id] = true
		res := brain.EnableAndVerifySkill(context.Background(), ws, id)
		if res.OK {
			log.Printf("crystallize: enabled+verified skill %q (mode=%s)", id, res.Mode)
			continue
		}
		log.Printf("crystallize: skill %q verify failed (%s): %s — quarantined for rewrite", id, res.Mode, res.Err)
	}
}
