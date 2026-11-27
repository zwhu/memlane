package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/reading"
	"github.com/zwhu/memlane/app/settings"
)

// Common knowledge is a normal official document with explicit metadata. It
// never impersonates a session learning and never copies a project document.
type CommonDraft = reading.CommonDraft

func commonCategory(d Document) string { return reading.CommonCategory(d.Metadata) }

func (c Client) commonAPI() (reading.CommonController, bool, error) {
	control, ok := c.controlClient()
	if !ok {
		return reading.CommonController{}, false, nil
	}
	client, err := newAppAPIClient(control, "")
	return reading.CommonController{Client: client}, true, err
}

type CommonWriteReceipt struct {
	Gateway          bool     `json:"-"`
	Status           string   `json:"status"`
	DatasetID        string   `json:"dataset_id"`
	DataID           string   `json:"data_id"`
	PipelineRunID    string   `json:"pipeline_run_id"`
	ContentHash      string   `json:"content_hash"`
	HTTPStatus       int      `json:"-"`
	OutcomeUnknown   bool     `json:"-"`
	ExpectedSHA256   [32]byte `json:"-"`
	HasExpectedSHA   bool     `json:"-"`
	ExpectedCategory string   `json:"-"`
}

// The receipt preserves the original official operation even on partial failure.
// Accepted/running is not a claim that the document is saved and processed.
func (c Client) writeCommon(ctx context.Context, p Project, draft CommonDraft, existing *Document) (CommonWriteReceipt, error) {
	var receipt CommonWriteReceipt
	body, err := draft.FormatDocument()
	if err != nil {
		return receipt, err
	}
	controller, managed, err := c.commonAPI()
	if err != nil {
		return receipt, err
	}
	if !managed {
		return receipt, fmt.Errorf("本地网关尚未配置")
	}
	var out api.SaveCommonMemoryResponse
	if existing == nil {
		cat := draft.Category
		title := draft.Title
		out, err = controller.Client.CreateCommonMemory(ctx, api.SaveCommonMemoryRequest{
			Category: &cat,
			Title:    &title,
			Content:  &body,
		})
	} else {
		if existing.Dataset != p.Dataset || commonCategory(*existing) == "" || commonCategory(*existing) != draft.Category {
			return receipt, fmt.Errorf("原记忆归属或分类发生变化，请重新读取；分类在创建时确定")
		}
		originalSource, _ := existing.Metadata["source"].(string)
		if strings.TrimSpace(draft.Source) != originalSource {
			return receipt, fmt.Errorf("此版本仅编辑标题和正文；分类与原始来源在创建时确定")
		}
		title := draft.Title
		out, err = controller.Client.UpdateCommonMemory(ctx, existing.ID, api.SaveCommonMemoryRequest{
			Title:   &title,
			Content: &body,
		})
	}
	if err != nil {
		receipt.OutcomeUnknown = true
		return receipt, err
	}
	docID := out.DocumentID
	if docID == "" {
		docID = out.DocID
	}
	receipt = CommonWriteReceipt{
		Gateway:   true,
		Status:    "completed",
		DatasetID: out.DatasetID,
		DataID:    docID,
	}
	if !out.OK || docID == "" || out.DatasetID != p.Dataset {
		receipt.OutcomeUnknown = true
		return receipt, fmt.Errorf("通用记忆保存归属未确认")
	}
	return receipt, nil
}

func (c Client) deleteCommon(ctx context.Context, p Project, d Document) error {
	if d.Dataset != p.Dataset || commonCategory(d) == "" {
		return fmt.Errorf("请选择当前数据组的通用记忆")
	}
	controller, managed, err := c.commonAPI()
	if err != nil {
		return err
	}
	if !managed {
		return fmt.Errorf("本地网关尚未配置")
	}
	return controller.Delete(ctx, d.ID)
}

