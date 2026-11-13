package simpledesktop

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type commonEditorState struct {
	Draft                 CommonDraft
	Existing              *Document
	Open, Busy, Submitted bool
	Message               string
	Receipt               CommonWriteReceipt
	Target                Project
	Category              string
	Delete                *Document
	Baseline              [32]byte
}

func (a *App) withdrawCommonEditor(dataset string) {
	e := &a.commonEdit
	if e.Existing != nil && (dataset == "" || e.Target.Dataset == dataset) {
		e.Open = false
		e.Existing = nil
		e.Draft = CommonDraft{}
		e.Baseline = [32]byte{}
		e.Message = "原知识已失效或无权读取，编辑内容已撤下；在途操作仍按实际回执核对。"
	}
}

func commonDraftFor(i Item) CommonDraft {
	draft := CommonDraft{Category: commonCategory(*i.Document)}
	draft.Source, _ = i.Document.Metadata["source"].(string)
	body := strings.TrimSpace(i.Body)
	if first, rest, ok := strings.Cut(body, "\n"); ok && strings.HasPrefix(first, "# ") {
		draft.Title = strings.TrimPrefix(first, "# ")
		body = strings.TrimSpace(rest)
	} else {
		draft.Title = i.Title
	}
	if suffix := "\n\n## 来源\n\n" + draft.Source; draft.Source != "" && strings.HasSuffix(body, suffix) {
		body = strings.TrimSuffix(body, suffix)
	}
	if at := strings.LastIndex(body, "\n\n## 适用条件\n\n"); at >= 0 {
		draft.Applicability = body[at+len("\n\n## 适用条件\n\n"):]
		body = body[:at]
	}
	draft.Body = body
	return draft
}

func (a *App) commonToolbar(c *ui.Context) {
	if a.Config.ProjectControl == nil {
		return
	}
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, "新增通用知识").Disabled(a.commonEdit.Busy).OnClick(func() {
			p, ok := a.current()
			if !ok || p.Dataset == "" {
				return
			}
			a.commonEdit = commonEditorState{Open: true, Draft: CommonDraft{Category: "preference"}, Target: p, Category: a.commonEdit.Category}
		})
		category := a.commonEdit.Category
		if category == "" {
			category = "全部分类"
		}
		ui.Select(c, &category, []string{"全部分类", "个人偏好", "通用经验"}).Width(120).OnChange(func() { a.commonEdit.Category = category; s := a.state(); s.Offset = 0; a.load(s) })
	})
	if a.commonEdit.Message != "" && !a.commonEdit.Open {
		ui.Text(c, a.commonEdit.Message).FontSize(10).TextColor(muted).LineHeight(1.6)
	}
}

func (a *App) commonCategoryFilter() string {
	switch a.commonEdit.Category {
	case "个人偏好":
		return "preference"
	case "通用经验":
		return "experience"
	}
	return ""
}

func (a *App) commonActions(c *ui.Context, i Item) {
	if a.page != "通用知识" || a.Config.ProjectControl == nil || i.Document == nil || commonCategory(*i.Document) == "" || !i.BodyLoaded {
		return
	}
	ui.Row(c).Gap(8).Children(func() {
		ui.Button(c, "编辑知识").Disabled(a.commonEdit.Busy).OnClick(func() {
			p, ok := a.current()
			if !ok {
				return
			}
			d := *i.Document
			a.commonEdit = commonEditorState{Open: true, Draft: commonDraftFor(i), Existing: &d, Target: p, Category: a.commonEdit.Category, Baseline: sha256.Sum256([]byte(i.Body))}
		})
		ui.Button(c, "删除知识").Disabled(a.commonEdit.Busy).OnClick(func() {
			d := *i.Document
			a.commonEdit.Delete = &d
			a.commonEdit.Message = "删除后将移除这条官方知识及关联索引。"
		})
		if a.commonEdit.Delete != nil && a.commonEdit.Delete.ID == i.ID {
			ui.Button(c, "确认删除此条").Disabled(a.commonEdit.Busy).OnClick(func() { a.deleteCommonKnowledge(*i.Document) })
			button(c, "保留", func() { a.commonEdit.Delete = nil })
		}
	})
}

func (a *App) deleteCommonKnowledge(document Document) {
	p, ok := a.current()
	if !ok || a.commonEdit.Busy {
		return
	}
	a.commonEdit.Busy = true
	client := a.Client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		err := client.deleteCommon(ctx, p, document)
		a.Update(func() {
			a.commonEdit.Busy = false
			if err != nil {
				if readingUnavailable(err) {
					a.withdrawCommonEditor(p.Dataset)
					a.InvalidateReading(p.Dataset)
				}
				a.commonEdit.Message = err.Error()
				return
			}
			a.commonEdit.Delete = nil
			if a.commonEdit.Existing != nil && a.commonEdit.Existing.ID == document.ID {
				a.withdrawCommonEditor(p.Dataset)
			}
			a.commonEdit.Message = "官方已确认删除"
			a.unavailableReadingItem(Item{ID: document.ID, Dataset: p.Dataset, Kind: "知识", Document: &document})
			if current, ok := a.current(); ok && current.Dataset == p.Dataset && a.page == "通用知识" {
				a.load(a.state())
			}
		})
	}()
}

