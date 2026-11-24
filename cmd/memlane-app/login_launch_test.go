package main

import "testing"

func TestShouldStartHiddenAtLogin(t *testing.T) {
	for _, tc := range []struct{ mygo, event, hidden bool }{
		{false, false, false}, {true, false, true},
		{false, true, true}, {true, true, true},
	} {
		if got := shouldStartHiddenAtLogin(tc.mygo, tc.event); got != tc.hidden {
			t.Errorf("MyGo=%t event=%t: hidden=%t, want %t", tc.mygo, tc.event, got, tc.hidden)
		}
	}
}

func TestIsLoginLaunchPropDescriptor(t *testing.T) {
	for _, tc := range []struct {
		kind, enum, code uint32
		want             bool
	}{
		{enumeratedDescriptor, launchedAsLoginItem, 0, true},
		{typeDescriptor, 0, launchedAsLoginItem, true},
		{enumeratedDescriptor, 0, launchedAsLoginItem, false},
		{typeDescriptor, launchedAsLoginItem, 0, false},
		{0, launchedAsLoginItem, launchedAsLoginItem, false},
	} {
		if got := isLoginLaunchPropDescriptor(tc.kind, tc.enum, tc.code); got != tc.want {
			t.Errorf("descriptor %#x, enum %#x, type %#x: got %t, want %t", tc.kind, tc.enum, tc.code, got, tc.want)
		}
	}
}
