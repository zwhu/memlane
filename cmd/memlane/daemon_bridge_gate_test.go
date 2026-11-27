package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/gateway/completion"
	"github.com/zwhu/memlane/kit/agents"
	"github.com/zwhu/memlane/kit/agents/bridge"
)

func TestActiveProfileGateResolvesBridgeExecutableAndAuthHome(t *testing.T) {
	root := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	require(t, err)

	store, err := domain.OpenStore(filepath.Join(resolvedRoot, "memlane.sqlite3"))
	require(t, err)
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	}()

	homeDir := filepath.Join(resolvedRoot, "home")
	traeDir := filepath.Join(homeDir, ".trae")
	require(t, os.MkdirAll(filepath.Join(traeDir, "cli"), 0o700))
	require(t, os.WriteFile(filepath.Join(traeDir, "cli", "auth.json"), []byte(`{"token":"fake"}`), 0o600))
	require(t, os.WriteFile(filepath.Join(traeDir, "traecli.toml"), []byte("model = \"GPT-6.1-Sol\"\nmodel_provider = \"trae\"\n"), 0o600))

	exePath := filepath.Join(resolvedRoot, "bin", "traecli")
	require(t, os.MkdirAll(filepath.Dir(exePath), 0o700))
	require(t, os.WriteFile(exePath, []byte("#!/bin/sh\nexit 0\n"), 0o700))

	ctx := context.Background()
	saved, err := store.SaveGenerationProfile(ctx, domain.GenerationProfile{
		ProfileID:  "agent-traex",
		Revision:   1,
		Mode:       "agent_bridge",
		BridgeKind: agents.HostTraeX,
		Model:      "GPT-6.1-Sol",
	}, "")
	require(t, err)
	require(t, store.SetActiveGeneration(ctx, saved.ProfileID, saved.Revision))

	gate := activeProfileGate{
		store: store,
		discovery: agents.DiscoveryOptions{
			Home:    homeDir,
			AppDirs: []string{},
			Environ: func(string) string { return "" },
			LookPath: func(name string) (string, error) {
				if name == "traecli" {
					return exePath, nil
				}
				return "", os.ErrNotExist
			},
		},
	}

	active, err := gate.ResolveActive(ctx)
	require(t, err)
	if active == nil {
		t.Fatal("expected active bridge profile")
	}
	if active.Executable != exePath {
		t.Fatalf("expected Executable=%q, got %q", exePath, active.Executable)
	}
	if active.AuthHome != traeDir {
		t.Fatalf("expected AuthHome=%q, got %q", traeDir, active.AuthHome)
	}

	rt, err := agents.CheckBridgeRuntime(active.BridgeKind, active.Executable, active.AuthHome, agents.ModelDefaultsOptions{})
	require(t, err)
	if rt.Executable != exePath || rt.Home != traeDir {
		t.Fatalf("unexpected verified runtime: %+v", rt)
	}
	if !active.FollowAgentDefaults || active.RouteModel != "GPT-6.1-Sol" {
		t.Fatalf("expected FollowAgentDefaults=true and RouteModel=GPT-6.1-Sol, got %+v", active)
	}

	// Updating the host CLI default model on disk must be followed live by the
	// completion server without failing with 409 bridge_profile_mismatch, while
	// still accepting requests routed with the saved RouteModel placeholder.
	require(t, os.WriteFile(filepath.Join(traeDir, "traecli.toml"), []byte("model = \"GPT-6.2-Astra\"\nmodel_provider = \"trae\"\n"), 0o600))
	var executedModel string
	srv, err := completion.NewServer(completion.Config{
		Token: "test-completion-token-0123456789abcdef",
		Gate:  gate,
		Executor: func(_ context.Context, opts bridge.ExecOptions, _ *bridge.ParsedRequest) (string, error) {
			executedModel = opts.Model
			return "ok", nil
		},
	})
	require(t, err)

	hw := httptest.NewRecorder()
	srv.Handler().ServeHTTP(hw, httptest.NewRequest(http.MethodGet, "/health", nil))
	if hw.Code != http.StatusOK {
		t.Fatalf("GET /health after host model switch = %d %s, want 200", hw.Code, hw.Body.String())
	}
	var healthBody map[string]any
	require(t, json.Unmarshal(hw.Body.Bytes(), &healthBody))
	if healthBody["model"] != "GPT-6.2-Astra" {
		t.Fatalf("health model = %v, want GPT-6.2-Astra", healthBody["model"])
	}

	// Request carrying Cognee's saved routing model name succeeds and executes with live model.
	cw := httptest.NewRecorder()
	creq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"GPT-6.1-Sol","messages":[{"role":"user","content":"hi"}]}`))
	creq.Header.Set("Authorization", "Bearer test-completion-token-0123456789abcdef")
	srv.Handler().ServeHTTP(cw, creq)
	if cw.Code != http.StatusOK || executedModel != "GPT-6.2-Astra" {
		t.Fatalf("routed completion = %d %s (executedModel=%q), want 200 / GPT-6.2-Astra", cw.Code, cw.Body.String(), executedModel)
	}

	// Request carrying an unrelated model name is still rejected.
	bw := httptest.NewRecorder()
	breq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"unrelated-model","messages":[{"role":"user","content":"hi"}]}`))
	breq.Header.Set("Authorization", "Bearer test-completion-token-0123456789abcdef")
	srv.Handler().ServeHTTP(bw, breq)
	if bw.Code != http.StatusBadRequest {
		t.Fatalf("unrelated model completion = %d %s, want 400", bw.Code, bw.Body.String())
	}
}
