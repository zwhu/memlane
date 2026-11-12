package desktop

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type officialDocumentView struct {
	Knowledge *records.OfficialKnowledge
	Binding   records.CogneeBinding
	Content   *records.OfficialDocumentText
	Window    *textWindow
	Scroll    ui.ScrollState
	Info      bool
	ErrorCode string
}
type officialDocumentsUI struct {
	Knowledge                         bool
	KnowledgePages                    map[int]records.OfficialKnowledgePage
	Source                            *officialDocumentSource
	Loading, Reading, Loaded, HasMore bool
	Query, Selected, ErrorCode        string
	SelectedRow                       *records.OfficialDocument
	Rows                              []records.OfficialDocument
	Pages                             map[int]records.OfficialDocumentPage
	Views                             map[string]*officialDocumentView
	Next                              int
	Sequence, ReadSequence            int64
	Cancel, ReadCancel                context.CancelFunc
	ListScroll                        ui.ScrollState
}

func (d *officialDocumentsUI) cancel() {
	d.closeSource()
	if d.Cancel != nil {
		d.Cancel()
	}
	if d.ReadCancel != nil {
		d.ReadCancel()
	}
	d.Sequence++
	d.ReadSequence++
}

func (a *App) openOfficialDocuments() {
	s := &a.conversations
	if !a.conversationPageVisible() || s.Return != nil || s.Documents != nil {
		return
	}
	if s.Cancel != nil {
		s.Cancel()
	}
	if s.ReadCancel != nil {
		s.ReadCancel()
	}
	s.Sequence++
	s.ReadSequence++
	s.Loading, s.Reading = false, false
	s.Documents = &officialDocumentsUI{Pages: map[int]records.OfficialDocumentPage{}, Views: map[string]*officialDocumentView{}}
	a.refreshOfficialDocuments(false)
}
func (a *App) closeOfficialDocuments() {
	s := &a.conversations
	if s.Documents == nil {
		return
	}
	s.Documents.cancel()
	s.Documents = nil
	a.refreshConversationCatalog(false)
	a.refreshSelectedConversation()
}
func officialDocumentError(err error) string {
	var remote *serviceipc.ServiceError
	if errors.As(err, &remote) && remote.Code == "unsupported_content" {
		return "unsupported_content"
	}
	return sourceReaderErrorCode(err)
}
func officialDocumentPermissionFailure(code string) bool {
	switch code {
	case "scope_denied", "permission_denied", "connection_revoked", "authorization_required", "cognee_authorization_required", "scope_changed":
		return true
	}
	return false
}
func (a *App) withdrawOfficialDocumentAccess(d *officialDocumentsUI) {
	d.closeSource()
	s := &a.conversations
	if d.Cancel != nil {
		d.Cancel()
	}
	d.Sequence++
	d.Loading = false
	d.Cancel = nil
	d.Rows, d.SelectedRow, d.Selected, d.Views = nil, nil, "", map[string]*officialDocumentView{}
	d.Pages = map[int]records.OfficialDocumentPage{}
	d.KnowledgePages = map[int]records.OfficialKnowledgePage{}
	d.Next, d.HasMore = 0, false
	// Hidden session caches share this grant. They must not reappear on return.
	s.Views = map[string]*conversationReadingState{}
	s.Pages = map[int]records.SessionPage{}
	s.Rows = nil
	s.Selected = ""
	s.SelectedRow = nil
	s.Binding = nil
	if d.ReadCancel != nil {
		d.ReadCancel()
	}
	d.ReadSequence++
	d.Reading = false
}

