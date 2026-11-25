package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/shell"
	kitui "github.com/zwhu/memlane/kit/ui"
)

type ResultItem struct {
	ID, ProjectID, DatasetID, Kind, Title, Date, Preview   string
	SourceDocument, UserID, Category, Summary, SearchMatch string
	Unread, IsSession, Unavailable                         bool
}

func (i ResultItem) key() string {
	return i.DatasetID + "/" + i.Kind + "/" + i.UserID + "/" + i.ID
}

func (i ResultItem) knowledge() bool {
	return strings.Contains(i.Kind, "知识") || i.Kind == "knowledge"
}

type State = StateOf[ResultItem, struct{}]
type Controller = ControllerOf[ResultItem, struct{}]
type Deps = DepsOf[ResultItem, struct{}]

type StateOf[T, L any] struct {
	HistoryDirty                         bool
	Open, Focus, Loading, Loaded         bool
	Query, Scope, Type, Error, Submitted string
	Rows                                 []T
	Lanes                                L
	Selection                            int
	KeyboardSelection                    bool
	Scroll                               ui.ScrollState
	Reveal                               bool
	lastSelIndY                          float32
	request, lastPublishSeq              uint64
	cancel                               context.CancelFunc
}

type Searcher interface {
	Search(context.Context, api.SearchRequest) (api.SearchResponse, error)
	RecentSearch(context.Context, int) (api.RecentSearchResponse, error)
}

type Publication[T, L any] struct {
	Sequence     uint64
	Lanes        L
	Rows         []T
	Err          error
	Message      string
	Loaded, Done bool
}

type Fetcher[T, L any] func(context.Context, string, L, func(Publication[T, L]))

type DepsOf[T, L any] struct {
	Dispatcher      shell.Dispatcher
	Router          shell.Router
	Fence           shell.SessionFence
	Theme           shell.ThemeProvider
	Client          *api.Client
	ClientFactory   func() (Searcher, error)
	Searcher        Searcher
	Catalog         func(scope string) []shell.ProjectRef
	TargetProject   func() (shell.ProjectRef, bool)
	ProjectName     func(datasetID string) string
	OnRememberQuery func(query, scope string)
	OnOpenItem      func(item T) error
	OnRestoreOrigin func()
	OnTrigger       func()
	ReadingDenied   func(error) bool
	CanDispatch     func() bool

	Describe       func(T) ResultItem
	FromResults    func([]ResultItem) []T
	Fetch          func(scope string) Fetcher[T, L]
	RenderIcon     func(*ui.Context, T)
	RenderMetadata func(*ui.Context, T)
	ItemText       func(T, string) (string, string)
}

type ControllerOf[T, L any] struct {
	DepsOf[T, L]
	State *StateOf[T, L]
}

func New(deps Deps) Controller {
	deps.Describe = func(i ResultItem) ResultItem { return i }
	deps.FromResults = func(rows []ResultItem) []ResultItem { return rows }
	return Controller{DepsOf: deps, State: &State{Type: "全部"}}
}

func (c *ControllerOf[T, L]) catalog(scope string) []shell.ProjectRef {
	if c.Catalog == nil {
		return nil
	}
	return c.Catalog(scope)
}

func (c *ControllerOf[T, L]) Signature() string {
	s := c.State
	var b strings.Builder
	if c.Fence != nil {
		b.WriteString(c.Fence.ReadingIdentity())
	}
	for _, value := range []string{strings.TrimSpace(s.Query), s.Scope} {
		fmt.Fprintf(&b, "/%d:%s", len(value), value)
	}
	for _, p := range c.catalog(s.Scope) {
		fmt.Fprintf(&b, "/%d:%s/%d:%s", len(p.ID), p.ID, len(p.DatasetID), p.DatasetID)
	}
	return b.String()
}

