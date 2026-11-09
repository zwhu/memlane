package desktop

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
)

type SourceReaderUI struct {
	Open, Loading, Technical bool
	Full                     bool
	Sequence                 int64
	Query                    records.SourceRead
	Result                   records.SourceRecord
	Error, Search            string
	ErrorDetail              string
	ReturnFocus              bool
	HadSessionOriginal       bool
	Sources                  []records.OrganizationSource
	Window                   *textWindow
	Matches                  []int
	Match                    int
	Scroll                   ui.ScrollState
	Cancel                   context.CancelFunc
	FocusOffset, FocusLength int
	Reveal                   bool
}

func (a *App) closeSourceReader() {
	if a.reader.Cancel != nil {
		a.reader.Cancel()
	}
	a.reader.Sequence++
	a.reader.Open, a.reader.ReturnFocus = false, false
	a.reader.Result = records.SourceRecord{}
	a.reader.Window = nil
	a.reader.Loading = false
}

func (a *App) openSourceReader(owner MemoryItem, source records.MergeTarget) {
	a.closeSourceReader()
	a.reader = SourceReaderUI{Open: true, Full: true, Sequence: a.reader.Sequence + 1,
		Query: records.SourceRead{Project: owner.Dataset, Owner: owner.ID, OwnerRevision: owner.Revision, Memory: source.Memory, Revision: source.Revision}}
	if a.organizationSourcesFor == owner.ID && a.organizationSourcesRevision == owner.Revision {
		a.reader.Sources = append([]records.OrganizationSource(nil), a.organizationSources...)
		// QA timestamps and trace sequence are separate official channels.
		// Preserve SDK order within each; never guess an interleaved clock.
		sort.SliceStable(a.reader.Sources, func(i, j int) bool {
			left, right := a.reader.Sources[i].Input.SourceDetail, a.reader.Sources[j].Input.SourceDetail
			if left == nil || left.Official == nil {
				return false
			}
			if right == nil || right.Official == nil {
				return true
			}
			if left.Official.Session != right.Official.Session {
				return left.Official.Session < right.Official.Session
			}
			if left.Official.Kind != right.Official.Kind {
				return left.Official.Kind == "qa"
			}
			return left.Official.Sequence < right.Official.Sequence
		})
	}
	a.refreshSourceReader()
}

func (a *App) refreshSourceReader() {
	s := &a.reader
	if !s.Open || s.Loading || a.libraryContext == nil || a.libraryContext.Err() != nil {
		return
	}
	s.Sequence++
	sequence, query := s.Sequence, s.Query
	s.Loading = true
	ctx, cancel := context.WithTimeout(a.libraryContext, 15*time.Second)
	s.Cancel = cancel
	go func() {
		defer cancel()
		result, err := a.library.ReadSource(ctx, query)
		if a.libraryContext.Err() != nil {
			return
		}
		a.dispatch(func() {
			s := &a.reader
			if !s.Open || s.Sequence != sequence || s.Query != query {
				return
			}
			s.Loading, s.Cancel = false, nil
			if err != nil {
				s.Result, s.Window = records.SourceRecord{}, nil
				s.Error = "原文读取未完成，请检查记忆服务与来源权限后重试。"
				s.ErrorDetail = err.Error()
				return
			}
			s.Error, s.ErrorDetail, s.Result = "", "", result
			if result.SessionOriginal != nil {
				s.HadSessionOriginal = true
			}
			if result.State != "available" {
				s.Window = nil
				return
			}
			shown := a.readerRecord()
			if s.Window == nil || s.Window.Body != shown.Text {
				s.Window = newTextWindow(shown.Text)
				s.Scroll.Y = 0
				if !s.Full || result.SessionOriginal == nil {
					a.locateSourceOffset(result.QuoteOffset)
				}
				s.Reveal = false // Open at metadata; explicit locate/search scrolls.
			}
		})
	}()
}

func (a *App) readerRecord() records.Version {
	if a.reader.Full && a.reader.Result.SessionOriginal != nil {
		return *a.reader.Result.SessionOriginal
	}
	return a.reader.Result.Record
}
func (a *App) sourceReaderMode(full bool) {
	s := &a.reader
	s.Full = full
	s.Window = newTextWindow(a.readerRecord().Text)
	s.Search, s.Matches, s.Match = "", nil, 0
	s.FocusOffset, s.FocusLength, s.Reveal, s.Scroll.Y = 0, 0, false, 0
}

func (a *App) locateSourceOffset(offset int) {
	s := &a.reader
	if s.Window == nil || offset < 0 || offset >= len(s.Window.Body) {
		return
	}
	s.Window.Page = max(0, sort.Search(len(s.Window.Offsets), func(i int) bool { return s.Window.Offsets[i] > offset })-1)
	quote := []rune(s.Result.Quote)
	s.FocusOffset, s.FocusLength = offset, len(string(quote[:min(240, len(quote))]))
	if s.Search != "" && strings.HasPrefix(s.Window.Body[offset:], s.Search) {
		s.FocusLength = len(s.Search)
	}
	s.Reveal = true
	s.Scroll.Y = 0
}

func (a *App) searchSource() {
	s := &a.reader
	s.Matches, s.Match = nil, 0
	if s.Window == nil || s.Search == "" {
		return
	}
	for start := 0; start < len(s.Window.Body); {
		at := strings.Index(s.Window.Body[start:], s.Search)
		if at < 0 {
			break
		}
		at += start
		s.Matches = append(s.Matches, at)
		start = at + len(s.Search)
	}
	if len(s.Matches) > 0 {
		a.locateSourceOffset(s.Matches[0])
	}
}
