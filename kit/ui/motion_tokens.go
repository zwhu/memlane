package ui

import (
	"math"
	"time"

	mygoui "github.com/egoist/mygo/ui"
)

// Visual V4 motion tokens (docs/design/visual-standards-v4-20261120.md#6-动效).
// Motion always reads real state; animations never announce task completion.
var (
	Smooth    = mygoui.Animation{Spring: mygoui.SpringOptions{Duration: 350 * time.Millisecond, Bounce: 0}}
	Snappy    = mygoui.Animation{Spring: mygoui.SpringOptions{Duration: 300 * time.Millisecond, Bounce: 0.15}}
	Bouncy    = mygoui.Animation{Spring: mygoui.SpringOptions{Duration: 420 * time.Millisecond, Bounce: 0.30}}
	Fast      = mygoui.Animation{Duration: 120 * time.Millisecond, Ease: mygoui.Linear}
	QuickFade = mygoui.Animation{Duration: 140 * time.Millisecond, Ease: mygoui.EaseOut}
)

// PoseTracker drives paint-only AnimatePose enter transitions in 2 frames:
// Callers include `run` in the element's c.Key(...) so Frame 1 always starts
// from a fresh pose state (r == nil -> velocity == 0, never using a 1ns curve
// that would inject huge velocity and cause up-then-down overshoot).
// Frame 1 snaps to the starting pose and requests 1 rebuild;
// Frame 2 heads monotonically for the resting pose on MyGo's paint-only repose().
type PoseTracker struct {
	armed    map[string]uint64
	launched map[string]uint64
}

func (t *PoseTracker) Apply(c *mygoui.Context, el mygoui.Element, key string, run uint64, start, rest mygoui.Pose, delay time.Duration, restAnim mygoui.Animation) mygoui.Element {
	return t.ApplyWith(c, el, key, run, start, rest, delay, Smooth, restAnim)
}

func (t *PoseTracker) ApplyWith(c *mygoui.Context, el mygoui.Element, key string, run uint64, start, rest mygoui.Pose, delay time.Duration, enterAnim, restAnim mygoui.Animation) mygoui.Element {
	reduce := c.Preferences().ReduceMotion
	if run == 0 || reduce {
		return el.AnimatePose(rest, restAnim)
	}
	if t.armed == nil {
		t.armed = map[string]uint64{}
		t.launched = map[string]uint64{}
	}
	if len(t.armed) > 256 {
		clear(t.armed)
		clear(t.launched)
	}
	if t.armed[key] != run {
		t.armed[key] = run
		delete(t.launched, key)
		el.AnimatePose(start, enterAnim)
		c.AnimationFrame()
		return el
	}
	if t.launched[key] != run {
		t.launched[key] = run
		anim := enterAnim
		anim.Delay = delay
		return el.AnimatePose(rest, anim)
	}
	return el.AnimatePose(rest, restAnim)
}

// PageEnter ("Page Rise / 页面上浮入场") reproduces .page-enter: monotonic
// bottom-to-top glide (translateY +10px -> 0, opacity 0 -> 1) on Smooth spring.
func (t *PoseTracker) PageEnter(c *mygoui.Context, el mygoui.Element, key string, run uint64) mygoui.Element {
	return t.Apply(c, el, "page:"+key, run, mygoui.Pose{Y: 10, Scale: 1, Fade: 1}, mygoui.Pose{Scale: 1}, 0, Smooth)
}

func (t *PoseTracker) ColdStartPageEnter(c *mygoui.Context, el mygoui.Element, key string, run uint64) mygoui.Element {
	return t.Apply(c, el, "page:"+key, run, mygoui.Pose{Y: 6, Scale: 1, Fade: 0.15}, mygoui.Pose{Scale: 1}, 0, Smooth)
}

// StaggerEnter ("Stagger Cascade / 列表错开缓进") reproduces .stagger > *:
// monotonic bottom-to-top glide (+8px -> 0, opacity 0 -> 1, 28ms per-row delay)
// merged cleanly with Hover Lift (-1px) and Press Tap (0.99).
func (t *PoseTracker) StaggerEnter(c *mygoui.Context, el mygoui.Element, key string, index int, run uint64, interactive bool) mygoui.Element {
	start := mygoui.Pose{Y: 8, Scale: 1, Fade: 1}
	return t.staggerEnter(c, el, key, index, run, interactive, start, time.Duration(index*28+40)*time.Millisecond)
}

func (t *PoseTracker) ColdStartStaggerEnter(c *mygoui.Context, el mygoui.Element, key string, index int, run uint64, interactive bool) mygoui.Element {
	start := mygoui.Pose{Y: 5, Scale: 1, Fade: 0.15}
	return t.staggerEnter(c, el, key, index, run, interactive, start, time.Duration(index*10)*time.Millisecond)
}

