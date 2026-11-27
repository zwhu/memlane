package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/reading"
)

type commonEditorState = reading.CommonEditorState[Document, Project, CommonWriteReceipt]

func (a *App) withdrawCommonEditor(dataset string) {
	e := &a.commonEdit
	if e.Delete != nil && (dataset == "" || e.Delete.Dataset == dataset || e.DeleteTarget.Dataset == dataset) {
		e.Delete = nil
		e.DeleteTarget = Project{}
		e.DeleteSubmitted = false
	}
	existingMatches := e.Existing != nil && (dataset == "" || e.Target.Dataset == dataset || e.Existing.Dataset == dataset)
	hasUnsavedDraft := e.Open || strings.TrimSpace(e.Draft.Title+e.Draft.Body+e.Draft.Applicability+e.Draft.Source) != ""
	draftMatches := hasUnsavedDraft && (dataset == "" || e.Target.Dataset == dataset)
	if existingMatches || draftMatches {
		e.Open = false
		e.Existing = nil
		e.Draft = CommonDraft{}
		e.Baseline = [32]byte{}
		e.Message = "原记忆已失效或无权读取，编辑内容已撤下；在途操作仍按实际回执核对。"
	}
}

func commonDraftFor(i Item) CommonDraft {
	source, _ := i.Document.Metadata["source"].(string)
	return reading.ParseDocument(i.Title, i.Body, commonCategory(*i.Document), source)
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
	if !a.syncReadingIdentity() {
		return
	}
	a.commonEdit.Busy = true
	a.commonEdit.DeleteSubmitted = true
	a.commonEdit.Delete = &document
	a.commonEdit.DeleteTarget = p
	deletedItem := Item{ID: document.ID, Dataset: p.Dataset, Kind: "知识", Date: documentDate(document), Document: &document}
	op := a.commonEdit.Generation
	authEpoch := a.authEpoch
	client := a.Client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		controller, managed, err := client.commonAPI()
		if err == nil && managed {
			err = controller.Delete(ctx, document.ID)
		}
		if !managed || legacyReadingRouteMissing(err) {
			err = client.deleteCommon(ctx, p, document)
		}
		a.Update(func() {
			if op != a.commonEdit.Generation {
				return
			}
			a.commonEdit.Busy = false
			if authEpoch != a.authEpoch || !a.syncReadingIdentity() {
				return
			}
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
			a.applyKnowledgeDocumentDeleted(deletedItem, true)
			if current, ok := a.current(); ok && current.Dataset == p.Dataset && a.page == "通用知识" {
				a.load(a.state())
			}
		})
	}()
}

func (a *App) commonEditor(c *ui.Context) { a.commonKnowledgeEditor(c) }

func (a *App) saveCommonKnowledge() {
	e := &a.commonEdit
	if !a.syncReadingIdentity() {
		return
	}
	if !e.StartSave() {
		return
	}
	formatted, _ := e.Draft.FormatDocument()
	expectedSHA := reading.BaselineSHA256(strings.TrimSuffix(formatted, "\n"))
	expectedCategory := e.Draft.Category
	op := e.Generation
	authEpoch := a.authEpoch
	client, p, draft, existing, baseline := a.Client, e.Target, e.Draft, e.Existing, e.Baseline
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 125*time.Second)
		defer cancel()
		receipt, err := saveCommonDraft(ctx, client, p, draft, existing, baseline)
		receipt.ExpectedSHA256 = expectedSHA
		receipt.HasExpectedSHA = true
		receipt.ExpectedCategory = expectedCategory
		if errors.Is(err, reading.ErrCommonChanged) {
			a.Update(func() {
				if op == a.commonEdit.Generation {
					e.Busy = false
					if authEpoch != a.authEpoch || !a.syncReadingIdentity() {
						return
					}
					e.Message = "原记忆已变化或无法读取，请刷新后重新编辑；本次未提交修改。"
				}
			})
			return
		}
		a.Update(func() {
			if op != a.commonEdit.Generation {
				return
			}
			e.Busy = false
			e.Receipt = receipt
			e.Submitted = true
			if authEpoch != a.authEpoch || !a.syncReadingIdentity() {
				return
			}
			if err != nil {
				if readingUnavailable(err) {
					a.withdrawCommonEditor(p.Dataset)
					a.InvalidateReading(p.Dataset)
				}
				e.Message = err.Error()
				return
			}
			e.Message = "官方已接收本次操作；正文保存和提炼终态尚待核对。"
			if existing != nil {
				a.unavailableReadingItem(Item{ID: existing.ID, Dataset: p.Dataset, Kind: "知识", Document: existing})
			}
		})
	}()
}