func (c *ControllerOf[T, L]) Open(restore bool, activePage string, currentProject shell.ProjectRef, hasCurrent bool) {
	s := c.State
	if s.Open {
		s.Focus = true
		return
	}
	hasCached := strings.TrimSpace(s.Query) != "" && (s.Loaded || len(s.Rows) > 0 || s.Submitted != "")
	if !restore && !hasCached {
		s.Scope, s.Query, s.Type = "", "", "全部"
		if hasCurrent && (activePage == "知识" || activePage == "图谱" || activePage == "project" || activePage == "graph") {
			s.Scope = currentProject.ID
		}
	}
	if s.Type == "" {
		s.Type = "全部"
	}
	s.Open, s.Focus, s.Reveal = true, true, true
	if strings.TrimSpace(s.Query) == "" {
		c.Submit()
	}
}

func (c *ControllerOf[T, L]) cancelRequest() {
	s := c.State
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.request++
	s.Loading = false
}

func (c *ControllerOf[T, L]) Close() {
	c.cancelRequest()
	c.State.Open, c.State.HistoryDirty = false, false
}

func (c *ControllerOf[T, L]) Invalidate(dataset string) {
	c.Close()
	s := c.State
	var empty L
	s.Lanes, s.Submitted = empty, ""
	if dataset == "" {
		s.Rows, s.Loaded = nil, false
		return
	}
	rows := s.Rows[:0]
	for _, row := range s.Rows {
		if c.Describe(row).DatasetID != dataset {
			rows = append(rows, row)
		}
	}
	clear(s.Rows[len(rows):])
	s.Rows = rows
}

func (c *ControllerOf[T, L]) CancelChanged() {
	c.cancelRequest()
	s := c.State
	var empty L
	s.Error, s.Submitted, s.Rows, s.Lanes = "", "", nil, empty
	s.Selection, s.KeyboardSelection, s.Loaded = 0, false, false
}

func (c *ControllerOf[T, L]) ToggleCurrentProject(projectID string) {
	if projectID == "" {
		return
	}
	s := c.State
	submitted := strings.TrimSpace(s.Query) == "" || s.Submitted == c.Signature()
	if s.Scope == projectID {
		s.Scope = ""
	} else {
		s.Scope = projectID
	}
	if !submitted {
		c.CancelChanged()
	}
	c.resetSelection()
	if submitted {
		c.Submit()
	}
}

func (c *ControllerOf[T, L]) SetType(kind string) {
	if kind == "记忆" {
		kind = "知识"
	}
	c.State.Type = kind
	c.resetSelection()
}

func (c *ControllerOf[T, L]) resetSelection() {
	s := c.State
	s.Selection, s.KeyboardSelection, s.Scroll.Y, s.Focus, s.Reveal = 0, false, 0, true, true
}

func (c *ControllerOf[T, L]) Submit() { c.submit(false) }
func (c *ControllerOf[T, L]) Retry()  { c.submit(true) }

func (c *ControllerOf[T, L]) submit(retry bool) {
	s := c.State
	query, signature := strings.TrimSpace(s.Query), c.Signature()
	if s.Loading && s.Submitted == signature {
		return
	}
	if query != "" && !retry && c.OnRememberQuery != nil {
		c.OnRememberQuery(query, s.Scope)
	}
	if c.Dispatcher == nil || c.CanDispatch != nil && !c.CanDispatch() {
		return
	}
	var previous L
	if retry && s.Submitted == signature {
		previous = s.Lanes
	}
	c.cancelRequest()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	s.cancel = cancel
	s.lastPublishSeq = 0
	s.Submitted, s.Loading, s.Loaded, s.Error = signature, true, false, ""
	s.Selection, s.KeyboardSelection = 0, false
	request, snapshot := s.request, shell.CaptureFence(c.Fence)
	dispatch := c.Dispatcher
	var fetch Fetcher[T, L]
	if c.Fetch != nil {
		fetch = c.Fetch(s.Scope)
	}
	if fetch == nil {
		fetch = c.APIFetcher(s.Scope)
	}
	go func() {
		defer cancel()
		fetch(ctx, query, previous, func(p Publication[T, L]) {
			dispatch.Dispatch(func() {
				if request != s.request || p.Sequence < s.lastPublishSeq {
					return
				}
				if signature != c.Signature() || (c.Fence != nil && !snapshot.Valid(c.Fence)) {
					s.Loading = false
					return
				}
				c.publish(query, p)
			})
		})
	}()
}

