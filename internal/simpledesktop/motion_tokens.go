package simpledesktop

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

// Visual V4 motion tokens (docs/ui-redesign/visual-standards-v4-20261120.md#6-动效).
// Motion always reads real state; animations never announce task completion.
var (
	v4Smooth = ui.Animation{Spring: ui.SpringOptions{Duration: 350 * time.Millisecond, Bounce: 0}}
	v4Snappy = ui.Animation{Spring: ui.SpringOptions{Duration: 300 * time.Millisecond, Bounce: 0.15}}
	v4Bouncy = ui.Animation{Spring: ui.SpringOptions{Duration: 420 * time.Millisecond, Bounce: 0.30}}
	v4Fast   = ui.Animation{Duration: 120 * time.Millisecond, Ease: ui.Linear}
)

func v4PageTransition() ui.ElementTransition {
	anim := v4Smooth
	return ui.ElementTransition{
		Animation: &anim,
		Enter:     &ui.Motion{Y: 8, Opacity: 0},
	}
}

func v4DocTransition() ui.ElementTransition {
	anim := v4Smooth
	return ui.ElementTransition{
		Animation: &anim,
		Enter:     &ui.Motion{X: 8, Opacity: 0},
	}
}

func v4RowEnterTransition(index int) ui.ElementTransition {
	anim := v4Smooth
	delay := time.Duration(min(index, 12)*28+40) * time.Millisecond
	anim.Delay = delay
	return ui.ElementTransition{
		Animation: &anim,
		Enter:     &ui.Motion{Y: 6, Opacity: 0},
	}
}

// v4SmoothEase evaluates the exact critically-damped spring (Duration=0.35s,
// Bounce=0) over 480ms, matching the V4 prototype's --spring-smooth curve.
func v4SmoothEase(t float32) float32 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	const span = 0.48
	const w = 2 * math.Pi / 0.35
	wt := w * float64(t) * span
	val := 1 - (1+wt)*math.Exp(-wt)
	end := 1 - (1+w*span)*math.Exp(-w*span)
	return float32(val / end)
}

var v4EnterFrames = []ui.Keyframe{
	{At: 0, Value: 0},
	{At: 480 * time.Millisecond, Value: 1, Ease: v4SmoothEase},
}

// v4PageEnter reproduces the V4 prototype's .page-enter motion (opacity 0->1,
// translateY 8px->0 on Smooth spring) purely on the paint/transform pass so
// layout geometry never jitters.
func v4PageEnter(el ui.Element, run uint64) ui.Element {
	p := el.Keyframes("v4-page-enter", v4EnterFrames, ui.KeyframeOptions{Run: run})
	if p < 1 {
		el.Opacity(p)
		el.Transform(ui.Translation(0, (1-p)*8))
	}
	return el
}

// v4StaggerEnter reproduces .stagger > * (opacity 0->1, translateY 6px->0,
// 28ms per-row stagger + 60ms base delay) for the first 12 visible rows.
func v4StaggerEnter(el ui.Element, index int, run uint64) ui.Element {
	if index < 0 || index >= 12 {
		return el
	}
	delay := time.Duration(index*28+60) * time.Millisecond
	p := el.Keyframes("v4-stagger-enter", v4EnterFrames, ui.KeyframeOptions{Run: run, Delay: delay})
	if p < 1 {
		el.Opacity(p)
		el.Transform(ui.Translation(0, (1-p)*6))
	}
	return el
}

// v4DocEnter reproduces .doc-enter (opacity 0->1, translateX 8px->0 on Smooth
// spring) when switching the active memory in the reading pane.
func v4DocEnter(el ui.Element, run uint64) ui.Element {
	p := el.Keyframes("v4-doc-enter", v4EnterFrames, ui.KeyframeOptions{Run: run})
	if p < 1 {
		el.Opacity(p)
		el.Transform(ui.Translation((1-p)*8, 0))
	}
	return el
}

func v4DropInTransition() ui.ElementTransition {
	anim := v4Bouncy
	return ui.ElementTransition{
		Animation: &anim,
		Enter:     &ui.Motion{Collapse: true, Y: -16, Scale: 0.98, Opacity: 0},
	}
}

func v4ModalTransition() ui.ElementTransition {
	anim := v4Bouncy
	return ui.ElementTransition{
		Animation: &anim,
		Enter:     &ui.Motion{Y: 10, Scale: 0.94, Opacity: 0},
	}
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
		now := float64(c.Now().UnixNano()) / 1e9
		if !reduce {
			c.AnimationFrame()
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
// pointer parallax. Stops animation frames when ReduceMotion is enabled.
func v4EmptyState(c *ui.Context, key, message, hint string) {
	reduce := c.Preferences().ReduceMotion
	wrap := ui.Column(c.Key(key)).Grow(1).MinHeight(180).FillWidth().AlignItems(ui.Center).Justify(ui.Center).Gap(sp3).Padding(sp5)
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
			now := float64(c.Now().UnixNano()) / 1e9
			if !reduce {
				c.AnimationFrame()
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
				if !reduce {
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
