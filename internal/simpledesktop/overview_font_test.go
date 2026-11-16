package simpledesktop

import (
	"image"
	"runtime"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Verify the SDK rendering seam, including scope isolation, rather than
// snapshotting the homepage's individual style constants.
func TestOverviewAntialiasingKeepsGeometryAndSiblingRendering(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Core Text rendering check")
	}
	render := func(flat bool) (*image.RGBA, ui.Rect, ui.Rect) {
		theme := *ui.LightTheme()
		theme.Antialiased = flat
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Column(c).Fill().Background(ui.Hex("#ffffff")).Children(func() {
				ui.Themed(c, &theme).Height(70).FillWidth().Children(func() {
					ui.Text(c, "Memory connected 项目记忆").FontSize(22).FontWeight(400).TextColor(ui.Hex("#748395"))
				})
				ui.Text(c, "Sibling reading remains unchanged").FontSize(16).FontWeight(400).TextColor(ui.Hex("#748395"))
			})
		}, 400, 120)
		text, _ := tt.Find("Memory connected 项目记忆")
		sibling, _ := tt.Find("Sibling reading remains unchanged")
		name := "font-normal"
		if flat {
			name = "font-antialiased"
		}
		fixtureImage(t, tt, name)
		return tt.Image(), text, sibling
	}
	normal, nBox, nSibling := render(false)
	flat, fBox, fSibling := render(true)
	if nBox != fBox || nSibling != fSibling {
		t.Fatal("antialiasing changed layout geometry")
	}
	coverage := func(im *image.RGBA, box ui.Rect) uint64 {
		var sum uint64
		for y := int(box.Y); y < int(box.Y+box.H); y++ {
			for x := int(box.X); x < int(box.X+box.W); x++ {
				p := im.RGBAAt(x, y)
				sum += uint64(255-p.R) + uint64(255-p.G) + uint64(255-p.B)
			}
		}
		return sum
	}
	n, f := coverage(normal, nBox), coverage(flat, fBox)
	if f >= n {
		t.Fatalf("opt-in did not reduce Core Text smoothing coverage: normal=%d flat=%d", n, f)
	}
	for y := 70; y < 120; y++ {
		for x := 0; x < 400; x++ {
			if normal.RGBAAt(x, y) != flat.RGBAAt(x, y) {
				t.Fatal("homepage antialiasing leaked into sibling rendering")
			}
		}
	}
	t.Logf("same geometry, coverage normal=%d antialiased=%d, sibling unchanged", n, f)
}
