package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zwhu/memlane/internal/controlplane"
)

func (c ProjectControlClient) request(ctx context.Context, method, path string, input, output any) error {
	return c.RequestWithHeaders(ctx, method, path, nil, input, output, 2*1024*1024)
}

func (c ProjectControlClient) RequestWithHeaders(ctx context.Context, method, path string, headers map[string]string, input, output any, maxBytes int64) error {
	route, _, _ := strings.Cut(path, "?")
	// These paths existed with a different response contract before the cutover.
	requiredReadingField := ""
	if method == http.MethodGet {
		switch {
		case route == "/v1/overview/bootstrap":
			requiredReadingField = "generation"
		case strings.HasPrefix(route, "/v1/projects/") && strings.HasSuffix(route, "/message-summary"):
			requiredReadingField = "unread_count"
		}
	}
	if requiredReadingField != "" {
		if err := c.RequireReadingContract(ctx); err != nil {
			return err
		}
	}

	origin, err := controlplane.ValidateLoopbackOrigin(c.Config.URL)
	if err != nil {
		return fmt.Errorf("项目管理服务尚未配置")
	}
	key, err := controlplane.ReadPrivateToken(c.Config.ManagementTokenFile, 32, true)
	if err != nil {
		return &ControlResponseError{
			Status:  http.StatusUnauthorized,
			Code:    "control_credential_unavailable",
			Message: "项目管理连接凭据不可用",
		}
	}
	var body io.Reader
	if input != nil {
		data, marshalErr := json.Marshal(input)
		if marshalErr != nil {
			return marshalErr
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, origin+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if k != "" && v != "" {
			req.Header.Set(k, v)
		}
	}
	if requiredReadingField != "" {
		req.Header.Set("X-Memlane-Reading-Contract", ReadingContract)
	}
	hc := controlplane.NewLoopbackHTTPClient(c.HTTP, 125*time.Second)
	response, err := hc.Do(req)
	if err != nil {
		if method != "GET" {
			return fmt.Errorf("设置结果未确认，请刷新核对原操作；不会自动重复提交")
		}
		return fmt.Errorf("项目目录暂时不可用，保留已载入项目")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		code, phase := parseControlError(snippet, key)
		message := fmt.Sprintf("项目管理请求未确认（HTTP %d），请核对原操作", response.StatusCode)
		if response.StatusCode == http.StatusConflict {
			message = formatControlConflictMessage(code, phase)
		}
		return &ControlResponseError{Status: response.StatusCode, Code: code, Phase: phase, Message: message}
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil
	}
	if maxBytes <= 0 {
		maxBytes = 2 * 1024 * 1024
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes {
		return fmt.Errorf("项目管理回执未确认，请刷新核对")
	}
	if requiredReadingField != "" {
		// The running backend may have changed after the successful capability probe.
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil || fields[requiredReadingField] == nil {
			return readingContractMismatch()
		}
	}
	if json.Unmarshal(data, output) != nil {
		return fmt.Errorf("项目管理回执未确认，请刷新核对")
	}
	return nil
}
