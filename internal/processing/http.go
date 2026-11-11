package processing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/zwhu/memlane/internal/credentials"
	"github.com/zwhu/memlane/internal/records"
)

// HTTPBinding is created from a frozen configuration and its exact credential
// version. Secrets are transient; callers clear them after the enclosing task.
type CredentialReader interface {
	Get(credentials.Reference, credentials.Target) ([]byte, error)
}

type HTTPBinding struct {
	OnEvent    func(string)           `json:"-"`
	Spec       records.ProcessingSpec `json:"spec"`
	Credential []byte                 `json:"-"`
}

func ResolveHTTP(ctx context.Context, spec records.ProcessingSpec, vault CredentialReader) (HTTPBinding, string, error) {
	var out HTTPBinding
	if ctx.Err() != nil {
		return out, "", ctx.Err()
	}
	if spec.Mode == "magpie" {
		if spec.Credential != "" || spec.CredentialRevision != 0 || spec.Provider != "" {
			return out, "", records.MergeError("invalid_configuration")
		}
		return HTTPBinding{Spec: spec}, records.MergeDigest(spec), nil
	}
	if spec.Mode != "provider" || vault == nil {
		return out, "", records.MergeError("independent_credential_required")
	}
	secret, err := vault.Get(credentials.Reference{ID: spec.Credential, Revision: spec.CredentialRevision}, credentials.Target{Provider: spec.Provider, Endpoint: spec.Endpoint, Protocol: spec.Protocol})
	if err != nil {
		return out, "", records.MergeError("independent_credential_unavailable")
	}
	if ctx.Err() != nil {
		clear(secret)
		return out, "", ctx.Err()
	}
	// Hash only; no credential value is a stored dependency or receipt field.
	return HTTPBinding{Spec: spec, Credential: secret}, records.MergeDigest([]any{spec, records.MergeDigest(secret)}), nil
}

func (b *HTTPBinding) Close() {
	clear(b.Credential)
	b.Credential = nil
}

type ProcessingModel = records.ProcessingModel
type ProcessingModelCatalog = records.ProcessingModelCatalog

