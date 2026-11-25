package settings

import (
	"context"

	"github.com/zwhu/memlane/app/api"
)

func applyServerUnreadSnapshot(q *ProjectQueueState, count int, rev int64, pending bool) bool {
	return q != nil && api.ApplyServerSnapshot(&q.UnreadState, count, rev, pending)
}

type projectUnreadAPI struct {
	control  ProjectControlClient
	identity string
}

func (t projectUnreadAPI) ReadingState(ctx context.Context, projectID string) (api.ReadingStateResponse, error) {
	client, err := newAppAPIClient(t.control, t.identity)
	if err != nil {
		return api.ReadingStateResponse{}, err
	}
	return client.ReadingState(ctx, projectID)
}