func (c *ControllerOf[T, L]) publish(query string, p Publication[T, L]) {
	s := c.State
	s.lastPublishSeq, s.Lanes = p.Sequence, p.Lanes
	var apiErr *api.APIError
	denied := errors.As(p.Err, &apiErr) && (apiErr.IsUnauthorized() || apiErr.IsForbidden())
	if p.Err != nil && c.ReadingDenied != nil {
		denied = denied || c.ReadingDenied(p.Err)
	}
	if denied && c.Fence != nil {
		c.Invalidate("")
		c.Fence.InvalidateReading()
		return
	}
	if p.Done {
		s.Loading = false
	}
	if p.Err != nil && query == "" {
		s.Error = p.Message
		return
	}
	if p.Done || p.Message != "" {
		s.Error = p.Message
	}
	previousKey := ""
	if s.KeyboardSelection && s.Selection >= 0 {
		if visible := c.VisibleRows(); s.Selection < len(visible) {
			previousKey = c.Describe(visible[s.Selection]).key()
		}
	}
	out := make([]T, 0, len(p.Rows))
	seen := make(map[string]bool, len(p.Rows))
	for _, knowledge := range []bool{true, false} {
		for _, row := range p.Rows {
			item := c.Describe(row)
			key := item.key()
			if item.knowledge() == knowledge && !seen[key] {
				out = append(out, row)
				seen[key] = true
			}
		}
	}
	s.Rows = out
	s.Reveal = s.Reveal || !s.Loaded || len(out) > 0
	s.Loaded = p.Loaded
	if !s.KeyboardSelection {
		s.Scroll.Y = 0
	} else if previousKey != "" {
		rows := c.VisibleRows()
		s.Selection = min(s.Selection, max(0, len(rows)-1))
		for n, row := range rows {
			if c.Describe(row).key() == previousKey {
				s.Selection = n
				break
			}
		}
	}
}

func (c *ControllerOf[T, L]) VisibleRows() []T {
	s := c.State
	if (s.Loading && !s.Loaded) || s.Submitted != c.Signature() {
		return nil
	}
	if s.Type != "知识" && s.Type != "原始记录" {
		return s.Rows
	}
	var rows []T
	for _, row := range s.Rows {
		if c.Describe(row).knowledge() == (s.Type == "知识") {
			rows = append(rows, row)
		}
	}
	return rows
}

func (c *ControllerOf[T, L]) SubmitOrOpen() {
	s := c.State
	if rows := c.VisibleRows(); s.KeyboardSelection && !s.Loading && s.Loaded && len(rows) > 0 {
		c.OpenResult(rows[max(0, min(s.Selection, len(rows)-1))])
		return
	}
	c.Submit()
}

func (c *ControllerOf[T, L]) OpenResult(row T) {
	s := c.State
	i := c.Describe(row)
	if i.Unavailable {
		return
	}
	var err error
	if c.OnOpenItem != nil {
		err = c.OnOpenItem(row)
	} else if c.Router != nil {
		err = c.Router.OpenItem(shell.ItemTarget{ProjectID: i.ProjectID, DatasetID: i.DatasetID, ItemID: i.ID,
			Kind: i.Kind, SourceDocument: i.SourceDocument, Title: i.Title, Date: i.Date, Preview: i.Preview})
	}
	if err != nil {
		s.Error = err.Error()
		return
	}
	if s.Open {
		c.Close()
	}
}

func (c *ControllerOf[T, L]) ReturnToSearch() {
	if c.OnRestoreOrigin != nil {
		c.OnRestoreOrigin()
	}
	c.Open(true, "", shell.ProjectRef{}, false)
}

func (c *ControllerOf[T, L]) tokens(ctx *ui.Context) *kitui.Tokens {
	if c.Theme != nil {
		if tokens := c.Theme.Tokens(); tokens != nil {
			return tokens
		}
	}
	return kitui.TokensFor(ctx.Theme().Dark)
}
