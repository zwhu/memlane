package desktop

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/zwhu/memlane/internal/records"
)

func conversationListWidth(c *ui.Context) float32 {
	w, _ := c.Size()
	if w < 1000 {
		return 248
	}
	return 316
}

func (a *App) conversationToolbar(c *ui.Context) {
	ui.Row(c).Height(56).Shrink(0).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).DragWindow().Children(func() {
		ui.Row(c).Width(conversationListWidth(c)).Shrink(0).Height(56).Padding(0, 24).Gap(10).BorderWidth(0, 1, 0, 0).BorderColor(design.Border).Children(func() {
			ui.Icon(c, icons["项目"]).FontSize(16).TextColor(design.Ink)
			ui.Text(c, a.scopeLabel(a.scope)).FontSize(14).Bold().SingleLine().Grow(1).MinWidth(0)
		})
		label := "原始记录 / 会话"
		if a.conversations.Documents != nil {
			label = "原始记录 / 文档"
		}
		if a.officialKnowledgeVisible() {
			label = "知识详情"
		}
		if a.projectKnowledgeReading() {
			label = "知识详情"
		}
		ui.Text(c, label).FontSize(11).TextColor(design.Muted).Margin(0, 26)
		if a.projectKnowledgeReading() {
			a.deliveryAssignmentProjectReturn(c)
			ui.Spacer(c)
			ui.Row(c).Gap(8).Padding(0, 20).Children(func() { a.projectKnowledgeActions(c) })
		}
		if a.conversationPageVisible() && a.conversations.Return != nil {
			ui.Spacer(c)
			quietButton(c, "返回知识").Key("conversation-return-knowledge").Margin(0, 20).OnClick(a.returnFromConversationSource)
		}
	})
}

