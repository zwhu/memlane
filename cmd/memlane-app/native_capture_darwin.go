//go:build e2e && darwin && cgo

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <dlfcn.h>
#import <AppKit/AppKit.h>

static char *memlane_copy_probe_text(void) {
 @autoreleasepool {
  NSString *text = [[NSPasteboard generalPasteboard] stringForType:NSPasteboardTypeString];
  return text ? strdup([text UTF8String]) : NULL;
 }
}

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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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
	items = append(items, mygo.Separator(), &mygo.MenuItem{Label: "保存当前窗口验证截图", Click: func(*mygo.MenuItem, *mygo.Window) {
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
	for _, probe := range []struct{ kind, label string }{{"question", "验证用户原文复制"}, {"answer", "验证Agent原文复制"}, {"loaded", "验证已载入内容复制"}, {"first-code", "验证首段代码复制"}, {"body-selection", "验证跨消息文字选择"}} {
		items = append(items, &mygo.MenuItem{Label: probe.label, Click: func(*mygo.MenuItem, *mygo.Window) {
			if nativeCopyExpected == nil {
				return
			}
			expected, ok := nativeCopyExpected(probe.kind)
			if !ok {
				return
			}
			value := C.memlane_copy_probe_text()
			if value == nil {
				return
			}
			actual := C.GoString(value)
			C.free(unsafe.Pointer(value))
			r := map[string]any{"synthetic_only": true, "kind": probe.kind, "actual_system_pasteboard": true, "matches_original": actual == expected, "actual_bytes": len(actual), "expected_bytes": len(expected), "actual_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(actual))), "expected_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(expected)))}
			if probe.kind == "body-selection" {
				markers := strings.SplitN(expected, "\x00", 2)
				delete(r, "matches_original")
				delete(r, "expected_bytes")
				delete(r, "expected_sha256")
				r["selection_covers_question_and_answer"] = len(markers) == 2 && strings.Contains(actual, markers[0]) && strings.Contains(actual, markers[1])
				r["excludes_fixed_header"] = !strings.Contains(actual, "仅显示已采集并由官方保留的问答与工具记录。")
			}
			if err := os.MkdirAll(directory, 0700); err != nil {
				return
			}
			data, err := json.MarshalIndent(r, "", "  ")
			if err != nil {
				return
			}
			path := filepath.Join(directory, "clipboard-"+probe.kind+"-"+time.Now().UTC().Format("20060102T150405.000000000Z")+".json")
			if err = os.WriteFile(path, data, 0600); err != nil {
				log.Printf("复制验证回执未保存")
			}
		}})
	}
	var capturing bool
	return append(items, &mygo.MenuItem{Label: "连续记录阅读窗口31秒", Click: func(*mygo.MenuItem, *mygo.Window) {
		if capturing || nativeCaptureDiagnostics == nil {
			return
		}
		win := window()
		if win == nil {
			return
		}
		dir := filepath.Join(directory, "continuous-"+time.Now().UTC().Format("20060102T150405.000000000Z"))
		if err := os.MkdirAll(dir, 0700); err != nil {
			return
		}
		capturing = true
		started := time.Now()
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			frames := []map[string]any{}
			for range ticker.C {
				finished := false
				mygo.RunOnMain(func() {
					elapsed := time.Since(started)
					if elapsed >= 31*time.Second {
						finished = true
						capturing = false
						return
					}
					path := filepath.Join(dir, fmt.Sprintf("frame-%04d.png", len(frames)))
					name := C.CString(path)
					code := C.memlane_capture_window(C.uintptr_t(win.NativeHandle()), name)
					C.free(unsafe.Pointer(name))
					frames = append(frames, map[string]any{"elapsed_ms": elapsed.Milliseconds(), "image": filepath.Base(path), "capture_code": int(code), "state": nativeCaptureDiagnostics(), "bounds": win.Bounds()})
				})
				if finished {
					break
				}
			}
			data, err := json.MarshalIndent(map[string]any{"synthetic_only": true, "duration_ms": time.Since(started).Milliseconds(), "frames": frames}, "", "  ")
			if err == nil {
				err = os.WriteFile(filepath.Join(dir, "frames.json"), data, 0600)
			}
			if err != nil {
				log.Printf("连续阅读窗口回执保存失败")
			} else {
				log.Printf("连续阅读窗口记录完成：%d帧", len(frames))
			}
		}()
	}})
}
