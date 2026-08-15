//go:build !darwin && !windows

package pet

type stubClickThrough struct{}

func newClickThrough() ClickThroughController {
	return &stubClickThrough{}
}

func (s *stubClickThrough) SetIgnoreMouse(ignore bool) error {
	_ = ignore
	// Linux: full per-pixel click-through needs compositor-specific APIs;
	// rely on compact window size when collapsed + CSS pointer-events for MVP.
	return nil
}

func (s *stubClickThrough) StartMouseWatch() {}

func platformScreenToContent(sx, sy float64) (float64, float64, bool) {
	_, _ = sx, sy
	return 0, 0, false
}
