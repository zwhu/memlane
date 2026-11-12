package desktop

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

type conversationReturnContext struct {
	Owner  MemoryItem
	Items  []MemoryItem
	Scroll ui.ScrollState
	Kind   string
}

func (a *App) openConversationSource(owner MemoryItem, source records.MergeTarget) {
	back := &conversationReturnContext{Owner: owner, Items: append([]MemoryItem(nil), a.items...), Scroll: a.detailScroll, Kind: a.recordKind}
	a.closeSourceReader()
	a.retireConversations()
	s := &a.conversations
	s.Active, s.Project, s.Sort = true, owner.Dataset, "最近活动"
	s.Views = map[string]*conversationReadingState{}
	s.Pages = map[int]records.SessionPage{}
	s.Markdown = conversationMarkdownOptions()
	s.Source = &records.SourceRead{Project: owner.Dataset, Owner: owner.ID, OwnerRevision: owner.Revision, Memory: source.Memory, Revision: source.Revision}
	s.Return = back
	a.recordKind = "raw"
	a.loadSequence++ // A pending ordinary list read cannot overwrite this route.
	a.refreshConversationSource()
}

func (a *App) returnFromConversationSource() {
	back := a.conversations.Return
	if back == nil {
		a.switchReadingKind("knowledge")
		return
	}
	a.retireConversations()
	a.loadSequence++
	a.recordKind, a.selectedID = back.Kind, back.Owner.ID
	owner := back.Owner
	a.selectedItem = &owner
	a.items = back.Items
	a.detailScroll = back.Scroll
	a.reader.ReturnFocus = true
	a.reader.Query.Owner = owner.ID
	a.refresh(false)
}

func (a *App) refreshConversationSource() {
	s := &a.conversations
	if !a.conversationPageVisible() || s.Source == nil || s.Reading || a.libraryContext == nil {
		return
	}
	s.Reading = true
	s.ReadSequence++
	query, sequence, epoch := *s.Source, s.ReadSequence, a.libraryEpoch
	life, client, dispatch := a.libraryContext, a.library, a.dispatch
	ctx, cancel := context.WithTimeout(life, 15*time.Second)
	s.ReadCancel = cancel
	go func() {
		defer cancel()
		result, err := client.SourceSession(ctx, query)
		if life.Err() != nil {
			return
		}
		dispatch(func() {
			s := &a.conversations
			if a.libraryEpoch != epoch || a.library.Root != client.Root || !a.conversationPageVisible() || s.Source == nil || *s.Source != query || s.ReadSequence != sequence {
				return
			}
			s.Reading, s.ReadCancel = false, nil
			if err != nil {
				s.SourceError = sourceReaderErrorCode(err)
				if !sourceReaderTransientFailure(s.SourceError) {
					s.SourceReceipt = nil
					s.Rows = nil
					s.Views = map[string]*conversationReadingState{}
					s.Selected = ""
					s.SelectedRow = nil
				}
				return
			}
			s.SourceError = ""
			s.Binding = &result.Binding
			s.Loaded = true
			key := conversationKey(result.Binding, result.Detail.SessionSummary)
			view := s.Views[key]
			if view == nil {
				view = &conversationReadingState{}
				s.Views[key] = view
				s.RevealSource = result.TargetLoaded
				view.Tools = result.TargetKind == "trace" && result.TargetLoaded
			}
			view.Detail = &result.Detail
			s.SourceReceipt = &result
			s.Selected, s.SelectedRow = key, &result.Detail.SessionSummary
			s.Rows = []records.SessionSummary{result.Detail.SessionSummary}
		})
	}()
}
