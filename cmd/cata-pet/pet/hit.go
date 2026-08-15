package pet

import "sync"

// HitRect is a window-content rectangle in CSS pixels (origin top-left of webview).
type HitRect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

var (
	hitMu    sync.RWMutex
	hitRects []HitRect
)

// SetHitRegions updates interactive regions for click-through hit testing.
func (a *App) SetHitRegions(rects []HitRect) {
	hitMu.Lock()
	hitRects = append([]HitRect(nil), rects...)
	hitMu.Unlock()
}

func copyHitRects() []HitRect {
	hitMu.RLock()
	defer hitMu.RUnlock()
	out := make([]HitRect, len(hitRects))
	copy(out, hitRects)
	return out
}

func pointInAnyHit(px, py float64, rects []HitRect) bool {
	for _, r := range rects {
		if px >= r.X && px <= r.X+r.W && py >= r.Y && py <= r.Y+r.H {
			return true
		}
	}
	return false
}
