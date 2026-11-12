package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/zwhu/memlane/internal/official"
	"github.com/zwhu/memlane/internal/records"
	"strings"
)

func (a *App) officialKnowledgeVisible() bool {
	d := a.conversations.Documents
	return d != nil && d.Knowledge && a.conversations.Active && a.conversations.Project == a.scope && a.page == "项目" && a.recordKind == "knowledge"
}

// Explicit new-source browsing leaves existing knowledge editing/history and
// drafts reachable. It does not choose the pending official write/history policy.
func (a *App) openOfficialKnowledge() {
	a.openOfficialKnowledgeWithBinding(nil)
}

func (a *App) defaultOfficialKnowledgeCandidate() bool {
	return a.page == "项目" && a.scope != "" && a.scope != a.officialKnowledgeBypass &&
		(a.recordKind == "knowledge" || a.recordKind == "") && a.browsing.View != "entities" && a.browsing.View != "relations" &&
		!a.editing && (a.detailMode == "" || a.detailMode == "view") && a.selectedID == "" &&
		!a.projectSettings.Open && !a.projectOrganizer.Open && !a.reader.Open && !a.knowledgeHistory.Open &&
		a.readingSearch == "" && a.readingFilter == "" && !a.readingFilters.active() && a.activityDate == ""
}

func (a *App) openOfficialKnowledgeWithBinding(binding *records.CogneeBinding) {
	if a.page != "项目" || a.scope == "" {
		return
	}
	a.rememberDraft()
	a.officialKnowledgeBypass = ""
	a.loadSequence++
	a.loading = false
	a.closeKnowledgeHistory()
	a.closeSourceReader()
	a.retireConversations()
	a.recordKind = "knowledge"
	s := &a.conversations
	s.Active, s.Project = true, a.scope
	if binding != nil {
		b := *binding
		s.Binding = &b
	}
	s.Documents = &officialDocumentsUI{Knowledge: true, KnowledgePages: map[int]records.OfficialKnowledgePage{}, Pages: map[int]records.OfficialDocumentPage{}, Views: map[string]*officialDocumentView{}}
	a.refreshOfficialDocuments(false)
}
func (a *App) closeOfficialKnowledge() {
	a.officialKnowledgeBypass = a.scope
	a.retireConversations()
	a.refresh(false)
}
func knowledgeProcessingLabel(state string) string {
	switch state {
	case "completed":
		return "已处理"
	case "not_completed":
		return "处理未完成"
	default:
		return "处理状态未知"
	}
}
func (a *App) officialKnowledgeTitle(row records.OfficialDocument) string {
	if d := a.conversations.Documents; d != nil {
		if v := d.Views[row.ID]; v != nil && v.Knowledge != nil {
			return v.Knowledge.Title
		}
	}
	return "未命名知识"
}

