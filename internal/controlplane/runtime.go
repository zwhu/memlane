package controlplane

import (
	"context"
	"net/http"
	"slices"
)

func (caps ControlCapabilities) HasFeature(feature string) bool {
	const readingContract = "catalog-cursor-v1"
	if feature == readingContract {
		return caps.ReadingContract == readingContract
	}
	return slices.Contains(caps.Features, feature)
}

func (c *Client) Capabilities(ctx context.Context) (ControlCapabilities, error) {
	var result ControlCapabilities
	err := c.Request(ctx, http.MethodGet, "/v1/capabilities", nil, &result)
	return result, err
}

func (c *Client) RuntimeMonitor(ctx context.Context, includeLogs bool) (ServiceMonitorSnapshot, error) {
	path := "/v1/runtime/monitor"
	if includeLogs {
		path += "?include_logs=1"
	}
	var result ServiceMonitorSnapshot
	err := c.Request(ctx, http.MethodGet, path, nil, &result)
	return result, err
}

func (c *Client) GenerationState(ctx context.Context) (GenerationDirectory, error) {
	var result GenerationDirectory
	err := c.Request(ctx, http.MethodGet, "/v1/generation", nil, &result)
	return result, err
}