func (a *App) commonEditor(c *ui.Context) {
	e := &a.commonEdit
	ui.Scroll(c.Key("common-editor")).Grow(1).MinHeight(0).Padding(28, 30).Gap(14).Children(func() {
		ui.Text(c, "管理通用知识").FontSize(21).Bold()
		ui.Text(c, "独立保存，供可读的 Agent 跨项目参考；项目明确约定优先。").FontSize(12).TextColor(muted)
		category := "个人偏好"
		if e.Draft.Category == "experience" {
			category = "通用经验"
		}
		ui.Select(c, &category, []string{"个人偏好", "通用经验"}).Width(180).Disabled(e.Busy || e.Submitted || e.Existing != nil).OnChange(func() {
			if category == "个人偏好" {
				e.Draft.Category = "preference"
			} else {
				e.Draft.Category = "experience"
			}
		})
		ui.Text(c, "标题").FontSize(11).TextColor(muted)
		ui.TextInput(c, &e.Draft.Title).Height(34).Label("通用知识标题").Disabled(e.Busy || e.Submitted)
		ui.Text(c, "正文").FontSize(11).TextColor(muted)
		ui.TextArea(c, &e.Draft.Body).Height(220).Label("通用知识正文").Disabled(e.Busy || e.Submitted)
		ui.Text(c, "适用条件").FontSize(11).TextColor(muted)
		ui.TextArea(c, &e.Draft.Applicability).Height(70).Label("通用知识适用条件").Disabled(e.Busy || e.Submitted)
		ui.Text(c, "原始来源").FontSize(11).TextColor(muted)
		ui.TextInput(c, &e.Draft.Source).Height(34).Label("通用知识来源").Disabled(e.Busy || e.Submitted || e.Existing != nil)
		if e.Existing != nil {
			ui.Text(c, "编辑保留创建时的分类与原始来源。").FontSize(11).TextColor(muted)
		}
		if e.Message != "" {
			ui.Text(c, e.Message).FontSize(12).LineHeight(1.8)
		}
		ui.Row(c).Gap(10).Children(func() {
			ui.Button(c, "保存到通用知识").Disabled(e.Busy || e.Submitted).OnClick(a.saveCommonKnowledge)
			if e.Submitted {
				ui.Button(c, "核对原保存结果").Disabled(e.Busy).OnClick(a.confirmCommonKnowledge)
			}
			ui.Button(c, "返回阅读").Disabled(e.Busy).OnClick(func() { e.Open = false })
		})
		if e.Receipt.PipelineRunID != "" {
			ui.Text(c, "原处理记录："+e.Receipt.PipelineRunID).FontSize(10).TextColor(muted)
		}
	})
}

func (a *App) saveCommonKnowledge() {
	e := &a.commonEdit
	if e.Busy || e.Submitted {
		return
	}
	if _, err := e.Draft.markdown(); err != nil {
		e.Message = err.Error()
		return
	}
	e.Busy = true
	e.Message = "正在提交，保存和处理结果将分别核对…"
	client, p, draft, existing, baseline := a.Client, e.Target, e.Draft, e.Existing, e.Baseline
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 125*time.Second)
		defer cancel()
		if existing != nil {
			current, err := client.read(ctx, Item{ID: existing.ID, Dataset: existing.Dataset, Kind: "知识", Document: existing})
			if err != nil || sha256.Sum256([]byte(current.Body)) != baseline {
				a.Update(func() {
					e.Busy = false
					if readingUnavailable(err) {
						a.withdrawCommonEditor(p.Dataset)
						a.InvalidateReading(p.Dataset)
						return
					}
					e.Message = "原知识已变化或无法读取，请刷新后重新编辑；本次未提交修改。"
				})
				return
			}
		}
		receipt, err := client.writeCommon(ctx, p, draft, existing)
		a.Update(func() {
			e.Busy = false
			e.Receipt = receipt
			e.Submitted = true
			if err != nil {
				if readingUnavailable(err) {
					a.withdrawCommonEditor(p.Dataset)
					a.InvalidateReading(p.Dataset)
				}
				e.Message = err.Error()
				return
			}
			e.Message = "官方已接收本次操作；正文保存和处理终态尚待核对。"
			if existing != nil {
				a.unavailableReadingItem(Item{ID: existing.ID, Dataset: p.Dataset, Kind: "知识", Document: existing})
			}
		})
	}()
}

func (a *App) confirmCommonKnowledge() {
	e := &a.commonEdit
	if e.Busy || !e.Submitted {
		return
	}
	e.Busy = true
	client, p, receipt, draft := a.Client, e.Target, e.Receipt, e.Draft
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		document, message, err := client.confirmCommon(ctx, p, receipt, draft)
		a.Update(func() {
			e.Busy = false
			e.Message = message
			if err != nil {
				if readingUnavailable(err) {
					a.withdrawCommonEditor(p.Dataset)
					a.InvalidateReading(p.Dataset)
				}
				e.Message = fmt.Sprint(message, "；", err)
				return
			}
			e.Existing = &document
			e.Receipt.DataID = document.ID
			a.unavailableReadingItem(Item{ID: document.ID, Dataset: p.Dataset, Kind: "知识", Document: &document})
			if current, ok := a.current(); ok && current.Dataset == p.Dataset && a.page == "通用知识" {
				a.load(a.state())
			}
		})
	}()
}
