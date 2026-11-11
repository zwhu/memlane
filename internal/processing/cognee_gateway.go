package processing

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

// This capability exists only for one daemon-owned task. Its token and content
// are never logged or written into configuration, task receipts or model files.
type CogneeGateway struct {
	Endpoint, Token string `json:"-"`
	ctx             context.Context
	cancel          context.CancelFunc
	server          *http.Server
	owner           *Owner
	snapshot        records.ProcessingSnapshot
	complete        cogneeCompletion
	mu              sync.Mutex
	closed          bool
	requests        int
	receipts        []CogneeGatewayReceipt
	active          sync.WaitGroup
	pending         chan struct{}
	serial          chan struct{}
	served, done    chan struct{}
	stopOnce        sync.Once
}

type CogneeGatewayProcess struct {
	Diagnostics           *agents.CompletionDiagnostics  `json:"diagnostics,omitempty"`
	Codex                 *agents.CodexCompletionReceipt `json:"codex,omitempty"`
	Started               bool                           `json:"started"`
	Terminal              bool                           `json:"terminal"`
	Reaped                bool                           `json:"reaped"`
	GroupGone             bool                           `json:"group_gone"`
	NativeSchemaRequested bool                           `json:"native_schema_requested"`
	SchemaResponseStatus  int                            `json:"schema_response_status,omitempty"`
	SelectedProvider      string                         `json:"selected_provider,omitempty"`
	SelectedModel         string                         `json:"selected_model,omitempty"`
}

// Only execution facts leave the gateway. No prompt, result, schema, task
// capability, endpoint, credential or provider diagnostic belongs in a receipt.
// Context values are independent observations: a cancelled HTTP request is
// not labelled a Python timeout; the effective gateway deadline is separate.
type CogneeGatewayDiagnostics struct {
	Stage          string `json:"stage"`
	FailureCode    string `json:"failure_code,omitempty"`
	ElapsedMS      int64  `json:"elapsed_ms"`
	RequestContext string `json:"request_context"`
	BudgetContext  string `json:"budget_context"`
	TaskContext    string `json:"task_context"`
}
type CogneeGatewayReceipt struct {
	// ObservationKind distinguishes admitted request rejection from completion.
	// Absent on legacy receipts is unknown, never an inferred model invocation.
	ObservationKind    string                         `json:"observation_kind,omitempty"`
	Diagnostics        *CogneeGatewayDiagnostics      `json:"diagnostics,omitempty"`
	Number             int                            `json:"number"`
	ProcessingRevision string                         `json:"processing_revision"`
	Mode               string                         `json:"mode"`
	Agent              string                         `json:"agent,omitempty"`
	RequestedModel     string                         `json:"requested_model"`
	Success            bool                           `json:"success"`
	ErrorCode          string                         `json:"error_code,omitempty"`
	HTTP               *records.ProcessingHTTPReceipt `json:"http,omitempty"`
	Process            *CogneeGatewayProcess          `json:"process,omitempty"`
}

func (g *CogneeGateway) Receipts() []CogneeGatewayReceipt {
	g.mu.Lock()
	defer g.mu.Unlock()
	result := append([]CogneeGatewayReceipt(nil), g.receipts...)
	for i := range result {
		if result[i].Diagnostics != nil {
			value := *result[i].Diagnostics
			result[i].Diagnostics = &value
		}
		if result[i].HTTP != nil {
			value := *result[i].HTTP
			result[i].HTTP = &value
		}
		if result[i].Process != nil {
			value := *result[i].Process
			value.Diagnostics = agents.SafeCompletionDiagnostics(value.Diagnostics)
			if value.Codex != nil {
				proof := *value.Codex
				value.Codex = &proof
			}
			result[i].Process = &value
		}
	}
	return result
}

type cogneeCompletion func(context.Context, Binding, string, string, json.RawMessage) (json.RawMessage, CogneeGatewayReceipt, error)

func (o *Owner) StartCogneeGateway(ctx context.Context, snapshot records.ProcessingSnapshot) (*CogneeGateway, error) {
	return o.startCogneeGateway(ctx, snapshot, o.completeCognee)
}

