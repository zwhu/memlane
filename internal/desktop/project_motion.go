package desktop

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

type projectTilePose struct{ X, Y, Z, Rotation, Scale, Alpha float32 }
type projectStagePose struct {
	Tiles                                                                                    [24]projectTilePose
	SourceAlpha, CardAlpha, CardX, CardY, BookAlpha, BookScale, BookY, PageAngle, CheckAlpha float32
	SourceRotation, SourceScale, SourceX, CardScale                                          float32
}
type projectStageMotion struct {
	Key, Stage string
	Attempt    int64
	Changed    time.Time
	From       projectStagePose
	Reenter    bool
}

var projectCollectEase = ui.CubicBezier(.2, .7, .2, 1)
var projectBookEase = ui.CubicBezier(.2, .8, .2, 1)
var projectCSSEase = ui.CubicBezier(.25, .1, .25, 1)
var projectCSSIn = ui.CubicBezier(.42, 0, 1, 1)
var projectCSSInOut = ui.CubicBezier(.42, 0, .58, 1)

func projectLoopPulse(phase, peak float64) float32 {
	phase = math.Mod(phase, 1)
	if phase <= peak {
		return projectCSSInOut(float32(phase / peak))
	}
	return 1 - projectCSSInOut(float32((phase-peak)/(1-peak)))
}

func projectStageTarget(stage string) (p projectStagePose) {
	p.SourceAlpha, p.CardAlpha, p.CardY = 1, .16, 15
	p.SourceRotation, p.SourceScale, p.CardScale = -7, 1, 1
	for i := range p.Tiles {
		angle, radius := float64(i)*2.39996, 34+math.Sqrt(float64(i))*15
		p.Tiles[i] = projectTilePose{X: float32(math.Round(math.Cos(angle) * radius)), Y: float32(math.Round(math.Sin(angle) * radius * .66)), Z: float32(i % 4 * 8), Rotation: float32((i*13)%45 - 22), Scale: .75, Alpha: .65}
		if stage != "ready" && stage != "inviting" {
			p.Tiles[i] = projectTilePose{X: float32(i%6)*22 - 55, Y: float32(i/6)*22 - 33, Scale: 1, Alpha: 1}
		}
		if stage == "inviting" {
			p.Tiles[i] = projectTilePose{X: float32(i%6)*22 - 55, Y: float32(i/6)*22 - 33, Scale: 1, Alpha: 1}
		}
		if stage == "saving" {
			p.Tiles[i].X += 145
			p.Tiles[i].Y -= 10
			p.Tiles[i].Scale = .2
			p.Tiles[i].Alpha = 0
		}
		if stage == "indexing" || stage == "completed" {
			p.Tiles[i].Alpha = 0
		}
		if stage == "failed" || stage == "cancelled" || stage == "cancelling" || stage == "interrupted" {
			p.Tiles[i].Alpha = .35
		}
	}
	switch stage {
	case "inviting":
		p.CardAlpha, p.CardY = .6, 0
	case "starting":
		p.SourceRotation, p.SourceScale, p.SourceX = -2, .96, -10
	case "organizing":
		p.SourceAlpha, p.CardAlpha, p.CardY = .4, .6, 0
		p.SourceRotation, p.SourceScale = -3, .9
	case "saving", "indexing":
		p.SourceAlpha, p.CardAlpha, p.CardY = .18, 1, 0
		p.SourceScale = .8
	case "completed":
		p.SourceAlpha, p.CardAlpha, p.CardX, p.CardY, p.BookAlpha, p.BookScale, p.PageAngle, p.CheckAlpha = 0, 0, -125, 15, 1, 1, 0, 1
		p.SourceScale, p.CardScale = .7, .5
	}
	return
}
func projectInterpolate(a, b, t float32) float32 { return a + (b-a)*t }

