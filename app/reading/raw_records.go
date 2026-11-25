package reading

import (
	"context"
	"errors"

	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/shell"
)

const RawBatchSize = 30

type RawTransport interface {
	ListRawRecords(context.Context, api.RawRecordsOptions) (api.RawRecordsPage, error)
	ListSessionChildren(context.Context, string, string, api.RawRecordsOptions) (api.RawRecordsPage, error)
}

type RawRecordsController struct {
	Client     *api.Client
	Transport  RawTransport
	Dispatcher shell.Dispatcher
	Fence      shell.SessionFence
	Options    api.RawRecordsOptions
	SessionID  string
	Items      []api.RawItem
	NextCursor string
	More, Busy bool
	Error      error
	request    uint64
}

func RawKind(kind string) string {
	switch kind {
	case "会话", "session":
		return "session"
	case "文档", "document":
		return "document"
	default:
		return "all"
	}
}

func NearRawListEnd(y, maxY float32) bool { return maxY > 0 && y >= maxY-160 }

func InvalidCursor(err error) bool {
	var e *api.APIError
	return errors.As(err, &e) && (e.Status == 400 || e.Status == 409) && (e.Code == "invalid_cursor" || e.Code == "invalid_argument")
}

func FetchRawPage[T any](ctx context.Context, opts api.RawRecordsOptions, fetch func(context.Context, api.RawRecordsOptions) (T, error)) (T, error) {
	opts.Kind, opts.Limit = RawKind(opts.Kind), RawBatchSize
	return fetch(ctx, opts)
}

func (c *RawRecordsController) Reload(ctx context.Context) { c.fetch(ctx, false) }

func (c *RawRecordsController) Append(ctx context.Context) {
	if !c.Busy && c.More && c.NextCursor != "" {
		c.fetch(ctx, true)
	}
}

func (c *RawRecordsController) AutoLoad(ctx context.Context, y, maxY float32) {
	if NearRawListEnd(y, maxY) {
		c.Append(ctx)
	}
}

func (c *RawRecordsController) fetch(ctx context.Context, appendPage bool) {
	opts := c.Options
	opts.Cursor = ""
	if appendPage {
		opts.Cursor = c.NextCursor
	}
	session := c.SessionID
	c.Busy, c.Error = true, nil
	c.request++
	request, fence := c.request, shell.CaptureFence(c.Fence)
	go func() {
		page, err := c.Page(ctx, session, opts)
		c.Dispatcher.Dispatch(func() {
			if request != c.request {
				return
			}
			c.Busy = false
			if ctx.Err() != nil || c.Fence != nil && !fence.Valid(c.Fence) {
				return
			}
			if appendPage && InvalidCursor(err) {
				c.More, c.NextCursor = false, ""
				c.Reload(ctx)
				return
			}
			c.Error = err
			if err != nil {
				if Status(err, 401, 403) {
					c.Items, c.More, c.NextCursor = nil, false, ""
					if c.Fence != nil {
						c.Fence.InvalidateReading()
					}
				}
				return
			}
			if !appendPage {
				c.Items = nil
			}
			c.Items = AppendDistinct(c.Items, page.Items, rawKey)
			c.Items, _ = EnforceSlidingWindowDir(c.Items, true)
			c.More, c.NextCursor = page.HasMore, page.NextCursor
		})
	}()
}

func rawKey(i api.RawItem) string {
	return i.DatasetID + "/" + i.Kind + "/" + i.UserID + "/" + i.SessionID + "/" + i.DocID + "/" + i.DocumentID
}

func (c RawRecordsController) Page(ctx context.Context, session string, opts api.RawRecordsOptions) (api.RawRecordsPage, error) {
	transport := c.Transport
	if transport == nil {
		if c.Client == nil {
			return api.RawRecordsPage{}, errors.New("原始记录服务不可用")
		}
		transport = c.Client
	}
	if session != "" {
		opts.Kind = "session"
	}
	return FetchRawPage(ctx, opts, func(ctx context.Context, opts api.RawRecordsOptions) (api.RawRecordsPage, error) {
		if session != "" {
			return transport.ListSessionChildren(ctx, opts.ProjectID, session, opts)
		}
		return transport.ListRawRecords(ctx, opts)
	})
}
