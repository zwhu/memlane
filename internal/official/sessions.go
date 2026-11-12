package official

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

// SessionReader reads Cognee's own cache. It neither imports it into the
// Memlane record/version store nor writes a second search index.
// The caller supplies an already authorized principal and dataset binding.
type SessionReader struct {
	Endpoint string
	Instance string
	Dataset  string
	Token    string `json:"-"`
}

type SessionSummary = records.SessionSummary
type SessionPage = records.SessionPage
type SessionQA = records.SessionQA
type SessionTrace = records.SessionTrace
type SessionDetail = records.SessionDetail

func canonicalSessionScope(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.String() == value
}

func (r SessionReader) verify(ctx context.Context) error {
	if !canonicalSessionScope(r.Instance) || !canonicalSessionScope(r.Dataset) || r.Token == "" {
		return errors.New("authorized official session binding required")
	}
	var identity struct {
		Instance string `json:"instance"`
		Version  string `json:"cognee_version"`
	}
	if err := Request(ctx, r.Endpoint, "/api/v1/memlane/identity", "GET", r.Token, "", nil, &identity); err != nil {
		return err
	}
	if identity.Instance != r.Instance || identity.Version != "1.6.7" {
		return errors.New("official session service identity changed")
	}
	return nil
}

func (r SessionReader) ListPage(ctx context.Context, limit, offset int) (SessionPage, error) {
	var page SessionPage
	if limit < 1 || limit > 500 || offset < 0 {
		return page, errors.New("bounded official session page required")
	}
	if err := r.verify(ctx); err != nil {
		return page, err
	}
	query := url.Values{"range": {"all"}, "limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}, "order_by": {"last_activity_at"}, "descending": {"true"}}
	if err := Request(ctx, r.Endpoint, "/api/v1/sessions?"+query.Encode(), "GET", r.Token, "", nil, &page); err != nil {
		return SessionPage{}, err
	}
	if page.Limit != limit || page.Offset != offset || page.Total < 0 || len(page.Sessions) > limit {
		return SessionPage{}, errors.New("official session pagination changed")
	}
	filtered := make([]SessionSummary, 0, len(page.Sessions))
	for _, item := range page.Sessions {
		if item.Dataset != r.Dataset {
			continue
		}
		if item.Session == "" || !canonicalSessionScope(item.User) {
			return SessionPage{}, errors.New("official session owner identity missing")
		}
		filtered = append(filtered, item)
	}
	page.Sessions = filtered
	if err := r.verify(ctx); err != nil {
		return SessionPage{}, err
	}
	return page, nil
}

func (r SessionReader) Read(ctx context.Context, selected SessionSummary) (SessionDetail, error) {
	var detail SessionDetail
	if selected.Dataset != r.Dataset || !canonicalSessionScope(selected.User) || selected.Session == "" || len(selected.Session) > 512 || strings.ContainsAny(selected.Session, "\x00\r\n") {
		return detail, errors.New("selected official session identity required")
	}
	if err := r.verify(ctx); err != nil {
		return detail, err
	}
	path := "/api/v1/sessions/" + url.PathEscape(selected.Session)
	if err := Request(ctx, r.Endpoint, path, "GET", r.Token, "", nil, &detail); err != nil {
		return SessionDetail{}, err
	}
	if detail.Session != selected.Session || detail.User != selected.User || detail.Dataset != selected.Dataset {
		return SessionDetail{}, errors.New("official session scope changed")
	}
	if detail.QACount < len(detail.QAs) || detail.ToolCount < len(detail.Traces) || len(detail.QAs) > 20 || len(detail.Traces) > 20 {
		return SessionDetail{}, errors.New("official session detail contract changed")
	}
	if err := r.verify(ctx); err != nil {
		return SessionDetail{}, err
	}
	return detail, nil
}

// A legacy citation has session identity but no owner. Resolve only when the
// bounded authorized catalog proves uniqueness, rather than accepting whichever
// owner's same-named session the detail endpoint happens to return.
func (r SessionReader) ReadUniqueSession(ctx context.Context, session string) (SessionDetail, error) {
	var selected *SessionSummary
	for offset := 0; offset < 5000; offset += 100 {
		page, err := r.ListPage(ctx, 100, offset)
		if err != nil {
			return SessionDetail{}, err
		}
		if page.Total > 5000 {
			return SessionDetail{}, errors.New("official session owner resolution exceeds budget")
		}
		for _, row := range page.Sessions {
			if row.Session != session {
				continue
			}
			if selected != nil {
				return SessionDetail{}, errors.New("official session owner is ambiguous")
			}
			copy := row
			selected = &copy
		}
		if !page.HasMore {
			if selected == nil {
				return SessionDetail{}, &HTTPError{Path: "/api/v1/sessions", Status: 404}
			}
			return r.Read(ctx, *selected)
		}
	}
	return SessionDetail{}, errors.New("official session owner resolution exceeds budget")
}
