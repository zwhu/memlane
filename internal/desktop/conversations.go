package desktop

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/zwhu/memlane/internal/records"
)

type conversationReadingState struct {
	Detail          *records.SessionDetail
	Scroll          ui.ScrollState
	Tools, Metadata bool
	ErrorCode       string
	TraceScroll     map[string]*ui.ScrollState
}

type ConversationUI struct {
	Documents                                 *officialDocumentsUI
	Active, Loading, Reading                  bool
	Project, Query, Sort, Selected, ErrorCode string
	Binding                                   *records.CogneeBinding
	SelectedRow                               *records.SessionSummary
	Pages                                     map[int]records.SessionPage
	Rows                                      []records.SessionSummary
	Loaded                                    bool
	Next                                      int
	HasMore                                   bool
	Sequence, ReadSequence                    int64
	Cancel, ReadCancel                        context.CancelFunc
	ListScroll                                ui.ScrollState
	Views                                     map[string]*conversationReadingState
	Markdown                                  markdown.Options
	Source                                    *records.SourceRead
	SourceReceipt                             *records.SourceSession
	SourceError                               string
	Return                                    *conversationReturnContext
	RevealSource                              bool
	ReadCandidate                             *conversationReadCandidate
	ReadReview                                *conversationReadReview
}

func conversationKey(b records.CogneeBinding, row records.SessionSummary) string {
	return records.MergeDigest([]string{b.Instance, row.Dataset, row.User, row.Session})
}

func (a *App) conversationPageVisible() bool {
	return a.conversations.Active && a.conversations.Project == a.scope && a.page == "项目" && (a.recordKind == "raw" || a.officialKnowledgeVisible()) && !a.projectSettings.Open && !a.projectOrganizer.Open
}

func (a *App) retireConversations() {
	s := &a.conversations
	if s.Documents != nil {
		s.Documents.cancel()
	}
	if s.ReadReview != nil && s.ReadReview.Cancel != nil {
		s.ReadReview.Cancel()
	}
	if s.Cancel != nil {
		s.Cancel()
	}
	if s.ReadCancel != nil {
		s.ReadCancel()
	}
	*s = ConversationUI{Sequence: s.Sequence + 1, ReadSequence: s.ReadSequence + 1}
}

func (a *App) openConversations() {
	if a.conversations.Active && a.conversations.Project == a.scope {
		a.refreshConversationCatalog(false)
		a.refreshSelectedConversation()
		return
	}
	a.retireConversations()
	s := &a.conversations
	s.Active, s.Project, s.Sort = true, a.scope, "最近活动"
	s.Views = map[string]*conversationReadingState{}
	s.Pages = map[int]records.SessionPage{}
	s.Markdown = conversationMarkdownOptions()
	a.refreshConversationCatalog(false)
}

func conversationMarkdownOptions() markdown.Options {
	colors := &editor.Theme{Text: design.Ink, Background: design.Surface}
	for i := range colors.Tokens {
		colors.Tokens[i].Color = design.Ink
	}
	colors.Tokens[editor.TokenKeyword].Color = ui.Hex("#6387aa")
	colors.Tokens[editor.TokenComment].Color = design.Muted
	return markdown.Options{ShareSelection: true, Theme: &markdown.Theme{
		FontSize: 12, LineHeight: 1.9, Text: design.Ink, Muted: design.Muted,
		CodeSize: 11.0 / 12, CodeBlockBackground: design.Surface, CodeBlockBorder: design.Border,
		TableBorder: design.Border, TableHeader: design.Surface, CodeColors: colors,
	}, CodeBlock: func(c *ui.Context, b *markdown.CodeBlockView) {
		ui.Column(c).Background(design.Surface).Border(1, design.Border).Radius(6).Clip().Children(func() {
			ui.Row(c).Padding(7, 12).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(design.Border).Children(func() {
				ui.Text(c, strings.ToUpper(b.Language)).FontSize(10).TextColor(design.Muted).Unselectable()
				ui.Spacer(c)
				quietButton(c, "复制代码").Disabled(b.Open).OnClick(func() { c.WriteClipboard(b.Code()) })
			})
			b.Body(c).Label("Markdown 代码横向滚动")
		})
	}}
}

