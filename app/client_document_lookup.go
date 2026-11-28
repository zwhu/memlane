package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zwhu/memlane/app/reading"
	"github.com/zwhu/memlane/app/settings"
)

func (c Client) documentByID(ctx context.Context, dataset, id string) (Item, error) {
	i, err := c.documentInfoByID(ctx, dataset, id)
	if err != nil {
		return i, err
	}
	if i.BodyLoaded {
		return i, nil
	}
	return c.read(ctx, i)
}
func (c Client) documentInfoByID(ctx context.Context, dataset, id string) (Item, error) {
	control, ok := c.controlClient()
	if !ok {
		return Item{}, fmt.Errorf("本地网关尚未配置")
	}
	if dataset == "" || id == "" {
		return Item{}, fmt.Errorf("官方未提供可定位的文档出处")
	}
	res, err := control.documentLookupV2(ctx, dataset, id)
	if err == nil {
		d := res.Document
		if d.Dataset != dataset || d.ID != id {
			return Item{}, fmt.Errorf("official document dataset changed")
		}
		if res.EffectiveDate != "" {
			d.FirstSourceDate = res.EffectiveDate
		}
		lowerKind := strings.ToLower(strings.TrimSpace(res.Kind))
		isKnowledge := lowerKind == "knowledge" || lowerKind == "common" || lowerKind == "direct_memory" || res.CommonCategory != "" || documentKnowledge(d)
		if isKnowledge {
			if d.Metadata == nil {
				d.Metadata = map[string]any{}
			}
			if res.CommonCategory != "" {
				if _, ok := d.Metadata["memlane_kind"]; !ok {
					d.Metadata["memlane_kind"] = "common_knowledge"
				}
				if _, ok := d.Metadata["category"]; !ok {
					d.Metadata["category"] = res.CommonCategory
				}
				if _, ok := d.Metadata["node_set"]; !ok {
					d.Metadata["node_set"] = []any{"memlane_common"}
				}
			} else if lowerKind == "direct_memory" || lowerKind == "user_context" || lowerKind == "memory" || res.Category == "直接保存" {
				if _, ok := d.Metadata["node_set"]; !ok {
					d.Metadata["node_set"] = []any{"user_context"}
				}
			} else {
				if _, ok := d.Metadata["node_set"]; !ok {
					nodeSets := []any{"session_learnings"}
					if res.SessionID != "" {
						nodeSets = append(nodeSets, "session_learnings:"+res.SessionID)
					}
					d.Metadata["node_set"] = nodeSets
				}
				if res.SessionID != "" {
					if _, ok := d.Metadata["session_id"]; !ok {
						d.Metadata["session_id"] = res.SessionID
					}
				}
				lessonType := strings.TrimSpace(res.LessonType)
				if lessonType == "" && res.Category != "" {
					lessonType = categoryLabelToLessonType[strings.TrimSpace(res.Category)]
				}
				if lessonType != "" {
					if _, ok := d.Metadata["lesson_type"]; !ok {
						d.Metadata["lesson_type"] = lessonType
					}
				}
				if res.StateKey != "" {
					if _, ok := d.Metadata["state_key"]; !ok {
						d.Metadata["state_key"] = res.StateKey
					}
				}
			}
		}
		if e := c.rememberKnowledgeDays([]Document{d}); e != nil {
			return Item{}, e
		}
		kind := "文档"
		if isKnowledge {
			kind = "知识"
		}
		date := documentDate(d)
		if date == "" && res.EffectiveDate != "" {
			date = res.EffectiveDate
		}
		source := documentSession(d)
		if source == "" {
			source = res.SessionID
		}
		title := strings.TrimSpace(res.Title)
		if title == "" {
			title = documentTitle(d)
		}
		item := Item{
			ID:             d.ID,
			Dataset:        dataset,
			Kind:           kind,
			Title:          title,
			Preview:        res.PreviewText,
			Date:           date,
			CommonCategory: res.CommonCategory,
			Document:       &d,
			Source:         source,
			SourceAgent:    strings.TrimSpace(res.AgentID),
		}
		if res.RecallCount > 0 {
			item.RecallStats = &KnowledgeRecallStats{Count: res.RecallCount}
		}
		if res.Content != nil {
			if err := reading.ValidateBody(dataset, id, d.Dataset, d.ID, res.Content); err != nil {
				return Item{}, err
			}
			item.Body = *res.Content
			item.BodyLoaded = true
			if item.Kind == "知识" && strings.TrimSpace(res.Title) == "" {
				item.Title = itemPresentation(item).DisplayTitle
			}
		}
		return item, nil
	}
	if settings.IsControlAuthDenied(err) {
		return Item{}, err
	}
	if settings.IsControlErrorCode(err, http.StatusNotFound, "document_not_found") {
		return Item{}, fmt.Errorf("官方未提供可定位的文档出处")
	}
	return Item{}, err
}

func documentDate(d Document) string {
	if documentKnowledge(d) {
		if day, ok := documentKnowledgeDay(d, time.Local); ok {
			return day
		}
		return ""
	}
	if d.Updated != "" {
		return d.Updated
	}
	return d.Created
}

func sessionTitle(question string) string {
	text := strings.TrimSpace(question)
	for _, sep := range []string{"\n", "。", "！", "？"} {
		if before, _, ok := strings.Cut(text, sep); ok {
			text = before
		}
	}
	chars := []rune(text)
	if len(chars) > 64 {
		return string(chars[:64]) + "…"
	}
	return text
}
