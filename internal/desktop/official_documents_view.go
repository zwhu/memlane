package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

func (a *App) officialDocumentsLayout(c *ui.Context) {
	d := a.conversations.Documents
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(conversationListWidth(c)).Shrink(0).BorderWidth(0, 1, 0, 0).BorderColor(design.Border).Children(func() {
			ui.Column(c).Shrink(0).Padding(24, 18, 16, 18).Gap(14).Children(func() {
				ui.Text(c, "项目内容").FontSize(10).TextColor(design.Muted)
				ui.Row(c).Gap(4).Padding(3).Radius(6).AlignSelf(ui.Start).Background(ui.Hex("#eef2f6")).Children(func() {
					quietButton(c, "知识").Padding(7, 12).OnClick(func() { a.switchReadingKind("knowledge") })
					quietButton(c, "原始记录").Padding(7, 12).Background(ui.Hex("#ffffff")).Radius(4).TextColor(design.Ink)
				})
				ui.TextInput(c.Key("official-document-search"), &d.Query).Label("搜索已载入文档").Placeholder("搜索已载入文档").Height(36).FontSize(11).Background(design.Surface).Border(1, design.Border).Radius(6)
				ui.Text(c, "按文档浏览").FontSize(10).TextColor(design.Muted)
			})
			ui.Scroll(c.Key("official-document-list")).TrackScroll(&d.ListScroll).Grow(1).MinHeight(0).Padding(0, 10).Gap(6).Children(func() {
				if len(d.Rows) == 0 {
					label := "暂无可读取文档"
					if !d.Loaded {
						label = "正在读取官方文档目录…"
					} else if d.ErrorCode != "" {
						label = "文档目录读取未完成，请核对连接后重试。"
					}
					ui.Text(c, label).FontSize(11).TextColor(design.Muted).Margin(20, 10)
				}
				shown := 0
				for _, row := range d.Rows {
					if d.Query != "" && !strings.Contains(strings.ToLower(officialDocumentTitle(row)), strings.ToLower(d.Query)) {
						continue
					}
					shown++
					button := ui.ButtonBase(c).Key("official-document-row-"+row.ID).AlignSelf(ui.Stretch).Padding(13, 12).Border(1, ui.Transparent).Radius(8)
					if d.Selected == row.ID {
						button.Background(design.ListSelected)
					}
					button.OnClick(func() { a.selectOfficialDocument(row) }).Children(func() {
						ui.Column(c).Gap(9).MinWidth(0).Children(func() {
							ui.Text(c, officialDocumentTitle(row)).FontSize(12).Bold().MaxLines(2)
							ui.Text(c, "文档 · "+officialDocumentUpdated(row)).FontSize(10).TextColor(design.Muted).MaxLines(1)
						})
					})
				}
				if len(d.Rows) > 0 && shown == 0 {
					ui.Text(c, "未找到匹配文档").FontSize(12).TextColor(design.Muted).Margin(20, 10)
				}
				if d.HasMore {
					quietButton(c, "载入更多文档").Disabled(d.Loading).OnClick(func() { a.refreshOfficialDocuments(true) })
				}
			})
			ui.Row(c).Height(48).Shrink(0).Padding(10, 18).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
				label := fmt.Sprintf("已载入 %d 篇文档", len(d.Rows))
				if d.ErrorCode != "" && len(d.Rows) == 0 {
					label = "目录刷新未完成"
				}
				ui.Text(c, label).FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0).SingleLine()
				if d.ErrorCode != "" {
					quietButton(c, "重试").Label("重试文档目录").Tooltip("文档目录刷新未完成，保留上次核验的内容。").Disabled(d.Loading).OnClick(func() { a.refreshOfficialDocuments(false) })
				}
				quietButton(c, "会话").Key("official-documents-close").OnClick(a.closeOfficialDocuments)
			})
		})
		ui.Column(c).Grow(1).MinWidth(0).Children(func() { a.officialDocumentReading(c) })
	})
}
func officialDocumentUpdated(row records.OfficialDocument) string {
	if row.UpdatedAt == nil || conversationActivity(*row.UpdatedAt) == "时间未知" {
		return "更新时间未提供"
	}
	return "更新于 " + conversationActivity(*row.UpdatedAt)
}
func (a *App) officialDocumentReading(c *ui.Context) {
	d := a.conversations.Documents
	if d.Source != nil {
		a.officialDocumentSourceReading(c)
		return
	}
	view := d.Views[d.Selected]
	padding := float32(30)
	size := float32(21)
	width, _ := c.Size()
	if width < 1000 {
		padding, size = 20, 19
	}
	body := ui.Scroll(c.Key("official-document-reading-"+d.Selected)).Grow(1).MinHeight(0).Padding(26, padding).Gap(18).Selectable()
	if view != nil {
		body.TrackScroll(&view.Scroll)
	}
	body.Children(func() {
		ui.Text(c, "原始文档").FontSize(10).TextColor(design.Muted)
		if d.SelectedRow == nil {
			ui.Text(c, "选择左侧文档，阅读官方保存的原文。").FontSize(12).TextColor(design.Muted)
			return
		}
		ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
			ui.Text(c, officialDocumentTitle(*d.SelectedRow)).FontSize(size).Bold().Grow(1).MinWidth(0)
			if view != nil {
				quietButton(c, "文档信息").FontSize(10).OnClick(func() { view.Info = !view.Info })
			}
		})
		ui.Text(c, "官方文档不等于已提炼知识，当前未提供处理状态。").FontSize(10).TextColor(design.Muted).LineHeight(1.8)
		if current, valid := a.currentOfficialDocument(); valid && current.Content.SessionReference != "" {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, "文档标记了来源会话；打开时核对当前关联与读取权限。").FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0)
				quietButton(c, "阅读来源会话").OnClick(a.openOfficialDocumentSource)
			})
		}
		if view != nil && view.Info {
			ui.Column(c).Gap(8).Padding(12).Border(1, design.Border).Radius(6).Children(func() {
				meta(c, "文档", d.SelectedRow.ID)
				meta(c, "数据集", d.SelectedRow.Dataset)
				if d.SelectedRow.MIMEType != "" {
					meta(c, "类型", d.SelectedRow.MIMEType)
				}
				ui.Text(c, officialDocumentUpdated(*d.SelectedRow)).FontSize(10).TextColor(design.Muted)
			})
		}
		if view == nil || view.Content == nil || view.Window == nil {
			message := "正在读取文档…"
			if view != nil && view.ErrorCode != "" {
				message = "文档原文暂不可读，请重新核对来源。"
				if view.ErrorCode == "unsupported_content" {
					message = "暂不支持此文档的文本预览。"
				}
			}
			ui.Text(c, message).FontSize(12).TextColor(design.Muted)
			return
		}
		w := view.Window
		w.Page = min(max(w.Page, 0), len(w.Offsets)-2)
		text := w.Body[w.Offsets[w.Page]:w.Offsets[w.Page+1]]
		if text == "" {
			ui.Text(c, "文档正文为空。").FontSize(12).TextColor(design.Muted)
		} else {
			ui.Text(c, text).Key(fmt.Sprintf("official-document-body-%s-%d", d.Selected, w.Page)).FontSize(12).LineHeight(1.9)
		}
	})
	ui.Row(c).Height(48).Shrink(0).Padding(10, padding).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		label := "尚未载入文档"
		_, available := a.currentOfficialDocument()
		if available {
			label = "官方原文"
			if len(view.Window.Offsets) > 2 {
				label = fmt.Sprintf("第 %d / %d 段", view.Window.Page+1, len(view.Window.Offsets)-1)
			}
		}
		if !available && view != nil && view.ErrorCode != "" {
			label = "文档读取未完成"
		}
		ui.Text(c, label).FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0).SingleLine()
		if available && len(view.Window.Offsets) > 2 {
			quietButton(c, "上一段").Disabled(view.Window.Page == 0).OnClick(func() { view.Window.Page--; view.Scroll.Y = 0 })
			quietButton(c, "下一段").Disabled(view.Window.Page >= len(view.Window.Offsets)-2).OnClick(func() { view.Window.Page++; view.Scroll.Y = 0 })
		}
		if view != nil && view.ErrorCode != "" && view.ErrorCode != "unsupported_content" {
			quietButton(c, "重试").Label("重试读取文档").Tooltip("文档刷新未完成，保留上次核验的内容。").Disabled(d.Reading).OnClick(a.refreshSelectedOfficialDocument)
		}
		quietButton(c, "复制文档").Key("official-document-copy").Disabled(!available || view.Content.Text == "").OnClick(func() {
			if current, valid := a.currentOfficialDocument(); valid && a.conversationPageVisible() {
				c.WriteClipboard(current.Content.Text)
			}
		})
	})
}
