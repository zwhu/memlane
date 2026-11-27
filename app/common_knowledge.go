package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

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
	var payload bytes.Buffer
	w := multipart.NewWriter(&payload)
	method, path := "POST", "/api/v1/remember"
	if existing == nil {
		// Blocking official ingestion returns the actual pipeline identifier and
		// terminal result. A background launch omits that identity on this pinned
		// service, so it cannot be correlated safely after concurrent writes.
		for key, value := range map[string]string{"raw_data": body, "datasetId": p.Dataset, "node_set": "memlane_common", "run_in_background": "false", "self_improvement": "false"} {
			if err = w.WriteField(key, value); err != nil {
				return receipt, err
			}
		}
		metadata, _ := json.Marshal([]map[string]string{{"memlane_kind": "common_knowledge", "category": draft.Category, "source": strings.TrimSpace(draft.Source)}})
		if err = w.WriteField("external_metadata", string(metadata)); err != nil {
			return receipt, err
		}
	} else {
		if existing.Dataset != p.Dataset || commonCategory(*existing) == "" || commonCategory(*existing) != draft.Category {
			return receipt, fmt.Errorf("原记忆归属或分类发生变化，请重新读取；分类在创建时确定")
		}
		originalSource, _ := existing.Metadata["source"].(string)
		if strings.TrimSpace(draft.Source) != originalSource {
			return receipt, fmt.Errorf("此版本仅编辑标题和正文；分类与原始来源在创建时确定")
		}
		method = "PATCH"
		path = "/api/v1/update?data_id=" + url.QueryEscape(existing.ID) + "&dataset_id=" + url.QueryEscape(p.Dataset)
		// Omit node_set/metadata: the official PATCH retains both. The fixed
		// update endpoint cannot change external metadata/category.
		file, createErr := w.CreateFormFile("data", existing.Name)
		if createErr != nil {
			return receipt, createErr
		}
		if _, err = io.WriteString(file, body); err != nil {
			return receipt, err
		}
	}
	if err = w.Close(); err != nil {
		return receipt, err
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Config.ServiceURL, "/")+path, &payload)
	if err != nil {
		return receipt, err
	}
	key, err := readCachedToken(c.Config.TokenFile, 1, false)
	if err != nil {
		return receipt, fmt.Errorf("official credential unavailable")
	}
	req.Header.Set("X-Api-Key", key)
	req.Header.Set("Content-Type", w.FormDataContentType())
	hc := newHTTPClient(c.HTTP, 120*time.Second)
	response, err := hc.Do(req)
	if err != nil {
		receipt.OutcomeUnknown = true
		return receipt, fmt.Errorf("保存结果未知，请核对原操作，不要重复提交")
	}
	defer response.Body.Close()
	receipt.HTTPStatus = response.StatusCode
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 || json.Unmarshal(data, &receipt) != nil {
		receipt.OutcomeUnknown = true
		return receipt, fmt.Errorf("保存回执未知，请核对官方原操作")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return receipt, fmt.Errorf("official HTTP %d；保留原操作并核对实际结果", response.StatusCode)
	}
	if receipt.DatasetID != p.Dataset {
		receipt.OutcomeUnknown = true
		return receipt, fmt.Errorf("通用记忆保存归属未确认")
	}
	if receipt.Status == "errored" || receipt.Status == "failed" {
		return receipt, fmt.Errorf("官方提炼失败，保留原操作并核对已保存内容")
	}
	valid := receipt.Status == "running" || receipt.Status == "completed" || receipt.Status == "incremental" || receipt.Status == "unchanged" || receipt.Status == "full_rebuild"
	if !valid {
		receipt.OutcomeUnknown = true
		return receipt, fmt.Errorf("官方保存状态未知，请核对原操作")
	}
	return receipt, nil
}

func (c Client) deleteCommon(ctx context.Context, p Project, d Document) error {
	if d.Dataset != p.Dataset || commonCategory(d) == "" {
		return fmt.Errorf("请选择当前数据组的通用记忆")
	}
	b, err := c.request(ctx, "DELETE", "/api/v1/datasets/"+url.PathEscape(p.Dataset)+"/data/"+url.PathEscape(d.ID), nil)
	if err != nil {
		return err
	}
	var receipt struct {
		Status        string `json:"status"`
		DatasetID     string `json:"datasetId"`
		DataID        string `json:"dataId"`
		DataRemaining *bool  `json:"dataRemaining"`
	}
	if json.Unmarshal(b, &receipt) != nil || receipt.DatasetID != p.Dataset || receipt.DataID != d.ID || receipt.Status != "success" || receipt.DataRemaining == nil || *receipt.DataRemaining {
		return fmt.Errorf("官方删除结果未确认，请核对原记忆")
	}
	return nil
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
		var runs []struct {
			Kind    string `json:"kind"`
			Dataset string `json:"dataset_id"`
			Run     string `json:"pipeline_run_id"`
			Status  string `json:"status"`
			Outcome string `json:"outcome"`
			Ended   string `json:"ended_at"`
		}
		err = c.get(ctx, "/api/v1/activity/pipeline-runs?dataset_id="+url.QueryEscape(p.Dataset)+"&limit=100&offset=0", &runs)
		if err != nil {
			return document, "正文已核对；提炼状态未确认", err
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
