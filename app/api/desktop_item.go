package api

import "encoding/json"

type Document struct {
	FirstSourceDate string         `json:"-"`
	ID              string         `json:"id"`
	Dataset         string         `json:"datasetId"`
	Name            string         `json:"name"`
	Label           *string        `json:"label"`
	MIME            string         `json:"mimeType"`
	Metadata        map[string]any `json:"externalMetadata"`
	Updated         string         `json:"updatedAt"`
	Created         string         `json:"createdAt"`
	ContentHash     string         `json:"contentHash"`
}

type Session struct {
	ID              string `json:"session_id"`
	Dataset         string `json:"dataset_id"`
	User            string `json:"user_id"`
	Agent           string `json:"agent_id"`
	Updated         string `json:"last_activity_at"`
	Started         string `json:"started_at"`
	IsSubagent      bool   `json:"is_subagent,omitempty"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	SubagentRole    string `json:"subagent_role,omitempty"`
	NestingDepth    int    `json:"nesting_depth,omitempty"`
}
type QA struct {
	ID                string `json:"qa_id"`
	Question          string `json:"question"`
	Answer            string `json:"answer"`
	QuestionTruncated bool   `json:"question_truncated"`
	AnswerTruncated   bool   `json:"answer_truncated"`
}
type Trace struct {
	ID     string          `json:"trace_id"`
	Name   string          `json:"origin_function"`
	Status string          `json:"status"`
	Params json.RawMessage `json:"method_params"`
	Result json.RawMessage `json:"method_return_value"`
	Error  string          `json:"error_message"`
}
type SessionDetail struct {
	Session
	Label     string  `json:"label"`
	QAs       []QA    `json:"qas"`
	Traces    []Trace `json:"traces"`
	Count     int     `json:"msg_count"`
	ToolCount int     `json:"tool_calls"`
}
type Item struct {
	RecallStats                                                           *KnowledgeRecallStats
	SearchMatch                                                           string
	SessionSummary                                                        string
	CommonCategory                                                        string
	ParentTitle                                                           string
	ID, Dataset, Kind, Title, Date, Preview, Body, Source, SourceDocument string
	Document                                                              *Document
	Session                                                               *Session
	Detail                                                                *SessionDetail
	Subagents                                                             []Item
	SubagentTotalCount                                                    int
	SubagentsHasMore                                                      bool
	SubagentsNextCursor                                                   string
	SubagentsLoading                                                      bool
	ReadingState, ReadingError                                            string
	BodyLoaded                                                            bool
	PreviewTruncated                                                      bool
	BodyTruncated                                                         bool
	ServerUnread                                                          bool
	ServerUnreadKnown                                                     bool
}

type KnowledgeRecallStats struct {
	Count          int64  `json:"count"`
	LastRecalledAt string `json:"last_recalled_at"`
	StartedAt      string `json:"started_at"`
}

func (i Item) Key() string {
	if i.Session != nil && i.Session.User != "" {
		return i.Dataset + "/" + i.Kind + "/" + i.Session.User + "/" + i.ID
	}
	return i.Dataset + "/" + i.Kind + "/" + i.ID
}