func (o *Owner) startCogneeGateway(ctx context.Context, snapshot records.ProcessingSnapshot, complete cogneeCompletion) (*CogneeGateway, error) {
	if o.Lifecycle == nil || o.Lifecycle.Err() != nil || ctx.Err() != nil || o.Acquire == nil || o.Ledger == nil {
		return nil, records.MergeError("service_stopping")
	}
	if _, err := o.ResolveTask(ctx, snapshot); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, records.MergeError("gateway_unavailable")
	}
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		listener.Close()
		return nil, records.MergeError("gateway_unavailable")
	}
	life, cancel := context.WithCancel(ctx)
	g := &CogneeGateway{Endpoint: "http://" + listener.Addr().String() + "/v1", Token: base64.RawURLEncoding.EncodeToString(secret[:]), ctx: life, cancel: cancel, owner: o, snapshot: snapshot, complete: complete, pending: make(chan struct{}, 16), serial: make(chan struct{}, 1), served: make(chan struct{}), done: make(chan struct{})}
	g.server = &http.Server{Handler: http.HandlerFunc(g.handle), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 130 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	o.mu.Lock()
	if o.closed || o.Lifecycle.Err() != nil {
		o.mu.Unlock()
		listener.Close()
		cancel()
		return nil, records.MergeError("service_stopping")
	}
	if o.cogneeGateways == nil {
		o.cogneeGateways = map[*CogneeGateway]struct{}{}
	}
	o.cogneeGateways[g] = struct{}{}
	o.wg.Add(1)
	o.mu.Unlock()
	go func() {
		defer close(g.served)
		_ = g.server.Serve(listener)
		g.stop()
	}()
	context.AfterFunc(life, g.stop)
	stopOwner := context.AfterFunc(o.Lifecycle, g.stop)
	go func() { <-g.done; stopOwner() }()
	return g, nil
}

func (g *CogneeGateway) stop() {
	g.stopOnce.Do(func() {
		g.mu.Lock()
		g.closed = true
		g.cancel()
		g.mu.Unlock()
		_ = g.server.Close()
		go func() {
			g.active.Wait()
			<-g.served
			g.owner.mu.Lock()
			delete(g.owner.cogneeGateways, g)
			g.owner.mu.Unlock()
			close(g.done)
			g.owner.wg.Done()
		}()
	})
}

func (g *CogneeGateway) Close(ctx context.Context) error {
	g.stop()
	select {
	case <-g.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type cogneeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}
type cogneeChatRequest struct {
	Model               string          `json:"model"`
	Messages            []cogneeMessage `json:"messages"`
	Stream              bool            `json:"stream"`
	MaxTokens           *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int            `json:"max_completion_tokens,omitempty"`
	ResponseFormat      struct {
		Type       string `json:"type"`
		JSONSchema struct {
			Name   string          `json:"name"`
			Strict *bool           `json:"strict,omitempty"`
			Schema json.RawMessage `json:"schema"`
		} `json:"json_schema"`
	} `json:"response_format"`
}

func cogneeText(raw json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&parts) != nil || len(parts) == 0 || len(parts) > 64 {
		return "", errors.New("text content required")
	}
	var values []string
	for _, part := range parts {
		if part.Type != "text" {
			return "", errors.New("text content required")
		}
		values = append(values, part.Text)
	}
	return strings.Join(values, "\n"), nil
}

func decodeCogneeChat(r *http.Request, w http.ResponseWriter) (cogneeChatRequest, string, string, *jsonschema.Resolved, error) {
	var q cogneeChatRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&q)
	var extra any
	if err != nil || decoder.Decode(&extra) != io.EOF || q.Model != "memlane-processing" || q.Stream || len(q.Messages) == 0 || len(q.Messages) > 64 || q.ResponseFormat.Type != "json_schema" || len(q.ResponseFormat.JSONSchema.Name) == 0 || len(q.ResponseFormat.JSONSchema.Name) > 128 || len(q.ResponseFormat.JSONSchema.Schema) == 0 || len(q.ResponseFormat.JSONSchema.Schema) > 65536 {
		return q, "", "", nil, errors.New("invalid schema completion request")
	}
	for _, limit := range []*int{q.MaxTokens, q.MaxCompletionTokens} {
		if limit != nil && *limit != 8192 {
			return q, "", "", nil, errors.New("fixed completion budget required")
		}
	}
	var schema jsonschema.Schema
	if json.Unmarshal(q.ResponseFormat.JSONSchema.Schema, &schema) != nil {
		return q, "", "", nil, errors.New("invalid output schema")
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return q, "", "", nil, errors.New("invalid output schema")
	}
	var system []string
	var conversation []map[string]string
	for _, message := range q.Messages {
		text, err := cogneeText(message.Content)
		if err != nil || len(text) > 1<<20 {
			return q, "", "", nil, errors.New("invalid text content")
		}
		switch message.Role {
		case "system", "developer":
			system = append(system, text)
		case "user", "assistant":
			conversation = append(conversation, map[string]string{"role": message.Role, "content": text})
		default:
			return q, "", "", nil, errors.New("tools are unavailable")
		}
	}
	prompt, _ := json.Marshal(conversation)
	systemText := strings.Join(system, "\n\n")
	if systemText == "" {
		systemText = "Return only JSON matching the supplied schema. No tools are available."
	}
	if len(conversation) == 0 || len(prompt)+len(systemText) > 1<<20 {
		return q, "", "", nil, errors.New("bounded conversation required")
	}
	return q, systemText, string(prompt), resolved, nil
}

