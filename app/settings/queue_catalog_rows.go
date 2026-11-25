package settings

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zwhu/memlane/app/api"
)

type v2KnowledgeItem struct {
	DocID            string `json:"doc_id"`
	DatasetID        string `json:"dataset_id"`
	ProjectID        string `json:"project_id"`
	Title            string `json:"title"`
	DocName          string `json:"doc_name"`
	DocLabel         string `json:"doc_label"`
	EffectiveDate    string `json:"effective_date"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
	ContentHash      string `json:"content_hash"`
	Kind             string `json:"kind"`
	Category         string `json:"category"`
	CommonCategory   string `json:"common_category"`
	LessonType       string `json:"lesson_type"`
	StateKey         string `json:"state_key"`
	SessionID        string `json:"session_id"`
	RecallCount      int64  `json:"recall_count"`
	IsUnread         bool   `json:"is_unread"`
	PreviewText      string `json:"preview_text"`
	PreviewTruncated bool   `json:"preview_truncated"`
	MatchField       string `json:"match_field,omitempty"`
}

var categoryLabelToLessonType = map[string]string{
	"项目状态":    "state",
	"踩坑经验":    "pitfall",
	"项目约定":    "convention",
	"代码位置与关联": "location",
	"代码变更":    "change",
	"约束条件":    "constraint",
	"其他":      "other",
}

func (row v2KnowledgeItem) toItem(recallStartedAt string) Item {
	kind := "知识"
	lowerKind := strings.ToLower(strings.TrimSpace(row.Kind))
	if lowerKind == "raw_doc" || lowerKind == "document" || lowerKind == "raw" || lowerKind == "raw_document" || row.Kind == "文档" {
		kind = "文档"
	}

	meta := map[string]any{}
	if kind == "知识" {
		if row.CommonCategory != "" {
			meta["memlane_kind"] = "common_knowledge"
			meta["category"] = row.CommonCategory
			meta["node_set"] = []any{"memlane_common"}
		} else if lowerKind == "direct_memory" || lowerKind == "user_context" || lowerKind == "memory" || row.Category == "直接保存" {
			meta["node_set"] = []any{"user_context"}
		} else {
			nodeSets := []any{"session_learnings"}
			if row.SessionID != "" {
				nodeSets = append(nodeSets, "session_learnings:"+row.SessionID)
				meta["session_id"] = row.SessionID
			}
			meta["node_set"] = nodeSets
			lessonType := strings.TrimSpace(row.LessonType)
			if lessonType == "" && row.Category != "" {
				lessonType = categoryLabelToLessonType[strings.TrimSpace(row.Category)]
			}
			if lessonType != "" {
				meta["lesson_type"] = lessonType
			}
			if row.StateKey != "" {
				meta["state_key"] = row.StateKey
			}
		}
	}

	var labelPtr *string
	if row.DocLabel != "" {
		l := row.DocLabel
		labelPtr = &l
	}
	doc := Document{
		FirstSourceDate: row.EffectiveDate,
		ID:              row.DocID,
		Dataset:         row.DatasetID,
		Name:            row.DocName,
		Label:           labelPtr,
		Metadata:        meta,
		Created:         row.CreatedAt,
		Updated:         row.UpdatedAt,
		ContentHash:     row.ContentHash,
	}

	date := row.EffectiveDate
	if date == "" {
		date = documentDate(doc)
		if kind == "知识" {
			date = ""
			if created, ok := api.OfficialTime(doc.Created); ok {
				date = created.In(time.Local).Format("2006-01-02")
			}
		}
	}
	title := strings.TrimSpace(row.Title)
	if title == "" {
		if preview := strings.TrimSpace(row.PreviewText); preview != "" {
			title = firstReadingSentenceLimit(preview, 240)
		}
	}
	if title == "" {
		title = documentTitle(doc)
		if kind == "知识" {
			title = readableDocumentTitle(doc)
			if title == "" {
				title = knowledgePlaceholder(date)
			}
		}
	}

	readingState := ""
	if row.PreviewText != "" || title != "" {
		readingState = "ready"
	}

	var stats *KnowledgeRecallStats
	if kind == "知识" {
		stats = &KnowledgeRecallStats{
			Count:     row.RecallCount,
			StartedAt: recallStartedAt,
		}
	}

	// Strictly decouple Preview from Body / BodyLoaded:
	// list cards display Preview directly while Body remains empty and BodyLoaded=false
	// until the full document is fetched via /v1/datasets/{ds}/documents/{doc}/body.
	return Item{
		ID:                row.DocID,
		Dataset:           row.DatasetID,
		Kind:              kind,
		Title:             title,
		Date:              date,
		Preview:           row.PreviewText,
		PreviewTruncated:  row.PreviewTruncated,
		Body:              "",
		BodyLoaded:        false,
		Source:            row.SessionID,
		CommonCategory:    row.CommonCategory,
		Document:          &doc,
		RecallStats:       stats,
		ReadingState:      readingState,
		ServerUnread:      row.IsUnread,
		ServerUnreadKnown: true,
	}
}

func v2IdentityHeaders(readingID string) map[string]string {
	if readingID == "" {
		return nil
	}
	return map[string]string{"X-Memlane-Reading-Identity": readingID}
}

type v2KnowledgePageOpts struct {
	Cursor                       string
	Direction                    string
	AnchorDocID                  string
	RecallAgentID, RecallEventID string
}

func (c ProjectControlClient) projectKnowledgePageV2(ctx context.Context, readingID, projectID, sortBy, category string, unreadOnly bool, recallAgentID, recallEventID string, limit int, opts v2KnowledgePageOpts) (v2ProjectKnowledgeResponse, error) {
	var out v2ProjectKnowledgeResponse
	q := url.Values{}
	if sortBy != "" {
		q.Set("sort", sortBy)
	}
	if category != "" {
		q.Set("category", category)
	}
	if unreadOnly {
		q.Set("unread_only", "1")
	}
	if recallAgentID != "" && recallEventID != "" {
		q.Set("recall_agent_id", recallAgentID)
		q.Set("recall_event_id", recallEventID)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(min(50, limit)))
	}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}
	if opts.Direction == "prev" || opts.Direction == "next" {
		q.Set("direction", opts.Direction)
	}
	if opts.AnchorDocID != "" {
		q.Set("anchor_doc_id", opts.AnchorDocID)
	}
	path := "/v1/projects/" + url.PathEscape(projectID) + "/knowledge"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	err := c.RequestWithHeaders(ctx, "GET", path, v2IdentityHeaders(readingID), nil, &out, 512*1024)
	return out, err
}

type UnreadPreviewItem struct {
	DocID         string `json:"doc_id"`
	Title         string `json:"title"`
	EffectiveDate string `json:"effective_date"`
	CreatedAt     string `json:"created_at"`
}

func isControlNotFound(err error) bool {
	var ce *ControlResponseError
	return errors.As(err, &ce) && ce.Status == http.StatusNotFound
}
