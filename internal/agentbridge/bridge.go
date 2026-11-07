// Package agentbridge converts Agent memory tools to the single local service.
package agentbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

type Config struct {
	Root, ConnectionID, Workspace string
	Generation                    int64
}

func (c Config) Validate() error {
	if !filepath.IsAbs(c.Root) || c.ConnectionID == "" || c.Generation < 1 {
		return errors.New("absolute root, connection ID and generation required")
	}
	if c.Workspace != "" && !filepath.IsAbs(c.Workspace) {
		return errors.New("workspace must be an explicit absolute path")
	}
	return nil
}

type Result struct {
	Protocol int             `json:"protocol_version"`
	OK       bool            `json:"ok"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    *Failure        `json:"error,omitempty"`
}
type Failure struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (c Config) Call(ctx context.Context, tool string, args records.AgentArguments) Result {
	data, _ := json.Marshal(args)
	if err := validate(tool, data); err != nil {
		return Result{Protocol: 1, Error: &Failure{Code: "invalid_request", Message: err.Error()}}
	}
	var raw json.RawMessage
	err := serviceipc.Call(ctx, c.Root, serviceipc.Request{Op: "agent", AgentRequest: records.AgentRequest{Protocol: 1, ConnectionID: c.ConnectionID, Generation: c.Generation, Workspace: c.Workspace, Tool: tool, Arguments: args}}, &raw)
	out := Result{Protocol: 1, OK: err == nil, Result: raw}
	if err != nil {
		f := &Failure{Code: "service_unavailable", Message: err.Error(), Retryable: true}
		var service *serviceipc.ServiceError
		if errors.As(err, &service) {
			f.Code = service.Code
			f.Retryable = service.Code == "index_unavailable" || service.Code == "service_error"
		}
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			f.Code = "timeout"
		}
		if errors.Is(err, context.Canceled) {
			f.Code = "cancelled"
			f.Retryable = false
		}
		if errors.Is(err, os.ErrPermission) {
			f.Code = "permission_denied"
			f.Retryable = false
		}
		out.Error = f
	}
	return out
}

func validate(tool string, raw json.RawMessage) error {
	switch tool {
	case "memory_scopes", "memory_save", "memory_get", "memory_search", "memory_status":
	default:
		return errors.New("unknown memory tool")
	}
	encoded, _ := json.Marshal(schema(tool))
	var s jsonschema.Schema
	if err := json.Unmarshal(encoded, &s); err != nil {
		return err
	}
	resolved, err := s.Resolve(nil)
	if err != nil {
		return err
	}
	var value any
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	if err = json.Unmarshal(raw, &value); err != nil {
		return err
	}
	return resolved.Validate(value)
}

func schema(tool string) map[string]any {
	str := func() any { return map[string]any{"type": "string"} }
	p := map[string]any{}
	p["scope"] = str()
	required := []string{}
	switch tool {
	case "memory_save":
		p["operation_id"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "description": "Stable ID for this save. Reuse unchanged on retries; different content conflicts."}
		p["text"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 524288}
		p["title"] = str()
		p["origin"] = map[string]any{"type": "string", "enum": []string{"knowledge", "raw"}}
		p["source"] = map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"fragment": str(), "references": map[string]any{"type": "array", "items": str()}}}
		required = []string{"operation_id", "text"}
	case "memory_search":
		p["scopes"] = map[string]any{"type": "array", "items": str()}
		p["query"] = map[string]any{"type": "string", "minLength": 1}
		p["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 100}
		required = []string{"query"}
	case "memory_get", "memory_status":
		p["memory_id"] = str()
		required = []string{"memory_id"}
	case "memory_scopes":
		p = map[string]any{}
	}
	return map[string]any{"type": "object", "properties": p, "required": required, "additionalProperties": false}
}

func (c Config) MCP(ctx context.Context) error {
	server := mcp.NewServer(&mcp.Implementation{Name: "memlane", Version: "0.0.1"}, &mcp.ServerOptions{Instructions: "共享本地项目记忆。先检索，保存使用稳定 operation_id；persisted 才表示已保存，index_state 单列。scope_required 时调用 memory_scopes 并让用户选择；其他项目须经 App 授权。已有知识的修改由用户在 App 确认。"})
	descriptions := map[string]string{
		"memory_save":   "保存 Agent 实际提交的新知识或原始记录。重试复用 operation_id；不覆盖已有知识。返回持久化回执和当前版本，索引状态独立。",
		"memory_search": "检索当前项目及允许的全局范围，返回可追溯的当前正文、来源和版本。其他项目必须显式选择已授权 scope。",
		"memory_get":    "读取允许范围中的当前正文、来源、版本和索引状态。",
		"memory_status": "查询已持久化记忆的当前版本与独立索引状态。",
		"memory_scopes": "列出本连接已获用户批准的范围，用于项目不明时让用户选择；不查询记忆。",
	}
	for _, name := range []string{"memory_scopes", "memory_search", "memory_get", "memory_save", "memory_status"} {
		server.AddTool(&mcp.Tool{Name: name, Description: descriptions[name], InputSchema: schema(name)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args records.AgentArguments
			err := validate(name, req.Params.Arguments)
			if err == nil && len(req.Params.Arguments) > 0 {
				err = json.Unmarshal(req.Params.Arguments, &args)
			}
			var out Result
			if err != nil {
				out = Result{Protocol: 1, Error: &Failure{Code: "invalid_request", Message: err.Error()}}
			} else {
				out = c.Call(ctx, name, args)
			}
			data, _ := json.Marshal(out)
			return &mcp.CallToolResult{IsError: !out.OK, Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}, StructuredContent: out}, nil
		})
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}
