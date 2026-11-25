package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/zwhu/memlane/core/domain"
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
func (capableEngine) SyncCatalog(context.Context, *domain.Store) (*SyncStats, error) {
	return &SyncStats{}, nil
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
