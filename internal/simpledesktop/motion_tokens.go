package simpledesktop

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

// Visual V4 motion tokens (docs/design/visual-standards-v4-20261120.md#6-动效).
// Motion always reads real state; animations never announce task completion.
var (
	v4Smooth    = ui.Animation{Spring: ui.SpringOptions{Duration: 350 * time.Millisecond, Bounce: 0}}
	v4Snappy    = ui.Animation{Spring: ui.SpringOptions{Duration: 300 * time.Millisecond, Bounce: 0.15}}
	v4Bouncy    = ui.Animation{Spring: ui.SpringOptions{Duration: 420 * time.Millisecond, Bounce: 0.30}}
	v4Fast      = ui.Animation{Duration: 120 * time.Millisecond, Ease: ui.Linear}
	v4QuickFade = ui.Animation{Duration: 140 * time.Millisecond, Ease: ui.EaseOut}
)

// poseEnterTracker drives paint-only AnimatePose enter transitions in 2 frames:
// Callers include `run` in the element's c.Key(...) so Frame 1 always starts
// from a fresh pose state (r == nil -> velocity == 0, never using a 1ns curve
// that would inject huge velocity and cause up-then-down overshoot).
// Frame 1 snaps to the starting pose and requests 1 rebuild;
// Frame 2 heads monotonically for the resting pose on MyGo's paint-only repose().
type poseEnterTracker struct {
	armed    map[string]uint64
	launched map[string]uint64
}

func (t *poseEnterTracker) apply(c *ui.Context, el ui.Element, key string, run uint64, start, rest ui.Pose, delay time.Duration, restAnim ui.Animation) ui.Element {
	return t.applyWith(c, el, key, run, start, rest, delay, v4Smooth, restAnim)
}

func (t *poseEnterTracker) applyWith(c *ui.Context, el ui.Element, key string, run uint64, start, rest ui.Pose, delay time.Duration, enterAnim, restAnim ui.Animation) ui.Element {
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

// v4PageEnter ("Page Rise / 页面上浮入场") reproduces .page-enter: monotonic
// bottom-to-top glide (translateY +10px -> 0, opacity 0 -> 1) on Smooth spring.
func (a *App) v4PageEnter(c *ui.Context, el ui.Element, key string, run uint64) ui.Element {
	return a.poseEnter.apply(c, el, "page:"+key, run, ui.Pose{Y: 10, Scale: 1, Fade: 1}, ui.Pose{Scale: 1}, 0, v4Smooth)
}

func (a *App) v4ColdStartPageEnter(c *ui.Context, el ui.Element, key string, run uint64) ui.Element {
	return a.poseEnter.apply(c, el, "page:"+key, run, ui.Pose{Y: 6, Scale: 1, Fade: 0.15}, ui.Pose{Scale: 1}, 0, v4Smooth)
}

// v4StaggerEnter ("Stagger Cascade / 列表错开缓进") reproduces .stagger > *:
// monotonic bottom-to-top glide (+8px -> 0, opacity 0 -> 1, 28ms per-row delay)
// merged cleanly with Hover Lift (-1px) and Press Tap (0.99).
func (a *App) v4StaggerEnter(c *ui.Context, el ui.Element, key string, index int, run uint64, interactive bool) ui.Element {
	start := ui.Pose{Y: 8, Scale: 1, Fade: 1}
	return a.staggerEnter(c, el, key, index, run, interactive, start, time.Duration(index*28+40)*time.Millisecond)
}

func (a *App) v4ColdStartStaggerEnter(c *ui.Context, el ui.Element, key string, index int, run uint64, interactive bool) ui.Element {
	start := ui.Pose{Y: 5, Scale: 1, Fade: 0.15}
	return a.staggerEnter(c, el, key, index, run, interactive, start, time.Duration(index*10)*time.Millisecond)
}

func (a *App) staggerEnter(c *ui.Context, el ui.Element, key string, index int, run uint64, interactive bool, start ui.Pose, delay time.Duration) ui.Element {
	rest := ui.Pose{Scale: 1}
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
			return el.AnimatePose(rest, v4Snappy)
		}
		return el
	}
	return a.poseEnter.apply(c, el, "row:"+key, run, start, rest, delay, v4Snappy)
}

// v4ContentFade applies an ultra-short paint-only opacity fade-in (0.15 -> 1.0,
// 140ms EaseOut) with zero X/Y translation when swapping detail content inside a
// stationary reading panel.
func (a *App) v4ContentFade(c *ui.Context, el ui.Element, key string, run uint64) ui.Element {
	return a.poseEnter.applyWith(c, el, "fade:"+key, run, ui.Pose{Scale: 1, Fade: 0.85}, ui.Pose{Scale: 1}, 0, v4QuickFade, v4QuickFade)
}

