package app

import (
	"context"
	"fmt"
	"net/url"

	"github.com/egoist/mygo/ui"
)

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
	knowledgeRecallBadge(c, i)
}
