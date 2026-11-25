package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Engine      string
	Endpoint    string
	TokenFile   string
	StorePath   string
	PythonBin   string
	WorkDir     string
	SourceDir   string
	HTTPTimeout time.Duration
	Options     map[string]any
}

type Factory func(ctx context.Context, cfg Config) (MemoryEngine, error)

var registry = struct {
	sync.RWMutex
	factories map[string]Factory
}{factories: make(map[string]Factory)}

func Register(name string, factory Factory) error {
	if name == "" || name != strings.TrimSpace(name) || factory == nil {
		return fmt.Errorf("register engine %q: %w", name, ErrInvalidArgument)
	}
	registry.Lock()
	defer registry.Unlock()
	if _, exists := registry.factories[name]; exists {
		return fmt.Errorf("engine %q already registered: %w", name, ErrConflict)
	}
	registry.factories[name] = factory
	return nil
}

func MustRegister(name string, factory Factory) {
	if err := Register(name, factory); err != nil {
		panic(err)
	}
}

func Open(ctx context.Context, cfg Config) (MemoryEngine, error) {
	registry.RLock()
	factory, ok := registry.factories[cfg.Engine]
	registry.RUnlock()
	if !ok {
		return nil, fmt.Errorf("open engine %q: %w", cfg.Engine, ErrUnknownEngine)
	}
	return factory(ctx, cfg)
}

func Registered() []string {
	registry.RLock()
	names := make([]string, 0, len(registry.factories))
	for name := range registry.factories {
		names = append(names, name)
	}
	registry.RUnlock()
	slices.Sort(names)
	return names
}
