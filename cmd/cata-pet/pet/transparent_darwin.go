//go:build darwin

package pet

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

static void petClearWebViews(NSView *v) {
	if ([v isKindOfClass:[WKWebView class]]) {
		WKWebView *wv = (WKWebView *)v;
		[wv setValue:@(NO) forKey:@"drawsBackground"];
		if (@available(macOS 12.0, *)) {
			wv.underPageBackgroundColor = [NSColor clearColor];
		}
	}
	for (NSView *child in v.subviews) {
		petClearWebViews(child);
	}
}

void petEnsureClearWindow(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		for (NSWindow *w in [NSApp windows]) {
			if (![w isVisible]) {
				continue;
			}
			[w setOpaque:NO];
			[w setBackgroundColor:[NSColor clearColor]];
			[w setHasShadow:NO];
			NSArray<NSView *> *subs = [w.contentView.subviews copy];
			for (NSView *sub in subs) {
				if ([sub isKindOfClass:[NSVisualEffectView class]]) {
					[sub removeFromSuperview];
				}
			}
			NSView *content = w.contentView;
			if (content != nil) {
				[content setWantsLayer:YES];
				content.layer.backgroundColor = [[NSColor clearColor] CGColor];
				petClearWebViews(content);
			}
		}
	});
}
*/
import "C"

func ensureClearWindow() {
	C.petEnsureClearWindow()
}
