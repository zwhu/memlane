//go:build darwin

package main

import (
	"runtime"
	"testing"

	"github.com/ebitengine/purego/objc"
)

func TestLoginLaunchAppleEventDescriptor(t *testing.T) {
	pool := loginLaunchRuntime()
	if pool == nil {
		t.Fatal("Objective-C autorelease pool unavailable")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	token := pool.push()
	defer pool.pop(token)
	class := objc.ID(objc.GetClass("NSAppleEventDescriptor"))
	if class == 0 {
		t.Fatal("NSAppleEventDescriptor unavailable")
	}
	loginEnum := class.Send(objc.RegisterName("descriptorWithEnumCode:"), launchedAsLoginItem)
	loginType := class.Send(objc.RegisterName("descriptorWithTypeCode:"), launchedAsLoginItem)
	otherEnum := class.Send(objc.RegisterName("descriptorWithEnumCode:"), uint32(0x74657374))
	unexpectedType := class.Send(objc.RegisterName("descriptorWithInt32:"), int32(launchedAsLoginItem))
	if loginEnum == 0 || loginType == 0 || otherEnum == 0 || unexpectedType == 0 {
		t.Fatal("failed to construct property descriptors")
	}
	if isLoginLaunchAppleEventDescriptor(0) {
		t.Fatal("nil descriptor reported as a login launch")
	}
	for _, tc := range []struct {
		name                string
		eventClass, eventID uint32
		prop                objc.ID
		want                bool
	}{
		{"other event class", 0x74657374, openApplicationEvent, loginEnum, false},
		{"other event ID", coreEventClass, 0x72617070, loginEnum, false},
		{"no launch property", coreEventClass, openApplicationEvent, 0, false},
		{"login enum", coreEventClass, openApplicationEvent, loginEnum, true},
		{"login type", coreEventClass, openApplicationEvent, loginType, true},
		{"other enum value", coreEventClass, openApplicationEvent, otherEnum, false},
		{"unexpected descriptor type", coreEventClass, openApplicationEvent, unexpectedType, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			token := pool.push()
			defer pool.pop(token)

			target := class.Send(objc.RegisterName("nullDescriptor"))
			ev := class.Send(objc.RegisterName("appleEventWithEventClass:eventID:targetDescriptor:returnID:transactionID:"),
				tc.eventClass, tc.eventID, target, int16(-1), int32(0))
			if ev == 0 {
				t.Fatal("failed to construct Apple Event")
			}
			if tc.prop != 0 {
				ev.Send(objc.RegisterName("setParamDescriptor:forKeyword:"), tc.prop, launchPropertyKeyword)
			}
			if got := isLoginLaunchAppleEventDescriptor(ev); got != tc.want {
				t.Fatalf("login launch=%t, want %t", got, tc.want)
			}
		})
	}
}

func TestWasOpenedAtLoginAtReadyWithoutApplication(t *testing.T) {
	// A Go test process has no AppKit launch event to classify.
	if wasOpenedAtLoginAtReady() {
		t.Fatal("unexpected login launch in the test process")
	}
}
