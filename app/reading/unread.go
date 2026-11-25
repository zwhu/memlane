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

type UnreadScopeStateOf[T any] = api.UnreadScopeStateOf[T]

func ApplyServerSnapshot[T any](state *UnreadScopeStateOf[T], unreadCount int, stateRev int64, writerPending bool) bool {
	return api.ApplyServerSnapshot(state, unreadCount, stateRev, writerPending)
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
