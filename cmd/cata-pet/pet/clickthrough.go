package pet

import "sync"

// ClickThroughController toggles OS-level mouse ignore for transparent areas.
type ClickThroughController interface {
	SetIgnoreMouse(ignore bool) error
	StartMouseWatch()
}

// NewClickThrough returns a platform controller (may be a no-op stub).
func NewClickThrough() ClickThroughController {
	return newClickThrough()
}

var (
	ignoreMu     sync.Mutex
	ignoreActive bool
	forceSolid   bool // expanded panel / busy: never click-through
)

// SetForceSolid locks the window as interactive (no click-through).
func (a *App) SetForceSolid(on bool) {
	ignoreMu.Lock()
	forceSolid = on
	ignoreMu.Unlock()
	if on {
		_ = a.ct.SetIgnoreMouse(false)
		setIgnoreCached(false)
	}
}

func setIgnoreCached(ignore bool) {
	ignoreMu.Lock()
	ignoreActive = ignore
	ignoreMu.Unlock()
}

func getIgnoreState() (ignore, force bool) {
	ignoreMu.Lock()
	defer ignoreMu.Unlock()
	return ignoreActive, forceSolid
}
