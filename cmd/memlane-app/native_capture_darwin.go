//go:build e2e && darwin && cgo

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit
#include <stdint.h>
#include <stdlib.h>
#include <dlfcn.h>
#import <AppKit/AppKit.h>

// Request only the NSWindow owned by this process. Never enumerate/capture
// other windows, the desktop or a display, and never request broader access.
static int memlane_capture_window(uintptr_t handle, const char *path) {
 @autoreleasepool {
  NSWindow *window = (NSWindow *)handle;
  if (!window || !window.visible || window.miniaturized) return 1;
  if (window.windowNumber <= 0) return 2;
  typedef CGImageRef (*OwnWindowImage)(CGRect, CGWindowListOption, CGWindowID, CGWindowImageOption);
  OwnWindowImage capture = (OwnWindowImage)dlsym(RTLD_DEFAULT, "CGWindowListCreateImage");
  if (!capture) return 6;
  [window displayIfNeeded];
  CGImageRef image = capture(CGRectNull, kCGWindowListOptionIncludingWindow,
    (CGWindowID)window.windowNumber, kCGWindowImageBoundsIgnoreFraming);
  if (!image) return 7;
  NSBitmapImageRep *bitmap = [[[NSBitmapImageRep alloc] initWithCGImage:image] autorelease];
  CGImageRelease(image);
  if (!bitmap) return 3;
  NSData *png = [bitmap representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
  if (!png) return 4;
  NSError *error = nil;
  return [png writeToFile:[NSString stringWithUTF8String:path] options:NSDataWritingWithoutOverwriting error:&error] ? 0 : 5;
 }
}
*/
import "C"

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"github.com/egoist/mygo"
)

func appendNativeCaptureMenu(items []*mygo.MenuItem, window func() *mygo.Window) []*mygo.MenuItem {
	directory := os.Getenv("MEMLANE_NATIVE_CAPTURE_DIR")
	if directory == "" || !filepath.IsAbs(directory) {
		return items
	}
	// This requires an explicitly marked verification destination. The default
	// client has neither this e2e code nor a capture menu item.
	marker, err := os.ReadFile(filepath.Join(filepath.Dir(directory), "source-info.json"))
	var info struct {
		SyntheticOnly bool `json:"synthetic_only"`
	}
	if err != nil || json.Unmarshal(marker, &info) != nil || !info.SyntheticOnly {
		return items
	}
	return append(items, mygo.Separator(), &mygo.MenuItem{Label: "保存当前窗口验证截图", Click: func(*mygo.MenuItem, *mygo.Window) {
		win := window()
		if win == nil {
			return
		}
		if err := os.MkdirAll(directory, 0700); err != nil {
			log.Printf("验证截图目录不可写：%v", err)
			return
		}
		stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
		path := filepath.Join(directory, stamp+".png")
		name := C.CString(path)
		defer C.free(unsafe.Pointer(name))
		code := C.memlane_capture_window(C.uintptr_t(win.NativeHandle()), name)
		if code != 0 {
			log.Printf("AppKit窗口截图未完成：%d", int(code))
			return
		}
		receipt := map[string]any{"synthetic_only": true, "capture": "CoreGraphics image of this process's own NSWindow only", "fixture_renderer": false, "system_window_chrome": true, "image": filepath.Base(path), "window_bounds": win.Bounds(), "captured_at": time.Now().UTC().Format(time.RFC3339Nano)}
		data, err := json.MarshalIndent(receipt, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(directory, stamp+".json"), append(data, '\n'), 0600)
		}
		if err != nil {
			log.Printf("截图回执未保存：%v", err)
		} else {
			log.Printf("AppKit验证截图已保存：%s", filepath.Base(path))
		}
	}})
}
