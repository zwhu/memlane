package simpledesktop

import (
	"context"
	"fmt"
	"net/url"

	"github.com/egoist/mygo/ui"
)

type KnowledgeRecallStats struct {
	Count          int64  `json:"count"`
	LastRecalledAt string `json:"last_recalled_at"`
	StartedAt      string `json:"started_at"`
}

func (c ProjectControlClient) knowledgeRecall(ctx context.Context, i Item) (*KnowledgeRecallStats, error) {
	var result KnowledgeRecallStats
	err := c.request(ctx, "GET", "/v1/knowledge/recalls?dataset_id="+url.QueryEscape(i.Dataset)+"&document_id="+url.QueryEscape(i.ID), nil, &result)
	if err != nil {
		return nil, err
	}
	if result.Count < 0 || result.StartedAt == "" {
		return nil, fmt.Errorf("召回统计尚未确认")
	}
	return &result, nil
}

func knowledgeRecallLabel(c *ui.Context, i Item) {
	if i.Kind != "知识" {
		return
	}
	count := int64(0)
	tip := "召回统计暂不可用，当前显示默认值 0；不代表历史从未召回。"
	if stats := i.RecallStats; stats != nil {
		count = stats.Count
		tip = "自 " + displayDate(stats.StartedAt) + " 起记录的 Agent 召回；人工搜索不计入，旧历史不补算。"
		if stats.LastRecalledAt != "" {
			tip += " 最近召回：" + displayDate(stats.LastRecalledAt)
		}
	}
	homeText(c, fmt.Sprintf("召回 %d 次", count), homeMeta).SingleLine().Tooltip(tip).Padding(2, 5).Radius(4).Background(ui.Hex("#edf5f1")).TextColor(ui.Hex("#5e8272"))
}