// A catalog proves returned model identifiers only. It does not imply schema
// capability, inference availability, subscription entitlement or quality.
func ReadModels(ctx context.Context, b HTTPBinding, after string) (ProcessingModelCatalog, error) {
	var out ProcessingModelCatalog
	if after != "" && (b.Spec.Protocol != "anthropic-messages" || len(after) > 256) {
		return out, records.MergeError("invalid_request")
	}
	route := "/models"
	if after != "" {
		route += "?" + url.Values{"after_id": []string{after}}.Encode()
	}
	response, err := b.request(ctx, "GET", route, nil)
	if err != nil {
		return out, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil || len(raw) > 2*1024*1024 {
		return out, records.MergeError("model_catalog_invalid")
	}
	if len(b.Credential) > 0 && bytes.Contains(raw, b.Credential) {
		return out, records.MergeError("model_catalog_invalid")
	}
	var value struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			Provider    string `json:"owned_by"`
		} `json:"data"`
		HasMore bool   `json:"has_more"`
		LastID  string `json:"last_id"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Data == nil || len(value.Data) > 10000 || len(value.LastID) > 256 || value.HasMore && value.LastID == "" {
		return out, records.MergeError("model_catalog_invalid")
	}
	out = ProcessingModelCatalog{Endpoint: b.Spec.Endpoint, Protocol: b.Spec.Protocol, Models: []ProcessingModel{}, HasMore: value.HasMore, LastID: value.LastID, ReadMS: time.Now().UnixMilli()}
	seen := map[string]bool{}
	for _, model := range value.Data {
		if model.ID == "" || len(model.ID) > 256 || len(model.DisplayName) > 512 || len(model.Provider) > 128 || !utf8.ValidString(model.ID) || !utf8.ValidString(model.DisplayName) || !utf8.ValidString(model.Provider) || strings.ContainsAny(model.ID+model.Provider, "\r\n\x00") || strings.TrimSpace(model.ID) != model.ID || seen[model.ID] {
			return ProcessingModelCatalog{}, records.MergeError("model_catalog_invalid")
		}
		if len(b.Credential) > 0 && (bytes.Contains([]byte(model.ID), b.Credential) || bytes.Contains([]byte(model.DisplayName), b.Credential) || bytes.Contains([]byte(model.Provider), b.Credential)) {
			return ProcessingModelCatalog{}, records.MergeError("model_catalog_invalid")
		}
		seen[model.ID] = true
		out.Models = append(out.Models, ProcessingModel{ID: model.ID, Name: model.DisplayName, Provider: model.Provider})
	}
	if ctx.Err() != nil {
		return ProcessingModelCatalog{}, ctx.Err()
	}
	return out, nil
}

type HTTPCompletion struct {
	Receipt  records.ProcessingHTTPReceipt `json:"http"`
	Model    string
	Output   json.RawMessage `json:"-"`
	Terminal bool
}

func (b HTTPBinding) request(ctx context.Context, method, route string, body []byte) (*http.Response, error) {
	u, err := url.Parse(b.Spec.Endpoint)
	local := err == nil && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Scheme != "https" && !(local && u.Scheme == "http")) ||
		(b.Spec.Mode != "provider" && b.Spec.Mode != "magpie") ||
		(b.Spec.Mode == "magpie" && (!local || u.Scheme != "http" || len(b.Credential) != 0)) {
		return nil, records.MergeError("invalid_configuration")
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(b.Spec.Endpoint, "/")+route, bytes.NewReader(body))
	if err != nil {
		return nil, records.MergeError("invalid_configuration")
	}
	if b.OnEvent != nil {
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				b.OnEvent("http.request_sent")
			}
		}, GotFirstResponseByte: func() { b.OnEvent("http.response_started") }}))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Memlane/1 memory-processing")
	req.Header.Set("X-Memlane-Internal-Processing", "1")
	if len(b.Credential) > 8192 || bytes.ContainsAny(b.Credential, "\r\n") {
		return nil, records.MergeError("invalid_credential")
	}
	if b.Spec.Protocol == "anthropic-messages" {
		req.Header.Set("anthropic-version", "2023-06-01")
		if len(b.Credential) > 0 {
			req.Header.Set("x-api-key", string(b.Credential))
		}
	} else if b.Spec.Protocol == "openai-chat" || b.Spec.Protocol == "openai-responses" {
		if len(b.Credential) > 0 {
			req.Header.Set("Authorization", "Bearer "+string(b.Credential))
		}
	} else {
		return nil, records.MergeError("unsupported_processing")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if b.Spec.Mode == "magpie" {
		transport.Proxy = nil
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Upstream errors can contain echoed credentials or request bodies.
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return nil, records.MergeError("provider_timeout")
		}
		return nil, records.MergeError("provider_unavailable")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		switch response.StatusCode {
		case 401, 403:
			return nil, records.MergeError("provider_authentication_failed")
		case 429:
			return nil, records.MergeError("provider_rate_limited")
		case 400, 422:
			return nil, records.MergeError("provider_request_invalid")
		default:
			return nil, records.MergeError("provider_request_failed")
		}
	}
	return response, nil
}

// CompleteHTTP binds the full schema to the selected native API. There is no
// prompt-only/json_object fallback, tool execution or automatic route switch.
// Cancelling stops local transport and rejects late results; upstream billing
// or cancellation acknowledgement requires separate provider-specific proof.
func CompleteHTTP(ctx context.Context, b HTTPBinding, system, prompt string, schema json.RawMessage) (out HTTPCompletion, resultErr error) {
	var sent atomic.Bool
	notify := b.OnEvent
	var notifyMu sync.Mutex
	b.OnEvent = func(event string) {
		notifyMu.Lock()
		defer notifyMu.Unlock()
		if event == "http.request_sent" {
			sent.Store(true)
		}
		if notify != nil {
			notify(event)
		}
	}
	defer func() {
		out.Receipt.RequestSent = sent.Load()
		out.Receipt.LocalCancelled = errors.Is(ctx.Err(), context.Canceled)
		out.Receipt.FinishedMS = time.Now().UnixMilli()
	}()
	if system == "" || prompt == "" || len(system)+len(prompt) > 1024*1024 || len(schema) > 65536 {
		return out, records.MergeError("invalid_request")
	}
	var definition jsonschema.Schema
	if json.Unmarshal(schema, &definition) != nil {
		return out, records.MergeError("invalid_request")
	}
	resolved, err := definition.Resolve(nil)
	if err != nil {
		return out, records.MergeError("invalid_request")
	}
	request := map[string]any{"model": b.Spec.Model, "stream": false}
	format := map[string]any{"type": "json_schema", "name": "memlane_response", "strict": true, "schema": schema}
	route := ""
	switch b.Spec.Protocol {
	case "openai-responses":
		route = "/responses"
		request["instructions"], request["input"] = system, prompt
		request["text"], request["store"] = map[string]any{"format": format}, false
	case "openai-chat":
		route = "/chat/completions"
		request["store"] = false
		request["messages"] = []any{map[string]string{"role": "system", "content": system}, map[string]string{"role": "user", "content": prompt}}
		request["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "memlane_response", "strict": true, "schema": schema}}
	case "anthropic-messages":
		route = "/messages"
		request["system"], request["max_tokens"] = system, 8192
		request["messages"] = []any{map[string]string{"role": "user", "content": prompt}}
		request["output_config"] = map[string]any{"format": map[string]any{"type": "json_schema", "schema": schema}}
	default:
		return out, records.MergeError("unsupported_processing")
	}
	if b.Spec.Model == "" {
		return out, records.MergeError("invalid_configuration")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return out, records.MergeError("invalid_request")
	}
	out.Receipt = records.ProcessingHTTPReceipt{Protocol: b.Spec.Protocol, RequestedModel: b.Spec.Model, NativeSchemaRequested: true, StartedMS: time.Now().UnixMilli()}
	response, err := b.request(ctx, "POST", route, raw)
	if err != nil {
		return out, err
	}
	defer response.Body.Close()
	out.Receipt.Status = response.StatusCode
	raw, err = io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if err != nil || len(raw) > 2*1024*1024 {
		return out, records.MergeError("provider_response_invalid")
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if len(b.Credential) > 0 && bytes.Contains(raw, b.Credential) {
		return out, records.MergeError("provider_response_invalid")
	}
	var value struct {
		Model      string `json:"model"`
		Status     string `json:"status"`
		StopReason string `json:"stop_reason"`
		Output     []struct {
			Type    string                        `json:"type"`
			Role    string                        `json:"role"`
			Content []struct{ Type, Text string } `json:"content"`
		} `json:"output"`
		Choices []struct {
			Finish  string `json:"finish_reason"`
			Message struct {
				Role, Content, Refusal string
				Tools                  []json.RawMessage `json:"tool_calls"`
				Function               json.RawMessage   `json:"function_call"`
			} `json:"message"`
		} `json:"choices"`
		Content []struct{ Type, Text string } `json:"content"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Model == "" {
		return out, records.MergeError("provider_response_invalid")
	}
	if len(value.Model) > 512 || strings.ContainsAny(value.Model, "\r\n\x00") {
		return out, records.MergeError("provider_response_invalid")
	}
	out.Receipt.ReportedModel = value.Model
	if b.Spec.Mode == "magpie" {
		for _, field := range []struct {
			name   string
			target *string
		}{{"X-Magpie-Provider", &out.Receipt.GatewayProvider}, {"X-Magpie-Model", &out.Receipt.GatewayModel}} {
			v := response.Header.Get(field.name)
			if len(v) > 512 || strings.ContainsAny(v, "\r\n\x00") {
				return out, records.MergeError("provider_response_invalid")
			}
			*field.target = v
		}
	}
	var result strings.Builder
	switch b.Spec.Protocol {
	case "openai-responses":
		if value.Status != "completed" || len(value.Output) == 0 {
			return out, records.MergeError("provider_response_incomplete")
		}
		for _, message := range value.Output {
			if message.Type == "reasoning" {
				continue
			}
			if message.Type != "message" || message.Role != "assistant" {
				return out, records.MergeError("unexpected_model_tool")
			}
			for _, part := range message.Content {
				if part.Type != "output_text" {
					return out, records.MergeError("provider_response_invalid")
				}
				result.WriteString(part.Text)
			}
		}
	case "openai-chat":
		if len(value.Choices) != 1 || value.Choices[0].Finish != "stop" {
			return out, records.MergeError("provider_response_incomplete")
		}
		message := value.Choices[0].Message
		if message.Role != "assistant" || message.Refusal != "" || len(message.Tools) != 0 || len(message.Function) != 0 && string(message.Function) != "null" {
			return out, records.MergeError("provider_response_invalid")
		}
		result.WriteString(message.Content)
	case "anthropic-messages":
		if value.StopReason != "end_turn" || len(value.Content) == 0 {
			return out, records.MergeError("provider_response_incomplete")
		}
		for _, part := range value.Content {
			if part.Type != "text" {
				return out, records.MergeError("unexpected_model_tool")
			}
			result.WriteString(part.Text)
		}
	}
	if len(b.Credential) > 0 && bytes.Contains([]byte(result.String()), b.Credential) {
		return out, records.MergeError("provider_response_invalid")
	}
	var content any
	if json.Unmarshal([]byte(result.String()), &content) != nil || resolved.Validate(content) != nil {
		return out, records.MergeError("model_schema_invalid")
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	out.Receipt.ResponseComplete, out.Receipt.SchemaValid, out.Receipt.FinishedMS = true, true, time.Now().UnixMilli()
	out.Model, out.Output, out.Terminal = value.Model, json.RawMessage(result.String()), true
	if b.OnEvent != nil {
		b.OnEvent("http.schema_valid")
	}
	return out, nil
}
