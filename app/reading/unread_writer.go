package reading

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/shell"
)

const (
	MarkReadBatchLimit = 200
	FailedReadLimit    = 4096
)

var ErrReadingChanged = errors.New("阅读授权已变更")

type readScope struct {
	identity, project, dataset string
	auth                       uint64
}

type readJob struct {
	scope     readScope
	transport UnreadTransport
	request   api.MarkReadRequest
	all       bool
	complete  func(api.MarkReadResponse, error)
}

// ReadWriter owns the ordering of writes. Its fence's AuthEpoch must be safe
// to read from a worker. Completion runs on the worker and must dispatch UI work.
type ReadWriter struct {
	fence shell.SessionFence

	mu         sync.Mutex
	queue      []*readJob
	active     *readJob
	revoked    uint64
	hasRevoked bool
	failed     []*readJob
	idle       chan struct{}
}

func NewReadWriter(fence shell.SessionFence) *ReadWriter {
	return &ReadWriter{fence: fence}
}

func (w *ReadWriter) valid(s readScope) bool {
	return w.fence == nil || w.fence.AuthEpoch() == s.auth
}

// MarkRead runs one completion for each coalesced batch, using its latest callback.
func (w *ReadWriter) MarkRead(transport UnreadTransport, req api.MarkReadRequest, complete func(api.MarkReadResponse, error)) {
	w.enqueue(transport, req, false, complete)
}

func (w *ReadWriter) MarkAllRead(transport UnreadTransport, req api.MarkReadRequest, complete func(api.MarkReadResponse, error)) {
	w.enqueue(transport, req, true, complete)
}

