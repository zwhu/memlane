package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/reading"
	"github.com/zwhu/memlane/app/settings"
)

func (a *App) commonDeletionBanner(c *ui.Context, i Item) {
	e := &a.commonEdit
	if !e.DeleteSubmitted {
		asking := true
		reading.ConfirmDelete(c, &tok.Tokens, &asking, i.ID, e.Busy,
			func() { e.Delete = nil }, func() { a.deleteCommonKnowledge(*i.Document) })
		return
	}

	ui.Row(c).Shrink(0).Padding(sp2, sp10).Gap(sp3).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(tok.Danger.Alpha(.35)).Background(tok.Danger.Alpha(.08)).Children(func() {
		v4StatusDot(c, "fail")
		ui.Text(c, "删除结果尚未确认，请核对原操作，不重复删除。").Font("system-ui").FontSize(fsCaption).TextColor(tok.Text1).Grow(1).MinWidth(0).MaxLines(2)
		v4SmallButton(c, "核对原删除结果", nil, a.confirmCommonDeletion).Disabled(e.Busy)
	})
}
func (a *App) confirmCommonDeletion() {
	e := &a.commonEdit
	if e.Busy || !a.syncReadingIdentity() || !e.DeleteSubmitted || e.Delete == nil {
		return
	}
	e.Busy = true
	d, p, op := *e.Delete, e.DeleteTarget, e.Generation
	authEpoch := a.authEpoch
	control, ok := a.accessClient()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		var present bool
		var err error
		if ok {
			var controller reading.CommonController
			controller, err = commonControllerFor(control)
			if err == nil {
				_, err = controller.Get(ctx, d.ID)
			}
			if legacyReadingRouteMissing(err) {
				_, err = control.documentLookupV2(ctx, p.Dataset, d.ID)
			}
			present = err == nil
			if reading.Status(err, 404) || settings.IsControlErrorCode(err, http.StatusNotFound, "document_not_found") {
				err = nil
			}
		} else {
			err = fmt.Errorf("控制面服务不可用，无法核对官方删除结果")
		}
		a.Update(func() {
			if op != a.commonEdit.Generation {
				return
			}
			e.Busy = false
			if authEpoch != a.authEpoch || !a.syncReadingIdentity() {
				return
			}
			if err != nil {
				e.Message = "删除结果仍未确认：" + err.Error()
				return
			}
			if present {
				e.DeleteSubmitted = false
				e.Message = "官方仍保留此条记忆；可重新确认删除，未自动重发。"
				return
			}
			e.DeleteSubmitted = false
			e.Delete = nil
			e.Message = "已核对：官方记忆已移除"
			// Reconciliation after an unknown delete response must not blindly decrement
			// overview totals if the item was already removed by a prior refresh.
			a.applyKnowledgeDocumentDeleted(Item{ID: d.ID, Dataset: p.Dataset, Kind: "知识", Date: documentDate(d), Document: &d}, false)
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
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Background(tok.Bg).Children(func() {
		ui.Row(c).Label("通用记忆编辑工具栏").Height(listHeaderH).Shrink(0).Padding(0, sp10).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(tok.Border).Children(func() {
			ui.Row(c).FillWidth().MaxWidth(readerMaxW).AlignItems(ui.Center).Gap(sp3).Children(func() {
				title := "新增通用记忆"
				if e.Existing != nil {
					title = "编辑通用记忆"
				}
				if e.Submitted {
					title = "保存结果"
				}
				ui.Text(c, title).Font("system-ui").FontSize(fsBody).FontWeight(550).TextColor(tok.Text1).SingleLine()
				ui.Spacer(c)
				catIdx := 0
				if e.Draft.Category == "experience" {
					catIdx = 1
				}
				catDisabled := locked || e.Existing != nil
				seg := v4Segmented(c, "common-edit-category", "记忆分类", &catIdx, []string{"个人偏好", "通用经验"}, func() {
					if catDisabled {
						return
					}
					if catIdx == 0 {
						e.Draft.Category = "preference"
					} else {
						e.Draft.Category = "experience"
					}
				})
				if catDisabled {
					seg.Disabled(true).Opacity(0.55).Tooltip("编辑已有记忆时不可切换分类")
				}
			})
		})
		ui.Scroll(c.Key("common-compact-editor")).Grow(1).MinHeight(0).TrackScroll(&a.commonPage.EditorScroll).Padding(sp6, sp10, sp8, sp10).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(readerMaxW).Gap(sp5).Children(func() {
				if len(a.Config.Common) > 1 {
					homeText(c, "保存到："+e.Target.Name, homeMeta)
				}
				ui.Field(c, "标题", func() {
					ui.TextInput(c.Key("common-draft-title-"+fmt.Sprint(e.Generation)), &e.Draft.Title).Label("通用记忆标题").Placeholder("一句话概括这条记忆").FillWidth().Height(36).FontSize(fsBody).Disabled(locked)
				})
				ui.Field(c, "正文", func() {
					ui.TextArea(c.Key("common-draft-body-"+fmt.Sprint(e.Generation)), &e.Draft.Body).Label("通用记忆正文").Placeholder("写下可复用的约定或经验，支持 Markdown").FillWidth().Height(min(float32(260), max(float32(180), (h-380)/2))).FontSize(fsReading).Disabled(locked)
				})
				ui.Column(c).FillWidth().Padding(sp4, 0, 0, 0).Gap(sp4).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Children(func() {
					label := "补充适用条件与来源（可选）"
					if a.commonPage.Options {
						label = "收起补充信息"
					}
					v4SmallButton(c, label, nil, func() { a.commonPage.Options = !a.commonPage.Options })
					if a.commonPage.Options {
						ui.Field(c, "适用条件", func() {
							ui.TextArea(c.Key("common-draft-applicability-"+fmt.Sprint(e.Generation)), &e.Draft.Applicability).Label("通用记忆适用条件").Placeholder("例如：适用于 TypeScript 项目").FillWidth().Height(72).FontSize(fsFootnote).Disabled(locked)
						})
						ui.Field(c, "原始来源", func() {
							ui.TextInput(c.Key("common-draft-source-"+fmt.Sprint(e.Generation)), &e.Draft.Source).Label("通用记忆来源").Placeholder("可填写出处、链接或说明").FillWidth().Height(34).FontSize(fsFootnote).Disabled(locked || e.Existing != nil)
						})
						if e.Existing != nil {
							homeText(c, "编辑保留创建时的分类与原始来源。", homeMeta)
						}
					}
				})
				if e.Submitted {
					ui.Column(c).FillWidth().Padding(sp3, sp4).Gap(sp2).BorderWidth(1, 0, 0, 0).BorderColor(tok.Border).Background(tok.Fill1).Radius(radius).Children(func() {
						saved := "待核对"
						if e.SaveVerified {
							saved = "已核对"
						}
						processing := e.Processing
						if processing == "" {
							processing = "待核对"
						}
						homeText(c, "正文保存："+saved, homeBody).FontSize(fsFootnote)
						homeText(c, "提炼状态："+processing, homeBody).FontSize(fsFootnote)
						if e.Receipt.PipelineRunID != "" {
							homeText(c, "原提炼记录："+e.Receipt.PipelineRunID, homeMeta).MaxLines(2)
						}
					})
				}
				if e.Message != "" {
					homeText(c, e.Message, homeBody).FontSize(fsFootnote).LineHeight(1.7)
				}
			})
		})
		appFooter(c, "通用记忆编辑栏").Padding(0, sp6).Gap(sp2).Children(func() {
			status := "仅保存在当前编辑草稿"
			if e.Busy {
				status = "正在核对官方操作…"
			} else if e.Submitted {
				status = "本次已提交，不重复发送"
			}
			homeText(c, status, homeMeta).Grow(1).MinWidth(0).MaxLines(2)
			if !e.Submitted {
				v4SmallButton(c, "放弃草稿", nil, func() { a.commonPage.Discard = true }).Disabled(e.Busy)
			}
			v4OutlineButton(c, "返回阅读", nil, func() { e.Open = false }).Disabled(e.Busy)
			if e.Submitted {
				ui.PrimaryButton(c, "核对保存结果").OnClick(a.confirmCommonKnowledge).Disabled(e.Busy)
				if e.SaveVerified && (e.Processing == "已完成" || strings.HasPrefix(e.Processing, "失败")) {
					v4OutlineButton(c, "完成", nil, func() { e.Completed = true; e.Open = false })
				} else {
					v4SmallButton(c, "结束本次记录", nil, func() {
						category := e.Category
						a.commonGeneration++
						a.commonEdit = commonEditorState{Generation: a.commonGeneration, Category: category}
						a.commonPage = commonKnowledgePageState{}
					}).Disabled(e.Busy)
				}
			} else {
				_, err := e.Draft.FormatDocument()
				ui.PrimaryButton(c, "保存").OnClick(a.saveCommonKnowledge).Disabled(e.Busy || err != nil)
			}
		})
	})
	reading.ConfirmDiscard(c, &tok.Tokens, &a.commonPage.Discard, func() {
		a.commonPage.Discard = false
		a.discardCommonDraft()
	})
}
