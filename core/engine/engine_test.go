package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
)

type basicEngine struct{ MemoryEngine }

type capableEngine struct{ basicEngine }

func (capableEngine) ApplyModel(context.Context, ApplyModelRequest) (ApplyModelResult, error) {
	return ApplyModelResult{}, nil
}
func (capableEngine) ModelStatus(context.Context) (ModelStatusResult, error) {
	return ModelStatusResult{}, nil
}
func (capableEngine) ProbeModel(context.Context, ProbeRequest) (ProbeResult, error) {
	return ProbeResult{}, nil
}
func (capableEngine) SnapshotCatalog(context.Context, []CatalogDatasetTarget, CatalogSnapshotOptions) (*CatalogSnapshot, error) {
	return &CatalogSnapshot{}, nil
}
func (capableEngine) ReadLocalBody(context.Context, LocalBodyRef) (string, error) {
	return "", nil
}

func TestRegistry(t *testing.T) {
	ctx := t.Context()
	cfg := Config{Engine: "test-engine", StorePath: "test-store"}
	want := &basicEngine{}
	factory := func(actualCtx context.Context, actual Config) (MemoryEngine, error) {
		if actualCtx != ctx || actual.Engine != cfg.Engine || actual.StorePath != cfg.StorePath {
			t.Fatalf("factory received unexpected context or config %+v", actual)
		}
		// Calling Registered here also checks that Open releases its registry lock.
		if !slices.Contains(Registered(), cfg.Engine) {
			t.Fatal("factory missing from registry")
		}
		return want, nil
	}
	if _, err := Open(ctx, cfg); !errors.Is(err, ErrUnknownEngine) {
		t.Fatalf("Open unknown engine error = %v", err)
	}
	if err := Register(cfg.Engine, factory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		registry.Lock()
		delete(registry.factories, cfg.Engine)
		registry.Unlock()
	})
	if err := Register(cfg.Engine, factory); !errors.Is(err, ErrConflict) {
		t.Fatalf("Register duplicate error = %v", err)
	}
	if err := Register("different", nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Register nil factory error = %v", err)
	}
	got, err := Open(ctx, cfg)
	if err != nil || got != want {
		t.Fatalf("Open = %v, %v", got, err)
	}
	if names := Registered(); !slices.IsSorted(names) || !slices.Contains(names, cfg.Engine) {
		t.Fatalf("Registered = %v", names)
	}
}

func TestOptionalCapabilities(t *testing.T) {
	if _, ok := AsModelController(basicEngine{}); ok {
		t.Fatal("basic engine appeared to support model control")
	}
	if _, ok := AsCatalogProjector(basicEngine{}); ok {
		t.Fatal("basic engine appeared to support catalog projection")
	}
	value := &capableEngine{}
	if got, ok := AsModelController(value); !ok || got != value {
		t.Fatal("model control capability missing")
	}
	if got, ok := AsCatalogProjector(value); !ok || got != value {
		t.Fatal("catalog projection capability missing")
	}
}

func TestEngineErrorUnwrap(t *testing.T) {
	engineErr := &EngineError{StatusCode: 409, Code: "model_digest_mismatch", Message: "digest changed", Err: ErrModelDigestMismatch}
	wrapped := fmt.Errorf("extract: %w", engineErr)
	var got *EngineError
	if !errors.Is(wrapped, ErrModelDigestMismatch) || !errors.As(wrapped, &got) || got != engineErr {
		t.Fatalf("engine error lost in %v", wrapped)
	}
	if actual := engineErr.Error(); actual != "HTTP 409: model_digest_mismatch: digest changed" {
		t.Fatalf("Error() = %q", actual)
	}
}

func TestSearchContractJSONRoundTrip(t *testing.T) {
	req := SearchRequest{
		Scopes: []Scope{{DatasetID: "ds-1", UserID: "u-1"}},
		Query:  "auth token refresh",
		Mode:   SearchModeHybrid,
		Filters: SearchFilters{
			Kind:                  "document",
			Limit:                 8,
			NodeSets:              []string{"arch", "auth"},
			SessionID:             "sess-42",
			IncludeSessionContext: true,
		},
		Limit: 8,
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal SearchRequest: %v", err)
	}
	var gotReq SearchRequest
	if err := json.Unmarshal(reqBytes, &gotReq); err != nil {
		t.Fatalf("unmarshal SearchRequest: %v", err)
	}
	if !reflect.DeepEqual(gotReq, req) {
		t.Fatalf("SearchRequest round-trip = %#v, want %#v", gotReq, req)
	}

	res := SearchResult{
		OK: true,
		Hits: []SearchHit{{
			DatasetID: "ds-1", DocumentID: "doc-1", Title: "auth.md", Snippet: "rotate tokens", Score: 0.12,
		}},
		GraphContext: &GraphSearchContext{
			Entities: []GraphSearchEntity{{
				DatasetID: "ds-1",
				Name:      "AuthService",
				Type:      "Service",
				Edges: []GraphSearchEdge{{
					Text:          "AuthService validates JWT",
					Source:        "AuthService",
					Target:        "JWT",
					Relationship:  "validates",
					Superseded:    true,
					Authoritative: true,
				}},
				Conflicts: []string{"Token TTL changed from 15m to 30m"},
			}},
			Facts:           []GraphSearchFact{{DatasetID: "ds-1", Text: "Refresh tokens rotate on use"}},
			Conflicts:       []GraphSearchConflict{{DatasetID: "ds-1", Text: "Token TTL changed from 15m to 30m"}},
			SessionGuidance: "## Active session guidance\n### Rules\n- Keep token TTL short",
			CodeFacts: []CodeSearchFact{{
				DatasetID: "ds-1",
				Name:      "AuthService.Verify",
				Kind:      "symbol",
				File:      "auth/service.go",
				Line:      42,
				Relations: []CodeSearchRelation{{Type: "calls", Target: "jwt.Parse"}},
			}},
			DegradedToChunks: true,
		},
	}
	resBytes, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal SearchResult: %v", err)
	}
	var gotRes SearchResult
	if err := json.Unmarshal(resBytes, &gotRes); err != nil {
		t.Fatalf("unmarshal SearchResult: %v", err)
	}
	if !reflect.DeepEqual(gotRes, res) {
		t.Fatalf("SearchResult round-trip = %#v, want %#v", gotRes, res)
	}

	caps := Capabilities{
		ToolGraph:      true,
		LanguageEgress: true,
		ModelHandoff:   true,
		HybridRecall:   true,
		SessionContext: true,
		CodeSearch:     true,
		SearchModes:    []string{string(SearchModeHybrid), string(SearchModeGraph), string(SearchModeChunks), string(SearchModeCode)},
	}
	capsBytes, err := json.Marshal(caps)
	if err != nil {
		t.Fatalf("marshal Capabilities: %v", err)
	}
	var gotCaps Capabilities
	if err := json.Unmarshal(capsBytes, &gotCaps); err != nil {
		t.Fatalf("unmarshal Capabilities: %v", err)
	}
	if !reflect.DeepEqual(gotCaps, caps) {
		t.Fatalf("Capabilities round-trip = %#v, want %#v", gotCaps, caps)
	}
}