func (a *App) refreshOfficialDocuments(more bool) {
	s := &a.conversations
	d := s.Documents
	if !a.conversationPageVisible() || d == nil || d.Loading || a.libraryContext == nil {
		return
	}
	d.Loading = true
	d.Sequence++
	sequence, epoch, project := d.Sequence, a.libraryEpoch, s.Project
	life, client, dispatch := a.libraryContext, a.library, a.dispatch
	ctx, cancel := context.WithTimeout(life, 15*time.Second)
	d.Cancel = cancel
	offset := 0
	if more {
		offset = d.Next
	}
	var saved *records.CogneeBinding
	if s.Binding != nil {
		copy := *s.Binding
		saved = &copy
	}
	go func() {
		defer cancel()
		bindings, err := client.CogneeBindings(ctx, project)
		var binding *records.CogneeBinding
		for _, b := range bindings {
			if b.MaterialSubmission {
				continue
			}
			if saved != nil && (b.Instance != saved.Instance || b.Dataset != saved.Dataset) {
				continue
			}
			if binding != nil {
				err = &conversationScopeError{}
				break
			}
			copy := b
			binding = &copy
		}
		var page records.OfficialDocumentPage
		var knowledge records.OfficialKnowledgePage
		if err == nil && binding != nil {
			query := records.OfficialDocumentQuery{Project: project, Instance: binding.Instance, Dataset: binding.Dataset, Limit: 20, Offset: offset}
			if d.Knowledge {
				knowledge, err = client.OfficialKnowledge(ctx, query)
				page = records.OfficialDocumentPage{Limit: knowledge.Limit, Offset: knowledge.Offset, HasMore: knowledge.HasMore}
				for _, item := range knowledge.Items {
					page.Documents = append(page.Documents, item.Content.Document)
				}
			} else {
				page, err = client.OfficialDocuments(ctx, query)
			}
		}
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			s := &a.conversations
			if a.libraryEpoch != epoch || a.library.Root != client.Root || s.Documents != d || s.Project != project || d.Sequence != sequence || !a.conversationPageVisible() {
				return
			}
			d.Loading, d.Cancel, d.Loaded = false, nil, true
			if err != nil {
				d.ErrorCode = officialDocumentError(err)
				if !sourceReaderTransientFailure(d.ErrorCode) {
					a.withdrawOfficialDocumentAccess(d)
				}
				return
			}
			bindingChanged := s.Binding != nil && binding != nil && *s.Binding != *binding
			d.ErrorCode = ""
			s.Binding = binding
			if binding == nil {
				a.withdrawOfficialDocumentAccess(d)
				return
			}
			d.Pages[offset] = page
			offsets := make([]int, 0, len(d.Pages))
			for at := range d.Pages {
				offsets = append(offsets, at)
			}
			sort.Ints(offsets)
			d.Rows = nil
			seen := map[string]bool{}
			for _, at := range offsets {
				p := d.Pages[at]
				for _, row := range p.Documents {
					if !seen[row.ID] {
						seen[row.ID] = true
						d.Rows = append(d.Rows, row)
					}
				}
				d.Next, d.HasMore = at+p.Limit, p.HasMore
			}
			metadataChanged := bindingChanged
			if bindingChanged {
				d.KnowledgePages = map[int]records.OfficialKnowledgePage{}
				d.closeSource()
				if d.ReadCancel != nil {
					d.ReadCancel()
				}
				d.ReadSequence++
				d.Reading = false
				for _, view := range d.Views {
					view.Content, view.Window = nil, nil
					view.Knowledge = nil
					view.ErrorCode = "scope_changed"
				}
				s.Views = map[string]*conversationReadingState{}
			}
			for _, row := range d.Rows {
				if row.ID == d.Selected && d.SelectedRow != nil && records.MergeDigest(row) != records.MergeDigest(*d.SelectedRow) {
					d.closeSource()
					if d.ReadCancel != nil {
						d.ReadCancel()
					}
					d.ReadSequence++
					d.Reading = false
					copy := row
					d.SelectedRow = &copy
					metadataChanged = true
					if view := d.Views[row.ID]; view != nil {
						view.Content, view.Window = nil, nil
						view.Knowledge = nil
						view.ErrorCode = "stale_source"
					}
				}
			}
			if d.Knowledge {
				d.KnowledgePages[offset] = knowledge
				if d.Selected != "" && !seen[d.Selected] {
					d.closeSource()
					d.Selected, d.SelectedRow = "", nil
				}
				knowledgeSeen := map[string]bool{}
				for _, at := range offsets {
					cached := d.KnowledgePages[at]
					for _, item := range cached.Items {
						if !seen[item.Content.Document.ID] {
							continue
						}
						id := item.Content.Document.ID
						if knowledgeSeen[id] {
							continue
						}
						knowledgeSeen[id] = true
						view := d.Views[id]
						if view == nil {
							view = &officialDocumentView{}
							d.Views[id] = view
						}
						if id == d.Selected && view.Content != nil {
							continue
						} // selected detail has its own refresh owner
						if view.Content == nil || view.Content.Text != item.Content.Text {
							if d.Selected == id {
								d.closeSource()
							}
							view.Window = newTextWindow(item.Content.Text)
						}
						content := item.Content
						copy := item
						view.Content, view.Knowledge, view.Binding, view.ErrorCode = &content, &copy, *binding, ""
					}
				}
			}
			if d.Selected == "" && len(d.Rows) > 0 {
				a.selectOfficialDocument(d.Rows[0])
			} else if metadataChanged {
				a.refreshSelectedOfficialDocument()
			}
		})
	}()
}
func (a *App) selectOfficialDocument(row records.OfficialDocument) {
	d := a.conversations.Documents
	if d == nil || a.conversations.Binding == nil {
		return
	}
	d.closeSource()
	if d.ReadCancel != nil {
		d.ReadCancel()
	}
	d.ReadSequence++
	d.Reading = false
	d.Selected = row.ID
	copy := row
	d.SelectedRow = &copy
	if d.Views[row.ID] == nil {
		d.Views[row.ID] = &officialDocumentView{}
	}
	a.refreshSelectedOfficialDocument()
}
func (a *App) refreshSelectedOfficialDocument() {
	s := &a.conversations
	d := s.Documents
	if !a.conversationPageVisible() || d == nil || d.Reading || d.SelectedRow == nil || s.Binding == nil || a.libraryContext == nil {
		return
	}
	row, binding := *d.SelectedRow, *s.Binding
	identity := records.MergeDigest(row)
	d.Reading = true
	d.ReadSequence++
	sequence, epoch, project := d.ReadSequence, a.libraryEpoch, s.Project
	life, client, dispatch := a.libraryContext, a.library, a.dispatch
	ctx, cancel := context.WithTimeout(life, 15*time.Second)
	d.ReadCancel = cancel
	go func() {
		defer cancel()
		query := records.OfficialDocumentQuery{Project: project, Instance: binding.Instance, Dataset: binding.Dataset, Selected: &row}
		var text records.OfficialDocumentText
		var knowledge *records.OfficialKnowledge
		var err error
		if d.Knowledge {
			item, e := client.OfficialKnowledgeText(ctx, query)
			err, text, knowledge = e, item.Content, &item
		} else {
			text, err = client.OfficialDocumentText(ctx, query)
		}
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			s := &a.conversations
			if a.libraryEpoch != epoch || a.library.Root != client.Root || s.Documents != d || s.Project != project || d.ReadSequence != sequence || d.Selected != row.ID || d.SelectedRow == nil || records.MergeDigest(*d.SelectedRow) != identity || s.Binding == nil || *s.Binding != binding || !a.conversationPageVisible() {
				return
			}
			d.Reading, d.ReadCancel = false, nil
			view := d.Views[row.ID]
			if view == nil {
				return
			}
			if err == nil && records.MergeDigest(text.Document) != identity {
				err = &serviceipc.ServiceError{Code: "stale_source", Message: "document receipt changed"}
			}
			if err != nil {
				view.ErrorCode = officialDocumentError(err)
				if !sourceReaderTransientFailure(view.ErrorCode) {
					d.closeSource()
					view.Content, view.Window = nil, nil
					view.Knowledge = nil
				}
				if officialDocumentPermissionFailure(view.ErrorCode) {
					d.ErrorCode = view.ErrorCode
					a.withdrawOfficialDocumentAccess(d)
				}
				return
			}
			view.ErrorCode = ""
			if d.Source != nil && (view.Content == nil || view.Content.SessionReference != text.SessionReference || view.Content.Text != text.Text) {
				d.closeSource()
			}
			if view.Content == nil || view.Content.Text != text.Text {
				view.Window = newTextWindow(text.Text)
			}
			view.Content = &text
			view.Knowledge = knowledge
			view.Binding = binding
		})
	}()
}

func (a *App) currentOfficialDocument() (*officialDocumentView, bool) {
	s := &a.conversations
	d := s.Documents
	if d == nil || s.Binding == nil || d.SelectedRow == nil {
		return nil, false
	}
	v := d.Views[d.Selected]
	if v == nil || v.Content == nil || v.Window == nil || v.Binding != *s.Binding || records.MergeDigest(v.Content.Document) != records.MergeDigest(*d.SelectedRow) {
		return v, false
	}
	return v, true
}
func officialDocumentTitle(row records.OfficialDocument) string {
	if row.Label != nil && strings.TrimSpace(*row.Label) != "" {
		return *row.Label
	}
	if strings.TrimSpace(row.Name) != "" {
		return row.Name
	}
	return "未命名文档"
}
