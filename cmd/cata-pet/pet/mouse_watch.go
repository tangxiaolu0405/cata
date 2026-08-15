package pet

// onGlobalMouseScreen is called from platform mouse monitors (screen coords).
func onGlobalMouseScreen(sx, sy float64) {
	a := mouseWatchApp
	if a == nil || a.ct == nil {
		return
	}
	_, force := getIgnoreState()
	if force {
		return
	}

	lx, ly, inWin := platformScreenToContent(sx, sy)
	wantIgnore := true
	if inWin {
		rects := copyHitRects()
		if len(rects) > 0 && pointInAnyHit(lx, ly, rects) {
			wantIgnore = false
		}
	}

	cur, _ := getIgnoreState()
	if cur == wantIgnore {
		return
	}
	_ = a.ct.SetIgnoreMouse(wantIgnore)
	setIgnoreCached(wantIgnore)
}

var mouseWatchApp *App

func bindMouseWatch(a *App) {
	mouseWatchApp = a
}