func (t *PoseTracker) staggerEnter(c *mygoui.Context, el mygoui.Element, key string, index int, run uint64, interactive bool, start mygoui.Pose, delay time.Duration) mygoui.Element {
	rest := mygoui.Pose{Scale: 1}
	if interactive {
		switch {
		case el.Pressed():
			rest.Scale = 0.99
		case el.Hovered():
			rest.Y = -1
		}
	}
	if index < 0 || index >= 8 {
		if interactive {
			return el.AnimatePose(rest, Snappy)
		}
		return el
	}
	return t.Apply(c, el, "row:"+key, run, start, rest, delay, Snappy)
}

// ContentFade applies an ultra-short paint-only opacity fade-in (0.15 -> 1.0,
// 140ms EaseOut) with zero X/Y translation when swapping detail content inside a
// stationary reading panel.
func (t *PoseTracker) ContentFade(c *mygoui.Context, el mygoui.Element, key string, run uint64) mygoui.Element {
	return t.ApplyWith(c, el, "fade:"+key, run, mygoui.Pose{Scale: 1, Fade: 0.85}, mygoui.Pose{Scale: 1}, 0, QuickFade, QuickFade)
}

// HeatmapWaveElapsed advances the left-to-right staggered wave grow animation
// for the heatmap canvas. Stops requesting frames once settled or under ReduceMotion.
func HeatmapWaveElapsed(c *mygoui.Context, p *mygoui.Painter, start time.Time, total time.Duration) (float64, bool) {
	if c.Preferences().ReduceMotion || start.IsZero() {
		return total.Seconds(), false
	}
	elapsed := p.Now().Sub(start)
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed >= total {
		return total.Seconds(), false
	}
	p.AnimationFrame()
	return elapsed.Seconds(), true
}

// BouncyScale evaluates an underdamped Bouncy spring (0 -> 1 with crisp pop)
// at t seconds for individual heatmap cells during wave grow.
func BouncyScale(t float64) float32 {
	if t <= 0 {
		return 0
	}
	if t >= 0.65 {
		return 1
	}
	const (
		w    = 2 * math.Pi / 0.42
		zeta = 0.62 // slight tactile pop visible on 11px squares
	)
	sq := math.Sqrt(1 - zeta*zeta)
	wd := w * sq
	val := 1 - math.Exp(-zeta*w*t)*(math.Cos(wd*t)+(zeta/sq)*math.Sin(wd*t))
	if val < 0 {
		return 0
	}
	return float32(val)
}

func ModalTransition() mygoui.ElementTransition {
	anim := Bouncy
	return mygoui.ElementTransition{
		Animation: &anim,
		Enter:     &mygoui.Motion{Y: 10, Scale: 0.94, Opacity: 0},
	}
}

func DialogTransition() mygoui.ElementTransition {
	anim := Snappy
	return mygoui.ElementTransition{
		Animation: &anim,
		Enter:     &mygoui.Motion{Scale: 0.96, Opacity: 0},
	}
}

// Spinner draws a compact 12px loading spinner. Under ReduceMotion it renders
// a static dot ring without requesting animation frames.
func Spinner(c *mygoui.Context, tok *Tokens) mygoui.Element {
	reduce := c.Preferences().ReduceMotion
	return mygoui.Box(c).Size(12, 12).Shrink(0).Draw(func(p *mygoui.Painter, r mygoui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		rad := float32(4.5)
		angle := float64(0)
		if !reduce {
			p.AnimationFrame()
			now := float64(p.Now().UnixNano()) / 1e9
			angle = math.Mod(now/0.7, 1) * 2 * math.Pi
		}
		for i := 0; i < 8; i++ {
			a := float64(i) * (2 * math.Pi / 8)
			x := cx + float32(math.Cos(a))*rad
			y := cy + float32(math.Sin(a))*rad
			p.Fill(mygoui.Rect{X: x - 0.9, Y: y - 0.9, W: 1.8, H: 1.8}, tok.BorderStrong, 0.9)
		}
		for i := 0; i < 4; i++ {
			a := angle + float64(i)*0.45
			x := cx + float32(math.Cos(a))*rad
			y := cy + float32(math.Sin(a))*rad
			alpha := float32(0.4 + 0.6*float64(i)/3.0)
			p.Fill(mygoui.Rect{X: x - 1.1, Y: y - 1.1, W: 2.2, H: 2.2}, tok.Accent.Alpha(alpha), 1.1)
		}
	})
}

// SkeletonBar draws a single V4 skeleton placeholder bar with a subtle shimmer.
// Under ReduceMotion it stays static and never requests animation frames.
func SkeletonBar(c *mygoui.Context, tok *Tokens, width, height, cornerRadius float32) mygoui.Element {
	reduce := c.Preferences().ReduceMotion
	box := mygoui.Box(c).Height(height).Radius(cornerRadius).Shrink(0)
	if width > 0 {
		box.Width(width)
	} else {
		box.FillWidth()
	}
	return box.Draw(func(p *mygoui.Painter, r mygoui.Rect) {
		base := tok.Fill2
		p.Fill(r, base, cornerRadius)
		if reduce || r.W <= 0 {
			return
		}
		p.AnimationFrame()
		now := float64(p.Now().UnixNano()) / 1e9
		phase := math.Mod(now/1.4, 1)
		bandW := r.W * 0.36
		x := r.X - bandW + float32(phase)*(r.W+bandW)
		clippedX := max(r.X, x)
		clippedRight := min(r.X+r.W, x+bandW)
		if clippedRight > clippedX {
			shine := tok.BgElevated.Alpha(0.55)
			if tok.Dark {
				shine = tok.Hover.Alpha(0.22)
			}
			p.Fill(mygoui.Rect{X: clippedX, Y: r.Y, W: clippedRight - clippedX, H: r.H}, shine, cornerRadius)
		}
	})
}