func (w *ReadWriter) enqueue(transport UnreadTransport, req api.MarkReadRequest, all bool, complete func(api.MarkReadResponse, error)) {
	if all {
		req.DocIDs = nil
	} else if len(req.DocIDs) == 0 {
		return
	}
	scope := readScope{identity: req.ReadingIdentity, project: req.ProjectID, dataset: req.DatasetID}
	if w.fence != nil {
		scope.auth = w.fence.AuthEpoch()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	ids := req.DocIDs
	w.failed = slices.DeleteFunc(w.failed, func(f *readJob) bool { return !w.valid(f.scope) })
	if !all {
		for i := len(w.queue) - 1; i >= 0; i-- {
			j := w.queue[i]
			if j.scope != scope {
				continue
			}
			if j.all {
				break
			}
			j.request.DocIDs = appendUnique(j.request.DocIDs, ids...)
			j.complete = complete
			return
		}
	}
	req.DocIDs = appendUnique(nil, ids...)
	w.queue = append(w.queue, &readJob{scope, transport, req, all, complete})
	w.startLocked()
}

func appendUnique(dst []string, ids ...string) []string {
	seen := make(map[string]bool, len(dst)+len(ids))
	for _, id := range dst {
		seen[id] = true
	}
	for _, id := range ids {
		if id != "" && !seen[id] {
			dst = append(dst, id)
			seen[id] = true
		}
	}
	return dst
}

func (w *ReadWriter) startLocked() {
	if w.idle == nil && len(w.queue) > 0 {
		w.idle = make(chan struct{})
		go w.drain()
	}
}

func (w *ReadWriter) drain() {
	for {
		w.mu.Lock()
		if len(w.queue) == 0 {
			close(w.idle)
			w.idle = nil
			w.mu.Unlock()
			return
		}
		j := w.queue[0]
		w.queue[0] = nil
		w.queue = w.queue[1:]
		if !j.all && w.valid(j.scope) {
			var retries []string
			w.failed = slices.DeleteFunc(w.failed, func(f *readJob) bool {
				if f.scope != j.scope {
					return false
				}
				retries = appendUnique(retries, f.request.DocIDs...)
				return true
			})
			j.request.DocIDs = appendUnique(retries, j.request.DocIDs...)
		}
		w.active = j
		w.mu.Unlock()
		var out api.MarkReadResponse
		var remaining []string
		err := ErrReadingChanged
		if !w.hasRevoked || w.revoked != j.scope.auth {
			out, remaining, err = w.write(j)
			if w.fence != nil && Status(err, 401, 403) {
				w.revoked, w.hasRevoked = j.scope.auth, true
			}
		}
		if !w.valid(j.scope) {
			err = ErrReadingChanged
		}
		w.mu.Lock()
		if j.all && err == nil {
			w.failed = slices.DeleteFunc(w.failed, func(f *readJob) bool { return f.scope == j.scope })
		}
		if len(remaining) > 0 && retryableReadError(err) && w.valid(j.scope) {
			w.retainFailure(j, remaining)
		}
		w.active = nil
		w.mu.Unlock()
		if j.complete != nil {
			j.complete(out, err)
		}
	}
}

func (w *ReadWriter) retainFailure(job *readJob, ids []string) {
	failed := *job
	failed.request.DocIDs = ids
	w.failed = append(w.failed, &failed)
	count := 0
	for _, f := range w.failed {
		count += len(f.request.DocIDs)
	}
	for count > FailedReadLimit {
		oldest := w.failed[0]
		drop := min(count-FailedReadLimit, len(oldest.request.DocIDs))
		oldest.request.DocIDs = slices.Clone(oldest.request.DocIDs[drop:])
		count -= drop
		if len(oldest.request.DocIDs) == 0 {
			w.failed[0] = nil
			w.failed = w.failed[1:]
		}
	}
}

func retryableReadError(err error) bool {
	var apiErr *api.APIError
	return err != nil && !errors.Is(err, ErrReadingChanged) &&
		(!errors.As(err, &apiErr) || apiErr.Status >= 500 || Status(err, 408, 429))
}

func (w *ReadWriter) write(job *readJob) (out api.MarkReadResponse, remaining []string, err error) {
	if !w.valid(job.scope) {
		return out, nil, ErrReadingChanged
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req := job.request
	if job.all {
		out, err = job.transport.MarkAllRead(ctx, req)
		return out, nil, err
	}
	for start := 0; start < len(req.DocIDs); start += MarkReadBatchLimit {
		chunk := req
		chunk.DocIDs = req.DocIDs[start:min(start+MarkReadBatchLimit, len(req.DocIDs))]
		for attempt := 0; attempt < 3; attempt++ {
			if !w.valid(job.scope) {
				return out, nil, ErrReadingChanged
			}
			out, err = job.transport.MarkRead(ctx, chunk)
			if err == nil || !retryableReadError(err) || ctx.Err() != nil || attempt == 2 {
				break
			}
			select {
			case <-ctx.Done():
				return out, req.DocIDs[start:], ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
			}
		}
		if err != nil {
			return out, req.DocIDs[start:], err
		}
	}
	return out, nil, nil
}

func (w *ReadWriter) HasPending(projectID, datasetID string) bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	match := func(j *readJob) bool {
		return j != nil && w.valid(j.scope) &&
			(projectID == "" || j.scope.project == projectID) &&
			(datasetID == "" || j.scope.dataset == datasetID)
	}
	return match(w.active) || slices.ContainsFunc(w.queue, match) || slices.ContainsFunc(w.failed, match)
}

func (w *ReadWriter) WaitFlush() {
	_ = w.wait(context.Background())
}

func (w *ReadWriter) wait(ctx context.Context) error {
	if w == nil {
		return nil
	}
	for {
		w.mu.Lock()
		idle := w.idle
		w.mu.Unlock()
		if idle == nil {
			return nil
		}
		select {
		case <-idle:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (w *ReadWriter) FlushWithTimeout(timeout time.Duration) error {
	if w == nil {
		return nil
	}
	ctx := context.Background()
	if timeout > 0 {
		var cancel func()
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if err := w.wait(ctx); err != nil {
		return fmt.Errorf("已读状态刷新超时: %w", err)
	}
	w.mu.Lock()
	for _, f := range w.failed {
		if w.valid(f.scope) {
			w.queue = append(w.queue, f)
		}
	}
	w.failed = nil
	w.startLocked()
	w.mu.Unlock()
	if err := w.wait(ctx); err != nil {
		return fmt.Errorf("已读状态刷新超时: %w", err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	count := 0
	for _, f := range w.failed {
		if w.valid(f.scope) {
			count += len(f.request.DocIDs)
		}
	}
	if count > 0 {
		return fmt.Errorf("仍有 %d 条已读状态未能保存", count)
	}
	return nil
}