func cogneeGatewayError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"type": "memlane_processing_error", "code": code, "message": code}})
}

// Only fixed local error codes may cross the owner diagnostic boundary.
func cogneeGatewayGuardCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var access *records.AccessError
	if !errors.As(err, &access) {
		return "unknown"
	}
	switch access.Code {
	case "service_stopping", "verification_required", "dependency_changed", "dependency_unavailable", "invalid_dependency", "connection_verification_required", "connection_changed", "unsupported_processing", "unsupported_agent_version", "unsupported_schema_binding",
		"agent_probe_version_output_limit", "agent_probe_version_cancelled", "agent_probe_version_timeout", "agent_probe_version_exited", "agent_probe_version_unavailable", "agent_probe_version_permission", "agent_probe_version_failed",
		"agent_probe_capability_output_limit", "agent_probe_capability_cancelled", "agent_probe_capability_timeout", "agent_probe_capability_exited", "agent_probe_capability_unavailable", "agent_probe_capability_permission", "agent_probe_capability_failed":
		return access.Code
	default:
		return "unknown"
	}
}

func (g *CogneeGateway) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" || r.URL.RawQuery != "" {
		cogneeGatewayError(w, 404, "unsupported_endpoint")
		return
	}
	if r.Method != http.MethodPost {
		cogneeGatewayError(w, 405, "unsupported_method")
		return
	}
	if r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+g.Token)) != 1 {
		cogneeGatewayError(w, 401, "task_authorization_required")
		return
	}
	q, system, prompt, schema, err := decodeCogneeChat(r, w)
	if err != nil {
		cogneeGatewayError(w, 400, "invalid_completion_request")
		return
	}
	g.mu.Lock()
	if g.closed || g.requests >= 128 {
		g.mu.Unlock()
		cogneeGatewayError(w, 409, "task_unavailable")
		return
	}
	select {
	case g.pending <- struct{}{}:
	default:
		g.mu.Unlock()
		cogneeGatewayError(w, 429, "task_busy")
		return
	}
	g.requests++
	g.active.Add(1)
	g.mu.Unlock()
	defer g.active.Done()
	defer func() { <-g.pending }()
	requestStarted := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	stop := context.AfterFunc(g.ctx, cancel)
	defer stop()
	// This observer is installed only after exact path/method/capability, bounded
	// schema decoding and the existing request/queue admission budgets pass.
	// No Process/HTTP object exists: this does not claim any model result
	// or process lifecycle fact. The owner seals it with ordinary failed receipts.
	reject := func(stage, responseCode string, cause error) {
		spec := g.snapshot.Revision.Spec
		receipt := CogneeGatewayReceipt{ObservationKind: "request_rejected", ProcessingRevision: g.snapshot.Revision.ID, Mode: spec.Mode, Agent: spec.Agent, RequestedModel: spec.Model, ErrorCode: responseCode,
			Diagnostics: &CogneeGatewayDiagnostics{Stage: stage, FailureCode: cogneeGatewayGuardCode(cause), ElapsedMS: time.Since(requestStarted).Milliseconds(), RequestContext: agents.CompletionContextState(r.Context().Err()), BudgetContext: agents.CompletionContextState(ctx.Err()), TaskContext: agents.CompletionContextState(g.ctx.Err())}}
		g.mu.Lock()
		receipt.Number = len(g.receipts) + 1
		g.receipts = append(g.receipts, receipt)
		g.mu.Unlock()
		cogneeGatewayError(w, 409, responseCode)
	}

	select {
	case g.serial <- struct{}{}:
		defer func() { <-g.serial }()
	case <-ctx.Done():
		reject("gateway_queue", "task_cancelled", ctx.Err())
		return
	}
	release, err := g.owner.Acquire(ctx)
	if err != nil {
		reject("owner_acquire", "processing_unavailable", err)
		return
	}
	defer release()
	binding, err := g.owner.ResolveTask(ctx, g.snapshot)
	if err != nil {
		reject("dependency_resolve", "processing_changed", err)
		return
	}
	output, receipt, err := g.complete(ctx, binding, system, prompt, q.ResponseFormat.JSONSchema.Schema)
	postflight := false
	if err == nil {
		postflight = true
		_, err = g.owner.ResolveTask(ctx, g.snapshot)
	}
	var value any
	valid := err == nil && ctx.Err() == nil && len(output) > 0 && len(output) <= 2<<20 && json.Unmarshal(output, &value) == nil && schema.Validate(value) == nil
	receipt.ProcessingRevision, receipt.Mode, receipt.RequestedModel = g.snapshot.Revision.ID, binding.Spec.Mode, binding.Spec.Model
	receipt.Agent = binding.Spec.Agent
	receipt.ObservationKind = "completion"
	receipt.Success = valid
	phase, code := "completed", ""
	if !valid {
		switch {
		case err != nil && postflight:
			phase, code = "postflight", "dependency_changed"
		case err != nil:
			phase, code = "completion", "completion_failed"
		case ctx.Err() != nil:
			phase, code = "gateway_context", "context_ended"
		case len(output) == 0:
			phase, code = "gateway_result", "empty_output"
		case len(output) > 2<<20:
			phase, code = "gateway_result", "output_limit"
		case !json.Valid(output):
			phase, code = "gateway_json", "invalid_json"
		default:
			phase, code = "gateway_schema", "schema_invalid"
		}
	}
	receipt.Diagnostics = &CogneeGatewayDiagnostics{Stage: phase, FailureCode: code, ElapsedMS: time.Since(requestStarted).Milliseconds(), RequestContext: agents.CompletionContextState(r.Context().Err()), BudgetContext: agents.CompletionContextState(ctx.Err()), TaskContext: agents.CompletionContextState(g.ctx.Err())}
	if !valid {
		receipt.ErrorCode = "completion_unconfirmed"
	}
	g.mu.Lock()
	receipt.Number = len(g.receipts) + 1
	g.receipts = append(g.receipts, receipt)
	g.mu.Unlock()
	if !valid {
		cogneeGatewayError(w, 502, "completion_unconfirmed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": "chatcmpl-" + uuid.NewString(), "object": "chat.completion", "created": time.Now().Unix(), "model": binding.Spec.Model, "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": string(output)}}}})
}