// v4HeatmapWaveElapsed advances the left-to-right staggered wave grow animation
// for the heatmap canvas. Stops requesting frames once settled or under ReduceMotion.
func v4HeatmapWaveElapsed(c *ui.Context, p *ui.Painter, start time.Time, total time.Duration) (float64, bool) {
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

// v4BouncyScale evaluates an underdamped Bouncy spring (0 -> 1 with crisp pop)
// at t seconds for individual heatmap cells during wave grow.
func v4BouncyScale(t float64) float32 {
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

func v4ModalTransition() ui.ElementTransition {
	anim := v4Bouncy
	return ui.ElementTransition{
		Animation: &anim,
		Enter:     &ui.Motion{Y: 10, Scale: 0.94, Opacity: 0},
	}
}

func v4DialogTransition() ui.ElementTransition {
	anim := v4Snappy
	return ui.ElementTransition{
		Animation: &anim,
		Enter:     &ui.Motion{Scale: 0.96, Opacity: 0},
	}
}

// v4Spinner draws a compact 12px loading spinner. Under ReduceMotion it renders
// a static dot ring without requesting animation frames.
func v4Spinner(c *ui.Context) ui.Element {
	reduce := c.Preferences().ReduceMotion
	return ui.Box(c).Size(12, 12).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
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
			p.Fill(ui.Rect{X: x - 0.9, Y: y - 0.9, W: 1.8, H: 1.8}, tok.BorderStrong, 0.9)
		}
		for i := 0; i < 4; i++ {
			a := angle + float64(i)*0.45
			x := cx + float32(math.Cos(a))*rad
			y := cy + float32(math.Sin(a))*rad
			alpha := float32(0.4 + 0.6*float64(i)/3.0)
			p.Fill(ui.Rect{X: x - 1.1, Y: y - 1.1, W: 2.2, H: 2.2}, tok.Accent.Alpha(alpha), 1.1)
		}
	})
}

// v4SkeletonBar draws a single V4 skeleton placeholder bar with a subtle shimmer.
// Under ReduceMotion it stays static and never requests animation frames.
func v4SkeletonBar(c *ui.Context, width, height, cornerRadius float32) ui.Element {
	reduce := c.Preferences().ReduceMotion
	box := ui.Box(c).Height(height).Radius(cornerRadius).Shrink(0)
	if width > 0 {
		box.Width(width)
	} else {
		box.FillWidth()
	}
	return box.Draw(func(p *ui.Painter, r ui.Rect) {
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
			p.Fill(ui.Rect{X: clippedX, Y: r.Y, W: clippedRight - clippedX, H: r.H}, shine, cornerRadius)
		}
	})
}

// v4RowInteractive applies the V4 hover lift (-1px) and press scale (0.99)
// via AnimatePose, which runs on the paint pass without rebuilding the view.
func v4RowInteractive(el ui.Element) ui.Element {
	pose := ui.Pose{Scale: 1}
	switch {
	case el.Pressed():
		pose.Scale = 0.99
	case el.Hovered():
		pose.Y = -1
	}
	return el.AnimatePose(pose, v4Snappy)
}

// v4ButtonPress applies a subtle 0.96 press scale to buttons and nav items.
func v4ButtonPress(el ui.Element, pressedScale float32) ui.Element {
	pose := ui.Pose{Scale: 1}
	if el.Pressed() {
		pose.Scale = pressedScale
	}
	return el.AnimatePose(pose, v4Snappy)
}

// v4StageBlocks draws the compact 3x2 bouncing block stage for running tasks.
// Under ReduceMotion it renders static blocks without requesting frames.
func v4StageBlocks(c *ui.Context) ui.Element {
	reduce := c.Preferences().ReduceMotion
	return ui.Box(c).Size(19, 12).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
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
			p.Fill(ui.Rect{X: x, Y: y + dy, W: 5, H: 5}, tok.Accent.Alpha(alpha), 1.5)
		}
	})
}

