package app

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

func (a *App) commonReadActions(c *ui.Context, i Item) {
	if a.Config.ProjectControl == nil || i.Document == nil || commonCategory(*i.Document) == "" || !i.BodyLoaded {
		return
	}
	e := &a.commonEdit
	pending := e.Submitted && !e.Completed
	label := "编辑"
	if commonHasDraft(*e) {
		label = "继续草稿"
		if pending {
			label = "查看保存结果"
		}
	}
	v4SmallButton(c, label, nil, func() { a.beginCommonDraft(&i) }).Disabled(e.Busy || e.DeleteSubmitted)
	v4DangerOutlineButton(c, "删除", true, func() { d := *i.Document; e.Delete = &d; e.Message = "" }).Disabled(e.Busy || pending || e.DeleteSubmitted)
}
func commonReadingParts(i Item) (CommonDraft, bool) {
	if i.Document == nil {
		return CommonDraft{}, false
	}
	d := commonDraftFor(i)
	raw, err := d.FormatDocument()
	return d, err == nil && strings.TrimSuffix(raw, "\n") == strings.TrimSuffix(i.Body, "\n")
}
func (a *App) commonKnowledgeReader(c *ui.Context, s *pane) {
	e := &a.commonEdit
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Background(tok.Bg).Children(func() {
		if s.Selected == nil {
			switch {
			case len(a.Config.Common) == 0:
				commonEmptyWithAction(c, "common-unconnected", "尚未连接记忆库", "", func() {
					v4OutlineButton(c, "前往设置", nil, func() { a.navigate("设置", 0) })
				})
			case !s.Loaded:
				if s.Error != "" {
					v4EmptyState(c, "common-loading", "读取失败", "")
				} else {
					ui.Column(c).Grow(1).MinHeight(0).FillWidth()
				}
			case len(s.Items) > 0:
				v4EmptyState(c, "common-select", "选择一条记忆", "")
			case s.Error != "":
				v4EmptyState(c, "common-refresh", "未能刷新，显示上次结果", "")
			case e.Category != "" || s.Offset > 0:
				commonEmptyWithAction(c, "common-filter-empty", "当前分类没有记忆", "", func() {
					v4OutlineButton(c, "显示全部", nil, func() { e.Category = ""; s.Offset = 0; a.load(s) })
				})
			default:
				commonEmptyWithAction(c, "common-empty", "还没有通用记忆", "个人偏好和跨项目经验放在这里。", func() {
					if a.Config.ProjectControl != nil {
						v4PrimaryButton(c, "添加记忆", func() { a.beginCommonDraft(nil) })
					}
				})
			}
			appFooter(c, "通用记忆阅读栏").Padding(0, sp6).Children(func() { homeText(c, "跨项目参考 · 项目明确约定优先", homeMeta) })
			return
		}
		i := *s.Selected
		if e.Delete != nil && e.Delete.ID == i.ID && e.Delete.Dataset == i.Dataset {
			a.commonDeletionBanner(c, i)
		}
		scroll := s.DetailScroll[i.Key()]
		if scroll == nil {
			scroll = &ui.ScrollState{}
			s.DetailScroll[i.Key()] = scroll
		}
		ui.Scroll(c.Key("common-detail-"+i.Key())).Grow(1).MinHeight(0).TrackScroll(scroll).Padding(sp6, sp10, sp8, sp10).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(readerMaxW).Gap(sp4).Children(func() {
				ui.Row(c).Label("通用记忆内容工具栏").FillWidth().MinHeight(28).Padding(0, 0, sp4, 0).Gap(sp2).AlignItems(ui.Center).
					BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
					agentLogo(c, recentAgentIcon(i), 18)
					v4CategoryChip(c, commonCategoryLabel(i.CommonCategory))
					row := ui.Row(c).MinWidth(0).AlignItems(ui.Center).Gap(4)
					row.Children(func() {
						ui.Text(c, displayDate(i.Date)).Font(fontUI).FontSize(fsCaption).FontFeatures("tnum").TextColor(tok.Text3).SingleLine()
						hoverCopyAction(c, row, "复制内容", func() { c.WriteClipboard(itemPresentation(i).VisibleBody); a.copyFeedback("内容") }).Disabled(!i.BodyLoaded)
					})
					v4RecallChip(c, i)
					knowledgeBasisTag(c, i)
					ui.Spacer(c)
					anchor := readingAction(c, "内容信息", readingInfo, func() { a.projectUI.InfoOpen = !a.projectUI.InfoOpen; a.projectUI.InfoKey = i.Key() })
					if a.projectUI.InfoKey != i.Key() {
						a.projectUI.InfoOpen = false
					}
					a.contentInformation(c, anchor, i)
					a.commonReadActions(c, i)
				})
				parts, canonical := commonReadingParts(i)
				title := projectDisplayTitle(i)
				if canonical {
					title = parts.Title
				}
				homeText(c, title, homeHeading).FontSize(fsTitle).LineHeight(1.35)
				if s.Error != "" {
					homeText(c, "内容未刷新 · "+s.Error, homeMeta)
				}
				if !i.BodyLoaded && s.Reading {
					v4SkeletonGroup(c, 3, 2, false, false)
				} else if canonical {
					a.commonMarkdown(c, "common-body-"+i.Key(), parts.Body)
					if parts.Applicability != "" {
						ui.Column(c).FillWidth().Margin(sp2, 0, 0, 0).Padding(sp4, 0, 0, 0).Gap(sp2).
							BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
							ui.Text(c, "适用条件").Font(fontUI).FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3).SingleLine()
							a.commonMarkdown(c, "common-conditions-"+i.Key(), parts.Applicability)
						})
					}
					if parts.Source != "" {
						ui.Column(c).FillWidth().Margin(sp2, 0, 0, 0).Padding(sp4, 0, 0, 0).Gap(sp2).
							BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
							ui.Text(c, "原始来源").Font(fontUI).FontSize(fsCaption).FontWeight(600).TextColor(tok.Text3).SingleLine()
							ui.Text(c, parts.Source).Font(fontUI).FontSize(fsFootnote).FontWeight(400).TextColor(tok.Text2).LineHeight(1.6)
						})
					}
				} else {
					a.commonMarkdown(c, "common-original-"+i.Key(), itemPresentation(i).VisibleBody)
				}
				if i.BodyTruncated {
					ui.Text(c, "正文超过 8 MiB，已截断显示").FontSize(fsCaption).TextColor(tok.Text3)
				}
			})
		})
		appFooter(c, "通用记忆阅读栏").Padding(0, sp6).Gap(sp2).Children(func() {
			homeText(c, "跨项目参考 · 项目明确约定优先", homeMeta)
			ui.Spacer(c)
			if time.Now().Before(a.projectUI.CopyUntil) {
				homeText(c, a.projectUI.Copied+"已复制", homeMeta)
			}
			v4OutlineButton(c, "复制正文", v4IconCopy, func() {
				c.WriteClipboard(itemPresentation(i).VisibleBody)
				a.copyFeedback("正文")
			}).Disabled(!i.BodyLoaded)
		})
	})
}
func (a *App) commonMarkdown(c *ui.Context, key, body string) {
	markdown.View(c.Key(key), body, a.v4MarkdownOptions(fsReading))
}
