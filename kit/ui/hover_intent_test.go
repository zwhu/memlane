package ui

import (
	"testing"
	"time"

	mygoui "github.com/egoist/mygo/ui"
)

func TestPointInSafeTriangle(t *testing.T) {
	submenu := mygoui.Rect{X: 200, Y: 100, W: 180, H: 160}
	anchor := Point{X: 120, Y: 150}

	if !PointInSafeTriangle(Point{X: 160, Y: 140}, anchor, submenu) {
		t.Fatal("expected diagonal midpoint toward submenu to be inside safe triangle")
	}
	if !PointInSafeTriangle(Point{X: 195, Y: 250}, anchor, submenu) {
		t.Fatal("expected lower diagonal path toward submenu bottom corner to be inside safe triangle")
	}
	if PointInSafeTriangle(Point{X: 140, Y: 240}, anchor, submenu) {
		t.Fatal("expected downward movement away from submenu to be outside safe triangle")
	}
	if PointInSafeTriangle(Point{X: 80, Y: 150}, anchor, submenu) {
		t.Fatal("expected backward movement away from submenu to be outside safe triangle")
	}
}

func TestHoverIntentSafeTriangleDefersClose(t *testing.T) {
	h := &HoverIntent{
		OpenDelay:  15 * time.Millisecond,
		CloseDelay: 20 * time.Millisecond,
	}
	submenu := mygoui.Rect{X: 200, Y: 100, W: 180, H: 160}
	done := make(chan struct{}, 4)
	update := func(fn func()) {
		fn()
		done <- struct{}{}
	}

	start := Point{X: 120, Y: 150}
	h.Sync(true, false, false, &start, submenu, update)
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for open delay")
	}
	if !h.Open() {
		t.Fatal("expected HoverIntent to open after OpenDelay")
	}

	// Move diagonally toward submenu: safe triangle keeps it open.
	mid := Point{X: 165, Y: 145}
	h.Sync(false, false, false, &mid, submenu, update)
	time.Sleep(40 * time.Millisecond)
	if !h.Open() {
		t.Fatal("expected safe triangle to hold HoverIntent open")
	}

	// Move clearly outside triangle: closes after CloseDelay.
	away := Point{X: 120, Y: 290}
	h.Sync(false, false, false, &away, submenu, update)
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for close delay")
	}
	if h.Open() {
		t.Fatal("expected HoverIntent to close after leaving safe triangle")
	}
}

func TestAllKitUISVGsParseCleanly(t *testing.T) {
	icons := []*mygoui.SVG{
		IconOverview, IconCommon, IconProfile, IconFolder, IconSettings,
		IconSearch, IconChev, IconDown, IconDoc, IconCopy, IconClose,
		IconRecall, IconInfo, IconCheck, IconRefresh, IconPlus,
		IconRefresh24, IconCopy24,
		SolidSVG("#5E6AD2", PathOverview),
		SolidSVG("#5E6AD2", PathCommon),
		SolidSVG("#5E6AD2", PathProfile),
		SolidSVG("#5E6AD2", PathFolder),
	}
	for i, ic := range icons {
		if ic == nil {
			t.Fatalf("icon[%d] is nil", i)
		}
	}
}