func (c Client) confirmCommon(ctx context.Context, control ProjectControlClient, hasControl bool, p Project, receipt CommonWriteReceipt, draft CommonDraft) (Document, string, error) {
	if !hasControl {
		return Document{}, "保存尚未核对", fmt.Errorf("控制面服务不可用，无法核对官方保存结果")
	}
	category := draft.Category
	expected, err := draft.FormatDocument()
	useHashOnly := false
	if err != nil {
		if receipt.HasExpectedSHA {
			useHashOnly = true
			if category == "" {
				category = receipt.ExpectedCategory
			}
		} else {
			return Document{}, "", err
		}
	}
	notFound := fmt.Errorf("未找到与本次正文一致的官方文档；保留原操作，不重复提交")
	var rows []Item
	if receipt.DataID != "" {
		res, lookupErr := control.documentLookupV2(ctx, p.Dataset, receipt.DataID)
		if settings.IsControlErrorCode(lookupErr, http.StatusNotFound, "document_not_found") {
			return Document{}, "保存尚未核对", notFound
		}
		if lookupErr != nil {
			return Document{}, "保存尚未核对", lookupErr
		}
		d := res.Document
		docCategory := commonCategory(d)
		if d.Dataset != p.Dataset || d.ID != receipt.DataID ||
			(category != "" && d.Metadata["memlane_kind"] == "common_knowledge" && docCategory != category) ||
			(category != "" && res.CommonCategory != "" && res.CommonCategory != category) ||
			(docCategory == "" && res.CommonCategory == "") ||
			(receipt.ContentHash != "" && d.ContentHash != receipt.ContentHash) {
			return Document{}, "保存尚未核对", notFound
		}
		if category == "" {
			category = docCategory
		}
		rows = []Item{{ID: d.ID, Dataset: p.Dataset, Kind: "知识", Title: documentTitle(d), Date: documentDate(d), Document: &d, CommonCategory: category}}
	} else {
		res, pageErr := control.commonKnowledgePageV2(ctx, p.Dataset, category, 30, v2KnowledgePageOpts{})
		if pageErr != nil {
			return Document{}, "保存尚未核对", pageErr
		}
		for _, row := range res.Items[:min(30, len(res.Items))] {
			if row.DocID == "" || row.DatasetID != p.Dataset || (category != "" && row.CommonCategory != category) ||
				(receipt.ContentHash != "" && row.ContentHash != "" && row.ContentHash != receipt.ContentHash) {
				continue
			}
			rows = append(rows, row.toItem(""))
			if len(rows) == 8 {
				break
			}
		}
	}
	var document Document
	for _, row := range rows {
		loaded, readErr := c.read(ctx, row)
		if readErr != nil {
			return Document{}, "官方正文读取未确认", readErr
		}
		// The official raw-text ingestion strips the final LF. Compare only
		// that transport normalization; retain all internal whitespace/content.
		normalized := strings.TrimSuffix(loaded.Body, "\n")
		if (!useHashOnly && normalized == strings.TrimSuffix(expected, "\n")) ||
			(useHashOnly && reading.BaselineSHA256(normalized) == receipt.ExpectedSHA256) {
			document = *row.Document
			break
		}
	}
	if document.ID == "" {
		return Document{}, "保存尚未核对", notFound
	}
	processing := "提炼状态未确认"
	if receipt.Status == "completed" || receipt.Status == "incremental" || receipt.Status == "unchanged" || receipt.Status == "full_rebuild" {
		processing = "提炼已完成"
	}
	if receipt.PipelineRunID != "" {
		runs, runsErr := control.pipelineRunsV2(ctx, p.Dataset, 100, 0)
		if runsErr != nil {
			return document, "正文已核对；提炼状态未确认", runsErr
		}
		for _, run := range runs {
			if run.Dataset == p.Dataset && run.Run == receipt.PipelineRunID && run.Kind == "pipeline" {
				switch strings.ToLower(run.Outcome) {
				case "succeeded", "success":
					processing = "提炼已完成"
				case "failed", "errored", "abandoned":
					processing = "提炼失败，已保存内容保留"
				default:
					switch strings.ToLower(run.Status) {
					case "pipelineruncompleted", "completed":
						processing = "提炼已完成"
					case "pipelinerunerrored", "errored", "failed":
						processing = "提炼失败，已保存内容保留"
					default:
						processing = "提炼状态：" + run.Status
					}
				}
				break
			}
		}
	}
	return document, "官方正文已核对；" + processing, nil
}