func (m *projectStageMotion) update(key string, attempt int64, stage string, now time.Time) {
	if m.Key == key && m.Attempt == attempt && m.Stage == stage && !m.Reenter {
		return
	}
	from, _ := m.sample(now, false)
	fromReady := (m.Stage == "ready" || m.Stage == "inviting") && stage == "starting" && !m.Reenter
	if (m.Key != key || m.Attempt != attempt || m.Stage == "" || m.Reenter) && !fromReady {
		from = projectStageTarget(stage)
		m.Changed = now.Add(-2 * time.Second)
	} else {
		m.Changed = now
	}
	if stage == "completed" && m.Stage != "completed" && !m.Reenter {
		from.BookAlpha, from.BookScale, from.BookY, from.PageAngle, from.CheckAlpha = 0, .6, 20, 78, 0
	}
	m.Key, m.Attempt, m.Stage, m.From, m.Reenter = key, attempt, stage, from, false
}

// Pure geometry sampling; elapsed time never mutates a task or writes data.
func (m projectStageMotion) sample(now time.Time, reduced bool) (p projectStagePose, animate bool) {
	p = projectStageTarget(m.Stage)
	if reduced || m.Changed.IsZero() || m.Stage == "failed" || m.Stage == "cancelled" || m.Stage == "cancelling" || m.Stage == "interrupted" {
		return p, false
	}
	elapsed := max(0, now.Sub(m.Changed).Seconds())
	duration := .3
	stagger := 0.0
	switch m.Stage {
	case "starting":
		duration, stagger = .85, .025
	case "saving":
		duration, stagger = .8, .03
	case "inviting":
		duration, stagger = .7, .02
	case "completed":
		duration = .9
	}
	global := projectBookEase(float32(min(1, elapsed/duration)))
	for i := range p.Tiles {
		t := float32(min(1, max(0, (elapsed-float64(i)*stagger)/duration)))
		if m.Stage == "saving" {
			t = projectCSSIn(t)
		} else {
			t = projectCollectEase(t)
		}
		a, b := m.From.Tiles[i], p.Tiles[i]
		p.Tiles[i] = projectTilePose{X: projectInterpolate(a.X, b.X, t), Y: projectInterpolate(a.Y, b.Y, t), Z: projectInterpolate(a.Z, b.Z, t), Rotation: projectInterpolate(a.Rotation, b.Rotation, t), Scale: projectInterpolate(a.Scale, b.Scale, t), Alpha: projectInterpolate(a.Alpha, b.Alpha, t)}
		if m.Stage == "ready" {
			wave := projectLoopPulse((elapsed+float64(i)*.22)/6, .5)
			p.Tiles[i].Y -= wave * 7
			p.Tiles[i].Rotation += wave * 4
			p.Tiles[i].Scale += wave * .05
		}
		if m.Stage == "organizing" {
			wave := projectLoopPulse((elapsed+float64(i)*.09)/1.7, .45)
			p.Tiles[i].Y -= wave * 4
			p.Tiles[i].Scale *= 1 - wave*.14
			p.Tiles[i].Alpha *= 1 - wave*.6
		}
	}
	opacity := projectCSSEase(float32(min(1, elapsed/.5)))
	source := projectCSSEase(float32(min(1, elapsed/.6)))
	card := projectBookEase(float32(min(1, elapsed/.7)))
	p.SourceAlpha = projectInterpolate(m.From.SourceAlpha, p.SourceAlpha, opacity)
	p.SourceRotation = projectInterpolate(m.From.SourceRotation, p.SourceRotation, source)
	p.SourceScale = projectInterpolate(m.From.SourceScale, p.SourceScale, source)
	p.SourceX = projectInterpolate(m.From.SourceX, p.SourceX, source)
	p.CardScale = projectInterpolate(m.From.CardScale, p.CardScale, card)
	p.CardAlpha = projectInterpolate(m.From.CardAlpha, p.CardAlpha, opacity)
	p.CardX = projectInterpolate(m.From.CardX, p.CardX, card)
	p.CardY = projectInterpolate(m.From.CardY, p.CardY, card)
	p.BookAlpha = projectInterpolate(m.From.BookAlpha, p.BookAlpha, global)
	p.BookScale = projectInterpolate(m.From.BookScale, p.BookScale, global)
	p.BookY = projectInterpolate(m.From.BookY, p.BookY, global)
	p.PageAngle = projectInterpolate(m.From.PageAngle, p.PageAngle, global)
	if m.Stage == "completed" {
		body := projectBookEase(float32(min(1, elapsed/.85)))
		p.BookAlpha = projectInterpolate(m.From.BookAlpha, 1, body)
		p.BookScale = projectInterpolate(m.From.BookScale, 1, body)
		p.BookY = projectInterpolate(m.From.BookY, 0, body)
		p.CheckAlpha = ui.EaseOut(float32(min(1, max(0, (elapsed-.55)/.35))))
	}
	animate = m.Stage == "ready" || m.Stage == "starting" || m.Stage == "organizing" || m.Stage == "indexing" || elapsed < duration+stagger*23
	return
}

