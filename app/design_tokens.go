package app

import (
	_ "embed"
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	kitui "github.com/zwhu/memlane/kit/ui"
)

type palette struct {
	kitui.Tokens
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
		panic(fmt.Sprintf("app: decode embedded brand bitmap: %v", err))
	}
	return bmp
}

func v4SolidSVG(color, paths string) *ui.SVG { return kitui.SolidSVG(color, paths) }

const (
	v4PathOverview = kitui.PathOverview
	v4PathCommon   = kitui.PathCommon
	v4PathProfile  = kitui.PathProfile
	v4PathFolder   = kitui.PathFolder
)

func makePalette(p palette) palette {
	p.SolidOverview = v4SolidSVG(p.AccentHex, v4PathOverview)
	p.SolidCommon = v4SolidSVG(p.AccentHex, v4PathCommon)
	p.SolidProfile = v4SolidSVG(p.AccentHex, v4PathProfile)
	p.SolidFolder = v4SolidSVG(p.AccentHex, v4PathFolder)
	p.BrandMark = sidebarBrandMark
	return p
}

var lightPalette = makePalette(palette{Tokens: *kitui.LightTokens()})
var darkPalette = makePalette(palette{Tokens: *kitui.DarkTokens()})

// tok is the palette of the current appearance. The app has one window and
// builds on one goroutine, so applyAppearance swaps it at the top of View
// before any page reads it.
var tok = &lightPalette

const (
	fsCaption   = kitui.FSCaption
	fsFootnote  = kitui.FSFootnote
	fsBody      = kitui.FSBody
	fsReading   = kitui.FSReading
	fsHeading   = kitui.FSHeading
	fsTitle     = kitui.FSTitle
	sp1         = kitui.Sp1
	sp2         = kitui.Sp2
	sp3         = kitui.Sp3
	sp4         = kitui.Sp4
	sp5         = kitui.Sp5
	sp6         = kitui.Sp6
	sp8         = kitui.Sp8
	sp10        = kitui.Sp10
	radius      = kitui.Radius
	radiusSmall = kitui.RadiusSmall
	radiusMenu  = kitui.RadiusMenu
	rowHeight1  = kitui.RowHeight1
	rowHeight2  = kitui.RowHeight2
	titlebarH   = kitui.TitlebarH
	sidebarW    = kitui.SidebarW
	listPaneW   = kitui.ListPaneW
	listHeaderH = kitui.ListHeaderH
	readerMaxW  = kitui.ReaderMaxW
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
	tokens := tok.Tokens
	tokens.Dark = dark
	return kitui.HomeTheme(&tokens)
}

func buildSidebarTheme() *ui.Theme { return kitui.SidebarTheme(&tok.Tokens) }

var sidebarSelectedTheme = buildSidebarSelectedTheme()

func buildSidebarSelectedTheme() *ui.Theme { return kitui.SidebarSelectedTheme(&tok.Tokens) }

var listTheme = buildListTheme()

func buildListTheme() *ui.Theme { return kitui.ListTheme(&tok.Tokens) }

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
	opts := kitui.MarkdownOptions(&tok.Tokens, size, a.md.CodeBlock)
	a.v4MD[key] = opts
	return opts
}

func buildTypography() map[homeTextRole]homeTypography {
	styles := kitui.BuildTypography(&tok.Tokens)
	result := make(map[homeTextRole]homeTypography, len(styles))
	for role, t := range styles {
		result[role] = homeTypography{t.Size, t.Weight, t.Color, t.LineHeight, t.Tracking}
	}
	return result
}

func recolorTypography() {
	homeType = buildTypography()
}
