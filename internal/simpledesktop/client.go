package simpledesktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zwhu/memlane/internal/bootstrap"
)

type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	Dataset     string `json:"dataset_id"`
	CodeDataset string `json:"code_dataset_id"`
	CodeEnabled bool   `json:"code_enabled"`
}
type Config struct {
	CodexExecutable string `json:"codex_executable"`
	AgentHosts      map[string]struct {
		Executable string `json:"executable"`
	} `json:"agent_hosts"`
	FirstInstall           *bootstrap.FirstInstallation `json:"first_install,omitempty"`
	FirstInstallDefault    bool                         `json:"-"`
	Python                 string                       `json:"python"`
	ProjectControl         *ProjectControlConfig        `json:"project_control"`
	ClientStateFile        string                       `json:"client_state_file"`
	OfficialData           string                       `json:"official_data"`
	OfficialSource         string                       `json:"official_source"`
	OfficialArchive        string                       `json:"official_archive"`
	ServiceEnvironmentFile string                       `json:"service_environment_file"`
	ServiceCommit          string                       `json:"service_commit"`
	GraphAssets            string                       `json:"graph_assets"`
	ConfigFile             string                       `json:"-"`
	ServiceURL             string                       `json:"service_url"`
	TokenFile              string                       `json:"token_file"`
	CompletionURL          string                       `json:"completion_url"`
	Model                  string                       `json:"model"`
	GenerationMode         string                       `json:"generation_mode"`
	GenerationFile         string                       `json:"generation_file"`
	GenerationSetupOnly    bool                         `json:"generation_setup_only"`
	Projects               []Project                    `json:"projects"`
	Common                 []Project                    `json:"common_datasets"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	c.ConfigFile = path
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(b, &c)
	if e != nil {
		return c, e
	}
	u, e := url.Parse(c.ServiceURL)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return c, fmt.Errorf("official service_url required")
	}
	return c, nil
}

type Client struct {
	Config         Config
	HTTP           *http.Client
	PolicyRevision string
}

func (c Client) controlClient() (ProjectControlClient, bool) {
	if c.Config.ProjectControl == nil {
		return ProjectControlClient{}, false
	}
	return ProjectControlClient{Config: *c.Config.ProjectControl, HTTP: c.HTTP}, true
}

func (c Client) request(ctx context.Context, method, path string, input any) ([]byte, error) {
	contentType := "application/json"
	var body io.Reader
	if form, ok := input.(url.Values); ok {
		body = strings.NewReader(form.Encode())
		contentType = "application/x-www-form-urlencoded"
	} else if input != nil {
		b, e := json.Marshal(input)
		if e != nil {
			return nil, e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Config.ServiceURL, "/")+path, body)
	if e != nil {
		return nil, e
	}
	if c.Config.TokenFile != "" {
		token, e := readCachedToken(c.Config.TokenFile, 0, false)
		if e != nil {
			return nil, fmt.Errorf("official credential unavailable")
		}
		req.Header.Set("X-Api-Key", token)
	}
	req.Header.Set("Content-Type", contentType)
	hc := newHTTPClient(c.HTTP, 45*time.Second)
	res, e := hc.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 256))
		return nil, &OfficialHTTPError{Status: res.StatusCode, Body: strings.TrimSpace(string(snippet))}
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 8*1024*1024+1))
	if e != nil {
		return nil, e
	}
	if len(b) > 8*1024*1024 {
		return nil, fmt.Errorf("official response exceeds reading limit")
	}
	return b, nil
}
func (c Client) get(ctx context.Context, path string, out any) error {
	b, e := c.request(ctx, "GET", path, nil)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, out)
}

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

func (d Document) title() string {
	if d.knowledge() {
		return presentKnowledge(d, "").DisplayTitle
	}
	if s := readableDocumentTitle(d); s != "" {
		return s
	}
	return d.Name
}
func (d Document) knowledge() bool {
	if commonCategory(d) != "" {
		return true
	}
	for _, n := range nodeSets(d) {
		if n == "session_learnings" || n == "user_context" || n == "memory" {
			return true
		}
	}
	return false
}

// These official document tags identify direct saves, not model-distilled
// lessons. Project documents and agent activity remain source material.
func (d Document) directMemory() bool {
	if commonCategory(d) != "" {
		return false
	}
	for _, n := range nodeSets(d) {
		if n == "session_learnings" {
			return false
		}
	}
	for _, n := range nodeSets(d) {
		if n == "user_context" || n == "memory" {
			return true
		}
	}
	return false
}
func (d Document) session() string {
	return uniqueLearningSession(d)
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
	ReadingState, ReadingError                                            string
	BodyLoaded                                                            bool
}

func (i Item) key() string { return i.Dataset + "/" + i.Kind + "/" + i.ID }
func (c Client) documents(ctx context.Context, p Project, offset int, raw bool) ([]Item, bool, error) {
	var rows []Document
	e := c.get(ctx, "/api/v1/datasets/"+url.PathEscape(p.Dataset)+"/data?limit=31&offset="+strconv.Itoa(offset), &rows)
	if e != nil {
		return nil, false, e
	}
	if e := c.rememberKnowledgeDays(rows); e != nil {
		return nil, false, e
	}
	more := len(rows) > 30
	if more {
		rows = rows[:30]
	}
	items := []Item{}
	for _, d := range rows {
		if d.Dataset != p.Dataset {
			return nil, false, fmt.Errorf("official document dataset changed")
		}
		if d.knowledge() == raw {
			continue
		}
		kind := "知识"
		if raw {
			kind = "文档"
		}
		copy := d
		items = append(items, Item{ID: d.ID, Dataset: d.Dataset, Kind: kind, Title: d.title(), Date: d.date(), Source: d.session(), Document: &copy})
	}
	return items, more, nil
}
func (c Client) sessions(ctx context.Context, p Project, offset int) ([]Item, bool, error) {
	var page struct {
		Sessions []Session `json:"sessions"`
		HasMore  bool      `json:"has_more"`
	}
	e := c.get(ctx, "/api/v1/sessions?range=all&limit=30&offset="+strconv.Itoa(offset)+"&order_by=last_activity_at&descending=true", &page)
	items := []Item{}
	for _, s := range page.Sessions {
		if s.Dataset != p.Dataset {
			continue
		}
		copy := s
		items = append(items, Item{ID: s.ID, Dataset: s.Dataset, Kind: "会话", Title: s.ID, Date: s.Updated, Session: &copy})
	}
	return items, page.HasMore, e
}
func (c Client) read(ctx context.Context, i Item) (Item, error) {
	if i.Document != nil {
		b, e := c.request(ctx, "GET", "/api/v1/datasets/"+url.PathEscape(i.Dataset)+"/data/"+url.PathEscape(i.ID)+"/raw", nil)
		if e != nil {
			return i, e
		}
		if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
			return i, fmt.Errorf("此官方文档不是 UTF-8 文本")
		}
		i.Body = string(b)
		i.BodyLoaded = true
		if i.Kind == "知识" {
			i.Title = itemPresentation(i).DisplayTitle
		}
		return i, nil
	}
	if i.Session != nil {
		item, _, err := c.readSessionHistory(ctx, i)
		return item, err
	}
	return i, nil
}
func (c Client) source(ctx context.Context, i Item) (Item, error) {
	if control, ok := c.controlClient(); ok && i.Dataset != "" && i.Source != "" && control.hasFeature(ctx, "session_lookup_v2") {
		userID := ""
		if i.Session != nil {
			userID = i.Session.User
		}
		res, err := control.sessionLookupV2(ctx, i.Dataset, i.Source, userID)
		if err == nil {
			s := res.Session
			if s.ID != i.Source || (s.Dataset != "" && s.Dataset != i.Dataset) {
				return i, fmt.Errorf("官方会话身份不匹配")
			}
			if s.Dataset == "" {
				s.Dataset = i.Dataset
			}
			return Item{ID: s.ID, Dataset: s.Dataset, Kind: "会话", Title: s.ID, Date: s.Updated, Session: &s}, nil
		}
		if isControlAuthDenied(err) {
			return i, err
		}
		if isControlErrorCode(err, http.StatusNotFound, "session_not_found") {
			return i, fmt.Errorf("官方未保留此记忆关联的会话")
		}
	}
	var page struct {
		Sessions []Session `json:"sessions"`
		HasMore  bool      `json:"has_more"`
	}
	for offset := 0; offset <= 100000; offset += 100 {
		if e := c.get(ctx, "/api/v1/sessions?range=all&limit=100&offset="+strconv.Itoa(offset), &page); e != nil {
			return i, e
		}
		for _, s := range page.Sessions {
			if s.Dataset == i.Dataset && s.ID == i.Source {
				return Item{ID: s.ID, Dataset: s.Dataset, Kind: "会话", Title: s.ID, Date: s.Updated, Session: &s}, nil
			}
		}
		if !page.HasMore {
			break
		}
	}
	return i, fmt.Errorf("官方未保留此记忆关联的会话")
}
func (c Client) search(ctx context.Context, projects []Project, q string, filters ...string) ([]Item, error) {
	return c.searchType(ctx, projects, q, "CHUNKS", filters...)
}

func (c Client) searchType(ctx context.Context, projects []Project, q, searchType string, filters ...string) ([]Item, error) {
	ids := []string{}
	for _, p := range projects {
		ids = append(ids, p.Dataset)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("请先配置项目与官方数据组关联")
	}
	input := map[string]any{"query": q, "search_type": searchType, "dataset_ids": ids, "only_context": false, "top_k": 30}
	filter := ""
	if len(filters) > 0 {
		filter = filters[0]
	}
	if filter == "知识" {
		input["node_name"] = []string{"session_learnings", "memlane_common", "user_context", "memory"}
		input["node_name_filter_operator"] = "OR"
	}
	b, e := c.request(ctx, "POST", "/api/v1/search", input)
	if e != nil {
		return nil, e
	}
	var groups []struct {
		Dataset string           `json:"dataset_id"`
		Results []map[string]any `json:"search_result"`
	}
	if e = json.Unmarshal(b, &groups); e != nil {
		return nil, e
	}
	items := []Item{}
	validDataset := map[string]bool{}
	for _, id := range ids {
		validDataset[id] = true
	}
	for _, g := range groups {
		if !validDataset[g.Dataset] {
			return nil, fmt.Errorf("official search dataset changed")
		}
		for _, r := range g.Results {
			stringField := func(k string) string { s, _ := r[k].(string); return s }
			id := stringField("id")
			text := stringField("text")
			if id == "" || text == "" {
				continue
			}
			title := stringField("document_name")
			if title == "" {
				title = "检索片段"
			}
			contentType := "原始材料片段"
			var metadata map[string]any
			if raw := stringField("external_metadata"); raw != "" {
				_ = json.Unmarshal([]byte(raw), &metadata)
			} else if m, ok := r["external_metadata"].(map[string]any); ok {
				metadata = m
			}
			if (Document{Metadata: metadata}).knowledge() {
				contentType = "知识片段"
			}
			if filter == "原始材料" && contentType == "知识片段" {
				continue
			}
			item := Item{ID: id, Dataset: g.Dataset, Kind: contentType, Title: title, Body: text, Preview: text, SourceDocument: stringField("document_id"), BodyLoaded: true}
			item.CommonCategory = commonCategory(Document{Metadata: metadata})
			item.Title = itemPresentation(item).DisplayTitle
			items = append(items, item)
		}
	}
	return items, nil
}

// Resolve a real official document id against the current dataset catalog.
// A title, filename or graph path is never used as a substitute for identity.
func (c Client) documentByID(ctx context.Context, dataset, id string) (Item, error) {
	i, err := c.documentInfoByID(ctx, dataset, id)
	if err != nil {
		return i, err
	}
	return c.read(ctx, i)
}
func (c Client) documentInfoByID(ctx context.Context, dataset, id string) (Item, error) {
	if control, ok := c.controlClient(); ok && dataset != "" && id != "" && control.hasFeature(ctx, "document_lookup_v2") {
		res, err := control.documentLookupV2(ctx, dataset, id)
		if err == nil {
			d := res.Document
			if d.Dataset != dataset || d.ID != id {
				return Item{}, fmt.Errorf("official document dataset changed")
			}
			if res.EffectiveDate != "" {
				d.FirstSourceDate = res.EffectiveDate
			}
			if e := c.rememberKnowledgeDays([]Document{d}); e != nil {
				return Item{}, e
			}
			kind := "文档"
			if d.knowledge() {
				kind = "知识"
			}
			date := d.date()
			if date == "" && res.EffectiveDate != "" {
				date = res.EffectiveDate
			}
			source := d.session()
			if source == "" {
				source = res.SessionID
			}
			item := Item{
				ID:             d.ID,
				Dataset:        dataset,
				Kind:           kind,
				Title:          d.title(),
				Date:           date,
				CommonCategory: res.CommonCategory,
				Document:       &d,
				Source:         source,
			}
			if res.RecallCount > 0 {
				item.RecallStats = &KnowledgeRecallStats{Count: res.RecallCount}
			}
			return item, nil
		}
		if isControlAuthDenied(err) {
			return Item{}, err
		}
		if isControlErrorCode(err, http.StatusNotFound, "document_not_found") {
			return Item{}, fmt.Errorf("官方未提供可定位的文档出处")
		}
	}
	for offset := 0; offset <= 100000; offset += 100 {
		var rows []Document
		if e := c.get(ctx, "/api/v1/datasets/"+url.PathEscape(dataset)+"/data?limit=100&offset="+strconv.Itoa(offset), &rows); e != nil {
			return Item{}, e
		}
		if e := c.rememberKnowledgeDays(rows); e != nil {
			return Item{}, e
		}
		for _, d := range rows {
			if d.Dataset != dataset {
				return Item{}, fmt.Errorf("official document dataset changed")
			}
			if d.ID == id {
				kind := "文档"
				if d.knowledge() {
					kind = "知识"
				}
				return Item{ID: d.ID, Dataset: dataset, Kind: kind, Title: d.title(), Date: d.date(), Document: &d, Source: d.session()}, nil
			}
		}
		if len(rows) < 100 {
			break
		}
	}
	return Item{}, fmt.Errorf("官方未提供可定位的文档出处")
}

func (d Document) date() string {
	if d.knowledge() {
		if day, ok := d.knowledgeDay(time.Local); ok {
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