// v4EmptyState renders the V4 floating three-block illustration with subtle
// pointer parallax. Gently breathes continuously while visible and stops
// immediately under ReduceMotion.
func v4EmptyState(c *ui.Context, key, message, hint string) {
	reduce := c.Preferences().ReduceMotion
	wrap := ui.Column(c.Key(key)).Grow(1).MinHeight(180).FillWidth().AlignItems(ui.Center).Justify(ui.Center).Gap(sp3).Padding(sp5)
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
	sx := wrap.Spring("empty-px", px, v4Smooth.Spring)
	sy := wrap.Spring("empty-py", py, v4Smooth.Spring)
	wrap.Children(func() {
		ui.Box(c).Size(76, 60).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
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
				p.Fill(ui.Rect{X: r.X + b.x + sx, Y: r.Y + b.y + sy + dy, W: 22, H: 22}, tok.Accent.Alpha(b.alpha), 5)
			}
		})
		if message != "" {
			homeText(c, message, homeBody).TextAlign(ui.Center)
		}
		if hint != "" {
			homeText(c, hint, homeMeta).TextAlign(ui.Center).MaxLines(3).MaxWidth(320)
		}
	})
}

func (a *App) onboardingPageEnter(c *ui.Context, el ui.Element, run uint64) {
	a.poseEnter.apply(c, el, "onboarding", run,
		ui.Pose{Y: 8, Scale: 1, Fade: 1}, ui.Pose{Scale: 1}, 0, v4Smooth)
}

func onboardingMascot(c *ui.Context, visible bool) ui.Element {
	reduce := c.Preferences().ReduceMotion
	mark := tok.BrandMark
	return ui.Box(c).Size(76, 76).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		if visible && !reduce {
			p.AnimationFrame()
			r.Y -= 2 * float32(1-math.Cos(float64(p.Now().UnixMilli()%4000)*2*math.Pi/4000))
		}
		p.Image(mark, r, ui.Contain)
	})
}

func onboardingCompletionMark(c *ui.Context, started time.Time) {
	reduce := c.Preferences().ReduceMotion
	ok := tok.OK
	ui.Box(c).Size(64, 64).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		scale, drawn := float32(1), float32(1)
		if !reduce && !started.IsZero() {
			elapsed := p.Now().Sub(started)
			if elapsed < 700*time.Millisecond {
				p.AnimationFrame()
				scale = max(0, min(1.12, v4BouncyScale(max(0, elapsed.Seconds()))))
				progress := max(0, min(1, (elapsed-200*time.Millisecond).Seconds()/0.5))
				drawn = float32(smoothPoseProgress(progress))
			}
		}
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		size := r.W * scale
		p.Fill(ui.Rect{X: cx - size/2, Y: cy - size/2, W: size, H: size}, ok.Alpha(0.14), size/2)
		if drawn > 0 {
			mark := new(ui.Path).MoveTo(cx-10*scale, cy).LineTo(cx-3*scale, cy+7*scale).LineTo(cx+12*scale, cy-8*scale)
			if drawn < 1 {
				mark = mark.Trim(0, drawn)
			}
			p.StrokePath(mark, 3.2*scale, ok)
		}
	})
}

func smoothPoseProgress(t float64) float64 {
	if t >= 1 {
		return 1
	}
	const spring = 8.0
	return 1 - (1+spring*t)*math.Exp(-spring*t)
}

// The installer uses the larger four-block stage from the onboarding prototype.
func onboardingStageBlocks(c *ui.Context, visible bool) ui.Element {
	reduce := c.Preferences().ReduceMotion
	color := tok.Accent
	return ui.Box(c).Size(40, 40).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		animated := visible && !reduce
		if animated {
			p.AnimationFrame()
		}
		for index, alpha := range []float32{1, 0.55, 0.55, 0.25} {
			y := r.Y + float32(index/2)*23
			if animated {
				phase := float64((p.Now().UnixMilli()-int64(index)*200)%1600) / 1600
				y -= 3 * float32((1-math.Cos(phase*2*math.Pi))/2)
			}
			p.Fill(ui.Rect{X: r.X + float32(index%2)*23, Y: y, W: 17, H: 17}, color.Alpha(alpha), 4)
		}
	})
}

func onboardingSelectionCheck(c *ui.Context, selected bool, corner float32) {
	border, fill := tok.BorderStrong, ui.Transparent
	if selected {
		border, fill = tok.Accent, tok.Accent
	}
	box := ui.Box(c).Size(18, 18).Shrink(0).Radius(corner).Border(1, border).Background(fill).
		AlignItems(ui.Center).Justify(ui.Center)
	if selected {
		box.Transition(ui.ElementTransition{Animation: &v4Bouncy, Enter: &ui.Motion{Scale: 0.74}})
		box.Children(func() { ui.Icon(c, v4IconCheck).FontSize(12).TextColor(tok.OnAccent) })
	}
}