func (a *App) conversationLayout(c *ui.Context) {
	s := &a.conversations
	if s.Documents != nil {
		if s.Documents.Knowledge {
			a.officialKnowledgeLayout(c)
			return
		}
		a.officialDocumentsLayout(c)
		return
	}
	ui.Row(c).Key("conversation-panes").Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Key("conversation-list").Width(conversationListWidth(c)).Shrink(0).BorderWidth(0, 1, 0, 0).BorderColor(design.Border).Children(func() {
			ui.Column(c).Shrink(0).Padding(24, 18, 16, 18).Gap(14).Children(func() {
				ui.Text(c, "项目内容").FontSize(10).TextColor(design.Muted)
				ui.Row(c).Gap(4).Padding(3).Radius(6).AlignSelf(ui.Start).Background(ui.Hex("#eef2f6")).Children(func() {
					quietButton(c, "知识").Key("conversation-tab-knowledge").Padding(7, 12).OnClick(func() {
						if s.Return != nil {
							a.returnFromConversationSource()
						} else {
							a.switchReadingKind("knowledge")
						}
					})
					quietButton(c, "原始记录").Padding(7, 12).Background(ui.Hex("#ffffff")).Radius(4).TextColor(design.Ink)
				})
				ui.TextInput(c.Key("conversation-search"), &s.Query).Label("搜索已载入会话").Placeholder("搜索已载入会话").Height(36).FontSize(11).Background(design.Surface).Border(1, design.Border).Radius(6)
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "按会话浏览").FontSize(10).TextColor(design.Muted).Grow(1)
					ui.Select(c.Key("conversation-sort"), &s.Sort, []string{"最近活动", "最早活动"}).Width(96).FontSize(10).Background(ui.Transparent).BorderWidth(0).Padding(0)
				})
			})
			ui.Scroll(c.Key("conversation-list-scroll")).TrackScroll(&s.ListScroll).Grow(1).MinHeight(0).Padding(0, 10).Gap(6).Children(func() {
				if sourceReaderTransientFailure(s.ErrorCode) && len(s.Rows) == 0 {
					ui.Text(c, "会话列表读取未完成，请重试。").FontSize(11).TextColor(design.Muted)
					quietButton(c, "重试会话列表").Disabled(s.Loading).OnClick(func() { a.refreshConversationCatalog(false) })
				}
				if s.ReadCandidate != nil {
					quietButton(c, "重新确认读取范围").Disabled(s.ReadReview != nil).OnClick(a.openConversationReadReview)
				}
				if !s.Loaded && len(s.Rows) == 0 && (s.ErrorCode == "" || sourceReaderTransientFailure(s.ErrorCode)) {
					message := "尚未取得官方会话列表。"
					if s.Loading || s.Reading {
						message = "正在核对官方会话与访问范围…"
					}
					if s.SourceError != "" {
						message = "出处读取未完成。请在右侧核对原因。"
					}
					ui.Text(c, message).FontSize(11).TextColor(design.Muted)
					return
				}
				if s.Binding == nil {
					title, message := "尚未接入官方会话", "当前项目尚未授权读取官方会话。"
					if s.ErrorCode != "" {
						title, message = "读取权限已变化", "已保存的内容未删除，重新确认授权后可继续查看。"
					}
					ui.Text(c, title).FontSize(12).Bold()
					ui.Text(c, message).FontSize(11).TextColor(design.Muted).LineHeight(1.8)
					return
				}
				rows := append([]records.SessionSummary(nil), s.Rows...)
				sort.SliceStable(rows, func(i, j int) bool {
					l, le := time.Parse(time.RFC3339Nano, rows[i].LastActivity)
					r, re := time.Parse(time.RFC3339Nano, rows[j].LastActivity)
					if le != nil {
						return false
					}
					if re != nil {
						return true
					}
					if s.Sort == "最早活动" {
						return l.Before(r)
					}
					return l.After(r)
				})
				shown := 0
				lastDay := ""
				for _, row := range rows {
					key := conversationKey(*s.Binding, row)
					view := s.Views[key]
					var detail *records.SessionDetail
					if view != nil {
						detail = view.Detail
					}
					title, excerpt := conversationTitle(detail), ""
					if detail != nil {
						for _, qa := range detail.QAs {
							if qa.Question != "" {
								excerpt = qa.Question
								break
							}
						}
					}
					searchText := title + "\n" + excerpt
					if detail != nil {
						searchText += conversationCopy(*detail)
					}
					if s.Query != "" && !strings.Contains(strings.ToLower(searchText), strings.ToLower(s.Query)) {
						continue
					}
					shown++
					day := conversationDay(row.LastActivity)
					if day != lastDay {
						ui.Text(c, day).FontSize(10).TextColor(design.Muted).Padding(10, 10, 4, 10)
						lastDay = day
					}
					button := ui.Button(c, "").Key("conversation-row-"+key).Label(title).Padding(14, 0).Radius(7).BorderWidth(0).Background(ui.Transparent).AlignItems(ui.Stretch)
					if key == s.Selected {
						button.Background(design.ListSelected)
					}
					button.OnClick(func() { a.selectConversation(row) }).Children(func() {
						ui.Row(c).Gap(9).AlignItems(ui.Start).Children(func() {
							accent := ui.Transparent
							if key == s.Selected {
								accent = ui.Hex("#809dbf")
							}
							ui.Box(c).Width(3).Height(20).Margin(4, 0, 0, 0).Radius(2).Background(accent).Shrink(0)
							ui.Column(c).Gap(9).Grow(1).MinWidth(0).Padding(0, 12, 0, 0).Children(func() {
								ui.Text(c, title).FontSize(12).Bold().MaxLines(1)
								if excerpt != "" {
									ui.Text(c, excerpt).FontSize(11).TextColor(design.Muted).MaxLines(1)
								}
								label := "问答片段"
								if row.Agent != "" {
									label = row.Agent + " · " + label
								}
								ui.Text(c, label+" · "+conversationActivity(row.LastActivity)).FontSize(10).TextColor(design.Muted).MaxLines(1)
							})
						})
					})
				}
				if shown == 0 {
					label := "暂无可读取会话"
					if s.Query != "" {
						label = "未找到匹配会话"
					}
					ui.Text(c, label).FontSize(12).TextColor(design.Muted).Margin(20, 10)
				}
				if s.HasMore {
					quietButton(c, "载入更多会话").Disabled(s.Loading).OnClick(func() { a.refreshConversationCatalog(true) })
				}
			})
			conversationFooter(c, fmt.Sprintf("已载入 %d 个会话", len(s.Rows)), 18, true, func() {
				if s.ErrorCode != "" && len(s.Rows) > 0 {
					quietButton(c, "重试").Label("重试会话列表").Tooltip("会话列表刷新未完成，保留上次核验的内容。").Disabled(s.Loading).OnClick(func() { a.refreshConversationCatalog(false) })
				}
				if s.Return == nil {
					quietButton(c, "文档").Key("official-documents-open").FontSize(10).Padding(3, 4).Tooltip("读取官方保存的非会话文档").OnClick(a.openOfficialDocuments)
					quietButton(c, "旧材料").Tooltip("读取迁移前已保存的材料；不混入官方会话").OnClick(func() { a.retireConversations(); a.refresh(false) })
				}
			})
		})
		ui.Column(c).Key("conversation-reading-pane").Grow(1).MinWidth(0).Children(func() { a.conversationReading(c) })
	})
}

func (a *App) conversationReading(c *ui.Context) {
	a.conversationReadingWith(c, &a.conversations, a.refreshSelectedConversation, a.refreshConversationSource, sourceReaderFailureMessage)
}

