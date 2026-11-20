package simpledesktop

import (
	"github.com/egoist/mygo/ui"
)

const (
	homeOverviewListHeight = 280
	homeSidebarWidth       = sidebarW
	homeHeaderHeight       = titlebarH
	homePageInset          = pageInset
	homeMaxWidth           = 1320
	homeColumnGap          = 32
	homeCalendarMaxWidth   = 900
	homeGridGap            = 12
	homeSectionOffset      = 1
)

var shellBackground = lightPalette.Sidebar
var shellBorder = lightPalette.Border

var homeInk = lightPalette.Text1
var homeMuted = lightPalette.Text3
var homeBorder = lightPalette.Border
var panelShadowInk = lightPalette.ShadowLift
var homeNavInk = lightPalette.Text2
var homeIconInk = lightPalette.Text2
var homeLinkInk = lightPalette.Accent
var homeBackground = lightPalette.Bg

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

var homeType = buildTypography()

func homeText(c *ui.Context, text string, role homeTextRole) ui.Element {
	t := homeType[role]
	e := ui.Text(c, text).Font("system-ui").FontSize(t.size).FontWeight(t.weight).TextColor(t.color).LineHeight(t.lineHeight).LetterSpacing(t.tracking)
	if role == homeStatValue {
		e.FontFeatures("tnum")
	}
	return e
}

// homeTheme and sidebarTheme follow the window appearance; applyAppearance
// rebuilds them from the V4 palette.
var homeTheme = buildHomeTheme(false)

var sidebarTheme = buildSidebarTheme()

func sidebarSelection(item ui.Element, selected bool, indicatorActive bool) ui.Element {
	item.Cursor(ui.CursorPointer).Font("system-ui").FontSize(fsBody).FontWeight(400).LetterSpacing(0).Radius(radius).TextColor(tok.Text1)
	v4ButtonPress(item, 0.98)
	if selected {
		bg := tok.AccentSoft
		if indicatorActive {
			bg = ui.Transparent
		}
		item.Background(bg).TextColor(tok.Accent).FontWeight(500)
	}
	return item
}

func homeSVG(paths string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">` + paths + `</svg>`))
}

var homeDocument = v4IconDoc
var homeCommon = v4IconCommon
var homeFolder = v4IconFolder
var homeArrow = v4IconChev
var homeRefresh = homeSVG(`<path d="M20 9a8 8 0 1 0 0 6M20 3v6h-6"/>`)
var homeSearch = v4IconSearch
var homeSettings = v4IconSettings

func homeLink(c *ui.Context, title string, fn func()) ui.Element {
	return textButton(c, title, fn)
}

func homeHoverColor(b ui.Element, key string, normal, hover ui.Color) ui.Color {
	target := normal
	if b.Hovered() {
		target = hover
	}
	return b.AnimateColor(key, target, v4Fast)
}

func homeNavSurface(b ui.Element, active bool) ui.Element {
	if active {
		return b.Background(tok.AccentSoft)
	}
	return b.Background(homeHoverColor(b, "nav-surface", ui.Transparent, tok.Hover))
}

func homeIconButton(c *ui.Context, label string, icon *ui.SVG, fn func()) ui.Element {
	b := ui.Button(c, "").Cursor(ui.CursorPointer).Label(label).Tooltip(label).Radius(radius).Children(func() { ui.Icon(c, icon).FontSize(14).TextColor(tok.Text2) }).OnClick(fn)
	v4ButtonPress(b, 0.96)
	return b
}
