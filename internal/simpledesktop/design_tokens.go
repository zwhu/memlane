package simpledesktop

import (
	_ "embed"
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// Visual V4 design tokens (docs/design/visual-standards-v4-20261120.md).
// Pages read colors from the active palette instead of writing ui.Hex
// directly, so the light and dark appearances stay in one place.
type palette struct {
	Dark bool

	AccentHex                                               string
	Accent, AccentHover, AccentSoft, AccentSofter, OnAccent ui.Color

	Bg, BgElevated, Sidebar, SidebarTint, Fill1, Fill2, Hover, Pressed ui.Color
	Border, BorderStrong, SegThumb                                     ui.Color

	Text1, Text2, Text3, Text4 ui.Color
	TooltipBg, TooltipText     ui.Color

	OK, Danger, Warning ui.Color
	ShadowLift          ui.Color

	Heat [5]ui.Color

	// Accent-tinted solid icons for selected sidebar items.
	SolidOverview, SolidCommon, SolidProfile, SolidFolder *ui.SVG
	BrandMark                                             ui.ImageSource
}

//go:embed brand_icons/sidebar_brand.png
var sidebarBrandPNG []byte

var sidebarBrandMark ui.ImageSource = mustDecodeBrandBitmap(sidebarBrandPNG)

func mustDecodeBrandBitmap(data []byte) *ui.Bitmap {
	bmp, err := ui.DecodeBitmap(data)
	if err != nil {
		panic(fmt.Sprintf("simpledesktop: decode embedded brand bitmap: %v", err))
	}
	return bmp
}

func v4SolidSVG(color, paths string) *ui.SVG {
	return ui.MustParseSVG([]byte(fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="%s" stroke="%s" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">%s</svg>`,
		color, color, paths,
	)))
}

const (
	v4PathOverview = `<rect x="3" y="3" width="6" height="6" rx="1.5"/><rect x="11" y="3" width="6" height="6" rx="1.5"/><rect x="3" y="11" width="6" height="6" rx="1.5"/><rect x="11" y="11" width="6" height="6" rx="1.5"/>`
	v4PathCommon   = `<path d="M5 3.5h10a1 1 0 0 1 1 1v12l-6-3.2-6 3.2v-12a1 1 0 0 1 1-1z"/>`
	v4PathProfile  = `<circle cx="10" cy="7" r="3.25"/><path d="M4 16.5c.8-2.9 3.2-4.5 6-4.5s5.2 1.6 6 4.5z"/>`
	v4PathFolder   = `<path d="M2.75 5.5a1.5 1.5 0 0 1 1.5-1.5h3.4l1.6 1.75h6.5a1.5 1.5 0 0 1 1.5 1.5v7.25a1.5 1.5 0 0 1-1.5 1.5H4.25a1.5 1.5 0 0 1-1.5-1.5z"/>`
)

func makePalette(p palette) palette {
	p.SolidOverview = v4SolidSVG(p.AccentHex, v4PathOverview)
	p.SolidCommon = v4SolidSVG(p.AccentHex, v4PathCommon)
	p.SolidProfile = v4SolidSVG(p.AccentHex, v4PathProfile)
	p.SolidFolder = v4SolidSVG(p.AccentHex, v4PathFolder)
	p.BrandMark = sidebarBrandMark
	return p
}

var lightPalette = makePalette(palette{
	AccentHex: "#5E6AD2",
	Accent:    ui.Hex("#5E6AD2"), AccentHover: ui.Hex("#5260C8"), AccentSoft: ui.Hex("#5E6AD2").Alpha(.12), AccentSofter: ui.Hex("#5E6AD2").Alpha(.06), OnAccent: ui.Hex("#FFFFFF"),
	Bg: ui.Hex("#FFFFFF"), BgElevated: ui.Hex("#FFFFFF"), Sidebar: ui.Hex("#F4F4F6"), SidebarTint: ui.Hex("#F4F4F6").Alpha(.78), Fill1: ui.Hex("#F5F5F7"), Fill2: ui.Hex("#EDEDF0"), Hover: ui.RGBA(0, 0, 0, .04), Pressed: ui.RGBA(0, 0, 0, .07),
	Border: ui.Hex("#E6E6EA"), BorderStrong: ui.Hex("#D5D5DB"), SegThumb: ui.Hex("#FFFFFF"),
	Text1: ui.Hex("#1B1B1F"), Text2: ui.Hex("#55585F"), Text3: ui.Hex("#7A7D85"), Text4: ui.Hex("#A3A6AD"),
	TooltipBg: ui.Hex("#1B1B1F"), TooltipText: ui.Hex("#FFFFFF"),
	OK: ui.Hex("#2E9E5B"), Danger: ui.Hex("#D64545"), Warning: ui.Hex("#D08A2F"),
	ShadowLift: ui.RGBA(0, 0, 0, .07),
	Heat:       [5]ui.Color{ui.Hex("#EDEDF0"), ui.Hex("#5E6AD2").Alpha(.22), ui.Hex("#5E6AD2").Alpha(.42), ui.Hex("#5E6AD2").Alpha(.68), ui.Hex("#5E6AD2").Alpha(.95)},
})

