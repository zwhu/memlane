package desktop

import (
	"github.com/egoist/mygo/ui"
	"math"
)

type graphCurve struct {
	Start, Control, End, Label ui.Point
	LabelWidth, LabelRight     float32
}

func layoutProjectGraph(nodes []projectGraphNode, width, viewport float32) ([]projectGraphNode, float32, float32, float32) {
	nodes = append([]projectGraphNode(nil), nodes...)
	nw := max(float32(84), 134*width/650)
	nh := max(float32(36), 49*width/650)
	counts := [2]int{}
	for _, n := range nodes {
		col := 0
		if !n.entity {
			col = 1
		}
		counts[col]++
	}
	rows := max(counts[0], counts[1])
	step := float32(86)
	if rows > 1 && rows <= 4 {
		step = min(float32(96), (viewport-64-nh)/float32(rows-1))
	} else if rows > 4 {
		step = max(float32(72), 86*width/650)
	}
	height := max(viewport, 64+nh+float32(max(0, rows-1))*step)
	left := nw/2 + max(float32(74), width*.16)
	right := width - nw/2 - 16
	indices := [2]int{}
	for i := range nodes {
		col := 0
		if !nodes[i].entity {
			col = 1
		}
		nodes[i].x = left
		if col == 1 {
			nodes[i].x = right
		}
		nodes[i].y = 32 + float32(indices[col])*step
		indices[col]++
	}
	return nodes, height, nw, nh
}
func graphPoint(x, y float32) ui.Point { return ui.Point{X: x, Y: y} }
func graphUnit(p ui.Point) ui.Point {
	length := float32(math.Hypot(float64(p.X), float64(p.Y)))
	if length == 0 {
		return graphPoint(1, 0)
	}
	return graphPoint(p.X/length, p.Y/length)
}

// Ray intersection with the actual rounded button bounds, not node centres.
func graphNodeBoundary(n projectGraphNode, toward ui.Point, w, h float32) ui.Point {
	cx, cy := n.x, n.y+h/2
	dx, dy := toward.X-cx, toward.Y-cy
	ax, ay := float32(math.Abs(float64(dx))), float32(math.Abs(float64(dy)))
	radius := float32(8)
	if n.entity {
		radius = h / 2
	}
	hw, hh := w/2, h/2
	sx, sy := float32(1), float32(1)
	if dx < 0 {
		sx = -1
	}
	if dy < 0 {
		sy = -1
	}
	var px, py float32
	if ax > 0 && ay*hw/ax <= hh-radius {
		px, py = hw, ay*hw/ax
	} else if ay > 0 && ax*hh/ay <= hw-radius {
		px, py = ax*hh/ay, hh
	} else {
		qx, qy := hw-radius, hh-radius
		a := ax*ax + ay*ay
		b := ax*qx + ay*qy
		disc := max(float32(0), b*b-a*(qx*qx+qy*qy-radius*radius))
		t := (b + float32(math.Sqrt(float64(disc)))) / max(a, float32(.001))
		px, py = t*ax, t*ay
	}
	return graphPoint(cx+sx*px, cy+sy*py)
}
func graphCurvePoint(q graphCurve, t float32) ui.Point {
	u := 1 - t
	return graphPoint(u*u*q.Start.X+2*u*t*q.Control.X+t*t*q.End.X, u*u*q.Start.Y+2*u*t*q.Control.Y+t*t*q.End.Y)
}
func projectGraphCurve(from, to projectGraphNode, w, h float32, index int, directed bool) graphCurve {
	a, b := graphPoint(from.x, from.y+h/2), graphPoint(to.x, to.y+h/2)
	same := float32(math.Abs(float64(a.X-b.X))) < 1
	control := graphPoint((a.X+b.X)/2, (a.Y+b.Y)/2)
	if same {
		control.X = max(float32(6), a.X-w/2-40-float32(index%4)*14)
	} else {
		control.Y += float32(index%3-1) * 16
	}
	start, end := graphNodeBoundary(from, control, w, h), graphNodeBoundary(to, control, w, h)
	q := graphCurve{Start: start, Control: control, End: end}
	if from.id == to.id {
		q.Start = graphNodeBoundary(from, graphPoint(control.X, a.Y-60), w, h)
		q.End = graphNodeBoundary(to, graphPoint(control.X, a.Y+60), w, h)
	}
	q.Label = graphCurvePoint(q, .5)
	if same {
		q.LabelWidth = max(float32(36), a.X-w/2-14)
		q.LabelRight = a.X - w/2 - 6
	} else {
		q.LabelWidth = max(float32(44), b.X-a.X-w-10)
	}
	return q
}
func paintProjectGraphCurve(p *ui.Painter, r ui.Rect, q graphCurve, label string, color ui.Color, stroke float32, directed bool, width float32) {
	var path ui.Path
	path.MoveTo(r.X+q.Start.X, r.Y+q.Start.Y).QuadTo(r.X+q.Control.X, r.Y+q.Control.Y, r.X+q.End.X, r.Y+q.End.Y)
	p.StrokePath(&path, stroke, color)
	if directed {
		d := graphUnit(graphPoint(q.End.X-q.Control.X, q.End.Y-q.Control.Y))
		var arrow ui.Path
		arrow.MoveTo(r.X+q.End.X-d.X*7+d.Y*3.5, r.Y+q.End.Y-d.Y*7-d.X*3.5).LineTo(r.X+q.End.X, r.Y+q.End.Y).LineTo(r.X+q.End.X-d.X*7-d.Y*3.5, r.Y+q.End.Y-d.Y*7+d.X*3.5)
		p.StrokePath(&arrow, stroke, color)
	}
	chars := []rune(label)
	limit := max(5, min(24, int(q.LabelWidth/5.2)))
	if len(chars) > limit {
		chars = append(chars[:limit-1], '…')
	}
	shown := string(chars)
	textWidth := float32(len(chars)) * 5.2
	x := max(float32(5), min(width-textWidth-5, q.Label.X-textWidth/2))
	if q.LabelRight > 0 {
		x = max(float32(5), min(x, q.LabelRight-textWidth))
	}
	y := q.Label.Y - 6
	p.Fill(ui.Rect{X: r.X + x - 2, Y: r.Y + y - 1, W: textWidth + 4, H: 14}, ui.Hex("#fafcff"), 2)
	p.Text(r.X+x, r.Y+y, shown, 9, design.Muted)
}
