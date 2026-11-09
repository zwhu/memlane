package desktop

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

// Frozen A revision 13: a hollow bulb, not a filled cloud. Geometry and
// stagger are stable; the 4.5s presentation cycle never advances detection.
var detectionBulb = func() [][2]float32 {
	mask := []string{"00001110000", "00111111100", "01100000110", "11000000011", "11000000011", "11000000011", "01100000110", "00110001100", "00011011000", "00011111000", "00011111000", "00001110000"}
	points := make([][2]float32, 0, 80)
	for y, row := range mask {
		for x, value := range row {
			if value == '1' {
				points = append(points, [2]float32{float32(x*11 + 12), float32(y*11 + 7)})
			}
		}
	}
	return points
}()

var detectionEase = ui.CubicBezier(.42, 0, .58, 1)

func detectionBlockPose(i int, elapsed float64, moving bool) (x, y, alpha float32) {
	target := detectionBulb[i]
	x, y, alpha = target[0], target[1], 1
	if !moving {
		return
	}
	phase := math.Mod(max(0, elapsed-float64(i)*.025), 4.5) / 4.5
	amount := float32(1)
	switch {
	case phase < .12:
	case phase < .47:
		amount = 1 - detectionEase(float32((phase-.12)/.35))
	case phase < .8:
		amount = 0
	default:
		amount = detectionEase(float32((phase - .8) / .2))
	}
	x += float32((i*29)%91-45) * amount
	y += float32((i*37)%91-45) * amount
	alpha = 1 - .65*amount
	return
}

func (a *App) detectionBlocks(c *ui.Context) {
	s := &a.firstLaunch
	moving := !s.DetectionFailed && !s.State.ReducedMotion && !c.Preferences().ReduceMotion
	start := s.ChangedAt
	failed := s.DetectionFailed
	ui.Box(c).Key("first-launch-blocks").Size(154, 154).Shrink(0).Margin(0, 0, 34, 0).Draw(func(p *ui.Painter, r ui.Rect) {
		elapsed := p.Now().Sub(start).Seconds()
		for i := range detectionBulb {
			x, y, alpha := detectionBlockPose(i, elapsed, moving)
			color := ui.Hex("#6e92b5")
			if (i+1)%3 == 0 {
				color = ui.Hex("#a7bed4")
			}
			if (i+1)%5 == 0 {
				color = ui.Hex("#c6d7e6")
			}
			if failed {
				color = design.Muted
			}
			color = color.Alpha(alpha)
			p.Fill(ui.Rect{X: r.X + x, Y: r.Y + y, W: 7, H: 7}, color, 1)
		}
		if moving {
			p.AnimationFrame()
		}
	})
}

func (a *App) firstLaunchDetectionView(c *ui.Context) {
	s := &a.firstLaunch
	ui.Column(c).Fill().Background(ui.Hex("#ffffff")).Children(func() {
		ui.Row(c).Shrink(0).Height(design.Toolbar).Padding(10, 24).Children(func() {
			ui.Spacer(c)
			quietButton(c, "稍后准备").Key("first-launch-skip").OnClick(func() { a.cancelFirstLaunch(true) })
		})
		ui.Column(c).Grow(1).Padding(60, 30, 60+design.Toolbar, 30).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			a.detectionBlocks(c)
			title := "正在检查本机环境"
			if s.DetectionFailed {
				title = "本机检查未完成"
			}
			ui.Text(c, title).FontSize(22).Bold()
			ui.Text(c, s.State.Message).Key("first-launch-check-message").FontSize(13).TextColor(design.Muted).TextAlign(ui.Center).MaxWidth(520).LineHeight(1.85).Margin(12, 0, 0, 0)
			if s.DetectionFailed {
				ui.Row(c).Gap(14).Margin(22, 0, 0, 0).Children(func() {
					quietButton(c, "稍后继续").OnClick(func() { a.cancelFirstLaunch(true) })
					firstLaunchPrimary(c, "重新检查").Key("first-launch-retry").OnClick(func() {
						if s.Startup {
							a.checkResumedFirstLaunch()
						} else {
							a.discoverFirstLaunch()
						}
					})
				})
			} else {
				ui.Text(c, "已有兼容组件将直接复用").FontSize(11).TextColor(design.Muted).Margin(28, 0, 0, 0)
			}
		})
	})
}

// Keep the first frame tied to the actual start, across individual checks.
func (s *FirstLaunchUI) beginDetection() {
	s.Detecting, s.DetectionFailed = true, false
	s.ChangedAt = time.Now()
}
