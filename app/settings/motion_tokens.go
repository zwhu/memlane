package settings

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func (a *Controller) onboardingPageEnter(c *ui.Context, el ui.Element, run uint64) {
	a.poseEnter.Apply(c, el, "onboarding", run,
		ui.Pose{Y: 8, Scale: 1, Fade: 1}, ui.Pose{Scale: 1}, 0, kitui.Smooth)
}

func onboardingMascot(c *ui.Context, mark ui.ImageSource, visible bool) ui.Element {
	reduce := c.Preferences().ReduceMotion
	return ui.Box(c).Size(76, 76).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		if visible && !reduce {
			p.AnimationFrame()
			r.Y -= 2 * float32(1-math.Cos(float64(p.Now().UnixMilli()%4000)*2*math.Pi/4000))
		}
		p.Image(mark, r, ui.Contain)
	})
}

func onboardingCompletionMark(tok *kitui.Tokens, c *ui.Context, started time.Time) {
	reduce := c.Preferences().ReduceMotion
	ok := tok.OK
	ui.Box(c).Size(64, 64).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		scale, drawn := float32(1), float32(1)
		if !reduce && !started.IsZero() {
			elapsed := p.Now().Sub(started)
			if elapsed < 700*time.Millisecond {
				p.AnimationFrame()
				scale = max(0, min(1.12, kitui.BouncyScale(max(0, elapsed.Seconds()))))
				progress := max(0, min(1, (elapsed-200*time.Millisecond).Seconds()/0.5))
				drawn = float32(kitui.SmoothPoseProgress(progress))
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

// The installer uses the larger four-block stage from the onboarding prototype.
func onboardingStageBlocks(tok *kitui.Tokens, c *ui.Context, visible bool) ui.Element {
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

func onboardingSelectionCheck(tok *kitui.Tokens, c *ui.Context, selected bool, corner float32) {
	border, fill := tok.BorderStrong, ui.Transparent
	if selected {
		border, fill = tok.Accent, tok.Accent
	}
	box := ui.Box(c).Size(18, 18).Shrink(0).Radius(corner).Border(1, border).Background(fill).
		AlignItems(ui.Center).Justify(ui.Center)
	if selected {
		box.Transition(ui.ElementTransition{Animation: &kitui.Bouncy, Enter: &ui.Motion{Scale: 0.74}})
		box.Children(func() { ui.Icon(c, kitui.IconCheck).FontSize(12).TextColor(tok.OnAccent) })
	}
}
