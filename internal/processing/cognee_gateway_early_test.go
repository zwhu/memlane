package processing

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zwhu/memlane/internal/records"
)

func TestCogneeGatewayEarlyGuardRetainsRequestObservation(t *testing.T) {
	for _, tc := range []struct{ stage, code string }{{"owner_acquire", "service_stopping"}, {"dependency_resolve", "agent_probe_version_timeout"}} {
		t.Run(tc.stage, func(t *testing.T) {
			var called atomic.Int32
			o, g := cogneeGatewayFixture(t, func(context.Context, Binding, string, string, json.RawMessage) (json.RawMessage, CogneeGatewayReceipt, error) {
				called.Add(1)
				return nil, CogneeGatewayReceipt{}, nil
			})
			guardError := &records.AccessError{Code: tc.code, Message: "private underlying error with raw content"}
			if tc.stage == "owner_acquire" {
				o.Acquire = func(context.Context) (func(), error) { return nil, guardError }
			} else {
				o.Resolve = func(context.Context, string, string, string, records.ProcessingSpec) (Binding, error) {
					return Binding{}, guardError
				}
			}
			status, _ := cogneeGatewayPost(t, g, g.Token, cogneeGatewayRequest)
			if status != 409 || called.Load() != 0 {
				t.Fatal("not the actual pre-completion seam", status, called.Load())
			}
			receipts := g.Receipts()
			if len(receipts) != 1 {
				t.Fatalf("valid task-cap request lost early guard observation: got %d", len(receipts))
			}
			if receipts[0].Success || receipts[0].Process != nil || receipts[0].HTTP != nil {
				t.Fatal("early guard invented completion/process evidence")
			}
			raw, _ := json.Marshal(receipts)
			var rows []map[string]any
			if err := json.Unmarshal(raw, &rows); err != nil {
				t.Fatal(err)
			}
			if rows[0]["observation_kind"] != "request_rejected" {
				t.Fatal("request observation can be mistaken for model execution", string(raw))
			}
			d := rows[0]["diagnostics"].(map[string]any)
			if d["stage"] != tc.stage || d["failure_code"] != tc.code {
				t.Fatal("actual fixed guard cause lost", string(raw))
			}
			for _, private := range []string{"private underlying", g.Token, g.Endpoint, "synthetic content", "Extract only", "properties"} {
				if strings.Contains(string(raw), private) {
					t.Fatal("unsafe early observation")
				}
			}
		})
	}
}

func TestCogneeGatewayEarlyObservationRejectsNoiseAndBoundsAdmission(t *testing.T) {
	o, g := cogneeGatewayFixture(t, func(context.Context, Binding, string, string, json.RawMessage) (json.RawMessage, CogneeGatewayReceipt, error) {
		t.Error("rejected request reached completion")
		return nil, CogneeGatewayReceipt{}, nil
	})
	const private = "private-error-endpoint-auth-schema-payload"
	o.Acquire = func(context.Context) (func(), error) {
		return nil, &records.AccessError{Code: private, Message: private}
	}
	for _, q := range []struct{ token, body string }{{"wrong-cap", cogneeGatewayRequest}, {g.Token, `{"model":"other-model"}`}, {g.Token, `{`}} {
		cogneeGatewayPost(t, g, q.token, q.body)
	}
	if len(g.Receipts()) != 0 {
		t.Fatal("unauthorized or malformed noise entered task audit")
	}
	for i := 0; i < 129; i++ {
		status, _ := cogneeGatewayPost(t, g, g.Token, cogneeGatewayRequest)
		if status != 409 {
			t.Fatal("guard/budget changed", i, status)
		}
	}
	rows := g.Receipts()
	if len(rows) != 128 {
		t.Fatal("request admission/receipt budget changed", len(rows))
	}
	for i, row := range rows {
		if row.Number != i+1 || row.Process != nil || row.HTTP != nil || row.Success || row.Diagnostics.FailureCode != "unknown" {
			t.Fatal("invented lifecycle or unsafe code", i, row)
		}
	}
	raw, _ := json.Marshal(rows)
	if len(raw) > 256*1024 || strings.Contains(string(raw), private) || strings.Contains(string(raw), g.Token) || strings.Contains(string(raw), g.Endpoint) {
		t.Fatal("unbounded or unsafe sealed observations")
	}
	var legacy CogneeGatewayReceipt
	if err := json.Unmarshal([]byte(`{"number":1,"success":false}`), &legacy); err != nil || legacy.ObservationKind != "" || legacy.Process != nil || legacy.Diagnostics != nil {
		t.Fatal("old receipt no longer unknown", err)
	}
}

func TestCogneeGatewayCompletionObservationRetainsExistingProof(t *testing.T) {
	_, g := cogneeGatewayFixture(t, func(context.Context, Binding, string, string, json.RawMessage) (json.RawMessage, CogneeGatewayReceipt, error) {
		return json.RawMessage(`{"ok":true}`), CogneeGatewayReceipt{}, nil
	})
	status, _ := cogneeGatewayPost(t, g, g.Token, cogneeGatewayRequest)
	rows := g.Receipts()
	if status != 200 || len(rows) != 1 || rows[0].ObservationKind != "completion" || !rows[0].Success {
		t.Fatal("completion receipt changed", status, rows)
	}
}

func TestCogneeGatewayAcquireDeadlineIsRequestEvidenceOnly(t *testing.T) {
	o, g := cogneeGatewayFixture(t, func(context.Context, Binding, string, string, json.RawMessage) (json.RawMessage, CogneeGatewayReceipt, error) {
		t.Error("deadline reached completion")
		return nil, CogneeGatewayReceipt{}, nil
	})
	o.Acquire = func(ctx context.Context) (func(), error) { <-ctx.Done(); return nil, ctx.Err() }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(cogneeGatewayRequest)).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer "+g.Token)
	response := httptest.NewRecorder()
	g.handle(response, request)
	rows := g.Receipts()
	if response.Code != 409 || len(rows) != 1 || rows[0].Process != nil || rows[0].Success {
		t.Fatal("deadline invented model facts", response.Code, rows)
	}
	d := rows[0].Diagnostics
	if d.Stage != "owner_acquire" || d.FailureCode != "deadline_exceeded" || d.RequestContext != "deadline_exceeded" || d.BudgetContext != "deadline_exceeded" || d.TaskContext != "active" {
		t.Fatal("deadline layer lost", d)
	}
}