func projectShapeRect(p *ui.Painter, r ui.Rect, fill, stroke ui.Color, radius float32) {
	p.Fill(r, fill, radius)
	p.Stroke(r, stroke, radius, 1)
}

func drawProjectStage(p *ui.Painter, r ui.Rect, pose projectStagePose, stage string, elapsed float64, reduced bool) {
	// A short stage crops the decorative floor, as the fixed prototype does.
	// Fitting the entire 280-high canvas made its cards and labels too small.
	scale := min(1, r.W/560)
	ox, oy := r.X+(r.W-560*scale)/2, r.Y+(r.H-280*scale)/2
	point := func(x, y float32) (float32, float32) { return ox + x*scale, oy + y*scale }
	rect := func(x, y, w, h float32) ui.Rect {
		x, y = point(x, y)
		return ui.Rect{X: x, Y: y, W: w * scale, H: h * scale}
	}
	line := func(x, y, w, h float32, color ui.Color) { p.Fill(rect(x, y, w, h), color, scale) }
	// Floor/grid and source layers match the fixed logical 560×280 scene.
	p.FillRadialGradient(rect(110, 33, 340, 240), ui.RadialGradient{Center: ui.Point{X: .5, Y: .5}, Radius: 170 * scale, Stops: []ui.GradientStop{{At: 0, Color: ui.Hex("#dce9f5").Alpha(.32)}, {At: 1, Color: ui.Transparent}}}, 0)
	for i := 0; i < 11; i++ {
		line(140+float32(i)*23, 179, 1, 50, ui.Hex("#adc3d7").Alpha(.12))
	}
	for i := 0; i < 4; i++ {
		line(140, 179+float32(i)*15, 230, 1, ui.Hex("#adc3d7").Alpha(.12))
	}
	floor := rect(106, 226, 358, 25)
	if r.H < 280*scale {
		floor.Y = r.Y + r.H - 29 - 25*scale
	}
	p.FillRadialGradient(floor, ui.RadialGradient{Radius: 150 * scale, Stops: []ui.GradientStop{{Color: ui.Hex("#718ba4").Alpha(.12)}, {Color: ui.Transparent, At: 1}}}, 12*scale)
	if pose.SourceAlpha > 0 {
		sx, sy := point(62, 148)
		p.Transform(ui.Rotation(pose.SourceRotation).Then(ui.Scaling(pose.SourceScale, pose.SourceScale)).Then(ui.Translation(pose.SourceX*scale, 0)), sx, sy, func() {
			a := pose.SourceAlpha
			for i := 2; i > 0; i-- {
				p.Fill(rect(17+float32(i)*5, 95+float32(i)*7, 90, 106), ui.Hex("#eaf0f7").Alpha(a*.7), 7*scale)
			}
			projectShapeRect(p, rect(17, 95, 90, 106), ui.Hex("#f8fbfe").Alpha(a), ui.Hex("#bccdde").Alpha(a), 7*scale)
			folder := (&ui.Path{})
			x, y := point(31, 112)
			folder.MoveTo(x, y+4*scale).LineTo(x+9*scale, y+4*scale).LineTo(x+12*scale, y+8*scale).LineTo(x+25*scale, y+8*scale).LineTo(x+25*scale, y+23*scale).LineTo(x, y+23*scale).Close()
			p.StrokePath(folder, 1.5*scale, ui.Hex("#6c879f").Alpha(a))
			for i, w := range []float32{47, 34, 42} {
				line(30, 145+float32(i)*7, w, 2, ui.Hex("#d0dce8").Alpha(a))
			}
			x, y = point(30, 182)
			p.Text(x, y, "当前仓库", 9*scale, ui.Hex("#7a8c9c").Alpha(a))
		})
	}
	for _, tile := range pose.Tiles {
		if tile.Alpha <= 0 {
			continue
		}
		project := func(x, y float32) (float32, float32) {
			x, y = projectTilePoint(tile, x, y)
			return point(x, y)
		}
		face := func(points ...ui.Point) *ui.Path {
			path := &ui.Path{}
			for i, pt := range points {
				x, y := project(pt.X, pt.Y)
				if i == 0 {
					path.MoveTo(x, y)
				} else {
					path.LineTo(x, y)
				}
			}
			return path.Close()
		}
		front := face(ui.Point{}, ui.Point{X: 14}, ui.Point{X: 14, Y: 14}, ui.Point{Y: 14})
		x, y := project(2, 3)
		p.Fill(ui.Rect{X: x, Y: y, W: 14 * scale * tile.Scale, H: 14 * scale * tile.Scale}, ui.Hex("#597991").Alpha(tile.Alpha*.06), scale)
		p.FillPathGradient(front, ui.LinearGradient{Angle: 135, From: ui.Hex("#c2d4e4").Alpha(tile.Alpha), To: ui.Hex("#8caac4").Alpha(tile.Alpha)})
		p.StrokePath(front, scale*.7, ui.Hex("#8aa5bd").Alpha(tile.Alpha))
		p.FillPath(face(ui.Point{}, ui.Point{X: 5, Y: -5}, ui.Point{X: 19, Y: -5}, ui.Point{X: 14}), ui.Hex("#dce7f1").Alpha(tile.Alpha))
		p.FillPath(face(ui.Point{X: 14}, ui.Point{X: 19, Y: -5}, ui.Point{X: 19, Y: 9}, ui.Point{X: 14, Y: 14}), ui.Hex("#7995ae").Alpha(tile.Alpha))
	}
	if stage == "starting" && !reduced {
		alpha := .15 + .75*projectLoopPulse(elapsed/1.3, .5)
		for i := 0; i < 4; i++ {
			line(146+float32(i)*23, 154, 16, 2, ui.Hex("#a5bdd2").Alpha(alpha))
		}
	}
	if pose.CardAlpha > 0 {
		cx, cy := point(480+pose.CardX, 142+pose.CardY)
		p.Transform(ui.Scaling(pose.CardScale, pose.CardScale), cx, cy, func() {
			for i, label := range []string{"项目约定", "系统架构", "关键模块"} {
				xOffset := []float32{0, -10, 4}[i]
				x, y := 412+pose.CardX+xOffset, 70+pose.CardY+float32(i)*51
				rx, ry := point(x+68, y+21)
				p.Transform(ui.Rotation([]float32{2, -3, 1}[i]), rx, ry, func() {
					projectShapeRect(p, rect(x, y, 136, 42), ui.Hex("#ffffff").Alpha(pose.CardAlpha), ui.Hex("#c9d8e6").Alpha(pose.CardAlpha), 5*scale)
					iconColor := ui.Hex("#6e8ca6").Alpha(pose.CardAlpha)
					iconPath := func(points ...ui.Point) *ui.Path {
						path := &ui.Path{}
						for n, q := range points {
							px, py := point(x+10+q.X, y+14+q.Y)
							if n == 0 {
								path.MoveTo(px, py)
							} else {
								path.LineTo(px, py)
							}
						}
						return path
					}
					if i == 0 {
						p.StrokePath(iconPath(ui.Point{X: 7}, ui.Point{X: 14, Y: 4}, ui.Point{X: 7, Y: 8}, ui.Point{Y: 4}).Close(), scale, iconColor)
						for _, dy := range []float32{4, 7} {
							p.StrokePath(iconPath(ui.Point{Y: 4 + dy}, ui.Point{X: 7, Y: 8 + dy}, ui.Point{X: 14, Y: 4 + dy}), scale, iconColor)
						}
					} else if i == 1 {
						for _, q := range []ui.Point{{}, {X: 9}, {Y: 9}, {X: 9, Y: 9}} {
							p.Stroke(rect(x+10+q.X, y+14+q.Y, 5, 5), iconColor, scale, scale)
						}
					} else {
						p.StrokePath(iconPath(ui.Point{Y: 2}, ui.Point{X: 5, Y: 2}, ui.Point{X: 7, Y: 5}, ui.Point{X: 14, Y: 5}, ui.Point{X: 14, Y: 13}, ui.Point{Y: 13}).Close(), scale, iconColor)
					}
					tx, ty := point(x+30, y+26)
					p.Text(tx, ty, label, 10*scale, ui.Hex("#6e8ca6").Alpha(pose.CardAlpha))
					line(x+119, y+18, 5, 5, ui.Hex("#a2bbd2").Alpha(pose.CardAlpha))
					if stage == "indexing" && !reduced {
						scan := projectCSSInOut(float32(math.Mod(max(0, elapsed-float64(i)*.25)/1.6, 1)))
						line(x+8+scan*110, y+4, 13, 34, ui.Hex("#e3edf7").Alpha(.6*pose.CardAlpha))
					}
				})
			}
		})
	}
	if pose.BookAlpha > 0 {
		bookScale := pose.BookScale
		center, y := float32(263), float32(96)+pose.BookY
		for side := -1; side <= 1; side += 2 {
			pagePoint := func(px, py float32) (float32, float32) {
				angle := float64(pose.PageAngle) * math.Pi / 180
				depth := float32(math.Sin(angle)) * px * bookScale
				perspective := 600 / (600 - depth)
				x := float32(side) * px * bookScale * float32(math.Cos(angle))
				return point(center+x*perspective, y+51*bookScale+(py-51)*bookScale*perspective-x*float32(side)*.14054)
			}
			page := &ui.Path{}
			x0, y0 := pagePoint(0, 0)
			x1, y1 := pagePoint(65, 0)
			cx, cy := pagePoint(71, 0)
			x2, y2 := pagePoint(71, 6)
			page.MoveTo(x0, y0).LineTo(x1, y1).QuadTo(cx, cy, x2, y2)
			x1, y1 = pagePoint(71, 97)
			cx, cy = pagePoint(71, 102)
			x2, y2 = pagePoint(66, 102)
			page.LineTo(x1, y1).QuadTo(cx, cy, x2, y2)
			x1, y1 = pagePoint(0, 102)
			page.LineTo(x1, y1)
			page.Close()
			p.FillPathGradient(page, ui.LinearGradient{Angle: 145, From: ui.Hex("#f8fbfe").Alpha(pose.BookAlpha), To: ui.Hex("#deebf5").Alpha(pose.BookAlpha)})
			p.StrokePath(page, scale, ui.Hex("#7c9bb6").Alpha(pose.BookAlpha))
			for j, w := range []float32{40, 27, 40} {
				px := float32(11)
				if side < 0 {
					px = 60 - w
				}
				x0, y0 := pagePoint(px, 24+float32(j)*15)
				x1, y1 := pagePoint(px+w, 24+float32(j)*15)
				p.StrokePath((&ui.Path{}).MoveTo(x0, y0).LineTo(x1, y1), 3*scale*bookScale, ui.Hex("#9eb8cf").Alpha(pose.BookAlpha))
			}
		}
		if pose.CheckAlpha > 0 {
			cx, cy := point(331, 96+pose.BookY)
			p.Fill(ui.Rect{X: cx - 14*scale, Y: cy - 14*scale, W: 29 * scale, H: 29 * scale}, ui.Hex("#f9fcff").Alpha(pose.CheckAlpha), 15*scale)
			p.Stroke(ui.Rect{X: cx - 14*scale, Y: cy - 14*scale, W: 29 * scale, H: 29 * scale}, ui.Hex("#9bb6cc").Alpha(pose.CheckAlpha), 15*scale, scale)
			p.StrokePath((&ui.Path{}).MoveTo(cx-7*scale, cy).LineTo(cx-2*scale, cy+5*scale).LineTo(cx+7*scale, cy-6*scale), 2*scale, ui.Hex("#5b7b96").Alpha(pose.CheckAlpha))
		}
	}
}

