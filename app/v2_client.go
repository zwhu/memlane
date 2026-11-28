package app

import (
	"context"
	"net/url"
	"strings"

	"github.com/zwhu/memlane/app/reading"
)

const (
	v2HeaderReadingIdentity = "X-Memlane-Reading-Identity"

	v2FeatureCatalogIndex          = "catalog_index_v2"
	v2FeatureReadingState          = "reading_state_v2"
	v2FeatureLocalBodyFastpath     = "local_body_fastpath_v2"
	v2FeatureOverview              = "overview_v2"
	v2FeatureKnowledgeCursor       = "knowledge_cursor_v2"
	v2FeatureCommonKnowledgeCursor = "common_knowledge_cursor_v2"
	v2FeatureRawRecords            = "raw_records_v2"
	v2FeatureSearch                = "search_v2"
	v2FeatureMessageSummary        = "message_summary_v2"
	v2FeatureProvenanceBackfill    = "provenance_backfill_v2"

	maxActiveListItems = reading.MaxActiveListItems
)

type v2DocumentBodyResponse struct {
	ID          string  `json:"id"`
	DatasetID   string  `json:"dataset_id"`
	Body        *string `json:"body"`
	MIMEType    string  `json:"mime_type"`
	ContentHash string  `json:"content_hash"`
	Truncated   bool    `json:"truncated"`
	BytesTotal  int64   `json:"bytes_total"`
	Source      string  `json:"source"`
}

func (c ProjectControlClient) documentBodyV2(ctx context.Context, datasetID, docID string) (v2DocumentBodyResponse, error) {
	var out v2DocumentBodyResponse
	path := "/v1/datasets/" + url.PathEscape(datasetID) + "/documents/" + url.PathEscape(docID) + "/body"
	// Allow up to 24 MiB JSON payload to cover worst-case JSON control-character escaping of an 8 MiB UTF-8 body.
	if err := c.requestWithLimit(ctx, "GET", path, nil, &out, 24*1024*1024); err != nil {
		return v2DocumentBodyResponse{}, err
	}
	return out, nil
}

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
	AgentID          string `json:"agent_id,omitempty"`
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
	}
	title := strings.TrimSpace(row.Title)
	if title == "" {
		if preview := strings.TrimSpace(row.PreviewText); preview != "" {
			title = firstReadingSentenceLimit(preview, 240)
		}
	}
	if title == "" {
		title = documentTitle(doc)
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
		SourceAgent:       strings.TrimSpace(row.AgentID),
		CommonCategory:    row.CommonCategory,
		Document:          &doc,
		RecallStats:       stats,
		ReadingState:      readingState,
		ServerUnread:      row.IsUnread,
		ServerUnreadKnown: true,
	}
}
