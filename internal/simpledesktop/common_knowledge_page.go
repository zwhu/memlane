package simpledesktop

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

type commonKnowledgePageState struct {
	Options, Discard, MenuOpen bool
	EditorScroll               ui.ScrollState
}

var commonPlus = homeSVG(`<path d="M12 5v14M5 12h14"/>`)
var commonEditIcon = homeSVG(`<path d="m16 3 5 5-12 12H4v-5zM13 6l5 5"/>`)

func commonCategoryLabel(category string) string {
	if category == "preference" {
		return "个人偏好"
	}
	if category == "experience" {
		return "通用经验"
	}
	return "通用知识"
}
func commonHasDraft(e commonEditorState) bool {
	return !e.Completed && (e.Open || e.Submitted || e.Existing != nil || strings.TrimSpace(e.Draft.Title+e.Draft.Body+e.Draft.Applicability+e.Draft.Source) != "")
}
func commonProcessingStatus(message string) string {
	switch {
	case strings.Contains(message, "处理已完成"):
		return "已完成"
	case strings.Contains(message, "处理失败"):
		return "失败 · 内容保留"
	case strings.Contains(message, "处理状态："):
		_, v, _ := strings.Cut(message, "处理状态：")
		return v
	default:
		return "待核对"
	}
}
func commonButton(c *ui.Context, label string, primary bool, fn func()) ui.Element {
	b := ui.Button(c, label).MinHeight(32).Padding(7, 14).FontSize(12).FontWeight(400).Border(1, homeBorder).Radius(6).TextColor(homeLinkInk).Background(ui.Hex("#ffffff"))
	if primary {
		b.Background(ui.Hex("#e5edf5")).BorderColor(ui.Hex("#d3dfeb"))
	}
	return b.OnClick(fn)
}
func (a *App) beginCommonDraft(item *Item) {
	e := &a.commonEdit
	a.projectUI.InfoOpen = false
	if e.DeleteSubmitted {
		e.Message = "先核对原删除结果，再开始新的修改。"
		return
	}
	if commonHasDraft(*e) || e.Busy {
		e.Open = true
		return
	}
	p, ok := a.current()
	if !ok || p.Dataset == "" || a.Config.ProjectControl == nil {
		return
	}
	a.commonGeneration++
	category := e.Category
	next := commonEditorState{Generation: a.commonGeneration, Open: true, Target: p, Category: category, Draft: CommonDraft{Category: "preference"}}
	if a.commonCategoryFilter() != "" {
		next.Draft.Category = a.commonCategoryFilter()
	}
	if item != nil {
		if item.Document == nil || !item.BodyLoaded || item.Dataset != p.Dataset || commonCategory(*item.Document) == "" {
			return
		}
		d := *item.Document
		next.Existing = &d
		next.Draft = commonDraftFor(*item)
		next.Baseline = sha256.Sum256([]byte(item.Body))
	}
	a.commonEdit = next
	a.commonPage = commonKnowledgePageState{Options: next.Draft.Source != "" || next.Draft.Applicability != ""}
}
func (a *App) discardCommonDraft() {
	e := &a.commonEdit
	if e.Busy || e.Submitted {
		return
	}
	category := e.Category
	a.commonGeneration++
	a.commonEdit = commonEditorState{Generation: a.commonGeneration, Category: category}
	a.commonPage = commonKnowledgePageState{}
}
func (a *App) commonListToolbar(c *ui.Context) {
	e := &a.commonEdit
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		homeText(c, "知识库", homeHeading).FontSize(13)
		ui.Spacer(c)
		label := "新增通用知识"
		if commonHasDraft(*e) {
			label = "继续草稿"
			if e.Submitted {
				label = "查看保存结果"
			}
		}
		b := commonButton(c, label, false, func() { a.beginCommonDraft(nil) })
		p, ok := a.current()
		b.Disabled(a.Config.ProjectControl == nil || !ok || p.Dataset == "" || e.DeleteSubmitted)
	})
	ui.Row(c).Gap(4).Children(func() {
		for _, label := range []string{"全部", "个人偏好", "通用经验"} {
			k := label
			active := (k == "全部" && a.commonCategoryFilter() == "") || e.Category == k
			b := ui.Button(c, "").Label("分类"+k).Padding(7, 10).BorderWidth(0).Radius(6).Background(ui.Transparent)
			if active {
				b.Background(ui.Hex("#e7eef6"))
			}
			b.Children(func() {
				t := homeText(c, k, homeMeta).FontSize(11)
				if active {
					t.FontWeight(600).TextColor(homeInk)
				}
			}).OnClick(func() {
				if k == "全部" {
					e.Category = ""
				} else {
					e.Category = k
				}
				e.Open = false
				s := a.state()
				s.Offset = 0
				a.load(s)
			})
		}
	})
	if len(a.Config.Common) > 1 {
		options := projectLabels(a.Config.Common)
		selected := options[min(max(a.project, 0), len(options)-1)]
		ui.Select(c, &selected, options).Label("通用知识库").FillWidth().Disabled(e.Open || e.Busy || (e.Submitted && !e.Completed) || e.DeleteSubmitted).OnChange(func() {
			for n, label := range options {
				if label == selected {
					a.navigate("通用知识", n)
					break
				}
			}
		})
	}
}
func (a *App) commonKnowledgePage(c *ui.Context) {
	s := a.state()
	e := &a.commonEdit
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(300).Shrink(0).BorderWidth(0, 1, 0, 0).BorderColor(homeBorder).Children(func() {
			ui.Column(c).Shrink(0).Padding(22, 18, 10, 18).Gap(14).Children(func() {
				a.commonListToolbar(c)
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					homeText(c, projectListStatus(s), homeMeta)
					ui.Spacer(c)
					readingAction(c, "刷新通用知识", homeRefresh, func() { a.load(s) })
				})
				if s.Loading && s.Loaded {
					homeText(c, "正在刷新，保留现有内容", homeMeta)
				}
				if s.Error != "" {
					homeText(c, "未刷新 · "+s.Error, homeMeta).MaxLines(3)
					projectLink(c, "重试读取", func() { a.load(s) })
				}
				if !e.Open && e.Message != "" {
					homeText(c, e.Message, homeMeta).MaxLines(3)
				}
				if e.DeleteSubmitted {
					projectLink(c, "核对原删除结果", a.confirmCommonDeletion).Disabled(e.Busy)
				}
			})
			ui.Scroll(c.Key("common-list-"+a.routeKey())).Grow(1).MinHeight(0).TrackScroll(&s.ListScroll).Padding(0, 12).Gap(6).Children(func() {
				if s.Loaded && len(s.Items) == 0 && !s.Loading && s.Error == "" {
					message := "还没有通用知识"
					if a.commonCategoryFilter() != "" {
						message = "当前分类还没有知识"
					}
					homeText(c, message, homeBody).Margin(14)
					homeText(c, "可新增个人偏好或可复用的经验。", homeMeta).Margin(0, 14)
				}
				for _, row := range s.Items {
					i := row
					b := ui.Button(c, "").Key("common-row-"+i.key()).Label(i.Title).Tooltip(i.Title).FillWidth().MinWidth(0).Padding(12, 14).Justify(ui.Start).BorderWidth(0).Radius(7)
					color := homeHoverColor(b, "common-row", ui.Hex("#f5f8fb").Alpha(0), ui.Hex("#f5f8fb"))
					if s.Selected != nil && s.Selected.key() == i.key() {
						color = ui.Hex("#edf3f9")
					}
					b.Background(color).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
							homeText(c, i.Title, homeTitle).FontSize(12).MaxLines(2)
							homeText(c, commonCategoryLabel(i.CommonCategory)+" · "+displayDate(i.Date), homeMeta).SingleLine()
						})
					}).OnClick(func() { e.Open = false; a.projectUI.InfoOpen = false; a.selectItem(s, i) })
				}
			})
			ui.Row(c).Height(48).Shrink(0).Padding(0, 12).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).Children(func() {
				homeText(c, projectListStatus(s), homeMeta)
				ui.Spacer(c)
				readingAction(c, "上一页", readingBack, func() { s.Offset = max(0, s.Offset-30); a.load(s) }).Disabled(s.Offset == 0 || s.Loading)
				homeText(c, fmt.Sprint(s.Offset/30+1), homeMeta)
				readingAction(c, "下一页", homeArrow, func() { s.Offset += 30; a.load(s) }).Disabled(!s.More || s.Loading)
			})
		})
		if e.Open {
			a.commonKnowledgeEditor(c)
		} else {
			a.commonKnowledgeReader(c, s)
		}
	})
}
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
	commonButton(c, label, false, func() { a.beginCommonDraft(&i) }).Disabled(e.Busy || e.DeleteSubmitted)
	commonButton(c, "删除", false, func() { d := *i.Document; e.Delete = &d; e.Message = "" }).TextColor(ui.Hex("#9a625e")).Disabled(e.Busy || pending || e.DeleteSubmitted)
}
func commonReadingParts(i Item) (CommonDraft, bool) {
	if i.Document == nil {
		return CommonDraft{}, false
	}
	d := commonDraftFor(i)
	raw, err := d.markdown()
	return d, err == nil && strings.TrimSuffix(raw, "\n") == strings.TrimSuffix(i.Body, "\n")
}
func (a *App) commonKnowledgeReader(c *ui.Context, s *pane) {
	e := &a.commonEdit
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		if s.Selected == nil {
			ui.Column(c).Grow(1).MinHeight(0).Padding(32).Gap(14).Children(func() {
				ui.Icon(c, homeCommon).FontSize(30).TextColor(homeMuted)
				title := "让通用经验延续到下一个项目"
				if len(s.Items) > 0 {
					title = "选择一条通用知识开始阅读"
				}
				homeText(c, title, homeHeading).FontSize(20)
				homeText(c, "个人偏好与通用经验独立保存，项目的明确约定优先。", homeBody).MaxWidth(500)
				if len(a.Config.Common) == 0 {
					homeText(c, "尚未关联通用知识库，请在设置中检查连接。", homeMeta)
					projectLink(c, "前往设置", func() { a.navigate("设置", 0) })
				} else if a.Config.ProjectControl != nil {
					commonButton(c, "新增第一条知识", true, func() { a.beginCommonDraft(nil) })
				}
			})
			return
		}
		i := *s.Selected
		ui.Row(c).Height(62).Shrink(0).Padding(0, 26, 0, 32).Gap(8).AlignItems(ui.Center).Children(func() {
			homeText(c, commonCategoryLabel(i.CommonCategory), homeMeta).FontWeight(500)
			homeText(c, "· "+displayDate(i.Date), homeMeta)
			ui.Spacer(c)
			anchor := readingAction(c, "内容信息", readingInfo, func() { a.projectUI.InfoOpen = !a.projectUI.InfoOpen; a.projectUI.InfoKey = i.key() })
			if a.projectUI.InfoKey != i.key() {
				a.projectUI.InfoOpen = false
			}
			a.contentInformation(c, anchor, i)
			readingAction(c, "复制内容", readingCopy, func() { c.WriteClipboard(itemPresentation(i).VisibleBody); a.copyFeedback("内容") }).Disabled(!i.BodyLoaded)
			a.commonReadActions(c, i)
		})
		if e.Delete != nil && e.Delete.ID == i.ID && e.Delete.Dataset == i.Dataset {
			a.commonDeletionBanner(c, i)
		}
		scroll := s.DetailScroll[i.key()]
		if scroll == nil {
			scroll = &ui.ScrollState{}
			s.DetailScroll[i.key()] = scroll
		}
		ui.Scroll(c.Key("common-detail-"+i.key())).Grow(1).MinHeight(0).TrackScroll(scroll).Padding(10, 32, 32, 32).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(760).Gap(20).Children(func() {
				parts, canonical := commonReadingParts(i)
				title := projectDisplayTitle(i)
				if canonical {
					title = parts.Title
				}
				homeText(c, title, homeHeading).FontSize(22).LineHeight(1.45)
				if s.Reading {
					homeText(c, "正在读取正文…", homeMeta)
				}
				if s.Error != "" {
					homeText(c, "内容未刷新 · "+s.Error, homeMeta)
				}
				if canonical {
					a.commonMarkdown(c, "common-body-"+i.key(), parts.Body)
					if parts.Applicability != "" {
						ui.Column(c).FillWidth().Padding(14, 16).Gap(8).Background(ui.Hex("#f6f9fc")).Border(1, homeBorder).Radius(8).Children(func() {
							homeText(c, "适用条件", homeMeta).FontWeight(500)
							a.commonMarkdown(c, "common-conditions-"+i.key(), parts.Applicability)
						})
					}
					if parts.Source != "" {
						ui.Column(c).Gap(6).Children(func() { homeText(c, "原始来源", homeMeta); homeText(c, parts.Source, homeBody).FontSize(12) })
					}
				} else {
					a.commonMarkdown(c, "common-original-"+i.key(), itemPresentation(i).VisibleBody)
				}
			})
		})
		ui.Row(c).Height(40).Shrink(0).Padding(0, 32).BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).Children(func() { homeText(c, "跨项目参考 · 项目明确约定优先", homeMeta) })
	})
}
func (a *App) commonMarkdown(c *ui.Context, key, body string) {
	if a.projectMD == nil {
		theme := markdown.Theme{}
		if a.md.Theme != nil {
			theme = *a.md.Theme
		}
		theme.FontSize = 13
		a.projectMD = &markdown.Options{Theme: &theme, CodeBlock: a.md.CodeBlock}
	}
	markdown.View(c.Key(key), body, a.projectMD)
}
func (a *App) commonDeletionBanner(c *ui.Context, i Item) {
	e := &a.commonEdit
	ui.Column(c).Shrink(0).Margin(0, 24, 10, 24).Padding(12, 16).Gap(8).Border(1, ui.Hex("#e5d7d5")).Radius(8).Background(ui.Hex("#fcf8f7")).Children(func() {
		message := "删除“" + i.Title + "”？官方确认后将移除此条知识及关联索引。"
		if e.DeleteSubmitted {
			message = "删除结果尚未确认，请核对原操作，不重复删除。"
		}
		homeText(c, message, homeBody).FontSize(12).MaxLines(3)
		ui.Row(c).Gap(10).Children(func() {
			if e.DeleteSubmitted {
				commonButton(c, "核对原删除结果", false, a.confirmCommonDeletion).Disabled(e.Busy)
			} else {
				commonButton(c, "确认删除此条", false, func() { a.deleteCommonKnowledge(*i.Document) }).Disabled(e.Busy).TextColor(ui.Hex("#9a625e"))
				projectLink(c, "保留知识", func() { e.Delete = nil })
			}
		})
	})
}
func (a *App) confirmCommonDeletion() {
	e := &a.commonEdit
	if e.Busy || !e.DeleteSubmitted || e.Delete == nil {
		return
	}
	e.Busy = true
	d, p, op := *e.Delete, e.DeleteTarget, e.Generation
	client := a.Client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		docs, err := client.overviewDocuments(ctx, p)
		present := false
		for _, doc := range docs {
			if doc.ID == d.ID {
				present = true
			}
		}
		a.Update(func() {
			if op != a.commonEdit.Generation {
				return
			}
			e.Busy = false
			if err != nil {
				e.Message = "删除结果仍未确认：" + err.Error()
				return
			}
			if present {
				e.DeleteSubmitted = false
				e.Message = "官方仍保留此条知识；可重新确认删除，未自动重发。"
				return
			}
			e.DeleteSubmitted = false
			e.Delete = nil
			e.Message = "已核对：官方知识已移除"
			a.unavailableReadingItem(Item{ID: d.ID, Dataset: p.Dataset, Kind: "知识", Document: &d})
			if cur, ok := a.current(); ok && a.page == "通用知识" && cur.Dataset == p.Dataset {
				a.load(a.state())
			}
		})
	}()
}
func (a *App) commonKnowledgeEditor(c *ui.Context) {
	e := &a.commonEdit
	_, h := c.Size()
	locked := e.Busy || e.Submitted
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		ui.Row(c).Height(62).Shrink(0).Padding(0, 32).Gap(12).AlignItems(ui.Center).Children(func() {
			title := "新增通用知识"
			if e.Existing != nil {
				title = "编辑通用知识"
			}
			if e.Submitted {
				title = "保存结果"
			}
			homeText(c, title, homeHeading).FontSize(15)
			ui.Spacer(c)
			category := commonCategoryLabel(e.Draft.Category)
			ui.Select(c, &category, []string{"个人偏好", "通用经验"}).Label("知识分类").Width(126).Disabled(locked || e.Existing != nil).OnChange(func() {
				if category == "个人偏好" {
					e.Draft.Category = "preference"
				} else {
					e.Draft.Category = "experience"
				}
			})
		})
		ui.Scroll(c.Key("common-compact-editor")).Grow(1).MinHeight(0).TrackScroll(&a.commonPage.EditorScroll).Padding(8, 32, 24, 32).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(760).Gap(16).Children(func() {
				if len(a.Config.Common) > 1 {
					homeText(c, "保存到："+e.Target.Name, homeMeta)
				}
				ui.Column(c).Gap(7).Children(func() {
					homeText(c, "标题", homeMeta)
					ui.TextInput(c.Key("common-draft-title-"+fmt.Sprint(e.Generation)), &e.Draft.Title).Label("通用知识标题").Placeholder("一句话概括这条知识").FillWidth().Height(38).FontSize(14).Background(ui.Hex("#ffffff")).Border(1, homeBorder).Radius(6).Disabled(locked)
				})
				ui.Column(c).Gap(7).Children(func() {
					homeText(c, "正文", homeMeta)
					ui.TextArea(c.Key("common-draft-body-"+fmt.Sprint(e.Generation)), &e.Draft.Body).Label("通用知识正文").Placeholder("写下可复用的约定或经验，支持 Markdown").FillWidth().Height(min(float32(260), max(float32(180), (h-380)/2))).FontSize(13).Background(ui.Hex("#ffffff")).Border(1, homeBorder).Radius(6).Disabled(locked)
				})
				label := "补充适用条件与来源（可选）"
				if a.commonPage.Options {
					label = "收起补充信息"
				}
				projectLink(c, label, func() { a.commonPage.Options = !a.commonPage.Options })
				if a.commonPage.Options {
					ui.Column(c).Gap(7).Children(func() {
						homeText(c, "适用条件", homeMeta)
						ui.TextArea(c.Key("common-draft-applicability-"+fmt.Sprint(e.Generation)), &e.Draft.Applicability).Label("通用知识适用条件").Placeholder("例如：适用于 TypeScript 项目").FillWidth().Height(72).FontSize(12).Background(ui.Hex("#ffffff")).Border(1, homeBorder).Radius(6).Disabled(locked)
					})
					ui.Column(c).Gap(7).Children(func() {
						homeText(c, "原始来源", homeMeta)
						ui.TextInput(c.Key("common-draft-source-"+fmt.Sprint(e.Generation)), &e.Draft.Source).Label("通用知识来源").Placeholder("可填写出处、链接或说明").FillWidth().Height(34).FontSize(12).Background(ui.Hex("#ffffff")).Border(1, homeBorder).Radius(6).Disabled(locked || e.Existing != nil)
					})
					if e.Existing != nil {
						homeText(c, "编辑保留创建时的分类与原始来源。", homeMeta)
					}
				}
				if e.Submitted {
					ui.Column(c).Padding(14, 16).Gap(9).Border(1, homeBorder).Radius(8).Background(ui.Hex("#f6f9fc")).Children(func() {
						saved := "待核对"
						if e.SaveVerified {
							saved = "已核对"
						}
						processing := e.Processing
						if processing == "" {
							processing = "待核对"
						}
						homeText(c, "正文保存："+saved, homeBody).FontSize(12)
						homeText(c, "处理状态："+processing, homeBody).FontSize(12)
						if e.Receipt.PipelineRunID != "" {
							homeText(c, "原处理记录："+e.Receipt.PipelineRunID, homeMeta).MaxLines(2)
						}
					})
				}
				if e.Message != "" {
					homeText(c, e.Message, homeBody).FontSize(12).LineHeight(1.7)
				}
			})
		})
		ui.Row(c).MinHeight(64).Shrink(0).Padding(12, 24, 12, 32).Gap(10).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(homeBorder).Children(func() {
			if a.commonPage.Discard {
				homeText(c, "放弃此份未保存草稿？", homeMeta).Grow(1)
				commonButton(c, "继续编辑", false, func() { a.commonPage.Discard = false })
				commonButton(c, "放弃并返回", false, a.discardCommonDraft)
				return
			}
			status := "仅保存在当前编辑草稿"
			if e.Busy {
				status = "正在核对官方操作…"
			} else if e.Submitted {
				status = "本次已提交，不重复发送"
			}
			homeText(c, status, homeMeta).Grow(1).MinWidth(0).MaxLines(2)
			if !e.Submitted {
				projectLink(c, "放弃草稿", func() { a.commonPage.Discard = true }).Disabled(e.Busy)
			}
			commonButton(c, "返回阅读", false, func() { e.Open = false }).Disabled(e.Busy)
			if e.Submitted {
				commonButton(c, "核对保存结果", true, a.confirmCommonKnowledge).Disabled(e.Busy)
				if e.SaveVerified && (e.Processing == "已完成" || strings.HasPrefix(e.Processing, "失败")) {
					commonButton(c, "完成", false, func() { e.Completed = true; e.Open = false })
				}
			} else {
				_, err := e.Draft.markdown()
				commonButton(c, "保存", true, a.saveCommonKnowledge).Disabled(e.Busy || err != nil)
			}
		})
	})
}