func projectTilePoint(tile projectTilePose, x, y float32) (float32, float32) {
	angle := float64(tile.Rotation) * math.Pi / 180
	cos, sin := float32(math.Cos(angle)), float32(math.Sin(angle))
	x, y = (x-7)*tile.Scale, (y-7)*tile.Scale
	projection := 750 / (750 - tile.Z)
	return 263 + (tile.X+7+x*cos-y*sin)*projection, 148 + (tile.Y+7+x*sin+y*cos)*projection
}

func (a *App) projectStage(c *ui.Context) {
	s := &a.projectOrganizer
	stage, key, attempt := "ready", "ready", int64(0)
	if s.Task != nil {
		stage, key, attempt = projectTaskVisualState(s.Task), s.Task.ID, s.Task.Attempt
		if s.Task.IndexRecovery != nil {
			key += ":index:" + s.Task.IndexRecovery.ID
		}
		if stage == "queued" {
			stage = "starting"
		}
	}
	if s.Task == nil && s.HistoryError != "" {
		stage, key = "failed", "historical-selection-failed"
	}
	width, height := c.Size()
	stageHeight := float32(280)
	if width < 1100 || height < 740 {
		stageHeight = 150
	}
	a.drawProjectScene(c, "project-knowledge-stage", key, attempt, stage, &s.Motion, stageHeight)
}

