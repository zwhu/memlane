package processing

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

// Actual SQLite authorization and loopback HTTP; completion is an explicit
// protocol fixture, never an actual model/Native acceptance claim.
func cogneeGatewayFixture(t *testing.T, complete cogneeCompletion) (*Owner, *CogneeGateway) {
	t.Helper()
	o, db, revision := ownerFixture(t)
	o.Run = func(_ context.Context, b PiBinding, _ func(int) error, _ func(string)) (agents.PiCompletionExecution, error) {
		return fixtureCompletion(b), nil
	}
	v, err := o.Begin(context.Background(), uuid.NewString(), revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	terminal := waitValidation(t, db, v.ID)
	if terminal.State != "succeeded" {
		t.Fatal("fixture validation failed", terminal.State)
	}
	if _, err = o.Activate(context.Background(), records.ProcessingActivation{OperationID: uuid.NewString(), Revision: revision.ID}); err != nil {
		t.Fatal(err)
	}
	configuration, err := db.ProcessingConfiguration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := records.ProcessingSnapshot{Revision: *configuration.Current.Revision, SelectionGeneration: configuration.Current.Generation, DependencyDigest: terminal.DependencyDigest, FrozenMS: time.Now().UnixMilli()}
	g, err := o.startCogneeGateway(context.Background(), snapshot, complete)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := g.Close(ctx); err != nil {
			t.Error("gateway cleanup did not complete", err)
		}
	})
	return o, g
}

const cogneeGatewayRequest = `{"model":"memlane-processing","messages":[{"role":"system","content":"Extract only supplied facts."},{"role":"user","content":[{"type":"text","text":"synthetic content"}]}],"stream":false,"max_completion_tokens":8192,"response_format":{"type":"json_schema","json_schema":{"name":"Example","strict":true,"schema":{"type":"object","required":["ok"],"additionalProperties":false,"properties":{"ok":{"type":"boolean"}}}}}}`

func cogneeGatewayPost(t *testing.T, g *CogneeGateway, token, body string) (int, []byte) {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, g.Endpoint+"/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(r)
	if err != nil {
		t.Fatal("loopback request failed", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}

func TestCogneeGatewayTaskTokenSchemaAndNoInventedUsage(t *testing.T) {
	var calls atomic.Int32
	_, g := cogneeGatewayFixture(t, func(_ context.Context, binding Binding, system, prompt string, schema json.RawMessage) (json.RawMessage, CogneeGatewayReceipt, error) {
		calls.Add(1)
		if binding.Spec.Model != "deepseek-flash" || !strings.Contains(system, "supplied facts") || !strings.Contains(prompt, "synthetic content") || !json.Valid(schema) {
			t.Error("frozen model, role content or schema changed")
		}
		return json.RawMessage(`{"ok":true}`), CogneeGatewayReceipt{}, nil
	})
	if code, _ := cogneeGatewayPost(t, g, "different-task", cogneeGatewayRequest); code != 401 {
		t.Fatal("foreign task was accepted", code)
	}
	for _, invalid := range []string{
		strings.Replace(cogneeGatewayRequest, `"stream":false`, `"stream":true`, 1),
		strings.Replace(cogneeGatewayRequest, `"user"`, `"tool"`, 1),
		strings.Replace(cogneeGatewayRequest, `"model":"memlane-processing"`, `"model":"other-model"`, 1),
		strings.Replace(cogneeGatewayRequest, `"stream":false`, `"stream":false,"tools":[]`, 1),
		cogneeGatewayRequest + `{}`,
	} {
		if code, _ := cogneeGatewayPost(t, g, g.Token, invalid); code != 400 {
			t.Fatal("unsupported request reached inference", code)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("rejected request called the completion executor")
	}
	code, data := cogneeGatewayPost(t, g, g.Token, cogneeGatewayRequest)
	var response map[string]any
	if code != 200 || json.Unmarshal(data, &response) != nil || response["model"] != "deepseek-flash" || response["usage"] != nil || calls.Load() != 1 || bytes.Contains(data, []byte(g.Token)) {
		t.Fatal("completion did not preserve actual model/schema or invented usage", code)
	}
	receipts := g.Receipts()
	encoded, _ := json.Marshal(receipts)
	if len(receipts) != 1 || !receipts[0].Success || receipts[0].Number != 1 || receipts[0].ProcessingRevision == "" || bytes.Contains(encoded, []byte(g.Token)) || bytes.Contains(encoded, []byte("synthetic content")) {
		t.Fatal("gateway execution receipt missing or leaked capability/content")
	}
}

func TestCogneeGatewayRejectsSchemaFailureAndPostInferenceDependencyDrift(t *testing.T) {
	for _, drift := range []bool{false, true} {
		var changed atomic.Bool
		o, g := cogneeGatewayFixture(t, func(_ context.Context, _ Binding, _, _ string, _ json.RawMessage) (json.RawMessage, CogneeGatewayReceipt, error) {
			if drift {
				changed.Store(true)
				return json.RawMessage(`{"ok":true}`), CogneeGatewayReceipt{}, nil
			}
			return json.RawMessage(`{"ok":"not a boolean"}`), CogneeGatewayReceipt{}, nil
		})
		o.Resolve = func(_ context.Context, _, _, _ string, spec records.ProcessingSpec) (Binding, error) {
			dependency := records.MergeDigest("explicit protocol fixture")
			if changed.Load() {
				dependency = "changed-after-inference"
			}
			return Binding{Spec: spec, Dependency: dependency}, nil
		}
		if code, body := cogneeGatewayPost(t, g, g.Token, cogneeGatewayRequest); code != 502 || bytes.Contains(body, []byte(`"ok"`)) {
			t.Fatal("unconfirmed or changed completion leaked success", code)
		}
		if receipts := g.Receipts(); len(receipts) != 1 || receipts[0].Success {
			t.Fatal("unconfirmed completion was recorded as successful")
		}
	}
}

func TestCogneeGatewayCloseCancelsAndWaitsForExecutor(t *testing.T) {
	started, reaped := make(chan struct{}), make(chan struct{})
	o, g := cogneeGatewayFixture(t, func(ctx context.Context, _ Binding, _, _ string, _ json.RawMessage) (json.RawMessage, CogneeGatewayReceipt, error) {
		close(started)
		<-ctx.Done()
		close(reaped)
		return nil, CogneeGatewayReceipt{}, ctx.Err()
	})
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		r, _ := http.NewRequest(http.MethodPost, g.Endpoint+"/chat/completions", strings.NewReader(cogneeGatewayRequest))
		r.Header.Set("Authorization", "Bearer "+g.Token)
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(r)
		if err == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("completion did not start")
	}
	closed := make(chan struct{})
	go func() { o.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("owner close left a live task gateway")
	}
	select {
	case <-reaped:
	default:
		t.Fatal("owner close returned before executor cleanup")
	}
	<-requestDone
}