func saveCommonDraft(ctx context.Context, client Client, p Project, draft CommonDraft, existing *Document, baseline [32]byte) (CommonWriteReceipt, error) {
	controller, managed, err := client.commonAPI()
	if err != nil {
		return CommonWriteReceipt{}, err
	}
	if managed {
		id := ""
		if existing != nil {
			id = existing.ID
		}
		out, err := controller.Save(ctx, id, draft, baseline)
		if !legacyReadingRouteMissing(err) {
			receipt := CommonWriteReceipt{Gateway: true, DatasetID: p.Dataset, DataID: out.DocumentID}
			if err == nil && (!out.OK || out.DocumentID == "" || out.DatasetID != p.Dataset) {
				err = fmt.Errorf("保存回执不完整，请核对原操作")
			}
			return receipt, err
		}
	}
	if existing != nil {
		current, err := client.read(ctx, Item{ID: existing.ID, Dataset: existing.Dataset, Kind: "知识", Document: existing})
		if readingUnavailable(err) {
			return CommonWriteReceipt{}, err
		}
		if err != nil || reading.BaselineSHA256(current.Body) != baseline {
			return CommonWriteReceipt{}, reading.ErrCommonChanged
		}
	}
	return client.writeCommon(ctx, p, draft, existing)
}

func confirmGatewayCommon(ctx context.Context, client Client, p Project, receipt CommonWriteReceipt, draft CommonDraft) (Document, string, error) {
	controller, ok, err := client.commonAPI()
	if err != nil {
		return Document{}, "", err
	}
	if !ok || receipt.DataID == "" {
		return Document{}, "正文保存待核对", nil
	}
	detail, err := controller.Get(ctx, receipt.DataID)
	if err != nil {
		return Document{}, "正文保存待核对", err
	}
	body, err := draft.FormatDocument()
	if err != nil {
		if !receipt.HasExpectedSHA {
			return Document{}, "", err
		}
		if detail.DatasetID != p.Dataset || reading.BaselineSHA256(strings.TrimSuffix(detail.Content, "\n")) != receipt.ExpectedSHA256 {
			return Document{}, "正文保存待核对", reading.ErrCommonChanged
		}
	} else if detail.DatasetID != p.Dataset || detail.Content != body {
		return Document{}, "正文保存待核对", reading.ErrCommonChanged
	}
	row := catalogRowFromAPI(detail.CatalogItem)
	doc := row.toItem("").Document
	if doc == nil || doc.ID != receipt.DataID {
		return Document{}, "", fmt.Errorf("通用记忆回执不完整")
	}
	return *doc, reading.CommonSaveStatus(detail.PipelineStatus), nil
}

func (a *App) confirmCommonKnowledge() {
	e := &a.commonEdit
	if e.Busy || !e.Submitted {
		return
	}
	a.syncReadingIdentity()
	e.Busy = true
	op := e.Generation
	authEpoch := a.authEpoch
	client, p, receipt, draft := a.Client, e.Target, e.Receipt, e.Draft
	control, hasControl := a.accessClient()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		var document Document
		var message string
		var err error
		if receipt.Gateway {
			document, message, err = confirmGatewayCommon(ctx, client, p, receipt, draft)
		} else {
			document, message, err = client.confirmCommon(ctx, control, hasControl, p, receipt, draft)
		}
		a.Update(func() {
			if op != a.commonEdit.Generation {
				return
			}
			if authEpoch != a.authEpoch || !a.syncReadingIdentity() {
				e.Busy = false
				return
			}
			if document.ID != "" {
				e.Receipt.DataID = document.ID
			}
			e.ApplyVerification(document.ID != "", message)
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