var darkPalette = makePalette(palette{
	Dark:      true,
	AccentHex: "#7C86E8",
	Accent:    ui.Hex("#7C86E8"), AccentHover: ui.Hex("#8E97EE"), AccentSoft: ui.Hex("#7C86E8").Alpha(.18), AccentSofter: ui.Hex("#7C86E8").Alpha(.08), OnAccent: ui.Hex("#FFFFFF"),
	Bg: ui.Hex("#1C1C1F"), BgElevated: ui.Hex("#242428"), Sidebar: ui.Hex("#242428"), SidebarTint: ui.Hex("#202024").Alpha(.78), Fill1: ui.Hex("#26262A"), Fill2: ui.Hex("#2D2D32"), Hover: ui.RGBA(255, 255, 255, .05), Pressed: ui.RGBA(255, 255, 255, .08),
	Border: ui.Hex("#2E2E33"), BorderStrong: ui.Hex("#3A3A40"), SegThumb: ui.Hex("#3A3A40"),
	Text1: ui.Hex("#EDEDF0"), Text2: ui.Hex("#A8A9B0"), Text3: ui.Hex("#808189"), Text4: ui.Hex("#5C5D64"),
	TooltipBg: ui.Hex("#2F2F36"), TooltipText: ui.Hex("#EDEDF0"),
	OK: ui.Hex("#3FBF73"), Danger: ui.Hex("#EF6363"), Warning: ui.Hex("#E3A048"),
	ShadowLift: ui.RGBA(0, 0, 0, .32),
	Heat:       [5]ui.Color{ui.Hex("#2D2D32"), ui.Hex("#7C86E8").Alpha(.25), ui.Hex("#7C86E8").Alpha(.45), ui.Hex("#7C86E8").Alpha(.70), ui.Hex("#7C86E8").Alpha(.98)},
})

// tok is the palette of the current appearance. The app has one window and
// builds on one goroutine, so applyAppearance swaps it at the top of View
// before any page reads it.
var tok = &lightPalette

// V4 type scale (6 levels).
const (
	fsCaption  float32 = 11
	fsFootnote float32 = 12
	fsBody     float32 = 13
	fsReading  float32 = 14
	fsHeading  float32 = 16
	fsTitle    float32 = 22
)

// V4 spacing and geometry (4px base grid).
const (
	sp1         float32 = 4
	sp2         float32 = 8
	sp3         float32 = 12
	sp4         float32 = 16
	sp5         float32 = 20
	sp6         float32 = 24
	sp8         float32 = 32
	sp10        float32 = 40
	radius      float32 = 6
	radiusSmall float32 = 4
	radiusMenu  float32 = 8
	rowHeight1  float32 = 32
	rowHeight2  float32 = 54
	titlebarH   float32 = 52
	sidebarW    float32 = 220
	listPaneW   float32 = 360
	listHeaderH float32 = 36
	readerMaxW  float32 = 680
)

var appliedDark *bool

// applyAppearance selects the palette for the window's appearance and
// rebuilds the themes and legacy color roles derived from it.
func applyAppearance(dark bool) {
	if appliedDark != nil && *appliedDark == dark {
		return
	}
	appliedDark = &dark
	tok = &lightPalette
	if dark {
		tok = &darkPalette
	}
	ink, muted, border = tok.Text1, tok.Text3, tok.Border
	blue = tok.Fill1
	homeInk, homeMuted, homeBorder = tok.Text1, tok.Text3, tok.Border
	homeNavInk, homeIconInk, homeLinkInk = tok.Text2, tok.Text2, tok.Accent
	homeBackground = tok.Bg
	shellBackground, shellBorder = tok.Sidebar, tok.Border
	panelShadowInk = tok.ShadowLift
	homeTheme = buildHomeTheme(dark)
	sidebarTheme = buildSidebarTheme()
	sidebarSelectedTheme = buildSidebarSelectedTheme()
	listTheme = buildListTheme()
	recolorTypography()
}

