package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
)

const ProtocolVersion = "2025-03-26"
const requestLimit = 1 << 20

type RememberRequest struct {
	Agent          domain.Agent
	Project        domain.Project
	UserID         string
	ReceiptUser    string
	Kind           engine.IngestKind
	Content        any
	RawContent     any
	SessionID      string
	MessageID      string
	IdempotencyKey string
	ContextDate    string
	Metadata       map[string]any
}

type RememberResult struct {
	OK             bool   `json:"ok"`
	Status         string `json:"status"`
	Saved          bool   `json:"saved"`
	TaskID         string `json:"task_id,omitempty"`
	EntryID        string `json:"entry_id,omitempty"`
	DocumentID     string `json:"document_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
	DatasetID      string `json:"dataset_id"`
	Deduplicated   bool   `json:"deduplicated"`
	Message        string `json:"message,omitempty"`
}

type TaskStatusResult struct {
	OK             bool   `json:"ok"`
	TaskID         string `json:"task_id"`
	ProjectID      string `json:"project_id"`
	DatasetID      string `json:"dataset_id"`
	Status         string `json:"status"`
	Saved          bool   `json:"saved"`
	EntryID        string `json:"entry_id,omitempty"`
	DocumentID     string `json:"document_id,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	LastError      string `json:"last_error,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type RememberService interface {
	Remember(ctx context.Context, req RememberRequest) (RememberResult, error)
	LookupTask(ctx context.Context, agent domain.Agent, taskID string) (TaskStatusResult, error)
}

type Config struct {
	Store           *domain.Store
	Engine          engine.MemoryEngine
	RememberService RememberService
	SearchTimeout   time.Duration
	SecretReader    func(path string) ([]byte, error)
	OnRecall        func(RecallObservation)
}

type Server struct {
	cfg         Config
	tools       []tool
	rememberMu  sync.RWMutex
	rememberSvc RememberService
}

func NewServer(cfg Config) (*Server, error) {
	if cfg.Store == nil || cfg.Engine == nil {
		return nil, domain.ErrInvalidArgument
	}
	if cfg.SearchTimeout <= 0 {
		cfg.SearchTimeout = 15 * time.Second
	}
	if cfg.SecretReader == nil {
		cfg.SecretReader = readSecret
	}
	s := &Server{cfg: cfg, rememberSvc: cfg.RememberService}
	s.tools = s.memoryTools()
	return s, nil
}

func (s *Server) SetRememberService(svc RememberService) {
	s.rememberMu.Lock()
	s.rememberSvc = svc
	s.rememberMu.Unlock()
}

func (s *Server) rememberService() RememberService {
	s.rememberMu.RLock()
	svc := s.rememberSvc
	s.rememberMu.RUnlock()
	return svc
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func rpcFailure(id json.RawMessage, code int, message string) *rpcResponse {
	if id == nil {
		id = json.RawMessage("null")
	}
	return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{code, message}}
}

func object(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '{'
}

func validID(raw json.RawMessage) bool {
	if raw == nil || bytes.Equal(raw, []byte("null")) {
		return true
	}
	first := raw[0]
	return first == '"' || first == '-' || first >= '0' && first <= '9'
}

func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.serveHTTP) }

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/mcp" && r.URL.Path != "/" && r.URL.Path != "" {
		http.NotFound(w, r)
		return
	}
	agent, status := s.authenticate(r)
	if status != http.StatusOK {
		if status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", "Bearer")
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	switch r.Method {
	case http.MethodGet:
		health, err := s.cfg.Engine.Health(r.Context())
		if err != nil {
			http.Error(w, "Engine health unavailable", http.StatusServiceUnavailable)
			return
		}
		reply := capabilities(ProtocolVersion)
		reply["engine"], reply["agent_id"], reply["ok"] = health.Engine, agent.ID, health.OK
		writeJSON(w, reply)
	case http.MethodPost:
		s.serveRPC(w, r, agent)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) serveRPC(w http.ResponseWriter, r *http.Request, agent domain.Agent) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, requestLimit))
	if err != nil {
		var sizeErr *http.MaxBytesError
		if errors.As(err, &sizeErr) {
			http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
			return
		}
	}
	if err != nil || !utf8.Valid(raw) || !json.Valid(raw) {
		writeJSON(w, rpcFailure(nil, -32700, "Parse error"))
		return
	}
	ctx := r.Context()
	if bytes.TrimSpace(raw)[0] == '[' {
		var batch []json.RawMessage
		_ = json.Unmarshal(raw, &batch)
		if len(batch) == 0 {
			writeJSON(w, rpcFailure(nil, -32600, "Invalid Request"))
			return
		}
		results := make([]*rpcResponse, 0, len(batch))
		for _, message := range batch {
			if response := s.handleRPC(ctx, agent, message); response != nil {
				results = append(results, response)
			}
		}
		if len(results) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSON(w, results)
		return
	}
	if response := s.handleRPC(ctx, agent, raw); response != nil {
		writeJSON(w, response)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleRPC(ctx context.Context, agent domain.Agent, raw json.RawMessage) *rpcResponse {
	var req rpcRequest
	if !object(raw) || json.Unmarshal(raw, &req) != nil || req.JSONRPC != "2.0" || req.Method == "" || !validID(req.ID) {
		return rpcFailure(nil, -32600, "Invalid Request")
	}
	result, err := s.dispatch(ctx, agent, req)
	if req.ID == nil {
		return nil
	}
	if err != nil {
		return rpcFailure(req.ID, err.Code, err.Message)
	}
	return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func capabilities(version string) map[string]any {
	return map[string]any{
		"protocolVersion": version,
		"serverInfo":      map[string]string{"name": "memlane", "version": "1.0.0"},
		"capabilities":    map[string]any{"tools": map[string]bool{"listChanged": false}},
	}
}

func (s *Server) dispatch(ctx context.Context, agent domain.Agent, req rpcRequest) (any, *rpcError) {
	invalid := &rpcError{-32602, "Invalid params"}
	if req.Params != nil && !object(req.Params) {
		return nil, invalid
	}
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		version := ProtocolVersion
		if req.Params != nil && json.Unmarshal(req.Params, &params) != nil {
			return nil, invalid
		}
		if params.ProtocolVersion == "2024-11-05" || params.ProtocolVersion == ProtocolVersion {
			version = params.ProtocolVersion
		}
		return capabilities(version), nil
	case "notifications/initialized", "ping":
		return struct{}{}, nil
	case "tools/list":
		list := make([]toolDefinition, 0, len(s.tools))
		for _, t := range s.tools {
			list = append(list, t.toolDefinition)
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if !object(req.Params) || json.Unmarshal(req.Params, &call) != nil || call.Name == "" {
			return nil, invalid
		}
		if call.Arguments == nil {
			call.Arguments = json.RawMessage("{}")
		}
		if !object(call.Arguments) {
			return nil, invalid
		}
		for _, t := range s.tools {
			if t.Name == call.Name {
				result, err := t.call(ctx, agent, call.Arguments)
				if errors.Is(err, domain.ErrInvalidArgument) || errors.Is(err, engine.ErrInvalidArgument) ||
					errors.Is(err, engine.ErrNotUTF8Text) || errors.Is(err, engine.ErrContentTooLarge) {
					return nil, invalid
				}
				if err != nil {
					if msg := toolErrorMessage(err); msg != "" {
						return toolResult(nil, msg), nil
					}
					return nil, &rpcError{-32603, "Internal error"}
				}
				return toolResult(result, ""), nil
			}
		}
		return nil, invalid
	default:
		return nil, &rpcError{-32601, "Method not found"}
	}
}

func toolResult(data any, message string) map[string]any {
	result := make(map[string]any)
	if message == "" {
		raw, _ := json.Marshal(data)
		message = string(raw)
		result["structuredContent"] = data
	} else {
		result["isError"] = true
	}
	result["content"] = []map[string]string{{"type": "text", "text": message}}
	return result
}

func toolErrorMessage(err error) string {
	switch {
	case errors.Is(err, domain.ErrForbidden), errors.Is(err, domain.ErrNotFound), errors.Is(err, engine.ErrForbidden), errors.Is(err, engine.ErrUnauthorized):
		return "Access denied"
	case errors.Is(err, domain.ErrConflict), errors.Is(err, engine.ErrConflict):
		return "Operation conflicts with an existing request"
	case errors.Is(err, context.DeadlineExceeded):
		return "Operation timed out"
	case errors.Is(err, engine.ErrUnavailable):
		return "Engine unavailable"
	case errors.Is(err, engine.ErrNotFound):
		return "Item not found"
	default:
		return ""
	}
}