func (a *App) refreshConversationCatalog(more bool) {
	s := &a.conversations
	if !a.conversationPageVisible() || s.Documents != nil || s.Loading || s.Source != nil || a.libraryContext == nil {
		return
	}
	s.Loading = true
	s.Sequence++
	sequence, epoch, project := s.Sequence, a.libraryEpoch, s.Project
	life, client, dispatch := a.libraryContext, a.library, a.dispatch
	ctx, cancel := context.WithTimeout(life, 15*time.Second)
	s.Cancel = cancel
	offset := 0
	if more {
		offset = s.Next
	}
	known := map[string]bool{}
	for key, view := range s.Views {
		known[key] = view.Detail != nil
	}
	var saved *records.CogneeBinding
	if s.Binding != nil {
		b := *s.Binding
		saved = &b
	}
	go func() {
		defer cancel()
		bindings, err := client.CogneeBindings(ctx, project)
		var connections []records.Connection
		if err == nil {
			for _, b := range bindings {
				if b.MaterialSubmission {
					connections, err = client.Connections(ctx)
					break
				}
			}
		}
		var binding *records.CogneeBinding
		for _, b := range bindings {
			if b.MaterialSubmission {
				eligible := false
				for _, connection := range connections {
					if connection.ID == b.ConnectionID && connection.Agent == "pi" {
						eligible = true
						break
					}
				}
				if !eligible {
					continue
				}
			} // pi material reads use only saved capture receipts, never history.
			if saved != nil && (saved.Instance != b.Instance || saved.Dataset != b.Dataset) {
				continue
			}
			if binding != nil {
				err = &conversationScopeError{}
				break
			}
			copy := b
			binding = &copy
		}
		var page records.SessionPage
		var candidate *conversationReadCandidate
		previews := map[string]records.SessionDetail{}
		if err == nil && binding != nil {
			page, err = client.Sessions(ctx, records.SessionQuery{Captured: binding.MaterialSubmission, Project: project, Instance: binding.Instance, Dataset: binding.Dataset, Limit: 20, Offset: offset})
			if err == nil {
				for _, row := range page.Sessions {
					key := conversationKey(*binding, row)
					if known[key] {
						continue
					}
					detail, e := client.Session(ctx, records.SessionQuery{Captured: binding.MaterialSubmission, Project: project, Instance: binding.Instance, Dataset: binding.Dataset, Selected: &row})
					if e == nil {
						previews[key] = detail
					} else if !sourceReaderTransientFailure(sourceReaderErrorCode(e)) {
						err = e
						break
					}
				}
			}
			if err == nil {
				// Revalidate after preview reads before displaying any captured
				// page. A concurrent revocation wins over earlier preview data.
				_, err = client.Sessions(ctx, records.SessionQuery{Captured: binding.MaterialSubmission, Project: project, Instance: binding.Instance, Dataset: binding.Dataset, Limit: 1})
			}
		}
		if err != nil && binding != nil && binding.Project == project {
			code := sourceReaderErrorCode(err)
			if code == "scope_denied" || code == "scope_changed" || code == "connection_revoked" {
				if connections, e := client.Connections(ctx); e == nil {
					candidate = conversationReadCandidateFor(*binding, connections)
				}
			}
		}
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			s := &a.conversations
			if a.libraryEpoch != epoch || a.library.Root != client.Root || s.Sequence != sequence || s.Project != project || !a.conversationPageVisible() {
				return
			}
			s.Loading, s.Cancel = false, nil
			s.ReadCandidate = candidate
			if err != nil {
				s.ErrorCode = sourceReaderErrorCode(err)
				if !sourceReaderTransientFailure(s.ErrorCode) {
					s.Rows, s.Views, s.Selected, s.Binding = nil, map[string]*conversationReadingState{}, "", nil
					s.Pages = map[int]records.SessionPage{}
				}
				return
			}
			s.ErrorCode, s.Loaded, s.Binding = "", true, binding
			if binding == nil {
				s.Rows, s.Views, s.Selected = nil, map[string]*conversationReadingState{}, ""
				s.Pages = map[int]records.SessionPage{}
				return
			}
			s.Pages[offset] = page
			offsets := make([]int, 0, len(s.Pages))
			for at := range s.Pages {
				offsets = append(offsets, at)
			}
			sort.Ints(offsets)
			s.Rows = nil
			seen := map[string]bool{}
			for _, at := range offsets {
				part := s.Pages[at]
				for _, row := range part.Sessions {
					key := conversationKey(*binding, row)
					if !seen[key] {
						seen[key] = true
						s.Rows = append(s.Rows, row)
					}
				}
				s.Next, s.HasMore = at+part.Limit, part.HasMore
			}

			for key, detail := range previews {
				if s.Views[key] == nil {
					s.Views[key] = &conversationReadingState{}
				}
				copy := detail
				s.Views[key].Detail = &copy
			}
			if s.Selected == "" && len(s.Rows) > 0 {
				a.selectConversation(s.Rows[0])
			}
		})
	}()
}

