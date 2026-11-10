//go:build e2e && darwin

package main

import (
	"encoding/json"
	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
	"github.com/zwhu/memlane/internal/desktop"
	"net"
	"os"
)

// This opt-in local probe exercises actual AppKit objects, without requesting
// machine-wide event injection, changing input sources or claiming VoiceOver.
func configureNativeProbe(win *mygo.Window, app *desktop.App) {
	path := os.Getenv("MEMLANE_NATIVE_PROBE")
	if path == "" {
		return
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		panic(err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		panic(err)
	}
	mygo.App.OnQuit(func() { listener.Close(); os.Remove(path) })
	lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}
	send, err := purego.Dlsym(lib, "objc_msgSend")
	if err != nil {
		panic(err)
	}
	var text func(objc.ID, objc.SEL) string
	purego.RegisterFunc(&text, send)
	type nativeRange struct{ Location, Length uintptr }
	var getRange func(objc.ID, objc.SEL) nativeRange
	purego.RegisterFunc(&getRange, send)
	str := func(id objc.ID) string {
		if id == 0 {
			return ""
		}
		return text(id, objc.RegisterName("UTF8String"))
	}
	sel := objc.RegisterName
	var instanceMethod func(objc.Class, objc.SEL) uintptr
	var implementation func(uintptr) uintptr
	var replaceImplementation func(uintptr, uintptr) uintptr
	purego.RegisterLibFunc(&instanceMethod, lib, "class_getInstanceMethod")
	purego.RegisterLibFunc(&implementation, lib, "method_getImplementation")
	purego.RegisterLibFunc(&replaceImplementation, lib, "method_setImplementation")
	inputCalls := map[string]int{}
	viewClass := objc.GetClass("MyGoSurfaceView")
	for _, name := range []string{"keyDown:", "insertText:replacementRange:", "setMarkedText:selectedRange:replacementRange:"} {
		method := instanceMethod(viewClass, sel(name))
		if method == 0 {
			continue
		}
		switch name {
		case "keyDown:":
			var original func(objc.ID, objc.SEL, objc.ID)
			purego.RegisterFunc(&original, implementation(method))
			replaceImplementation(method, uintptr(objc.NewIMP(func(self objc.ID, cmd objc.SEL, ev objc.ID) {
				inputCalls["keyDown"]++
				original(self, cmd, ev)
			})))
		case "insertText:replacementRange:":
			var original func(objc.ID, objc.SEL, objc.ID, nativeRange)
			purego.RegisterFunc(&original, implementation(method))
			replaceImplementation(method, uintptr(objc.NewIMP(func(self objc.ID, cmd objc.SEL, value objc.ID, r nativeRange) {
				inputCalls["insertText"]++
				original(self, cmd, value, r)
			})))
		case "setMarkedText:selectedRange:replacementRange:":
			var original func(objc.ID, objc.SEL, objc.ID, nativeRange, nativeRange)
			purego.RegisterFunc(&original, implementation(method))
			replaceImplementation(method, uintptr(objc.NewIMP(func(self objc.ID, cmd objc.SEL, value objc.ID, selected, replacement nativeRange) {
				inputCalls["setMarkedText"]++
				original(self, cmd, value, selected, replacement)
			})))
		}
	}
	keyEvents := []map[string]any{}
	block := objc.NewBlock(func(_ objc.Block, event objc.ID) objc.ID {
		kind := event.Send(sel("type"))
		entry := map[string]any{"type": kind, "keycode": event.Send(sel("keyCode")), "flags": event.Send(sel("modifierFlags"))}
		if kind == 10 || kind == 11 {
			entry["character_length"] = event.Send(sel("charactersIgnoringModifiers")).Send(sel("length"))
		}
		keyEvents = append(keyEvents, entry)
		if len(keyEvents) > 128 {
			keyEvents = keyEvents[len(keyEvents)-128:]
		}
		return event
	})
	monitor := objc.ID(objc.GetClass("NSEvent")).Send(sel("addLocalMonitorForEventsMatchingMask:handler:"), uintptr((1<<10)|(1<<11)|(1<<12)), block)
	mygo.App.OnQuit(func() { objc.ID(objc.GetClass("NSEvent")).Send(sel("removeMonitor:"), monitor); block.Release() })
	go func() {
		for {
			connection, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				defer connection.Close()
				dec := json.NewDecoder(connection)
				enc := json.NewEncoder(connection)
				for {
					var req struct {
						Stage   string `json:"stage"`
						Reduced bool   `json:"reduced"`
						Op      string `json:"op"`
						Width   int    `json:"width"`
						Height  int    `json:"height"`
					}
					if dec.Decode(&req) != nil {
						return
					}
					var result any
					mygo.RunOnMain(func() {
						w := objc.ID(win.NativeHandle())
						pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(sel("new"))
						defer pool.Send(sel("drain"))
						switch req.Op {
						case "motion-fixture":
							if err := app.UseProjectMotionFixture(req.Stage, req.Reduced); err != nil {
								result = map[string]string{"error": err.Error()}
							} else {
								win.Update(func() {})
								result = map[string]any{"visual_fixture_only": true, "stage": req.Stage, "reduced": req.Reduced}
							}
							return
						case "motion":
							result = app.ProjectMotionDiagnostics()
							return
						case "close", "minimize", "zoom":
							index := map[string]int{"close": 0, "minimize": 1, "zoom": 2}[req.Op]
							b := w.Send(sel("standardWindowButton:"), index)
							if b == 0 {
								result = map[string]string{"error": "missing native window button"}
								return
							}
							b.Send(sel("performClick:"), uintptr(0))
						case "restore":
							w.Send(sel("deminiaturize:"), uintptr(0))
							win.Show()
							win.Focus()
						case "resize":
							win.SetSize(req.Width, req.Height)
						case "snapshot":
						default:
							result = map[string]string{"error": "unsupported probe operation"}
							return
						}
						buttons := []any{}
						for i := 0; i < 3; i++ {
							b := w.Send(sel("standardWindowButton:"), i)
							buttons = append(buttons, map[string]any{"exists": b != 0, "hidden": b.Send(sel("isHidden")) != 0, "enabled": b.Send(sel("isEnabled")) != 0})
						}
						nodes := []any{}
						materials := []any{}
						var localSurface objc.ID
						var visit func(objc.ID, int)
						visit = func(obj objc.ID, depth int) {
							if obj == 0 || depth > 25 {
								return
							}
							cls := str(obj.Send(sel("class")).Send(sel("description")))
							if cls == "MyGoSurfaceView" {
								localSurface = obj
							}
							if cls == "NSVisualEffectView" {
								materials = append(materials, map[string]any{"class": cls, "material": obj.Send(sel("material")), "state": obj.Send(sel("state"))})
							}
							kids := obj.Send(sel("subviews"))
							count := uintptr(kids.Send(sel("count")))
							for i := uintptr(0); i < count; i++ {
								visit(kids.Send(sel("objectAtIndex:"), i), depth+1)
							}
						}
						visit(w.Send(sel("contentView")), 0)
						var access func(objc.ID, int)
						access = func(obj objc.ID, depth int) {
							if obj == 0 || depth > 25 {
								return
							}
							if obj.Send(sel("respondsToSelector:"), sel("accessibilityRole")) == 0 {
								return
							}
							label := ""
							for _, name := range []string{"accessibilityLabel", "accessibilityTitle"} {
								if obj.Send(sel("respondsToSelector:"), sel(name)) != 0 {
									label += str(obj.Send(sel(name)))
								}
							}
							role := str(obj.Send(sel("accessibilityRole")))
							if role != "" {
								nodes = append(nodes, map[string]any{"label": label, "role": role, "depth": depth})
							}
							kids := obj.Send(sel("accessibilityChildren"))
							count := uintptr(kids.Send(sel("count")))
							for i := uintptr(0); i < count; i++ {
								access(kids.Send(sel("objectAtIndex:"), i), depth+1)
							}
						}
						access(localSurface, 0)
						responder := w.Send(sel("firstResponder"))
						inputContext := responder.Send(sel("inputContext"))
						inputState := map[string]any{"context_exists": inputContext != 0, "selected_input_source": str(inputContext.Send(sel("selectedKeyboardInputSource"))), "method_calls": inputCalls}
						if responder.Send(sel("respondsToSelector:"), sel("selectedRange")) != 0 {
							selection := getRange(responder, sel("selectedRange"))
							inputState["selection_location"], inputState["selection_length"] = selection.Location, selection.Length
						}
						result = map[string]any{"text_input_state": inputState, "key_window": w.Send(sel("isKeyWindow")) != 0, "first_responder_class": str(responder.Send(sel("class")).Send(sel("description"))), "key_events": keyEvents, "visible": w.Send(sel("isVisible")) != 0, "minimized": w.Send(sel("isMiniaturized")) != 0, "zoomed": w.Send(sel("isZoomed")) != 0, "buttons": buttons, "bounds": win.Bounds(), "materials": materials, "nsaccessibility": nodes}
					})
					if enc.Encode(result) != nil {
						return
					}
				}
			}()
		}
	}()
}