func (a *App) conversationReadingWith(c *ui.Context, s *ConversationUI, refresh, refreshSource func(), failureMessage func(string) string) {
	if s.ReadReview != nil {
		a.conversationReadReviewView(c)
		return
	}
	view := s.Views[s.Selected]
	var d *records.SessionDetail
	if view != nil {
		d = view.Detail
	}
	width, _ := c.Size()
	padding := float32(30)
	titleSize := float32(21)
	if width < 1000 {
		padding = 20
		titleSize = 19
	}
	if d != nil {
		a.conversationReadingHeader(c, s, view, d, padding, titleSize, width < 1000)
	}
	body := ui.Scroll(c.Key("conversation-body-"+s.Selected)).Grow(1).MinHeight(0).Padding(20, padding, 26, padding).Gap(20).Selectable()
	if view != nil {
		body.TrackScroll(&view.Scroll)
	}
	body.Children(func() {
		if d == nil {
			ui.Text(c, "会话记录").FontSize(10).TextColor(design.Muted)
			message := "选择左侧会话，阅读官方已保存的问答与工具记录。"
			if s.Reading {
				message = "正在读取官方已保存的会话片段…"
			} else if s.SourceError != "" {
				message = failureMessage(s.SourceError)
			} else if view != nil && view.ErrorCode != "" {
				message = failureMessage(view.ErrorCode)
			}
			ui.Text(c, message).FontSize(12).LineHeight(1.9)
			if view != nil && view.ErrorCode != "" {
				quietButton(c, "重试读取会话").Disabled(s.Reading).OnClick(refresh)
			}
			if s.SourceError != "" && sourceReaderCanRetry(SourceReaderUI{ErrorCode: s.SourceError}) {
				quietButton(c, "重试读取出处").Disabled(s.Reading).OnClick(refreshSource)
			}
			return
		}

		if view.Metadata {
			ui.Column(c).Gap(8).Padding(12).Border(1, design.Border).Radius(6).Children(func() {
				meta(c, "服务", s.Binding.Instance)
				meta(c, "数据集", d.Dataset)
				meta(c, "会话", d.Session)
				meta(c, "所属用户", d.User)
				if s.Binding.Workspace != "" {
					meta(c, "工作目录", s.Binding.Workspace)
				}
				ui.Text(c, "活动时间来自官方会话；QA创建时间不代表原生发言时间。").FontSize(10).TextColor(design.Muted)
				if len(d.Traces) > 0 {
					ui.Text(c, "工具记录属于本会话，当前来源未提供与单条问答的对应关系。").FontSize(10).TextColor(design.Muted).LineHeight(1.8)
				}
			})
		}

		if link := s.SourceReceipt; link != nil {
			label := "已关联知识引用的原始记录。"
			if !link.TargetLoaded {
				label = "引用记录不在本次官方返回的最近片段中；当前仅打开对应会话，未定位到引用。"
			}
			ui.Text(c, label).FontSize(10).TextColor(design.Muted).LineHeight(1.8)
		}
		for i, qa := range d.QAs {
			id := qa.ID
			if id == "" {
				id = records.MergeDigest([]any{qa.Question, qa.Answer, i})
			}
			block := ui.Column(c).Key("qa-" + id).Gap(14)
			if link := s.SourceReceipt; link != nil && link.TargetLoaded && link.TargetKind == "qa" && link.TargetID == id {
				if s.RevealSource {
					block.ScrollIntoView()
					s.RevealSource = false
				}
			}
			block.Children(func() {
				if qa.Question != "" {
					a.conversationRole(c, s, view, d, "你", icons["通用知识"], "复制用户原文", qa.Question)
					ui.Text(c, qa.Question).FontSize(12).LineHeight(1.9).Padding(11, 14).Background(design.ListSelected).Border(1, design.Border).Radius(7)
					if qa.QuestionTruncated {
						ui.Text(c, "问题为已保存的截断片段。").FontSize(10).TextColor(design.Muted)
					}
				}
				if qa.Answer != "" {
					label := "Agent"
					if d.Agent != "" {
						label = d.Agent
					}
					a.conversationRole(c, s, view, d, label, icons["Agent"], "复制Agent原文", qa.Answer)
					indent := float32(28)
					if width < 1000 {
						indent = 0
					}
					ui.Column(c).Padding(0, 0, 0, indent).Children(func() { markdown.View(c.Key("answer-"+id), qa.Answer, &s.Markdown) })
					if qa.AnswerTruncated {
						ui.Text(c, "回答为已保存的截断片段。").FontSize(10).TextColor(design.Muted)
					}
				}
			})
		}
		if len(d.Traces) > 0 {
			ui.Column(c).Key("conversation-tools").Border(1, design.Border).Radius(6).Background(design.Surface).Children(func() {
				label := fmt.Sprintf("本会话工具记录 · %d 条", len(d.Traces))
				action := "展开"
				if view.Tools {
					action = "收起"
				}
				quietButton(c, "").Key("conversation-tools-toggle").Label(action + label).Padding(12).Justify(ui.Start).AlignItems(ui.Stretch).OnClick(func() { view.Tools = !view.Tools }).Children(func() {
					ui.Row(c).Gap(8).Grow(1).Children(func() {
						chevron := "›"
						if view.Tools {
							chevron = "⌄"
						}
						ui.Text(c, chevron).FontSize(12).TextColor(design.Muted)
						ui.Icon(c, icons["Agent"]).FontSize(14).TextColor(design.Muted)
						ui.Text(c, label).FontSize(10).TextColor(design.Muted).Grow(1).MinWidth(0).SingleLine()
					})
				})
				if view.Tools {
					for _, trace := range d.Traces {
						a.conversationTraceWith(c, trace, s)
					}
				}
			})
		}
	})
	label := "尚未载入内容"
	if d != nil {
		label = fmt.Sprintf("已载入 %d 组问答 · %d 条工具记录", len(d.QAs), len(d.Traces))
	}
	conversationFooter(c, label, padding, false, func() {
		if d != nil && s.SourceError != "" {
			quietButton(c, "重试").Label("重试刷新出处").Tooltip("刷新暂未完成，正在显示上次核验的内容。").Disabled(s.Reading).OnClick(refreshSource)
		} else if d != nil && view.ErrorCode != "" {
			quietButton(c, "重试").Label("重试刷新会话").Tooltip("刷新暂未完成，正在显示上次核验的内容。").Disabled(s.Reading).OnClick(refresh)
		}
		quietButton(c, "复制已载入内容").Padding(5, 9).Border(1, design.Border).Radius(5).Disabled(d == nil || len(d.QAs)+len(d.Traces) == 0).OnClick(func() {
			current := s.Views[s.Selected]
			if s.Active && a.conversationPageVisible() && current != nil && current.Detail != nil {
				c.WriteClipboard(conversationCopy(*current.Detail))
			}
		})
	})
}

