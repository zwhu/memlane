package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zwhu/memlane/app/reading"
	"github.com/zwhu/memlane/app/settings"
)

type Config = settings.Config
type Project = settings.Project
type ProjectControlConfig = settings.ProjectControlConfig

func LoadConfig(path string) (Config, error) { return settings.LoadConfig(path) }

type Client struct {
	Config         Config
	HTTP           *http.Client
	PolicyRevision string
}

func (c Client) controlClient() (ProjectControlClient, bool) {
	if c.Config.ProjectControl == nil || strings.TrimSpace(c.Config.ProjectControl.URL) == "" {
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

func documentTitle(d Document) string {
	if documentKnowledge(d) {
		return presentKnowledge(d, "").DisplayTitle
	}
	if s := readableDocumentTitle(d); s != "" {
		return s
	}
	return d.Name
}
func documentKnowledge(d Document) bool {
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
func documentDirectMemory(d Document) bool {
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
func documentSession(d Document) string {
	return uniqueLearningSession(d)
}

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
		if documentKnowledge(d) == raw {
			continue
		}
		kind := "知识"
		if raw {
			kind = "文档"
		}
		copy := d
		items = append(items, Item{ID: d.ID, Dataset: d.Dataset, Kind: kind, Title: documentTitle(d), Date: documentDate(d), Source: documentSession(d), Document: &copy})
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
func canonicalIDMatch(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	return strings.EqualFold(strings.ReplaceAll(a, "-", ""), strings.ReplaceAll(b, "-", ""))
}

func (c Client) read(ctx context.Context, i Item) (Item, error) {
	if i.Document != nil {
		if control, ok := c.controlClient(); ok && i.Dataset != "" && i.ID != "" {
			res, err := control.readingBody(ctx, i.Dataset, i.ID)
			if err == nil {
				if err := reading.ValidateBody(i.Dataset, i.ID, res.DatasetID, res.ID, res.Body); err != nil {
					return i, err
				}
				body := *res.Body
				i.Body = body
				i.BodyLoaded = true
				i.BodyTruncated = res.Truncated
				if res.ContentHash != "" && i.Document.ContentHash == "" {
					docCopy := *i.Document
					docCopy.ContentHash = res.ContentHash
					i.Document = &docCopy
				}
				if i.Kind == "知识" {
					i.Title = itemPresentation(i).DisplayTitle
				}
				return i, nil
			}
			if settings.IsControlAuthDenied(err) {
				return i, err
			}
			if settings.IsControlErrorCode(err, http.StatusNotFound, "document_not_found") {
				return i, &OfficialHTTPError{Status: http.StatusNotFound, Body: "document_not_found"}
			}
			return i, err
		}
		b, e := c.request(ctx, "GET", "/api/v1/datasets/"+url.PathEscape(i.Dataset)+"/data/"+url.PathEscape(i.ID)+"/raw", nil)
		if e != nil {
			return i, e
		}
		if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
			return i, fmt.Errorf("此官方文档不是 UTF-8 文本: %w", reading.ErrInvalidBody)
		}
		i.Body = string(b)
		i.BodyLoaded = true
		i.BodyTruncated = false
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
	if control, ok := c.controlClient(); ok && i.Dataset != "" && i.Source != "" {
		userID := ""
		if i.Session != nil {
			userID = i.Session.User
		}
		s, err := control.readingSourceSession(ctx, i.Dataset, i.Source, userID)
		if err == nil {
			if s.ID != i.Source || (s.Dataset != "" && s.Dataset != i.Dataset) {
				return i, fmt.Errorf("官方会话身份不匹配")
			}
			if s.Dataset == "" {
				s.Dataset = i.Dataset
			}
			return Item{ID: s.ID, Dataset: s.Dataset, Kind: "会话", Title: s.ID, Date: s.Updated, Session: &s}, nil
		}
		if settings.IsControlAuthDenied(err) {
			return i, err
		}
		if reading.Status(err, http.StatusNotFound) || settings.IsControlErrorCode(err, http.StatusNotFound, "session_not_found") {
			return i, fmt.Errorf("官方未保留此记忆关联的会话")
		}
		return i, err
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
