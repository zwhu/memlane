package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

func (a *App) conversationReadingHeader(c *ui.Context, s *ConversationUI, view *conversationReadingState, d *records.SessionDetail, padding, titleSize float32, narrow bool) {
	ui.Column(c).Key("conversation-fixed-header").Shrink(0).Padding(26, padding, 0, padding).Gap(20).Children(func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "会话记录").FontSize(10).TextColor(design.Muted)
			ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
				ui.Text(c, conversationTitle(d)).FontSize(titleSize).Bold().Grow(1).MinWidth(0).MaxLines(2)
				quietButton(c, "").Key("conversation-record-info").Label("记录信息").Tooltip("记录信息").AlignItems(ui.Stretch).OnClick(func() { view.Metadata = !view.Metadata }).Children(func() {
					ui.Row(c).Gap(6).Children(func() {
						ui.Icon(c, icons["info"]).FontSize(14).TextColor(design.Muted)
						if !narrow {
							ui.Text(c, "记录信息").FontSize(10).TextColor(design.Muted)
						}
					})
				})
			})
			ui.Text(c, a.scopeLabel(s.Project)+" · "+conversationActivity(d.LastActivity)+" · 问答片段").FontSize(10).TextColor(design.Muted)
		})
		notice := "仅显示官方已保存的问答与工具记录。当前接口最多返回各自最近 20 条。"
		if s.Binding.MaterialSubmission {
			notice = "仅显示已采集并由官方保留的问答与工具记录。"
		}
		if len(d.QAs) == 0 && len(d.Traces) == 0 {
			notice = "本次没有返回可读问答或工具。可能尚无内容、缓存不可用或已过期，原因尚未确认。"
		}
		ui.Row(c).Gap(8).AlignItems(ui.Start).Padding(10, 12).Background(design.Surface).Border(1, design.Border).Radius(5).Children(func() {
			ui.Icon(c, icons["info"]).FontSize(12).TextColor(design.Muted).Margin(3, 0, 0, 0)
			ui.Text(c, notice).FontSize(10).TextColor(design.Muted).LineHeight(1.8).Grow(1).MinWidth(0)
		})
	})
}

func (a *App) conversationRole(c *ui.Context, s *ConversationUI, view *conversationReadingState, d *records.SessionDetail, label string, icon *ui.SVG, copyLabel, text string) {
	ui.Row(c).Gap(8).Children(func() {
		ui.Row(c).Width(20).Height(20).Radius(10).Border(1, design.Border).AlignItems(ui.Center).Justify(ui.Center).Children(func() { ui.Icon(c, icon).FontSize(12).TextColor(design.Muted) })
		ui.Text(c, label).FontSize(11).Bold().Grow(1).MinWidth(0)
		quietButton(c, "").Label(copyLabel).Tooltip(copyLabel).Children(func() { ui.Icon(c, icons["copy"]).FontSize(12).TextColor(design.Muted) }).OnClick(func() {
			if s.Active && a.conversationPageVisible() && s.Views[s.Selected] == view && view.Detail == d {
				c.WriteClipboard(text)
			}
		})
	})
}