func (o *Owner) completeCognee(ctx context.Context, binding Binding, system, prompt string, schema json.RawMessage) (json.RawMessage, CogneeGatewayReceipt, error) {
	var receipt CogneeGatewayReceipt
	if binding.Spec.Mode == "provider" || binding.Spec.Mode == "magpie" {
		result, err := o.completeHTTP(ctx, binding, system, prompt, schema, nil)
		receipt.HTTP = &result.Receipt
		if err != nil || !result.Terminal || !result.Receipt.RequestSent || !result.Receipt.ResponseComplete || !result.Receipt.NativeSchemaRequested || !result.Receipt.SchemaValid {
			return nil, receipt, records.MergeError("completion_unconfirmed")
		}
		return result.Output, receipt, nil
	}
	if binding.Spec.Mode != "agent" || (binding.Spec.Agent != "pi" && binding.Spec.Agent != "codex") {
		return nil, receipt, records.MergeError("unsupported_processing")
	}
	var result agents.PiCompletionExecution
	var err error
	if binding.Spec.Agent == "codex" {
		result, err = CompleteCodex(ctx, binding, system, prompt, schema, nil, nil)
	} else {
		result, err = agents.RunPiCompletion(ctx, agents.PiCompletionOptions{Executable: binding.Executable, AuthHome: binding.AuthHome, Provider: binding.Spec.Provider, Model: binding.Spec.Model, SystemPrompt: system, Prompt: prompt, OutputSchema: schema, GuardExecutable: binding.Helper})
	}
	receipt.Process = &CogneeGatewayProcess{Diagnostics: agents.SafeCompletionDiagnostics(result.Diagnostics), Codex: result.Codex, Started: result.Started, Terminal: result.Terminal, Reaped: result.Reaped, GroupGone: result.GroupGone, NativeSchemaRequested: result.NativeSchemaRequested, SchemaResponseStatus: result.NativeSchemaResponseStatus, SelectedProvider: result.SelectedProvider, SelectedModel: result.SelectedModel}
	if result.Started && !result.GroupGone && o.Poison != nil {
		o.Poison()
	}
	confirmed := result.Terminal && result.Reaped && result.GroupGone && result.NativeSchemaRequested && result.NativeSchemaResponseStatus == 200 && result.SelectedProvider == binding.Spec.Provider && result.SelectedModel == binding.Spec.Model
	if binding.Spec.Agent == "codex" {
		confirmed = CodexCompletionConfirmed(result, binding)
	}
	if err != nil || !confirmed {
		return nil, receipt, records.MergeError("completion_unconfirmed")
	}
	return result.Final, receipt, nil
}