// Counts never share their narrow-window line with retry or copy actions.
// Keep the footer height constant so feedback cannot move the reading viewport.
func conversationFooter(c *ui.Context, label string, padding float32, list bool, actions func()) {
	width, _ := c.Size()
	if width < 1000 {
		ui.Column(c).Shrink(0).Height(48).Padding(3, padding).Gap(1).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
			ui.Text(c, label).FontSize(10).TextColor(design.Muted).SingleLine().Height(14)
			ui.Row(c).Height(26).Gap(8).AlignItems(ui.Center).Children(actions)
		})
		return
	}
	ui.Row(c).Shrink(0).Height(48).Padding(10, padding).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(design.Border).Children(func() {
		count := ui.Text(c, label).FontSize(10).TextColor(design.Muted).MinWidth(0).SingleLine()
		if list {
			count.Width(92).Shrink(0)
		} else {
			count.Grow(1)
		}
		actions()
	})
}

func (a *App) conversationTrace(c *ui.Context, trace records.SessionTrace) {
	a.conversationTraceWith(c, trace, &a.conversations)
}

func (a *App) conversationTraceWith(c *ui.Context, trace records.SessionTrace, s *ConversationUI) {
	block := ui.Column(c).Key("trace-"+trace.ID).Padding(12).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(design.Border)
	if link := s.SourceReceipt; link != nil && link.TargetLoaded && link.TargetKind == "trace" && link.TargetID == trace.ID && s.RevealSource {
		block.ScrollIntoView()
		s.RevealSource = false
	}
	block.Children(func() {
		status := trace.Status
		if status == "success" {
			status = "成功"
		} else if status == "error" {
			status = "失败"
		} else if status == "" {
			status = "状态未提供"
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, trace.Function).FontSize(11).Bold().Grow(1).MinWidth(0)
			ui.Text(c, status).FontSize(10).TextColor(design.Muted)
		})
		for _, field := range []struct {
			name string
			raw  json.RawMessage
		}{{"参数", trace.Params}, {"结果", trace.Result}} {
			label := field.name
			if (field.name == "参数" && trace.ParamsTruncated) || (field.name == "结果" && trace.ResultTruncated) {
				label += " · 已截断"
			}
			ui.Text(c, label).FontSize(10).TextColor(design.Muted)
			a.conversationTraceField(c, s, trace.ID, field.name, field.raw)
		}
		var resultText string
		_ = json.Unmarshal(trace.Result, &resultText)
		if trace.Error != "" && trace.Error != resultText {
			if trace.ErrorTruncated {
				ui.Text(c, "错误 · 已截断").FontSize(10).TextColor(design.Muted)
			}
			ui.Text(c, trace.Error).FontSize(11).LineHeight(1.7)
		}
	})
}
