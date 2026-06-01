package evolve

import (
	"time"

	"cata/internal/config"
)

func cycleInterval() time.Duration {
	if config.Config != nil && config.Config.Evolution.CycleInterval > 0 {
		return time.Duration(config.Config.Evolution.CycleInterval) * time.Second
	}
	return DefaultCycleSeconds * time.Second
}
