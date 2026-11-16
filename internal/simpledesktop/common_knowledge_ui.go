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
	Generation                               uint64
	Completed, SaveVerified, DeleteSubmitted bool
	Processing                               string
	DeleteTarget                             Project
	Draft                                    CommonDraft
	Existing                                 *Document
	Open, Busy, Submitted                    bool
	Message                                  string
	Receipt                                  CommonWriteReceipt
	Target                                   Project
	Category                                 string
	Delete                                   *Document
	Baseline                                 [32]byte
}

func (a *App) withdrawCommonEditor(dataset string) {
	e := &a.commonEdit
	if e.Existing != nil && (dataset == "" || e.Target.Dataset == dataset) {
		e.Open = false
		e.Existing = nil
		e.Draft = CommonDraft{}
		e.Baseline = [32]byte{}
		e.Message = "原记忆已失效或无权读取，编辑内容已撤下；在途操作仍按实际回执核对。"
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

func (a *App) commonToolbar(c *ui.Context) { a.commonListToolbar(c) }

func (a *App) commonCategoryFilter() string {
	switch a.commonEdit.Category {
	case "个人偏好":
		return "preference"
	case "通用经验":
		return "experience"
	}
	return ""
}

func (a *App) commonActions(c *ui.Context, i Item) { a.commonReadActions(c, i) }

func (a *App) deleteCommonKnowledge(document Document) {
	p, ok := a.current()
	if !ok || a.commonEdit.Busy || a.commonEdit.DeleteSubmitted {
		return
	}
	if document.Dataset != p.Dataset || commonCategory(document) == "" {
		a.commonEdit.Message = "请选择当前记忆库的有效通用记忆"
		return
	}
	a.commonEdit.Busy = true
	a.commonEdit.DeleteSubmitted = true
	a.commonEdit.Delete = &document
	a.commonEdit.DeleteTarget = p
	op := a.commonEdit.Generation
	client := a.Client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		err := client.deleteCommon(ctx, p, document)
		a.Update(func() {
			if op != a.commonEdit.Generation {
				return
			}
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
			a.commonEdit.DeleteSubmitted = false
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

func (a *App) commonEditor(c *ui.Context) { a.commonKnowledgeEditor(c) }

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
	op := e.Generation
	client, p, draft, existing, baseline := a.Client, e.Target, e.Draft, e.Existing, e.Baseline
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 125*time.Second)
		defer cancel()
		if existing != nil {
			current, err := client.read(ctx, Item{ID: existing.ID, Dataset: existing.Dataset, Kind: "知识", Document: existing})
			if err != nil || sha256.Sum256([]byte(current.Body)) != baseline {
				a.Update(func() {
					if op != a.commonEdit.Generation {
						return
					}
					e.Busy = false
					if readingUnavailable(err) {
						a.withdrawCommonEditor(p.Dataset)
						a.InvalidateReading(p.Dataset)
						return
					}
					e.Message = "原记忆已变化或无法读取，请刷新后重新编辑；本次未提交修改。"
				})
				return
			}
		}
		receipt, err := client.writeCommon(ctx, p, draft, existing)
		a.Update(func() {
			if op != a.commonEdit.Generation {
				return
			}
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
	op := e.Generation
	client, p, receipt, draft := a.Client, e.Target, e.Receipt, e.Draft
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		document, message, err := client.confirmCommon(ctx, p, receipt, draft)
		a.Update(func() {
			if op != a.commonEdit.Generation {
				return
			}
			if document.ID != "" {
				e.SaveVerified = true
				e.Receipt.DataID = document.ID
			}
			e.Processing = commonProcessingStatus(message)
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
