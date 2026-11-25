package reading

import (
	"context"
	"errors"

	"github.com/zwhu/memlane/app/api"
)

type UnreadTransport interface {
	ReadingState(context.Context, string) (api.ReadingStateResponse, error)
	MarkRead(context.Context, api.MarkReadRequest) (api.MarkReadResponse, error)
	MarkAllRead(context.Context, api.MarkReadRequest) (api.MarkReadResponse, error)
}

var _ UnreadTransport = (*api.Client)(nil)

type UnreadScopeState = UnreadScopeStateOf[api.CatalogItem]

type UnreadScopeStateOf[T any] struct {
	ServerUnreadKnown    bool
	ServerUnreadCount    int
	ServerUnreadStateRev int64
	ServerUnreadSnapshot uint64
	UnreadHasMore        bool
	UnreadPreview        []T
	Error                string
	ReadIDs              map[string]bool
}

func ApplyServerSnapshot[T any](state *UnreadScopeStateOf[T], unreadCount int, stateRev int64, writerPending bool) bool {
	if state == nil || stateRev < state.ServerUnreadStateRev ||
		(writerPending && stateRev <= state.ServerUnreadStateRev) {
		return false
	}
	state.ServerUnreadStateRev = stateRev
	state.ServerUnreadKnown = true
	state.ServerUnreadCount = max(0, unreadCount)
	state.ServerUnreadSnapshot++
	return true
}

func CanMarkRead(kind string, hasDoc, isKnowledge, bodyLoaded, bodyTruncated bool) bool {
	return (kind == "知识" || kind == "knowledge") && hasDoc && isKnowledge && bodyLoaded && !bodyTruncated
}

func Status(err error, codes ...int) bool {
	var e *api.APIError
	if !errors.As(err, &e) {
		return false
	}
	for _, code := range codes {
		if e.Status == code {
			return true
		}
	}
	return false
}