// RowInteractive applies the V4 hover lift (-1px) and press scale (0.99)
// via AnimatePose, which runs on the paint pass without rebuilding the view.
func RowInteractive(el mygoui.Element) mygoui.Element {
	pose := mygoui.Pose{Scale: 1}
	switch {
	case el.Pressed():
		pose.Scale = 0.99
	case el.Hovered():
		pose.Y = -1
	}
	return el.AnimatePose(pose, Snappy)
}

// ButtonPress applies a subtle 0.96 press scale to buttons and nav items.
func ButtonPress(el mygoui.Element, pressedScale float32) mygoui.Element {
	pose := mygoui.Pose{Scale: 1}
	if el.Pressed() {
		pose.Scale = pressedScale
	}
	return el.AnimatePose(pose, Snappy)
}

// StageBlocks draws the compact 3x2 bouncing block stage for running tasks.
// Under ReduceMotion it renders static blocks without requesting frames.
func StageBlocks(c *mygoui.Context, tok *Tokens) mygoui.Element {
	reduce := c.Preferences().ReduceMotion
	return mygoui.Box(c).Size(19, 12).Shrink(0).Draw(func(p *mygoui.Painter, r mygoui.Rect) {
		now := float64(p.Now().UnixNano()) / 1e9
		if !reduce {
			p.AnimationFrame()
		}
		for k := 0; k < 6; k++ {
			col, row := k%3, k/3
			x := r.X + float32(col)*7
			y := r.Y + float32(row)*7
			alpha := float32(0.85)
			dy := float32(0)
			if !reduce {
				phase := math.Mod(now/1.2-float64(k)*0.075, 1)
				if phase < 0 {
					phase += 1
				}
				switch {
				case phase < 0.35:
					t := phase / 0.35
					dy = float32((1 - t) * -4)
					alpha = float32(0.2 + 0.8*t)
				case phase < 0.75:
					alpha = 1
				default:
					t := (phase - 0.75) / 0.25
					alpha = float32(1 - 0.75*t)
				}
			}
			p.Fill(mygoui.Rect{X: x, Y: y + dy, W: 5, H: 5}, tok.Accent.Alpha(alpha), 1.5)
		}
	})
}

// EmptyState renders the V4 floating three-block illustration with subtle
// pointer parallax. Gently breathes continuously while visible and stops
// immediately under ReduceMotion.
func EmptyState(c *mygoui.Context, tok *Tokens, key, message, hint string) mygoui.Element {
	reduce := c.Preferences().ReduceMotion
	wrap := mygoui.Column(c.Key(key)).Grow(1).MinHeight(180).FillWidth().AlignItems(mygoui.Center).Justify(mygoui.Center).Gap(Sp3).Padding(Sp5)
	animate := !reduce
	var px, py float32
	if !reduce {
		if x, y, ok := wrap.PointerPosition(); ok {
			b := wrap.Bounds()
			if b.W > 1 && b.H > 1 {
				dx := (x - b.W/2) / b.W
				dy := (y - b.H/2) / b.H
				px = max(-10, min(10, -dx*12))
				py = max(-8, min(8, -dy*10))
			}
		}
	}
	sx := wrap.Spring("empty-px", px, Smooth.Spring)
	sy := wrap.Spring("empty-py", py, Smooth.Spring)
	return wrap.Children(func() {
		mygoui.Box(c).Size(76, 60).Shrink(0).Draw(func(p *mygoui.Painter, r mygoui.Rect) {
			now := float64(p.Now().UnixNano()) / 1e9
			if animate {
				p.AnimationFrame()
			}
			blocks := []struct {
				x, y, alpha float32
				offset      float64
			}{
				{6, 22, 0.90, 0},
				{30, 8, 0.55, 0.8},
				{46, 30, 0.30, 1.6},
			}
			for _, b := range blocks {
				dy := float32(0)
				if animate {
					dy = float32(math.Sin((now+b.offset)*2*math.Pi/3.2) * -4.5)
				}
				p.Fill(mygoui.Rect{X: r.X + b.x + sx, Y: r.Y + b.y + sy + dy, W: 22, H: 22}, tok.Accent.Alpha(b.alpha), 5)
			}
		})
		if message != "" {
			Text(c, tok, message, RoleBody).TextAlign(mygoui.Center)
		}
		if hint != "" {
			Text(c, tok, hint, RoleMeta).TextAlign(mygoui.Center).MaxLines(3).MaxWidth(320)
		}
	})
}

func SmoothPoseProgress(t float64) float64 {
	if t >= 1 {
		return 1
	}
	const spring = 8.0
	return 1 - (1+spring*t)*math.Exp(-spring*t)
}