// This local error never contains upstream prose or credentials.
type conversationScopeError struct{}

func (*conversationScopeError) Error() string { return "choose one authorized official dataset" }

func (a *App) selectConversation(row records.SessionSummary) {
	s := &a.conversations
	if s.Binding == nil {
		return
	}
	if s.ReadCancel != nil {
		s.ReadCancel()
	}
	s.ReadSequence++
	s.Reading = false
	s.Selected = conversationKey(*s.Binding, row)
	copy := row
	s.SelectedRow = &copy
	if s.Views[s.Selected] == nil {
		s.Views[s.Selected] = &conversationReadingState{}
	}
	a.refreshSelectedConversation()
}

func (a *App) refreshSelectedConversation() {
	s := &a.conversations
	if !a.conversationPageVisible() || s.Documents != nil || s.Reading || s.Binding == nil || s.Selected == "" || s.Source != nil || a.libraryContext == nil {
		return
	}
	var selected *records.SessionSummary
	for _, row := range s.Rows {
		if conversationKey(*s.Binding, row) == s.Selected {
			copy := row
			selected = &copy
			break
		}
	}
	if selected == nil {
		// A page boundary can move while new sessions arrive. Continue checking
		// the selected session itself even if it leaves the currently read page.
		if s.SelectedRow == nil || conversationKey(*s.Binding, *s.SelectedRow) != s.Selected {
			return
		}
		copy := *s.SelectedRow
		selected = &copy
	}
	binding, key := *s.Binding, s.Selected
	s.Reading = true
	s.ReadSequence++
	sequence, epoch, project := s.ReadSequence, a.libraryEpoch, s.Project
	life, client, dispatch := a.libraryContext, a.library, a.dispatch
	ctx, cancel := context.WithTimeout(life, 15*time.Second)
	s.ReadCancel = cancel
	go func() {
		defer cancel()
		detail, err := client.Session(ctx, records.SessionQuery{Captured: binding.MaterialSubmission, Project: project, Instance: binding.Instance, Dataset: binding.Dataset, Selected: selected})
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			s := &a.conversations
			if a.libraryEpoch != epoch || a.library.Root != client.Root || s.ReadSequence != sequence || s.Selected != key || s.Project != project || s.Binding == nil || *s.Binding != binding || !a.conversationPageVisible() {
				return
			}
			s.Reading, s.ReadCancel = false, nil
			view := s.Views[key]
			if view == nil {
				return
			}
			if err != nil {
				view.ErrorCode = sourceReaderErrorCode(err)
				if !sourceReaderTransientFailure(view.ErrorCode) {
					view.Detail = nil
				}
				return
			}
			view.ErrorCode = ""
			// The message and scroll keys remain independent of receipt pointers.
			view.Detail = &detail
		})
	}()
}

func conversationTitle(detail *records.SessionDetail) string {
	if detail == nil {
		return "未命名会话"
	}
	if strings.TrimSpace(detail.Label) != "" {
		return detail.Label
	}
	for _, qa := range detail.QAs {
		if strings.TrimSpace(qa.Question) != "" {
			line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(qa.Question), "\n", 2)[0])
			title := []rune(line)
			if len(title) > 48 {
				return string(title[:48]) + "…"
			}
			return line
		}
	}
	return "未命名会话"
}

func conversationActivity(value string) string {
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.Local().Format("2006-01-02 15:04")
	}
	return "时间未知"
}

func conversationDay(value string) string {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "时间未知"
	}
	day := t.Local().Format("2006-01-02")
	now := time.Now()
	if day == now.Format("2006-01-02") {
		return "今天"
	}
	if day == now.AddDate(0, 0, -1).Format("2006-01-02") {
		return "昨天"
	}
	return day
}

func conversationCopy(d records.SessionDetail) string {
	var b strings.Builder
	for _, qa := range d.QAs {
		if qa.Question != "" {
			fmt.Fprintf(&b, "你：\n%s\n\n", qa.Question)
		}
		if qa.Answer != "" {
			fmt.Fprintf(&b, "Agent：\n%s\n\n", qa.Answer)
		}
	}
	if len(d.Traces) > 0 {
		b.WriteString("本会话工具记录（未提供逐轮关联）：\n")
	}
	for _, trace := range d.Traces {
		fmt.Fprintf(&b, "%s · %s\n参数：%s\n结果：%s\n错误：%s\n\n", trace.Function, trace.Status, trace.Params, trace.Result, trace.Error)
	}
	return b.String()
}