func (a *App) projectKnowledgePreview(c *ui.Context) {
	stage := "ready"
	if a.projectPreviewInviting {
		stage = "inviting"
	}
	width, height := c.Size()
	stageHeight := float32(300)
	if width < 1100 || height < 740 {
		stageHeight = 210
	}
	ui.Column(c).Key("project-empty-preview").Grow(1).Padding(24).Gap(16).Children(func() {
		ui.Text(c, "让项目知识逐步积累").FontSize(design.Title).Bold()
		ui.Text(c, "从日常会话积累内容，也可以主动整理现有仓库。").FontSize(11).TextColor(design.Muted)
		a.drawProjectScene(c, "project-empty-preview-stage", "preview:"+a.scope, 0, stage, &a.projectPreview, stageHeight)
		ui.Text(c, "项目整理是可选的").FontSize(11).TextColor(design.Muted)
		ui.Text(c, "你也可以直接开始新会话，让后续提交的知识逐步积累。").FontSize(10).TextColor(design.Muted)
	})
}

func (a *App) drawProjectScene(c *ui.Context, elementKey, key string, attempt int64, stage string, state *projectStageMotion, stageHeight float32) {
	state.update(key, attempt, stage, c.Now())
	motion := *state
	reduced := a.windowHidden || a.firstLaunch.State.ReducedMotion || c.Preferences().ReduceMotion
	scene := ui.Box(c).Key(elementKey).Height(stageHeight).MinHeight(150).Shrink(0)
	rx, ry := float32(0), float32(0)
	x, y, over := scene.PointerPosition()
	bounds := scene.LayoutBounds()
	if !reduced && over && bounds.W > 0 && bounds.H > 0 {
		rx, ry = -min(.5, max(-.5, y/bounds.H-.5))*7, min(.5, max(-.5, x/bounds.W-.5))*10
	}
	if !reduced {
		animation := ui.Animation{Duration: 300 * time.Millisecond, Ease: ui.EaseOut}
		rx, ry = scene.AnimateTo("tilt-x", rx, animation), scene.AnimateTo("tilt-y", ry, animation)
	}
	scene.Perspective(750).RotateX(rx).RotateY(ry).Draw(func(p *ui.Painter, r ui.Rect) {
		started := projectDrawStart()
		pose, animate := motion.sample(p.Now(), reduced)
		drawProjectStage(p, r, pose, stage, max(0, p.Now().Sub(motion.Changed).Seconds()), reduced)
		if animate {
			p.AnimationFrame()
		}
		a.recordProjectDraw(stage, p.Now(), started, animate, reduced, rx, ry, [4]float32{r.X, r.Y, r.W, r.H})
	})
}
