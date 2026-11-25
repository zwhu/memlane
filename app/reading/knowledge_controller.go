package reading

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/zwhu/memlane/app/api"
	"github.com/zwhu/memlane/app/shell"
)

const KnowledgeBatchSize = 30

type KnowledgeTransport interface {
	ListKnowledge(context.Context, api.KnowledgePageOptions) (api.KnowledgePage, error)
	GetKnowledgeDocumentBody(context.Context, string, string) (api.DocumentBody, error)
	GetKnowledgeDocument(context.Context, string, string) (api.DocumentDetail, error)
}

type KnowledgeDeps struct {
	Client     *api.Client
	Transport  KnowledgeTransport
	Dispatcher shell.Dispatcher
	Router     shell.Router
	Fence      shell.SessionFence
	Theme      shell.ThemeProvider
}

type KnowledgeController struct{ KnowledgeDeps }

func (c KnowledgeController) transport() KnowledgeTransport {
	if c.Transport != nil {
		return c.Transport
	}
	if c.Client == nil {
		return nil
	}
	return c.Client
}

func (c KnowledgeController) List(ctx context.Context, opts api.KnowledgePageOptions) (api.KnowledgePage, error) {
	transport := c.transport()
	if transport == nil {
		return api.KnowledgePage{}, errors.New("知识服务不可用")
	}
	return FetchKnowledgePage(ctx, opts, transport.ListKnowledge)
}

func FetchKnowledgePage[T any](ctx context.Context, opts api.KnowledgePageOptions, fetch func(context.Context, api.KnowledgePageOptions) (T, error)) (T, error) {
	if opts.Limit <= 0 {
		opts.Limit = KnowledgeBatchSize
	}
	return fetch(ctx, opts)
}

func (c KnowledgeController) Document(ctx context.Context, dataset, id string) (api.DocumentDetail, error) {
	transport := c.transport()
	if transport == nil {
		return api.DocumentDetail{}, errors.New("知识文档服务不可用")
	}
	return transport.GetKnowledgeDocument(ctx, dataset, id)
}

func (c KnowledgeController) Body(ctx context.Context, dataset, id string) (api.DocumentBody, error) {
	transport := c.transport()
	if transport == nil {
		return api.DocumentBody{}, errors.New("正文服务不可用")
	}
	body, err := transport.GetKnowledgeDocumentBody(ctx, dataset, id)
	if err == nil {
		err = ValidateBody(dataset, id, body.DatasetID, body.DocumentID, &body.Content)
	}
	return body, err
}

func (c KnowledgeController) Open(ctx context.Context, dataset, id string, publish func(api.DocumentBody, error)) {
	fence := shell.CaptureFence(c.Fence)
	go func() {
		body, err := c.Body(ctx, dataset, id)
		c.Dispatcher.Dispatch(func() {
			if ctx.Err() != nil || c.Fence != nil && !fence.Valid(c.Fence) {
				return
			}
			if err != nil {
				body = api.DocumentBody{}
				if c.Fence != nil && (Status(err, 401, 403, 404, 413) || errors.Is(err, ErrInvalidBody)) {
					c.Fence.InvalidateReading()
				}
			}
			publish(body, err)
		})
	}()
}

var ErrInvalidBody = errors.New("官方文档正文无效")

func ValidateBody(dataset, id, resultDataset, resultID string, body *string) error {
	match := func(a, b string) bool {
		return a != "" && b != "" && strings.EqualFold(strings.ReplaceAll(a, "-", ""), strings.ReplaceAll(b, "-", ""))
	}
	if body == nil || !match(dataset, resultDataset) || !match(id, resultID) {
		return fmt.Errorf("官方文档正文回执不完整: %w", ErrInvalidBody)
	}
	if !utf8.ValidString(*body) || strings.IndexByte(*body, 0) >= 0 {
		return fmt.Errorf("此官方文档不是 UTF-8 文本: %w", ErrInvalidBody)
	}
	return nil
}

func (c KnowledgeController) OpenSource(target shell.ItemTarget) error {
	if c.Router == nil {
		return errors.New("阅读导航不可用")
	}
	return c.Router.OpenItem(target)
}

func (c KnowledgeController) SourceSession(ctx context.Context, dataset, session, user string) (api.RawItem, error) {
	if c.Client == nil {
		return api.RawItem{}, errors.New("来源会话服务不可用")
	}
	return c.Client.GetSession(ctx, dataset, session, user)
}
