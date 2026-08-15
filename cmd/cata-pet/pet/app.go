package pet

import (
	"context"
	"fmt"
	"math"
	"sync"

	"cata/internal/cata/config"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails-bound desktop pet application.
type App struct {
	ctx      context.Context
	conn     *Conn
	ct       ClickThroughController
	settings Settings

	mu      sync.Mutex
	started bool
}

// NewApp constructs the pet app.
func NewApp() *App {
	s := LoadSettings()
	return &App{
		conn:     NewConn(s.Cwd),
		ct:       NewClickThrough(),
		settings: s,
	}
}

// Startup is called by Wails on launch.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	bindMouseWatch(a)
	_ = config.InitBrainPath()
	if err := EnsureServer(); err != nil {
		a.emit("pet:error", err.Error())
	} else {
		a.emit("pet:mood", "idle")
	}
	a.SetAlwaysOnTop(a.settings.AlwaysOnTop)
	ensureClearWindow()
	// Default: click-through; global mouse watch re-enables over hit regions.
	_ = a.ct.SetIgnoreMouse(true)
	setIgnoreCached(true)
	a.ct.StartMouseWatch()
	a.started = true
}

// DomReady notifies frontend that backend is ready.
func (a *App) DomReady(ctx context.Context) {
	a.ctx = ctx
	ensureClearWindow()
	a.emit("pet:ready", a.Status())
}

func (a *App) emit(event string, data any) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, event, data)
}

// Emit implements StreamEmitter.
func (a *App) Emit(event string, data any) {
	a.emit(event, data)
}

// Status returns current pet status for the UI.
func (a *App) Status() map[string]any {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	return map[string]any{
		"cwd":           a.conn.Cwd(),
		"always_on_top": s.AlwaysOnTop,
		"busy":          a.conn.Busy(),
		"socket":        config.ResolvedSocketPath(),
	}
}

// Send starts a chat turn with the given text.
func (a *App) Send(text string) error {
	if err := EnsureServer(); err != nil {
		return err
	}
	_ = a.ct.SetIgnoreMouse(false)
	setIgnoreCached(false)
	return a.conn.Chat(text, a)
}

// RespondExec approves or denies a pending command.
func (a *App) RespondExec(confirmID string, approved bool) error {
	return a.conn.RespondExec(confirmID, approved)
}

// RespondChoice answers a user_choice prompt.
func (a *App) RespondChoice(choiceID string, selected []string) error {
	return a.conn.RespondChoice(choiceID, selected)
}

// Cancel aborts the current stream.
func (a *App) Cancel() {
	a.conn.Cancel()
}

// SetCwd updates the output directory and persists settings.
func (a *App) SetCwd(cwd string) error {
	if cwd == "" {
		return fmt.Errorf("cwd required")
	}
	a.conn.SetCwd(cwd)
	a.mu.Lock()
	a.settings.Cwd = cwd
	s := a.settings
	a.mu.Unlock()
	return SaveSettings(s)
}

// GetCwd returns the current output cwd.
func (a *App) GetCwd() string {
	return a.conn.Cwd()
}

// SetAlwaysOnTop toggles window always-on-top and persists.
func (a *App) SetAlwaysOnTop(on bool) {
	a.mu.Lock()
	a.settings.AlwaysOnTop = on
	s := a.settings
	a.mu.Unlock()
	_ = SaveSettings(s)
	if a.ctx != nil {
		runtime.WindowSetAlwaysOnTop(a.ctx, on)
	}
}

// GetAlwaysOnTop returns the always-on-top preference.
func (a *App) GetAlwaysOnTop() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings.AlwaysOnTop
}

// SetClickThrough enables OS click-through (transparent areas).
// Prefer SetHitRegions + global mouse watch; this remains for force-solid paths.
func (a *App) SetClickThrough(ignore bool) error {
	if ignore {
		a.SetForceSolid(false)
	}
	setIgnoreCached(ignore)
	return a.ct.SetIgnoreMouse(ignore)
}

// MoveWindowBy offsets the window (used for click-vs-drag on the cat).
// dx/dy are float64 because JS pointer events often send fractional screen coords (Retina).
func (a *App) MoveWindowBy(dx, dy float64) {
	if a.ctx == nil {
		return
	}
	idx := int(math.Round(dx))
	idy := int(math.Round(dy))
	if idx == 0 && idy == 0 {
		return
	}
	x, y := runtime.WindowGetPosition(a.ctx)
	runtime.WindowSetPosition(a.ctx, x+idx, y+idy)
}

// ResizeWindow sets the outer window size (collapse vs expand).
func (a *App) ResizeWindow(width, height float64) {
	if a.ctx == nil {
		return
	}
	w := int(math.Round(width))
	h := int(math.Round(height))
	if w < 160 {
		w = 160
	}
	if h < 180 {
		h = 180
	}
	runtime.WindowSetSize(a.ctx, w, h)
}
