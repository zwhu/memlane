// Package ui provides V4 primitives for building MyGo interfaces.
package ui

import (
	mygoui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// Tokens holds the colors for one appearance.
type Tokens struct {
	Dark bool

	AccentHex                                               string
	Accent, AccentHover, AccentSoft, AccentSofter, OnAccent mygoui.Color

	Bg, BgElevated, Sidebar, SidebarTint, Fill1, Fill2, Hover, Pressed mygoui.Color
	Border, BorderStrong, SegThumb                                     mygoui.Color

	Text1, Text2, Text3, Text4 mygoui.Color
	TooltipBg, TooltipText     mygoui.Color

	OK, Danger, Warning mygoui.Color
	ShadowLift          mygoui.Color

	Heat [5]mygoui.Color
}

// LightTokens returns the V4 light palette as a caller-owned snapshot.
func LightTokens() *Tokens {
	return &Tokens{
		AccentHex:    "#5E6AD2",
		Accent:       mygoui.Hex("#5E6AD2"),
		AccentHover:  mygoui.Hex("#5260C8"),
		AccentSoft:   mygoui.Hex("#5E6AD2").Alpha(.12),
		AccentSofter: mygoui.Hex("#5E6AD2").Alpha(.06),
		OnAccent:     mygoui.Hex("#FFFFFF"),
		Bg:           mygoui.Hex("#FFFFFF"),
		BgElevated:   mygoui.Hex("#FFFFFF"),
		Sidebar:      mygoui.Hex("#F4F4F6"),
		SidebarTint:  mygoui.Hex("#F4F4F6").Alpha(.78),
		Fill1:        mygoui.Hex("#F5F5F7"),
		Fill2:        mygoui.Hex("#EDEDF0"),
		Hover:        mygoui.RGBA(0, 0, 0, .04),
		Pressed:      mygoui.RGBA(0, 0, 0, .07),
		Border:       mygoui.Hex("#E6E6EA"),
		BorderStrong: mygoui.Hex("#D5D5DB"),
		SegThumb:     mygoui.Hex("#FFFFFF"),
		Text1:        mygoui.Hex("#1B1B1F"),
		Text2:        mygoui.Hex("#55585F"),
		Text3:        mygoui.Hex("#7A7D85"),
		Text4:        mygoui.Hex("#A3A6AD"),
		TooltipBg:    mygoui.Hex("#1B1B1F"),
		TooltipText:  mygoui.Hex("#FFFFFF"),
		OK:           mygoui.Hex("#2E9E5B"),
		Danger:       mygoui.Hex("#D64545"),
		Warning:      mygoui.Hex("#D08A2F"),
		ShadowLift:   mygoui.RGBA(0, 0, 0, .07),
		Heat: [5]mygoui.Color{
			mygoui.Hex("#EDEDF0"),
			mygoui.Hex("#5E6AD2").Alpha(.22),
			mygoui.Hex("#5E6AD2").Alpha(.42),
			mygoui.Hex("#5E6AD2").Alpha(.68),
			mygoui.Hex("#5E6AD2").Alpha(.95),
		},
	}
}

// DarkTokens returns the V4 dark palette as a caller-owned snapshot.
func DarkTokens() *Tokens {
	return &Tokens{
		Dark:         true,
		AccentHex:    "#7C86E8",
		Accent:       mygoui.Hex("#7C86E8"),
		AccentHover:  mygoui.Hex("#8E97EE"),
		AccentSoft:   mygoui.Hex("#7C86E8").Alpha(.18),
		AccentSofter: mygoui.Hex("#7C86E8").Alpha(.08),
		OnAccent:     mygoui.Hex("#FFFFFF"),
		Bg:           mygoui.Hex("#1C1C1F"),
		BgElevated:   mygoui.Hex("#242428"),
		Sidebar:      mygoui.Hex("#242428"),
		SidebarTint:  mygoui.Hex("#202024").Alpha(.78),
		Fill1:        mygoui.Hex("#26262A"),
		Fill2:        mygoui.Hex("#2D2D32"),
		Hover:        mygoui.RGBA(255, 255, 255, .05),
		Pressed:      mygoui.RGBA(255, 255, 255, .08),
		Border:       mygoui.Hex("#2E2E33"),
		BorderStrong: mygoui.Hex("#3A3A40"),
		SegThumb:     mygoui.Hex("#3A3A40"),
		Text1:        mygoui.Hex("#EDEDF0"),
		Text2:        mygoui.Hex("#A8A9B0"),
		Text3:        mygoui.Hex("#808189"),
		Text4:        mygoui.Hex("#5C5D64"),
		TooltipBg:    mygoui.Hex("#2F2F36"),
		TooltipText:  mygoui.Hex("#EDEDF0"),
		OK:           mygoui.Hex("#3FBF73"),
		Danger:       mygoui.Hex("#EF6363"),
		Warning:      mygoui.Hex("#E3A048"),
		ShadowLift:   mygoui.RGBA(0, 0, 0, .32),
		Heat: [5]mygoui.Color{
			mygoui.Hex("#2D2D32"),
			mygoui.Hex("#7C86E8").Alpha(.25),
			mygoui.Hex("#7C86E8").Alpha(.45),
			mygoui.Hex("#7C86E8").Alpha(.70),
			mygoui.Hex("#7C86E8").Alpha(.98),
		},
	}
}

func TokensFor(dark bool) *Tokens {
	if dark {
		return DarkTokens()
	}
	return LightTokens()
}

// V4 type scale (6 levels).
const (
	FSCaption  float32 = 11
	FSFootnote float32 = 12
	FSBody     float32 = 13
	FSReading  float32 = 14
	FSHeading  float32 = 16
	FSTitle    float32 = 22
)

// V4 spacing and geometry (4px base grid).
const (
	Sp1         float32 = 4
	Sp2         float32 = 8
	Sp3         float32 = 12
	Sp4         float32 = 16
	Sp5         float32 = 20
	Sp6         float32 = 24
	Sp8         float32 = 32
	Sp10        float32 = 40
	Radius      float32 = 6
	RadiusSmall float32 = 4
	RadiusMenu  float32 = 8
	RowHeight1  float32 = 32
	RowHeight2  float32 = 54
	TitlebarH   float32 = 52
	SidebarW    float32 = 220
	ListPaneW   float32 = 360
	ListHeaderH float32 = 36
	ReaderMaxW  float32 = 680
)

// Insets belong to their container level; do not reuse overview styling for pane toolbars.
const (
	PageInset     float32 = 32
	PageTopInset  float32 = 24
	PaneInset     float32 = 24
	ModuleInset   float32 = 16
	SectionGap    float32 = 24
	ControlGap    float32 = 16
	ListItemInset float32 = 8
)

func HomeTheme(tok *Tokens) *mygoui.Theme {
	t := *mygoui.LightTheme()
	if tok.Dark {
		t = *mygoui.DarkTheme()
	}
	t.Font = "system-ui"
	t.FontSize = FSBody
	t.Radius = Radius
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

func SidebarTheme(tok *Tokens) *mygoui.Theme {
	t := *HomeTheme(tok)
	// Keep antialiased smoothing in sidebar to match V4 prototype weights.
	t.Antialiased = true
	t.FontSize = FSBody
	t.Radius = Radius
	t.Accent = tok.Text2
	t.AccentText = tok.Accent
	t.SurfaceHover = tok.Hover
	t.SurfacePressed = tok.AccentSoft
	return &t
}

func SidebarSelectedTheme(tok *Tokens) *mygoui.Theme {
	t := *SidebarTheme(tok)
	// Both focused and unfocused selected sidebar items draw their icon in Accent;
	// keep t.Text neutral so tooltips launched from the item keep normal contrast.
	t.Accent = tok.Accent
	t.AccentText = tok.Accent
	t.SurfacePressed = tok.AccentSoft
	return &t
}

func ListTheme(tok *Tokens) *mygoui.Theme {
	t := *HomeTheme(tok)
	t.Radius = Radius
	t.Accent = tok.AccentSoft
	t.AccentText = tok.Text1
	t.SurfaceHover = tok.Hover
	t.SurfacePressed = tok.AccentSoft
	return &t
}

func MarkdownOptions(tok *Tokens, size float32, codeBlock func(*mygoui.Context, *markdown.CodeBlockView)) *markdown.Options {
	if size <= 0 {
		size = FSReading
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
	return &markdown.Options{Theme: theme, CodeBlock: codeBlock}
}

func SidebarSelection(item mygoui.Element, tok *Tokens, selected bool, indicatorActive bool) mygoui.Element {
	item.Cursor(mygoui.CursorPointer).Font("system-ui").FontSize(FSBody).FontWeight(400).LetterSpacing(0).Radius(Radius).TextColor(tok.Text1)
	ButtonPress(item, 0.98)
	if selected {
		bg := tok.AccentSoft
		if indicatorActive {
			bg = mygoui.Transparent
		}
		item.Background(bg).TextColor(tok.Accent).FontWeight(500)
	}
	return item
}

func HoverColor(b mygoui.Element, key string, normal, hover mygoui.Color) mygoui.Color {
	target := normal
	if b.Hovered() {
		target = hover
	}
	return b.AnimateColor(key, target, Fast)
}

func NavSurface(b mygoui.Element, tok *Tokens, active bool) mygoui.Element {
	if active {
		return b.Background(tok.AccentSoft)
	}
	return b.Background(HoverColor(b, "nav-surface", mygoui.Transparent, tok.Hover))
}

func PaneHeading(c *mygoui.Context, tok *Tokens, title string, actions func()) mygoui.Element {
	return mygoui.Row(c).Height(32).Shrink(0).AlignItems(mygoui.Center).Children(func() {
		Text(c, tok, title, RoleHeading).FontSize(14).FontWeight(600)
		mygoui.Spacer(c)
		actions()
	})
}

func ListSurface(c *mygoui.Context, tok *Tokens, content func()) {
	mygoui.Column(c).FillWidth().Gap(0).Background(tok.Bg).Border(1, tok.Border).Radius(Radius).Padding(Sp2, Sp4).Children(content)
}

type TextRole uint8

const (
	RoleBody TextRole = iota
	RoleBrand
	RoleWelcome
	RoleEyebrow
	RoleStatValue
	RoleStatUnit
	RoleStatLabel
	RoleHeading
	RolePanelHeading
	RoleTitle
	RoleMeta
	RoleAction
	RoleActivityTitle
	RolePath
	RoleTopTitle
	RoleReadableMeta
)

type Typography struct {
	Size                 float32
	Weight               int
	Color                mygoui.Color
	LineHeight, Tracking float32
}

func BuildTypography(tok *Tokens) map[TextRole]Typography {
	table := typography(tok)
	result := make(map[TextRole]Typography, len(table))
	for role, value := range table {
		result[TextRole(role)] = value
	}
	return result
}

func typography(tok *Tokens) [RoleReadableMeta + 1]Typography {
	// Weight 550 in CoreText matches browser antialiased 600 on PingFang SC + SF Pro.
	return [RoleReadableMeta + 1]Typography{
		RoleReadableMeta:  {FSCaption, 400, tok.Text3, 1.45, 0},
		RoleMeta:          {FSCaption, 400, tok.Text3, 1.45, 0},
		RolePath:          {FSCaption, 400, tok.Text3, 1.45, 0},
		RoleEyebrow:       {FSCaption, 500, tok.Text3, 1.3, 0},
		RoleStatLabel:     {FSCaption, 400, tok.Text3, 1.2, 0},
		RoleStatUnit:      {FSFootnote, 400, tok.Text2, 1.2, 0},
		RoleBody:          {FSFootnote, 400, tok.Text2, 1.65, 0},
		RoleAction:        {FSFootnote, 400, tok.Accent, 1.2, 0},
		RoleActivityTitle: {FSFootnote, 400, tok.Text1, 1.5, 0},
		RoleTopTitle:      {FSBody, 550, tok.Text1, 1.2, 0},
		RoleTitle:         {FSBody, 500, tok.Text1, 1.45, 0},
		RolePanelHeading:  {FSBody, 550, tok.Text1, 1.3, 0},
		RoleHeading:       {FSHeading, 550, tok.Text1, 1.35, -.2},
		RoleBrand:         {FSHeading, 550, tok.Text1, 1.25, -.2},
		RoleWelcome:       {FSHeading, 550, tok.Text1, 1.3, -.2},
		RoleStatValue:     {FSTitle, 550, tok.Text1, 1.2, -.5},
	}
}

func Text(c *mygoui.Context, tok *Tokens, text string, role TextRole) mygoui.Element {
	var style Typography
	if role <= RoleReadableMeta {
		style = typography(tok)[role]
	}
	return style.Render(c, text, role)
}

func (t Typography) Render(c *mygoui.Context, text string, role TextRole) mygoui.Element {
	e := mygoui.Text(c, text).Font("system-ui").FontSize(t.Size).FontWeight(t.Weight).TextColor(t.Color).LineHeight(t.LineHeight).LetterSpacing(t.Tracking)
	if role == RoleStatValue {
		e.FontFeatures("tnum")
	}
	return e
}
