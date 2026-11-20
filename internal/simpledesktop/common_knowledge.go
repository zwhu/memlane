package simpledesktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Common knowledge is a normal official document with explicit metadata. It
// never impersonates a session learning and never copies a project document.
type CommonDraft struct {
	Title, Body, Applicability, Source, Category string
}

func (d CommonDraft) markdown() (string, error) {
	if d.Category != "preference" && d.Category != "experience" {
		return "", fmt.Errorf("请选择个人偏好或通用经验")
	}
	if strings.TrimSpace(d.Title) == "" || strings.ContainsAny(d.Title, "\r\n") || len([]rune(d.Title)) > 120 || strings.TrimSpace(d.Body) == "" {
		return "", fmt.Errorf("请填写单行标题和正文")
	}
	body := "# " + strings.TrimSpace(d.Title) + "\n\n" + strings.TrimSpace(d.Body)
	if strings.TrimSpace(d.Applicability) != "" {
		body += "\n\n## 适用条件\n\n" + strings.TrimSpace(d.Applicability)
	}
	if strings.TrimSpace(d.Source) != "" {
		body += "\n\n## 来源\n\n" + strings.TrimSpace(d.Source)
	}
	if len(body) > 256*1024 {
		return "", fmt.Errorf("通用记忆超过单条大小限制")
	}
	return body + "\n", nil
}

func commonCategory(d Document) string {
	kind, _ := d.Metadata["memlane_kind"].(string)
	category, _ := d.Metadata["category"].(string)
	if kind == "common_knowledge" && (category == "preference" || category == "experience") {
		return category
	}
	return ""
}

func (c Client) commonDocumentItems(ctx context.Context, p Project, category string) ([]Item, error) {
	rows, err := c.overviewDocuments(ctx, p)
	if err != nil {
		return nil, err
	}
	items := []Item{}
	for _, d := range rows {
		kind := commonCategory(d)
		if kind == "" || (category != "" && kind != category) {
			continue
		}
		copy := d
		items = append(items, Item{ID: d.ID, Dataset: d.Dataset, Kind: "知识", Title: d.title(), Date: d.date(), Document: &copy, CommonCategory: kind})
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, _ := officialTime(items[i].Date)
		b, _ := officialTime(items[j].Date)
		if a.Equal(b) {
			return items[i].key() < items[j].key()
		}
		return a.After(b)
	})
	return items, nil
}
func (c Client) commonDocuments(ctx context.Context, p Project, offset int, category string) ([]Item, bool, error) {
	items, err := c.commonDocumentItems(ctx, p, category)
	if err != nil {
		return nil, false, err
	}
	start := min(max(offset, 0), len(items))
	end := min(start+30, len(items))
	return items[start:end], end < len(items), nil
}

type CommonWriteReceipt struct {
	Status         string `json:"status"`
	DatasetID      string `json:"dataset_id"`
	DataID         string `json:"data_id"`
	PipelineRunID  string `json:"pipeline_run_id"`
	ContentHash    string `json:"content_hash"`
	HTTPStatus     int    `json:"-"`
	OutcomeUnknown bool   `json:"-"`
}

// The receipt preserves the original official operation even on partial failure.
// Accepted/running is not a claim that the document is saved and processed.
func (c Client) writeCommon(ctx context.Context, p Project, draft CommonDraft, existing *Document) (CommonWriteReceipt, error) {
	var receipt CommonWriteReceipt
	body, err := draft.markdown()
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
		return receipt, fmt.Errorf("官方处理失败，保留原操作并核对已保存内容")
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

func (c Client) confirmCommon(ctx context.Context, p Project, receipt CommonWriteReceipt, draft CommonDraft) (Document, string, error) {
	expected, err := draft.markdown()
	if err != nil {
		return Document{}, "", err
	}
	rows, err := c.commonDocumentItems(ctx, p, draft.Category)
	if receipt.DataID == "" && len(rows) > 30 {
		rows = rows[:30]
	}
	if err != nil {
		return Document{}, "", err
	}
	var document Document
	reads := 0
	for _, row := range rows {
		d := *row.Document
		if receipt.DataID != "" && d.ID != receipt.DataID {
			continue
		}
		if receipt.ContentHash != "" && d.ContentHash != receipt.ContentHash {
			continue
		}
		if reads >= 8 {
			break
		}
		reads++
		loaded, readErr := c.read(ctx, row)
		if readErr != nil {
			return Document{}, "官方正文读取未确认", readErr
		}
		// The official raw-text ingestion strips the final LF. Compare only
		// that transport normalization; retain all internal whitespace/content.
		if strings.TrimSuffix(loaded.Body, "\n") == strings.TrimSuffix(expected, "\n") {
			document = d
			break
		}
	}
	if document.ID == "" {
		return Document{}, "保存尚未核对", fmt.Errorf("未找到与本次正文一致的官方文档；保留原操作，不重复提交")
	}
	processing := "处理状态未确认"
	if receipt.Status == "completed" || receipt.Status == "incremental" || receipt.Status == "unchanged" || receipt.Status == "full_rebuild" {
		processing = "处理已完成"
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
			return document, "正文已核对；处理状态未确认", err
		}
		for _, run := range runs {
			if run.Dataset == p.Dataset && run.Run == receipt.PipelineRunID && run.Kind == "pipeline" {
				switch strings.ToLower(run.Outcome) {
				case "succeeded", "success":
					processing = "处理已完成"
				case "failed", "errored", "abandoned":
					processing = "处理失败，已保存内容保留"
				default:
					switch strings.ToLower(run.Status) {
					case "pipelineruncompleted", "completed":
						processing = "处理已完成"
					case "pipelinerunerrored", "errored", "failed":
						processing = "处理失败，已保存内容保留"
					default:
						processing = "处理状态：" + run.Status
					}
				}
				break
			}
		}
	}
	return document, "官方正文已核对；" + processing, nil
}
