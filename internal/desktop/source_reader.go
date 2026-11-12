package desktop

import (
	"context"
	"errors"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type SourceReaderUI struct {
	Open, Loading, Technical bool
	DirectRaw                bool // The raw-record tab uses the verified reader directly.
	Full                     bool
	Sequence                 int64
	Query                    records.SourceRead
	Result                   records.SourceRecord
	Error, Search            string
	ErrorDetail              string
	ErrorCode                string
	ReturnFocus              bool
	HadSessionOriginal       bool
	Sources                  []records.OrganizationSource
	Window                   *textWindow
	Matches                  []int
	Match                    int
	Scroll                   ui.ScrollState
	Cancel                   context.CancelFunc
	FocusOffset, FocusLength int
	RevealLayout             bool // Wait for current inline glyph layout before revealing a match.
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
	if !s.Open || s.Loading || a.libraryContext == nil || a.libraryContext.Err() != nil || !sourceReaderCanRetry(*s) {
		return
	}
	s.Sequence++
	sequence, query := s.Sequence, s.Query
	s.Loading = true
	life, client, deliver, epoch := a.libraryContext, a.library, a.dispatch, a.libraryEpoch
	ctx, cancel := context.WithTimeout(life, 15*time.Second)
	s.Cancel = cancel
	go func() {
		defer cancel()
		result, err := client.ReadSource(ctx, query)
		if life.Err() != nil {
			return
		}
		deliver(func() {
			s := &a.reader
			if a.libraryEpoch != epoch || a.library.Root != client.Root || life.Err() != nil || !s.Open || s.Sequence != sequence || s.Query != query {
				return
			}
			s.Loading, s.Cancel = false, nil
			if err != nil {
				s.ErrorCode = sourceReaderErrorCode(err)
				if a.runtimeStatus.Phase == "stopped" && s.ErrorCode == "service_unavailable" {
					s.ErrorCode = "service_stopped"
				}
				// Only explicit transient failures retain a previously verified
				// receipt for this exact query/epoch. Authorization, provenance,
				// version and unclassified failures withdraw it immediately.
				if !sourceReaderTransientFailure(s.ErrorCode) {
					s.Result, s.Window = records.SourceRecord{}, nil
				}
				s.Error = sourceReaderFailureMessage(s.ErrorCode)
				s.ErrorDetail = "代码：" + s.ErrorCode
				return
			}
			s.Error, s.ErrorDetail, s.ErrorCode, s.Result = "", "", "", result
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

func sourceReaderTransientFailure(code string) bool {
	switch code {
	case "timeout", "deadline_exceeded", "service_unavailable", "cognee_unavailable":
		return true
	}
	return false
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
	s.RevealLayout = false
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

// Only safe protocol codes enter presentation. Provider/transport error text
// never becomes a source paragraph or a technical-details disclosure.
func sourceReaderErrorCode(err error) string {
	var remote *serviceipc.ServiceError
	if errors.As(err, &remote) && remote.Code != "" {
		if remote.Code == "reload_required" {
			return "scope_changed"
		}
		if remote.Code == "disconnected" || remote.Code == "connection_not_found" || remote.Code == "restore_reconnect_required" {
			return "connection_revoked"
		}
		switch remote.Code {
		case "scope_denied", "permission_denied", "connection_revoked", "authorization_required", "cognee_authorization_required", "invalid_provenance", "not_found", "source_not_found", "original_unavailable", "version_changed", "stale_source", "scope_changed", "service_stopped", "service_unavailable", "cognee_unavailable", "service_error", "timeout", "deadline_exceeded", "index_unavailable":
			return remote.Code
		}
		return "source_read_failed"
	}
	if errors.Is(err, os.ErrPermission) {
		return "permission_denied"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var transport *net.OpError
	if errors.As(err, &transport) {
		if transport.Timeout() {
			return "timeout"
		}
		return "service_unavailable"
	}
	return "source_read_failed"
}
func sourceReaderFailureMessage(code string) string {
	switch code {
	case "scope_denied", "permission_denied", "connection_revoked", "authorization_required", "cognee_authorization_required":
		return "当前来源访问权限不足或已撤销。请返回知识，确认项目授权后重新打开。"
	case "invalid_provenance", "not_found", "source_not_found", "original_unavailable":
		return "来源身份缺失或无法核验。引用关系保留，请返回知识核对来源。"
	case "version_changed", "stale_source", "scope_changed":
		return "知识或来源版本已经变化。请返回知识，核对当前版本后重新打开出处。"
	case "service_stopped":
		return "记忆服务已停止。请先查看服务状态，明确启动后重新打开出处。"
	case "cancelled":
		return "本次原文读取已取消，请返回知识后重新打开。"
	case "source_read_failed":
		return "原文读取失败，原因尚未确认。可以重新读取，旧来源正文保持不可读。"
	case "timeout", "deadline_exceeded":
		return "原文读取超时，尚未确认来源内容。可以重新读取。"
	default:
		return "原文服务暂不可用，尚未确认来源内容。可以重新读取。"
	}
}
func sourceReaderCanRetry(s SourceReaderUI) bool {
	if s.Loading {
		return false
	}
	switch s.ErrorCode {
	case "scope_denied", "permission_denied", "connection_revoked", "authorization_required", "cognee_authorization_required", "invalid_provenance", "not_found", "source_not_found", "original_unavailable", "version_changed", "stale_source", "scope_changed", "service_stopped", "cancelled":
		return false
	}
	if s.Full && s.HadSessionOriginal && s.Result.SessionOriginal == nil {
		switch s.Result.OriginalState {
		case "source_changed", "owner_changed", "upstream_changed", "upstream_unavailable":
			return false
		}
	}
	switch s.Result.State {
	case "source_changed", "owner_changed", "upstream_changed", "upstream_unavailable":
		return false
	}
	return true
}
