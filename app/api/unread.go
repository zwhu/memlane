package api

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
