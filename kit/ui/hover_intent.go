package ui

import (
	"sync"
	"time"

	mygoui "github.com/egoist/mygo/ui"
)

// Point represents a 2D logical coordinate in window space.
type Point struct {
	X, Y float32
}

// PointInSafeTriangle reports whether cursor lies inside (or on the boundary of)
// the safe triangle formed by anchor (previous pointer position when leaving the
// trigger) and the two target edge corners of a submenu or hover popover.
func PointInSafeTriangle(cursor, anchor Point, target mygoui.Rect) bool {
	if target.W <= 0 || target.H <= 0 {
		return false
	}
	c1, c2 := safeTriangleCorners(anchor, target)
	return pointInTriangle(cursor, anchor, c1, c2)
}

func safeTriangleCorners(anchor Point, target mygoui.Rect) (Point, Point) {
	left, right := target.X, target.X+target.W
	top, bottom := target.Y, target.Y+target.H
	switch {
	case anchor.X < left:
		return Point{left, top}, Point{left, bottom}
	case anchor.X > right:
		return Point{right, top}, Point{right, bottom}
	case anchor.Y < top:
		return Point{left, top}, Point{right, top}
	default:
		return Point{left, bottom}, Point{right, bottom}
	}
}

func pointInTriangle(p, a, b, c Point) bool {
	const eps = 1e-4
	d1 := crossSign(p, a, b)
	d2 := crossSign(p, b, c)
	d3 := crossSign(p, c, a)
	hasNeg := d1 < -eps || d2 < -eps || d3 < -eps
	hasPos := d1 > eps || d2 > eps || d3 > eps
	return !(hasNeg && hasPos)
}

func crossSign(p1, p2, p3 Point) float32 {
	return (p1.X-p3.X)*(p2.Y-p3.Y) - (p2.X-p3.X)*(p1.Y-p3.Y)
}

// HoverIntent manages open delay, close delay, keyboard focus opening, and
// safe-triangle cursor protection for desktop menus and hover popovers using
// one-shot timers (zero idle polling).
type HoverIntent struct {
	OpenDelay  time.Duration
	CloseDelay time.Duration

	mu          sync.Mutex
	open        bool
	triggerHov  bool
	panelHov    bool
	triggerFoc  bool
	clicked     bool
	leaveAnchor Point
	hasAnchor   bool
	showTimer   *time.Timer
	hideTimer   *time.Timer
}

// DefaultHoverIntent returns a HoverIntent configured with 300ms open / 200ms close.
func DefaultHoverIntent() *HoverIntent {
	return &HoverIntent{
		OpenDelay:  300 * time.Millisecond,
		CloseDelay: 200 * time.Millisecond,
	}
}

func (h *HoverIntent) Open() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.open
}

func (h *HoverIntent) SetOpen(open bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopTimersLocked()
	h.open = open
	if !open {
		h.triggerHov = false
		h.panelHov = false
		h.hasAnchor = false
	}
}

func (h *HoverIntent) MarkClicked() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clicked = true
	h.stopTimersLocked()
	h.open = false
	h.triggerHov = false
	h.panelHov = false
	h.hasAnchor = false
}

func (h *HoverIntent) Cancel() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopTimersLocked()
}

func (h *HoverIntent) stopTimersLocked() {
	if h.showTimer != nil {
		h.showTimer.Stop()
		h.showTimer = nil
	}
	if h.hideTimer != nil {
		h.hideTimer.Stop()
		h.hideTimer = nil
	}
}

// Sync updates hover/focus/cursor state and schedules one-shot open/close timers
// through updateFn (typically App.Update). When cursor and targetRect are valid
// and the pointer travels inside the safe triangle toward the open popover/submenu,
// closing is deferred until the pointer leaves the triangle or enters the target.
func (h *HoverIntent) Sync(triggerHover, panelHover, triggerFocus bool, cursor *Point, targetRect mygoui.Rect, updateFn func(func())) {
	h.mu.Lock()
	defer h.mu.Unlock()

	openDelay := h.OpenDelay
	if openDelay <= 0 {
		openDelay = 300 * time.Millisecond
	}
	closeDelay := h.CloseDelay
	if closeDelay <= 0 {
		closeDelay = 200 * time.Millisecond
	}

	prevFocus := h.triggerFoc
	h.triggerFoc = triggerFocus
	if !triggerFocus {
		h.clicked = false
	}
	if triggerFocus && !prevFocus && !h.clicked {
		h.stopTimersLocked()
		h.triggerHov = true
		h.open = true
		return
	}
	if h.clicked && triggerFocus {
		return
	}

	if triggerHover && cursor != nil {
		h.leaveAnchor = *cursor
		h.hasAnchor = true
	}

	inSafe := false
	if h.open && !triggerHover && !panelHover && cursor != nil && h.hasAnchor && targetRect.W > 0 && targetRect.H > 0 {
		inSafe = PointInSafeTriangle(*cursor, h.leaveAnchor, targetRect)
	}

	prevActive := h.triggerHov || h.panelHov
	nextActive := triggerHover || panelHover || inSafe
	h.triggerHov = triggerHover
	h.panelHov = panelHover || inSafe

	if prevActive == nextActive {
		return
	}
	if nextActive {
		if h.hideTimer != nil {
			h.hideTimer.Stop()
			h.hideTimer = nil
		}
		if h.open || h.showTimer != nil {
			return
		}
		if updateFn == nil {
			return
		}
		h.showTimer = time.AfterFunc(openDelay, func() {
			updateFn(func() {
				h.mu.Lock()
				h.showTimer = nil
				if h.triggerHov || h.panelHov {
					h.open = true
				}
				h.mu.Unlock()
			})
		})
		return
	}

	if h.showTimer != nil {
		h.showTimer.Stop()
		h.showTimer = nil
	}
	if !h.open || h.hideTimer != nil {
		return
	}
	if updateFn == nil {
		h.open = false
		return
	}
	h.hideTimer = time.AfterFunc(closeDelay, func() {
		updateFn(func() {
			h.mu.Lock()
			h.hideTimer = nil
			if !h.triggerHov && !h.panelHov {
				h.open = false
				h.hasAnchor = false
			}
			h.mu.Unlock()
		})
	})
}
