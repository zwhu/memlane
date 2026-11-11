package serviceipc

import (
	"context"

	"github.com/zwhu/memlane/internal/records"
)

func (l Library) AuthorizeProjectCogneeMaterial(ctx context.Context, request records.CogneeProjectAuthorization) (records.CogneeBinding, error) {
	var out records.CogneeBinding
	err := Call(ctx, l.Root, Request{Op: "cognee_project_authorize", CogneeProjectAuthorization: &request, UserConfirmed: true}, &out)
	return out, err
}
