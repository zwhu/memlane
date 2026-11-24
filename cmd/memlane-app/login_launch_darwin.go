//go:build darwin

package main

import (
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

var loginLaunchRuntime = sync.OnceValue(func() *loginLaunchPool {
	lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil
	}
	push, pushErr := purego.Dlsym(lib, "objc_autoreleasePoolPush")
	pop, popErr := purego.Dlsym(lib, "objc_autoreleasePoolPop")
	if pushErr != nil || popErr != nil || push == 0 || pop == 0 {
		return nil
	}
	if _, err := purego.Dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
		return nil
	}
	pool := &loginLaunchPool{}
	purego.RegisterFunc(&pool.push, push)
	purego.RegisterFunc(&pool.pop, pop)
	return pool
})

type loginLaunchPool struct {
	push func() uintptr
	pop  func(uintptr)
}

func wasOpenedAtLoginAtReady() bool {
	pool := loginLaunchRuntime()
	if pool == nil {
		return false
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	token := pool.push()
	defer pool.pop(token)

	managerClass := objc.ID(objc.GetClass("NSAppleEventManager"))
	if managerClass == 0 {
		return false
	}
	manager := managerClass.Send(objc.RegisterName("sharedAppleEventManager"))
	if manager == 0 {
		return false
	}
	// MyGo cached the startup flag before macOS delivered this event.
	return isLoginLaunchAppleEventDescriptor(manager.Send(objc.RegisterName("currentAppleEvent")))
}

func isLoginLaunchAppleEventDescriptor(ev objc.ID) bool {
	if ev == 0 ||
		objc.Send[uint32](ev, objc.RegisterName("eventClass")) != coreEventClass ||
		objc.Send[uint32](ev, objc.RegisterName("eventID")) != openApplicationEvent {
		return false
	}
	prop := ev.Send(objc.RegisterName("paramDescriptorForKeyword:"), launchPropertyKeyword)
	if prop == 0 {
		return false
	}
	descType := objc.Send[uint32](prop, objc.RegisterName("descriptorType"))
	var enumCode, typeCode uint32
	switch descType {
	case enumeratedDescriptor:
		enumCode = objc.Send[uint32](prop, objc.RegisterName("enumCodeValue"))
	case typeDescriptor:
		typeCode = objc.Send[uint32](prop, objc.RegisterName("typeCodeValue"))
	}
	return isLoginLaunchPropDescriptor(descType, enumCode, typeCode)
}
