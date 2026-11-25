package graph

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/shell"
	kitui "github.com/zwhu/memlane/kit/ui"
)

func (g *Controller) toolbar(c *ui.Context, tok *kitui.Tokens) {
	p, ok := g.project()
	code := g.Mode == ModeCode
	ui.Row(c).Shrink(0).Padding(kitui.Sp3, kitui.Sp4, kitui.Sp2, kitui.Sp4).Gap(kitui.Sp3).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
		selected := 0
		if code {
			selected = 1
		}
		kitui.Segmented(c, tok, "graph-mode-seg", "图谱类型", &selected, []string{"实体关系", "代码图谱"}, func() {
			g.ResetView([]string{ModeEntity, ModeCode}[selected])
			if selected == 1 && ok && !p.CodeEnabled {
				return
			}
			g.Load()
		})
		ui.Spacer(c.Key("graph-toolbar-spacer"))
		if !code && ok && flag(g.HasControl) && g.OnPreviewToolGraph != nil {
			kitui.SmallButton(c, tok, "创建工具图谱", nil, func() { g.OnPreviewToolGraph(p.ID) })
		}
		if code && ok && !p.CodeEnabled {
			return
		}
		if code && ok {
			kitui.SmallButton(c, tok, "关闭代码索引", nil, func() { runToggle(g.OnToggleCode, false) })
			kitui.SmallPrimaryButton(c, tok, "更新代码图谱", func() { run(g.OnIndexCode) }).Disabled(g.CodeIndexBlocked())
		}
		kitui.SmallButton(c.Key("graph-refresh"), tok, "刷新图谱", kitui.IconRefresh, func() {
			g.Load()
			run(g.OnRestoreCodeState)
		}).Disabled(g.Loading)
	})
	if !code && ok && g.RenderPendingHint != nil {
		g.RenderPendingHint(c, p)
	}
}

func (g *Controller) codeGraphEmpty(c *ui.Context, tok *kitui.Tokens) {
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Background(tok.Bg).Padding(kitui.Sp8).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		ui.Column(c).MaxWidth(480).FillWidth().AlignItems(ui.Center).Gap(kitui.Sp3).Children(func() {
			ui.Column(c).FillWidth().MaxHeight(200).AlignItems(ui.Center).Children(func() {
				kitui.EmptyState(c, tok, "code-graph-empty", "代码图谱尚未开启", "从文件、函数与调用关系，了解这个项目的代码结构。首次开启会建立代码索引，后续由你手动更新。")
			})
			label := "开启代码图谱"
			if g.CodeBusy {
				label = "正在开启…"
			}
			ui.PrimaryButton(c, label).Disabled(g.CodeIndexBlocked()).OnClick(func() { runToggle(g.OnToggleCode, true) })
			if g.Error != "" {
				ui.Text(c, g.Error).Font("system-ui").FontSize(kitui.FSFootnote).TextColor(tok.Danger).MaxLines(4).TextAlign(ui.Center)
			}
			// Restored index activity is cached globally and is not scoped to this
			// disabled project. Do not present it as this project's enable result.
			if g.CodeStatus != "" && !strings.HasPrefix(g.CodeStatus, "官方代码索引状态：") {
				kitui.Text(c, tok, g.CodeStatus, kitui.RoleMeta).MaxLines(4).TextAlign(ui.Center)
			}
		})
	})
	ui.Row(c).Label("图谱状态栏").Height(48).Shrink(0).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).AlignItems(ui.Center).Padding(0, kitui.Sp4).Children(func() {
		kitui.Text(c, tok, "仅当前项目 · 由你选择是否开启", kitui.RoleMeta)
	})
}

type PendingHintRenderer func(*ui.Context, shell.ProjectRef)

func (g *Controller) RenderView(c *ui.Context) {
	var tok *kitui.Tokens
	if g.Theme != nil {
		tok = g.Theme.Tokens()
	}
	if tok == nil {
		tok = kitui.TokensFor(c.Theme().Dark)
	}
	if flag(g.IsSetupOnly) {
		ui.Column(c.Key("graph-setup-notice")).Margin(kitui.Sp3, kitui.PageInset).
			Padding(kitui.Sp4).Gap(kitui.Sp2).Radius(kitui.Radius).Background(tok.Fill1).Border(1, tok.Border).Children(func() {
			kitui.Text(c, tok, "配置记忆提炼后可查看图谱", kitui.RoleBody)
			open := g.OnOpenSettings
			if open == nil && g.Router != nil {
				open = func() { g.Router.NavigateSettings("记忆提炼") }
			}
			kitui.SmallButton(c, tok, "前往配置", nil, open).AlignSelf(ui.Start)
		})
		return
	}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		g.toolbar(c, tok)
		if p, ok := g.project(); ok && g.Mode == ModeCode && !p.CodeEnabled {
			g.codeGraphEmpty(c, tok)
			return
		}
		if g.Error != "" {
			ui.Text(c, g.Error).FontSize(11).TextColor(tok.Text3).Margin(10, 32)
		}
		if g.Mode == ModeCode && g.CodeStatus != "" {
			ui.Text(c, g.CodeStatus).FontSize(11).TextColor(tok.Text3).Margin(10, 32)
		}
		if g.Host != nil && g.Host.View() != nil && g.Host.HasFrame() {
			ui.Column(c.Key("graph-canvas-container")).Grow(1).MinHeight(0).Margin(0, kitui.PageInset, kitui.PageInset, kitui.PageInset).Radius(12).Clip().Children(func() {
				ui.WebView(c.Key("official-graph-canvas"), g.Host.View()).Grow(1).MinHeight(0)
			})
		}
		if g.Selection != "" {
			ui.Text(c, "选中节点 · "+g.Selection).FontSize(10).TextColor(tok.Text3).MaxLines(3).Margin(10, 32)
			kitui.OutlineButton(c, tok, "查看官方文档出处", nil, g.OnOpenNodeSource)
		}
	})
}

func run(fn func()) {
	if fn != nil {
		fn()
	}
}
func runToggle(fn func(bool), enabled bool) {
	if fn != nil {
		fn(enabled)
	}
}
