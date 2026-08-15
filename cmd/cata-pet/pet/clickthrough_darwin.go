//go:build darwin

package pet

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

void petSetIgnoreMouse(int ignore) {
	dispatch_async(dispatch_get_main_queue(), ^{
		for (NSWindow *w in [NSApp windows]) {
			if (![w isVisible]) {
				continue;
			}
			[w setIgnoresMouseEvents:(ignore ? YES : NO)];
			[w setAcceptsMouseMovedEvents:YES];
			[w setOpaque:NO];
			[w setBackgroundColor:[NSColor clearColor]];
			[w setHasShadow:NO];
		}
	});
}

void petMouseLocation(double *outX, double *outY) {
	NSPoint screen = [NSEvent mouseLocation];
	*outX = (double)screen.x;
	*outY = (double)screen.y;
}

int petScreenPointInWindow(double sx, double sy, double *outX, double *outY) {
	__block int ok = 0;
	void (^work)(void) = ^{
		NSPoint screen = NSMakePoint(sx, sy);
		for (NSWindow *w in [NSApp windows]) {
			if (![w isVisible]) {
				continue;
			}
			NSRect frame = [w frame];
			if (!NSPointInRect(screen, frame)) {
				continue;
			}
			NSRect content = [w contentRectForFrameRect:frame];
			CGFloat localX = screen.x - content.origin.x;
			CGFloat localYFromBottom = screen.y - content.origin.y;
			CGFloat localY = content.size.height - localYFromBottom;
			*outX = (double)localX;
			*outY = (double)localY;
			ok = 1;
			break;
		}
	};
	if ([NSThread isMainThread]) {
		work();
	} else {
		dispatch_sync(dispatch_get_main_queue(), work);
	}
	return ok;
}
*/
import "C"
import (
	"sync"
	"time"
)

type darwinClickThrough struct {
	once sync.Once
}

func newClickThrough() ClickThroughController {
	return &darwinClickThrough{}
}

func (d *darwinClickThrough) SetIgnoreMouse(ignore bool) error {
	v := C.int(0)
	if ignore {
		v = 1
	}
	C.petSetIgnoreMouse(v)
	return nil
}

func (d *darwinClickThrough) StartMouseWatch() {
	d.once.Do(func() {
		go func() {
			// 降频到 100ms：20ms 的 50Hz dispatch_sync 会持续打断主线程渲染/事件循环，
			// 且主线程忙时阻塞轮询 goroutine（极端有死锁风险）。
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for range ticker.C {
				var sx, sy C.double
				C.petMouseLocation(&sx, &sy)
				onGlobalMouseScreen(float64(sx), float64(sy))
			}
		}()
	})
}

func platformScreenToContent(sx, sy float64) (lx, ly float64, ok bool) {
	var ox, oy C.double
	if C.petScreenPointInWindow(C.double(sx), C.double(sy), &ox, &oy) == 0 {
		return 0, 0, false
	}
	return float64(ox), float64(oy), true
}
