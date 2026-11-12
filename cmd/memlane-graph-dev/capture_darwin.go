//go:build memlane_graph_e2e && darwin && cgo

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit
#include <stdint.h>
#include <stdlib.h>
#include <dlfcn.h>
#import <AppKit/AppKit.h>
static int capture_graph_window(uintptr_t handle, const char *path) {
 @autoreleasepool {
  NSWindow *window = (NSWindow *)handle;
  if (!window || !window.visible || window.miniaturized || window.windowNumber <= 0) return 1;
  typedef CGImageRef (*OwnImage)(CGRect, CGWindowListOption, CGWindowID, CGWindowImageOption);
  OwnImage capture = (OwnImage)dlsym(RTLD_DEFAULT, "CGWindowListCreateImage");
  if (!capture) return 2;
  [window displayIfNeeded];
  CGImageRef image = capture(CGRectNull, kCGWindowListOptionIncludingWindow,
    (CGWindowID)window.windowNumber, kCGWindowImageBoundsIgnoreFraming);
  if (!image) return 3;
  NSBitmapImageRep *bitmap = [[[NSBitmapImageRep alloc] initWithCGImage:image] autorelease];
  CGImageRelease(image);
  NSData *png = [bitmap representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
  if (!png) return 4;
  return [png writeToFile:[NSString stringWithUTF8String:path] options:NSDataWritingWithoutOverwriting error:nil] ? 0 : 5;
 }
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Called on the UI thread and captures only this process's supplied NSWindow.
func captureSystemWindow(handle uintptr, path string) error {
	name := C.CString(path)
	defer C.free(unsafe.Pointer(name))
	if code := C.capture_graph_window(C.uintptr_t(handle), name); code != 0 {
		return fmt.Errorf("own window capture unavailable: %d", int(code))
	}
	return nil
}