func buildHomeTheme(dark bool) *ui.Theme {
	t := *ui.LightTheme()
	if dark {
		t = *ui.DarkTheme()
	}
	t.Font = "system-ui"
	t.FontSize = fsBody
	t.Radius = radius
	t.Antialiased = true
	t.Text, t.TextMuted, t.Border = tok.Text1, tok.Text3, tok.Border
	t.Background = tok.Bg
	t.Surface = tok.Fill1
	t.SurfaceHover = tok.Hover
	t.SurfacePressed = tok.Pressed
	t.Accent, t.AccentText = tok.Accent, tok.OnAccent
	t.Inverse, t.InverseText = tok.TooltipBg, tok.TooltipText
	t.ScrollbarWidth = 4
	t.Scrollbar = tok.BorderStrong
	t.Focus = tok.Accent
	return &t
}

func buildSidebarTheme() *ui.Theme {
	t := *homeTheme
	// Keep antialiased smoothing in sidebar to match V4 prototype weights.
	t.Antialiased = true
	t.FontSize = fsBody
	t.Radius = radius
	t.Accent = tok.Text2
	t.AccentText = tok.Accent
	t.SurfaceHover = tok.Hover
	t.SurfacePressed = tok.AccentSoft
	return &t
}

var sidebarSelectedTheme = buildSidebarSelectedTheme()

func buildSidebarSelectedTheme() *ui.Theme {
	t := *buildSidebarTheme()
	// Both focused and unfocused selected sidebar items draw their icon in Accent;
	// keep t.Text neutral so tooltips launched from the item keep normal contrast.
	t.Accent = tok.Accent
	t.AccentText = tok.Accent
	t.SurfacePressed = tok.AccentSoft
	return &t
}

var listTheme = buildListTheme()

func buildListTheme() *ui.Theme {
	t := *homeTheme
	t.Radius = radius
	t.Accent = tok.AccentSoft
	t.AccentText = tok.Text1
	t.SurfaceHover = tok.Hover
	t.SurfacePressed = tok.AccentSoft
	return &t
}

func (a *App) v4MarkdownOptions(size float32) *markdown.Options {
	if size <= 0 {
		size = fsReading
	}
	key := v4MDKey{dark: tok.Dark, size: size}
	if a.v4MD == nil {
		a.v4MD = map[v4MDKey]*markdown.Options{}
	}
	if opts, ok := a.v4MD[key]; ok {
		return opts
	}
	theme := &markdown.Theme{
		FontSize:            size,
		LineHeight:          1.8,
		Text:                tok.Text1,
		Muted:               tok.Text3,
		CodeSize:            0.9,
		CodeBlockBackground: tok.Fill1,
		CodeBlockBorder:     tok.Border,
	}
	opts := &markdown.Options{Theme: theme, CodeBlock: a.md.CodeBlock}
	a.v4MD[key] = opts
	return opts
}

func buildTypography() map[homeTextRole]homeTypography {
	// Weight 550 in CoreText matches browser antialiased 600 on PingFang SC + SF Pro.
	return map[homeTextRole]homeTypography{
		homeReadableMeta:  {fsCaption, 400, tok.Text3, 1.45, 0},
		homeMeta:          {fsCaption, 400, tok.Text3, 1.45, 0},
		homePath:          {fsCaption, 400, tok.Text3, 1.45, 0},
		homeEyebrow:       {fsCaption, 500, tok.Text3, 1.3, 0},
		homeStatLabel:     {fsCaption, 400, tok.Text3, 1.2, 0},
		homeStatUnit:      {fsFootnote, 400, tok.Text2, 1.2, 0},
		homeBody:          {fsFootnote, 400, tok.Text2, 1.65, 0},
		homeAction:        {fsFootnote, 400, tok.Accent, 1.2, 0},
		homeActivityTitle: {fsFootnote, 400, tok.Text1, 1.5, 0},
		homeTopTitle:      {fsBody, 550, tok.Text1, 1.2, 0},
		homeTitle:         {fsBody, 500, tok.Text1, 1.45, 0},
		homePanelHeading:  {fsBody, 550, tok.Text1, 1.3, 0},
		homeHeading:       {fsHeading, 550, tok.Text1, 1.35, -.2},
		homeBrand:         {fsHeading, 550, tok.Text1, 1.25, -.2},
		homeWelcome:       {fsHeading, 550, tok.Text1, 1.3, -.2},
		homeStatValue:     {fsTitle, 550, tok.Text1, 1.2, -.5},
	}
}

func recolorTypography() {
	homeType = buildTypography()
}
