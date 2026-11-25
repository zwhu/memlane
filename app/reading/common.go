package reading

import (
	"context"
	"errors"

	"github.com/zwhu/memlane/app/api"
)

type CommonTransport interface {
	ListCommonMemory(context.Context, api.KnowledgePageOptions) (api.KnowledgePage, error)
	GetCommonMemory(context.Context, string) (api.DocumentDetail, error)
	CreateCommonMemory(context.Context, api.SaveCommonMemoryRequest) (api.SaveCommonMemoryResponse, error)
	UpdateCommonMemory(context.Context, string, api.SaveCommonMemoryRequest) (api.SaveCommonMemoryResponse, error)
	DeleteCommonMemory(context.Context, string) error
}

type CommonController struct {
	Client    *api.Client
	Transport CommonTransport
}

func (c CommonController) transport() CommonTransport {
	if c.Transport != nil {
		return c.Transport
	}
	return c.Client
}

func (c CommonController) List(ctx context.Context, opts api.KnowledgePageOptions) (api.KnowledgePage, error) {
	return FetchKnowledgePage(ctx, opts, c.transport().ListCommonMemory)
}

func (c CommonController) Get(ctx context.Context, id string) (api.DocumentDetail, error) {
	return c.transport().GetCommonMemory(ctx, id)
}

var ErrCommonChanged = errors.New("原记忆已变化，请刷新后重新编辑")

func (c CommonController) Save(ctx context.Context, id string, draft CommonDraft, baseline [32]byte) (api.SaveCommonMemoryResponse, error) {
	body, err := FormatDocument(draft)
	if err != nil {
		return api.SaveCommonMemoryResponse{}, err
	}
	input := api.SaveCommonMemoryRequest{Category: &draft.Category, Title: &draft.Title, Content: &body}
	if id == "" {
		return c.transport().CreateCommonMemory(ctx, input)
	}
	current, err := c.Get(ctx, id)
	if err != nil {
		return api.SaveCommonMemoryResponse{}, err
	}
	if BaselineSHA256(current.Content) != baseline || current.CommonCategory != "" && current.CommonCategory != draft.Category {
		return api.SaveCommonMemoryResponse{}, ErrCommonChanged
	}
	input.Category = nil
	return c.transport().UpdateCommonMemory(ctx, id, input)
}

func (c CommonController) Delete(ctx context.Context, id string) error {
	return c.transport().DeleteCommonMemory(ctx, id)
}