func (a *App) officialKnowledgeLayout(c *ui.Context) {
	d := a.conversations.Documents
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(conversationListWidth(c)).Shrink(0).BorderWidth(0, 1, 0, 0).BorderColor(design.Border).Children(func() {
			ui.Column(c).Shrink(0).Padding(24, 18, 16, 18).Gap(14).Children(func() {
				ui.Text(c, "项目内容").FontSize(10).TextColor(design.Muted)
				ui.Row(c).Gap(4).Padding(3).Radius(6).AlignSelf(ui.Start).Background(ui.Hex("#eef2f6")).Children(func() {
					quietButton(c, "知识").Padding(7, 12).Background(ui.Hex("#ffffff")).Radius(4).TextColor(design.Ink)
					quietButton(c, "原始记录").Padding(7, 12).OnClick(func() { a.retireConversations(); a.switchReadingKind("raw") })
				})
				ui.TextInput(c.Key("official-knowledge-search"), &d.Query).Label("搜索已载入知识").Placeholder("搜索已载入知识标题与正文").Height(36).FontSize(11).Background(design.Surface).Border(1, design.Border).Radius(6)
				ui.Text(c, "知识").FontSize(10).TextColor(design.Muted)
			})
			ui.Scroll(c.Key("official-knowledge-list")).TrackScroll(&d.ListScroll).Grow(1).MinHeight(0).Padding(0, 10).Gap(6).Children(func() {
				shown := 0
				for _, row := range d.Rows {
					v := d.Views[row.ID]
					if v == nil || v.Knowledge == nil {
						continue
					}
					k := v.Knowledge
					body := official.LearningBody(k.Content)
					if d.Query != "" && !strings.Contains(strings.ToLower(k.Title+"\n"+body), strings.ToLower(d.Query)) {
						continue
					}
					shown++
					button := ui.ButtonBase(c).Key("official-knowledge-row-"+row.ID).AlignSelf(ui.Stretch).Padding(13, 0).Border(1, ui.Transparent).Radius(8)
					if d.Selected == row.ID {
						button.Background(design.ListSelected)
					}
					button.OnClick(func() { a.selectOfficialDocument(row) }).Children(func() {
						ui.Row(c).Gap(9).AlignItems(ui.Start).Children(func() {
							accent := ui.Transparent
							if d.Selected == row.ID {
								accent = ui.Hex("#809dbf")
							}
							ui.Box(c).Width(3).Height(20).Margin(4, 0, 0, 0).Radius(2).Background(accent).Shrink(0)
							ui.Column(c).Gap(9).Grow(1).MinWidth(0).Padding(0, 12, 0, 0).Children(func() {
								ui.Text(c, k.Title).FontSize(12).Bold().MaxLines(2)
								ui.Text(c, preview(body)).FontSize(11).LineHeight(1.7).TextColor(design.Muted).MaxLines(2)
								ui.Row(c).Gap(6).Children(func() {
									ui.Text(c, "来源："+knowledgeSourceLabel(k.Content)).FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0).MaxLines(1)
									ui.Text(c, knowledgeProcessingLabel(k.ProcessingState)).FontSize(10).TextColor(design.Muted).Shrink(0)
								})
							})
						})
					})
				}
				if shown == 0 {
					label := "当前目录页没有会话知识"
					if !d.Loaded {
						label = "正在读取知识…"
					} else if d.ErrorCode != "" {
						label = "知识读取未完成，请重试"
					} else if d.Query != "" {
						label = "未找到匹配知识"
					}
					ui.Text(c, label).FontSize(12).TextColor(design.Muted).Margin(20, 10)
				}
				if d.HasMore {
					quietButton(c, "载入更多知识").Disabled(d.Loading).OnClick(func() { a.refreshOfficialDocuments(true) })
				}
			})
			ui.Row(c).Height(48).Shrink(0).Padding(10, 18).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
				label := fmt.Sprintf("已载入 %d 条知识", len(d.Rows))
				if len(d.Rows) == 0 && d.ErrorCode != "" {
					label = "目录刷新未完成"
				}
				ui.Text(c, label).FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0).SingleLine()
				if d.ErrorCode != "" {
					quietButton(c, "重试").Disabled(d.Loading).OnClick(func() { a.refreshOfficialDocuments(false) })
				}
				quietButton(c, "返回项目内容").OnClick(a.closeOfficialKnowledge)
			})
		})
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			if d.Source != nil {
				a.officialDocumentSourceReading(c)
				return
			}
			a.officialKnowledgeReading(c)
		})
	})
}
func (a *App) officialKnowledgeReading(c *ui.Context) {
	d := a.conversations.Documents
	view, valid := a.currentOfficialDocument()
	padding, size := float32(30), float32(21)
	w, _ := c.Size()
	narrow := w < 1000
	if narrow {
		padding, size = 20, 19
	}
	if valid && view.Knowledge != nil {
		k := view.Knowledge
		ui.Column(c).Key("official-knowledge-fixed-header").Shrink(0).Padding(26, padding, 0, padding).Gap(8).Children(func() {
			ui.Text(c, knowledgeKindLabel(k.Content)).FontSize(10).TextColor(design.Muted)
			ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
				ui.Text(c, k.Title).FontSize(size).Bold().TextColor(design.Ink).Grow(1).MinWidth(0).MaxLines(2)
				quietButton(c, "").Label("记录信息").Tooltip("记录信息").OnClick(func() { view.Info = !view.Info }).Children(func() {
					ui.Row(c).Gap(6).Children(func() {
						ui.Icon(c, icons["info"]).FontSize(14).TextColor(design.Muted)
						if !narrow {
							ui.Text(c, "记录信息").FontSize(10).TextColor(design.Muted)
						}
					})
				})
			})
			status := knowledgeProcessingLabel(k.ProcessingState)
			if updated := officialKnowledgeUpdated(k.Content.Document); updated != "" {
				status += " · " + updated
			}
			ui.Row(c).Gap(8).Margin(0, 0, 12, 0).Children(func() {
				ui.Text(c, knowledgeSourceLabel(k.Content)).FontSize(10).TextColor(design.Muted)
				ui.Text(c, status).FontSize(10).TextColor(design.Muted).Padding(3, 6).Background(design.Surface).Radius(3)
			})
		})
	}
	body := ui.Scroll(c.Key("official-knowledge-reading-"+d.Selected)).Grow(1).MinHeight(0).Padding(18, padding, 26, padding).Gap(18).Selectable()
	if view != nil {
		body.TrackScroll(&view.Scroll)
	}
	body.Children(func() {
		if !valid || view.Knowledge == nil {
			ui.Text(c, "选择左侧知识，阅读官方保存的正文。").FontSize(12).TextColor(design.Muted)
			return
		}
		k := view.Knowledge
		if view.Info {
			ui.Column(c).Gap(8).Padding(12).Border(1, design.Border).Radius(6).Children(func() {
				meta(c, "来源", knowledgeSourceLabel(k.Content))
				meta(c, "保存位置", "Cognee")
				if k.ProcessingState == "not_completed" {
					meta(c, "处理状态", "引擎尚未报告处理完成，当前显示已保存内容")
				}
				if k.TitleFromBody {
					meta(c, "标题", "根据已保存正文截取")
				}
				if info, ok := official.CodingLesson(k.Content); ok {
					meta(c, "知识类型", knowledgeKindLabel(k.Content))
					if knowledgeKindLabel(k.Content) == "其他经验" {
						meta(c, "来源类型", info.LessonType)
					}
					if info.StateKey != "" {
						meta(c, "状态标识", info.StateKey)
					}
					if info.SessionDate != "" {
						meta(c, "会话日期", info.SessionDate)
					}
					if info.GitCommit != "" {
						meta(c, "关联提交", info.GitCommit)
					}
					if info.GitBranch != "" {
						meta(c, "分支", info.GitBranch)
					}
					if len(info.Files) > 0 {
						meta(c, "相关文件", strings.Join(info.Files, "\n"))
					}
				}
				ui.Text(c, "当前支持查看和复制，暂不支持编辑或恢复版本。").FontSize(10).TextColor(design.Muted)
			})
		}
		if a.readingMarkdown == nil {
			a.readingMarkdown = knowledgeMarkdownOptions()
		}
		markdown.View(c.Key("official-knowledge-body-"+d.Selected), official.LearningBody(k.Content), a.readingMarkdown)
		if refs := official.ImportedKnowledgeSources(k.Content); len(refs) > 0 {
			ui.Column(c).Padding(14).Border(1, design.Border).Radius(6).Gap(10).Children(func() {
				for index, ref := range refs {
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, "原始依据").FontSize(11).Bold().TextColor(design.Ink).Grow(1)
						quietButton(c, fmt.Sprintf("阅读原文 %d", index+1)).OnClick(func() { a.openImportedDocumentSource(ref.Document) })
					})
				}
				ui.Text(c, "保留了知识与原始记录的关联。").FontSize(10).TextColor(design.Muted)
			})
		}
		if k.Content.SessionReference != "" {
			ui.Column(c).Padding(14).Border(1, design.Border).Radius(6).Gap(10).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, "原始依据").FontSize(11).Bold().TextColor(design.Muted).Grow(1)
					if k.Content.SessionReference != "" {
						quietButton(c, "阅读对话").OnClick(a.openOfficialDocumentSource)
					}
				})
				label := "保留了内容迁移前的来源记录。"
				if k.Content.SessionReference != "" {
					label = "来源关联到整个会话，未定位具体问答或工具。"
				}
				ui.Text(c, label).FontSize(11).TextColor(design.Muted)
			})
		}
	})
	ui.Row(c).Height(48).Shrink(0).Padding(10, padding).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		label := "知识"
		if view != nil && view.ErrorCode != "" {
			label = "刷新未完成，保留上次内容"
		}
		ui.Text(c, label).FontSize(10).TextColor(design.Muted).Grow(1)
		if view != nil && view.ErrorCode != "" {
			quietButton(c, "重试").Disabled(d.Reading).OnClick(a.refreshSelectedOfficialDocument)
		}
		quietButton(c, "复制知识").Disabled(!valid || view.Knowledge == nil).OnClick(func() {
			if v, ok := a.currentOfficialDocument(); ok && v.Knowledge != nil && a.officialKnowledgeVisible() {
				c.WriteClipboard(official.LearningBody(*v.Content))
			}
		})
	})
}

func knowledgeKindLabel(content records.OfficialDocumentText) string {
	info, ok := official.CodingLesson(content)
	if !ok {
		return "知识"
	}
	switch info.LessonType {
	case "state":
		return "项目状态"
	case "pitfall":
		return "避坑经验"
	case "change":
		return "变更记录"
	default:
		return "其他经验"
	}
}

func officialKnowledgeUpdated(row records.OfficialDocument) string {
	if row.UpdatedAt == nil || conversationActivity(*row.UpdatedAt) == "时间未知" {
		return ""
	}
	return "更新于 " + conversationActivity(*row.UpdatedAt)
}

func knowledgeSourceLabel(content records.OfficialDocumentText) string {
	if content.SessionReference != "" {
		return "会话整理"
	}
	return "已保存内容"
}
