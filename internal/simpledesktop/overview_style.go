package simpledesktop

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// Selected A, 3c1c845: style.css followed by suite.css. Keep these roles
// local to the overview so its correction does not restyle reading/settings.
const (
	homeOverviewListHeight = 280
	homeSidebarWidth       = 200
	homeHeaderHeight       = 48
	homePageInset          = pageInset
	homeMaxWidth           = 1320
	homeColumnGap          = 32
	homeCalendarMaxWidth   = 900
	homeGridGap            = 12
	// Calibrate the Core Text heading band against the fixed 2x A image.
	homeSectionOffset = 1
)

var shellBackground = ui.Hex("#ededee")
var shellBorder = ui.Hex("#dedee1")

var homeInk = ui.Hex("#273340")
var homeMuted = ui.Hex("#7b8998")
var homeBorder = ui.Hex("#e6ebef")
var panelShadowInk = ui.RGBA(0, 0, 0, 0.045)
var homeNavInk = ui.Hex("#536679")
var homeIconInk = ui.Hex("#7189a1")
var homeLinkInk = ui.Hex("#7e90a3")
var homeBackground = ui.Hex("#ffffff")

type homeTextRole uint8

const (
	homeBody homeTextRole = iota
	homeBrand
	homeWelcome
	homeEyebrow
	homeStatValue
	homeStatUnit
	homeStatLabel
	homeHeading
	homePanelHeading
	homeTitle
	homeMeta
	homeAction
	homeActivityTitle
	homePath
	homeTopTitle
	homeReadableMeta
)

type homeTypography struct {
	size                 float32
	weight               int
	color                ui.Color
	lineHeight, tracking float32
}

var homeType = map[homeTextRole]homeTypography{
	homeReadableMeta:  {12, 400, ui.Hex("#596675"), 20.0 / 12, 0},
	homeBody:          {14, 400, ui.Hex("#4b5563"), 22.0 / 14, 0},
	homeBrand:         {19, 650, homeInk, 1.25, -.5},
	homeWelcome:       {20, 600, homeInk, 1.25, -.5},
	homeEyebrow:       {10, 400, ui.Hex("#8795a3"), 1.2, 1.5},
	homeStatValue:     {27, 550, homeInk, 1.2, -1},
	homeStatUnit:      {11, 400, ui.Hex("#8793a0"), 1.2, 0},
	homeStatLabel:     {10, 400, ui.Hex("#8b96a2"), 1.2, 0},
	homeHeading:       {16, 600, homeInk, 1.5, 0},
	homePanelHeading:  {13, 600, homeInk, 1.2, -.15},
	homeTitle:         {14, 500, ui.Hex("#394855"), 22.0 / 14, 0},
	homeMeta:          {12, 400, ui.Hex("#667085"), 20.0 / 12, 0},
	homeAction:        {11, 400, homeLinkInk, 1.2, 0},
	homeActivityTitle: {11, 400, ui.Hex("#596a79"), 18.0 / 11, 0},
	homePath:          {10, 400, homeMuted, 1.5, 0},
	homeTopTitle:      {12, 500, ui.Hex("#53616f"), 1.2, 0},
}

func homeText(c *ui.Context, text string, role homeTextRole) ui.Element {
	t := homeType[role]
	e := ui.Text(c, text).Font("system-ui").FontSize(t.size).FontWeight(t.weight).TextColor(t.color).LineHeight(t.lineHeight).LetterSpacing(t.tracking)
	if role == homeStatValue {
		e.FontFeatures("tnum")
	}
	return e
}

var homeTheme = func() *ui.Theme {
	t := *ui.LightTheme()
	t.Font = "system-ui"
	t.Antialiased = true
	t.Text, t.TextMuted, t.Border = homeInk, homeMuted, homeBorder
	t.Background = homeBackground
	t.ScrollbarWidth = 3
	t.Scrollbar = ui.Hex("#c8d4e0")
	t.Focus = ui.Hex("#6b8db1")
	return &t
}()

// Keep the SDK sidebar's accent local: content controls retain their own theme.
var sidebarTheme = func() *ui.Theme {
	t := *homeTheme
	// Native sidebar labels need the system's normal font smoothing; the
	// web-reference antialiasing override remains local to content typography.
	t.Antialiased = false
	t.FontSize = 13
	t.Accent, t.AccentText = homeIconInk, homeInk
	t.SurfacePressed = ui.Hex("#d8e3ef")
	return &t
}()

func sidebarSelection(item ui.Element, selected bool) ui.Element {
	item.Cursor(ui.CursorPointer).Font("system-ui").FontSize(13).FontWeight(500).LetterSpacing(0).TextColor(homeInk)
	if selected {
		item.Background(ui.Hex("#d8e3ef")).TextColor(homeInk)
	}
	return item
}

func homeSVG(paths string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round">` + paths + `</svg>`))
}

var homeDocument = homeSVG(`<path d="M6 3h8l4 4v14H6Z"/><path d="M14 3v5h4M9 12h6M9 16h5"/>`)
var homeCommon = homeSVG(`<path d="M5 20V9h14v11M8 9V5h8v4M9 13h6M9 17h4"/>`)
var homeFolder = homeSVG(`<path d="M3 6h6l2-2h5l2 3h3v13H3Z"/>`)
var homeArrow = homeSVG(`<path d="m9 6 6 6-6 6"/>`)
var homeRefresh = homeSVG(`<path d="M20 9a8 8 0 1 0 0 6M20 3v6h-6"/>`)
var homeSearch = homeSVG(`<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/>`)
var homeSettings = homeSVG(`<path d="m9 3-1 3-3 1-2 5 2 5 3 1 1 3h6l1-3 3-1 2-5-2-5-3-1-1-3z"/><circle cx="12" cy="12" r="3"/>`)

func homeLink(c *ui.Context, title string, fn func()) ui.Element {
	return textButton(c, title, fn)
}

func homeHoverColor(b ui.Element, key string, normal, hover ui.Color) ui.Color {
	target := normal
	if b.Hovered() {
		target = hover
	}
	// AnimateColor handles interruption, re-entry and ReduceMotion itself;
	// these 150ms CSS-ease transitions never affect task state or geometry.
	return b.AnimateColor(key, target, ui.Animation{Duration: 150 * time.Millisecond, Ease: ui.CubicBezier(.25, .1, .25, 1)})
}

func homeNavSurface(b ui.Element, active bool) ui.Element {
	if active {
		return b.Background(ui.Hex("#d7e4f1"))
	}
	color := ui.Hex("#dee8f2")
	return b.Background(homeHoverColor(b, "nav-surface", color.Alpha(0), color))
}

func homeIconButton(c *ui.Context, label string, icon *ui.SVG, fn func()) ui.Element {
	return ui.Button(c, "").Cursor(ui.CursorPointer).Label(label).Tooltip(label).Children(func() { ui.Icon(c, icon).FontSize(14) }).OnClick(fn)
}
