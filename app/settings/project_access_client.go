package settings

import (
	"context"
	"fmt"
	"net/url"
)

func (c ProjectControlClient) directory(ctx context.Context) (AccessDirectory, error) {
	var result AccessDirectory
	err := c.request(ctx, "GET", "/v1/projects", nil, &result)
	return result, err
}

func (c ProjectControlClient) defaults(ctx context.Context, revision int64, policy ProjectPolicy) (AccessDirectory, error) {
	var result AccessDirectory
	err := c.request(ctx, "PUT", "/v1/defaults", map[string]any{"expected_revision": revision, "fields": policy}, &result)
	return result, err
}

func (c ProjectControlClient) overrides(ctx context.Context, revision int64, project, agent string, fields map[string]any) (AccessDirectory, error) {
	var result AccessDirectory
	err := c.request(ctx, "PUT", "/v1/projects/"+url.PathEscape(project)+"/overrides", map[string]any{"expected_revision": revision, "agent_id": agent, "fields": fields}, &result)
	return result, err
}

func (c ProjectControlClient) SetProjectAutoCapture(ctx context.Context, projectID string, enabled bool) (AccessProject, error) {
	var result AccessProject
	err := c.request(ctx, "PUT", "/v1/projects/"+url.PathEscape(projectID)+"/auto-capture", map[string]bool{"enabled": enabled}, &result)
	if err == nil && (result.ID != projectID || result.AutoCapture == nil || *result.AutoCapture != enabled || result.AutoCaptureVersion < 1) {
		err = fmt.Errorf("自动采集设置回执不完整")
	}
	return result, err
}

func (c ProjectControlClient) reconcile(ctx context.Context, revision int64, project string) (AccessDirectory, error) {
	var result AccessDirectory
	err := c.request(ctx, "POST", "/v1/projects/"+url.PathEscape(project)+"/reconcile", map[string]any{"expected_revision": revision}, &result)
	return result, err
}

func (c ProjectControlClient) deleteProject(ctx context.Context, projectID, datasetID string) error {
	path := "/v1/projects/" + url.PathEscape(projectID)
	if ds := url.QueryEscape(datasetID); ds != "" {
		path += "?dataset_id=" + ds
	}
	return c.request(ctx, "DELETE", path, nil, nil)
}
