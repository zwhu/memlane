package app

import (
	"context"
	"net/url"

	"github.com/zwhu/memlane/app/settings"
)

func (c ProjectControlClient) request(ctx context.Context, method, path string, input, output any) error {
	return c.requestWithHeaders(ctx, method, path, nil, input, output, 2*1024*1024)
}

func (c ProjectControlClient) requestWithLimit(ctx context.Context, method, path string, input, output any, maxBytes int64) error {
	return c.requestWithHeaders(ctx, method, path, nil, input, output, maxBytes)
}

func (c ProjectControlClient) requestWithHeaders(ctx context.Context, method, path string, headers map[string]string, input, output any, maxBytes int64) error {
	return settings.ProjectControlClient(c).RequestWithHeaders(ctx, method, path, headers, input, output, maxBytes)
}

func (c ProjectControlClient) directory(ctx context.Context) (AccessDirectory, error) {
	var result AccessDirectory
	err := c.request(ctx, "GET", "/v1/projects", nil, &result)
	return result, err
}

func (c ProjectControlClient) create(ctx context.Context, revision int64, name, workspace, dataset string) (AccessProject, error) {
	var result AccessProject
	input := map[string]any{"expected_revision": revision, "name": name}
	if workspace != "" {
		input["workspace"] = workspace
	}
	if dataset != "" {
		input["dataset_id"] = dataset
	}
	err := c.request(ctx, "POST", "/v1/projects", input, &result)
	return result, err
}

type LinkableDataset struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c ProjectControlClient) linkableDatasets(ctx context.Context) ([]LinkableDataset, error) {
	var result []LinkableDataset
	err := c.request(ctx, "GET", "/v1/linkable-datasets", nil, &result)
	return result, err
}

func (c ProjectControlClient) alias(ctx context.Context, revision int64, project, workspace string) (AccessProject, error) {
	var result AccessProject
	err := c.request(ctx, "POST", "/v1/projects/"+url.PathEscape(project)+"/aliases", map[string]any{"expected_revision": revision, "workspace": workspace}, &result)
	return result, err
}

// rename changes only the client display name; dataset identity and bindings stay fixed.
func (c ProjectControlClient) rename(ctx context.Context, revision int64, project, name string) (AccessProject, error) {
	var result AccessProject
	err := c.request(ctx, "PUT", "/v1/projects/"+url.PathEscape(project)+"/name", map[string]any{"expected_revision": revision, "name": name}, &result)
	return result, err
}
