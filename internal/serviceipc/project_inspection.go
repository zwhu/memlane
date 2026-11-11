package serviceipc

import (
	"context"
	"github.com/zwhu/memlane/internal/records"
)

func (l Library) InspectProject(ctx context.Context, path string) (records.ProjectInspection, error) {
	var out records.ProjectInspection
	err := Call(ctx, l.Root, Request{Op: "inspect_project", Project: records.ScopeDraft{Path: path}}, &out)
	return out, err
}
