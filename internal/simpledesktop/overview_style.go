package simpledesktop

import (
	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

const (
	homeOverviewListHeight = 280
	homeSidebarWidth       = sidebarW
	homeHeaderHeight       = titlebarH
	homePageInset          = pageInset
	homeMaxWidth           = 1320
	homeColumnGap          = 32
	homeCalendarMaxWidth   = 900
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

type homeTextRole = kitui.TextRole

const (
	homeBody          = kitui.RoleBody
	homeBrand         = kitui.RoleBrand
	homeWelcome       = kitui.RoleWelcome
	homeEyebrow       = kitui.RoleEyebrow
	homeStatValue     = kitui.RoleStatValue
	homeStatUnit      = kitui.RoleStatUnit
	homeStatLabel     = kitui.RoleStatLabel
	homeHeading       = kitui.RoleHeading
	homePanelHeading  = kitui.RolePanelHeading
	homeTitle         = kitui.RoleTitle
	homeMeta          = kitui.RoleMeta
	homeAction        = kitui.RoleAction
	homeActivityTitle = kitui.RoleActivityTitle
	homePath          = kitui.RolePath
	homeTopTitle      = kitui.RoleTopTitle
	homeReadableMeta  = kitui.RoleReadableMeta
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
	return (kitui.Typography{Size: t.size, Weight: t.weight, Color: t.color, LineHeight: t.lineHeight, Tracking: t.tracking}).Render(c, text, role)
}

// homeTheme and sidebarTheme follow the window appearance; applyAppearance
// rebuilds them from the V4 palette.
var homeTheme = buildHomeTheme(false)

var sidebarTheme = buildSidebarTheme()

var homeDocument = v4IconDoc
var homeCommon = v4IconCommon
var homeArrow = v4IconChev
var homeRefresh = kitui.IconRefresh24
var homeSearch = v4IconSearch
var homeSettings = v4IconSettings

func homeLink(c *ui.Context, title string, fn func()) ui.Element {
	return textButton(c, title, fn)
}

func sidebarSelection(item ui.Element, selected bool, indicatorActive bool) ui.Element {
	return kitui.SidebarSelection(item, &tok.Tokens, selected, indicatorActive)
}

func homeSVG(paths string) *ui.SVG {
	return kitui.HomeSVG(paths)
}

func homeHoverColor(b ui.Element, key string, normal, hover ui.Color) ui.Color {
	return kitui.HoverColor(b, key, normal, hover)
}

func homeNavSurface(b ui.Element, active bool) ui.Element {
	return kitui.NavSurface(b, &tok.Tokens, active)
}

func homeIconButton(c *ui.Context, label string, icon *ui.SVG, fn func()) ui.Element {
	return kitui.IconButton(c, &tok.Tokens, label, icon, fn)
}
