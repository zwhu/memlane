package records

import "encoding/json"

type SessionSummary struct {
	Session      string `json:"session_id"`
	User         string `json:"user_id"`
	Dataset      string `json:"dataset_id"`
	Agent        string `json:"agent_id"`
	Status       string `json:"status"`
	StartedAt    string `json:"started_at"`
	LastActivity string `json:"last_activity_at"`
	EndedAt      string `json:"ended_at"`
}

// Pagination belongs to the upstream visible-session catalog. The catalog
// may span datasets: Total must never be presented as a project's count.
type SessionPage struct {
	Sessions []SessionSummary `json:"sessions"`
	Total    int              `json:"total"`
	Limit    int              `json:"limit"`
	Offset   int              `json:"offset"`
	HasMore  bool             `json:"has_more"`
}

type SessionQA struct {
	QuestionTruncated bool   `json:"question_truncated,omitempty"`
	AnswerTruncated   bool   `json:"answer_truncated,omitempty"`
	ID                string `json:"qa_id"`
	Question          string `json:"question"`
	Answer            string `json:"answer"`
	// Time is official QA creation time, not native user/assistant speech time.
	Time string `json:"time"`
}

type SessionTrace struct {
	ParamsTruncated bool            `json:"params_truncated,omitempty"`
	ResultTruncated bool            `json:"result_truncated,omitempty"`
	ErrorTruncated  bool            `json:"error_truncated,omitempty"`
	ID              string          `json:"trace_id"`
	Function        string          `json:"origin_function"`
	Status          string          `json:"status"`
	Params          json.RawMessage `json:"method_params"`
	Result          json.RawMessage `json:"method_return_value"`
	Error           string          `json:"error_message"`
}

// Cognee 1.6.7 detail returns only the last 20 entries of each channel.
// QA and traces have no shared request key or reliable interleaving clock.
// Empty channels do not prove a complete empty conversation: the API also
// returns them when its cache is unavailable. No permanent archive is implied.
type SessionDetail struct {
	SessionSummary
	Label     string         `json:"label"`
	QACount   int            `json:"msg_count"`
	ToolCount int            `json:"tool_calls"`
	QAs       []SessionQA    `json:"qas"`
	Traces    []SessionTrace `json:"traces"`
}

// SessionQuery contains only trusted scope identities, never an endpoint or key.
type SessionQuery struct {
	Captured bool            `json:"captured,omitempty"`
	Project  string          `json:"project"`
	Instance string          `json:"instance"`
	Dataset  string          `json:"dataset"`
	Limit    int             `json:"limit"`
	Offset   int             `json:"offset"`
	Selected *SessionSummary `json:"selected,omitempty"`
}

// SourceSession is a verified lineage read. Target names the real upstream
// QA/trace identity, never a guessed turn inferred from prose or timestamps.
type SourceSession struct {
	Binding      CogneeBinding `json:"binding"`
	Detail       SessionDetail `json:"detail"`
	TargetKind   string        `json:"target_kind"`
	TargetID     string        `json:"target_id"`
	TargetLoaded bool          `json:"target_loaded"`
}
