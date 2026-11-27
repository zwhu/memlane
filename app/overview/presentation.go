package overview

import (
	"strings"

	"github.com/egoist/mygo/ui"
	kitui "github.com/zwhu/memlane/kit/ui"
)

const (
	sp1               = kitui.Sp1
	sp2               = kitui.Sp2
	sp3               = kitui.Sp3
	sp4               = kitui.Sp4
	sp5               = kitui.Sp5
	sp6               = kitui.Sp6
	sp8               = kitui.Sp8
	radius            = kitui.Radius
	radiusSmall       = kitui.RadiusSmall
	rowHeight2        = kitui.RowHeight2
	fsBody            = kitui.FSBody
	fsCaption         = kitui.FSCaption
	fsFootnote        = kitui.FSFootnote
	homeBody          = kitui.RoleBody
	homeHeading       = kitui.RoleHeading
	homeReadableMeta  = kitui.RoleReadableMeta
	homeMeta          = kitui.RoleMeta
	homeAction        = kitui.RoleAction
	homeStatValue     = kitui.RoleStatValue
	homeStatUnit      = kitui.RoleStatUnit
	homeActivityTitle = kitui.RoleActivityTitle
)

var defaultTokens = kitui.LightTokens()

func (a *ControllerOf[T]) tokens() *kitui.Tokens {
	if a.Theme != nil {
		return a.Theme.Tokens()
	}
	return defaultTokens
}

func (a *ControllerOf[T]) pageEnter(c *ui.Context, el ui.Element, key string, run uint64) {
	cold := a.State.ColdStartMotion
	if a.Theme != nil && a.Theme.ReduceMotion() {
		return
	}
	if a.PageEnter != nil {
		a.PageEnter(c, el, key, run, cold)
	} else if cold {
		a.pose.ColdStartPageEnter(c, el, key, run)
	} else {
		a.pose.PageEnter(c, el, key, run)
	}
}

func (a *ControllerOf[T]) staggerEnter(c *ui.Context, el ui.Element, key string, index int, run uint64, interactive bool) {
	cold := a.State.ColdStartMotion
	if a.Theme != nil && a.Theme.ReduceMotion() {
		return
	}
	if a.StaggerEnter != nil {
		a.StaggerEnter(c, el, key, index, run, interactive, cold)
	} else if cold {
		a.pose.ColdStartStaggerEnter(c, el, key, index, run, interactive)
	} else {
		a.pose.StaggerEnter(c, el, key, index, run, interactive)
	}
}

func MiddleProjectName(name string) string {
	r := []rune(name)
	if len(r) <= 10 {
		return name
	}
	return string(r[:4]) + "…" + string(r[len(r)-5:])
}

func KnowledgePreview(title string) string {
	// A source excerpt, not a newly inferred conclusion. Full text stays in
	// the existing reader; no model call or stored-content rewrite is needed.
	return strings.Join(strings.Fields(title), " ")
}

// Static placeholders match the final 2-line/3-line row geometry without
// suggesting an empty result or shifting layout on swap.
func loadingRows(c *ui.Context, tok *kitui.Tokens, label string, compact bool) {
	ui.Column(c).FillWidth().Label(label).Padding(sp1, sp2).Gap(0).Children(func() {
		count := 5
		if compact {
			count = 4
		}
		for n := 0; n < count; n++ {
			row := ui.Row(c).FillWidth().Padding(sp3, 0).Gap(sp3).AlignItems(ui.Center)
			if compact {
				row.MinHeight(52)
			} else {
				row.MinHeight(68)
			}
			row.Children(func() {
				if compact {
					ui.Box(c).Size(6, 6).Shrink(0).Radius(3).Background(tok.Fill2)
				} else {
					ui.Box(c).Size(22, 22).Shrink(0).Radius(radius).Background(tok.Fill2)
				}
				ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
					width := float32(360)
					if compact {
						width = 150
					}
					if n%2 == 1 {
						width *= .82
					}
					ui.Box(c).Width(width).MaxWidth(width).Height(12).Radius(radiusSmall).Background(tok.Fill2)
					if !compact {
						ui.Box(c).Width(width * .72).MaxWidth(width * .72).Height(12).Radius(radiusSmall).Background(tok.Fill2)
					}
					ui.Box(c).Width(width * .45).Height(9).Radius(3).Background(tok.Fill1)
				})
			})
		}
	})
}
