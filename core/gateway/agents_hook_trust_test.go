package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/gateway"
	"github.com/zwhu/memlane/kit/agents"
)

func writeFakeHookTrustCLI(t *testing.T, path, respFile, counterFile string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	counterLine := ""
	if counterFile != "" {
		counterLine = fmt.Sprintf("echo 1 >> %q\n", counterFile)
	}
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" != "app-server" ]; then exit 0; fi
%swhile IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*) printf '%%s\n' '{"id":1,"result":{}}' ;;
    *'"method":"hooks/list"'*) IFS= read -r resp < %q; printf '%%s\n' "$resp" ;;
  esac
done
`, counterLine, respFile)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeHooksListJSON(t *testing.T, path, hooksPath, hostKind string, specs []struct {
	event       string
	trustStatus string
	enabled     bool
}) {
	t.Helper()
	items := make([]map[string]any, 0, len(specs))
	for i, sp := range specs {
		items = append(items, map[string]any{
			"key":         fmt.Sprintf("%s:%s:%d:0", hooksPath, strings.ToLower(sp.event), i),
			"eventName":   sp.event,
			"command":     fmt.Sprintf("'/bin/memlane' hook %s %s --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'", hostKind, sp.event),
			"sourcePath":  hooksPath,
			"enabled":     sp.enabled,
			"trustStatus": sp.trustStatus,
		})
	}
	raw, err := json.Marshal(map[string]any{
		"id":     2,
		"result": map[string]any{"data": []any{map[string]any{"hooks": items}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionsNeedsHookTrustOverridesHistoricalReceiptAndCaches(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	binDir := filepath.Join(home, "bin")

	codexProfile := filepath.Join(home, ".codex")
	codexHooks := filepath.Join(codexProfile, "hooks.json")
	codexTOML := filepath.Join(codexProfile, "config.toml")
	if err := os.MkdirAll(codexProfile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexHooks, []byte(`{"hooks":{"Stop":[{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook codex Stop --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'","_memlane_managed":"memlane-hook"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexTOML, []byte("model = \"gpt-5.4\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	traexProfile := filepath.Join(home, ".trae")
	traexHooks := filepath.Join(traexProfile, "cli", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(traexHooks), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(traexHooks, []byte(`{"hooks":{"Stop":[{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook traex Stop --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'","_memlane_managed":"memlane-hook"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	codexRespFile := filepath.Join(home, "codex_hooks_list.json")
	codexCountFile := filepath.Join(home, "codex_calls.log")
	writeHooksListJSON(t, codexRespFile, codexHooks, agents.HostCodex, []struct {
		event, trustStatus string
		enabled            bool
	}{
		{"SessionStart", "untrusted", true},
		{"UserPromptSubmit", "untrusted", true},
		{"PostToolUse", "untrusted", true},
		{"PostToolUseFailure", "modified", true},
		{"Stop", "modified", true},
		{"PreCompact", "modified", true},
	})
	codexBin := writeFakeHookTrustCLI(t, filepath.Join(binDir, "codex"), codexRespFile, codexCountFile)

	traexRespFile := filepath.Join(home, "traex_hooks_list.json")
	writeHooksListJSON(t, traexRespFile, traexHooks, agents.HostTraeX, []struct {
		event, trustStatus string
		enabled            bool
	}{
		{"SessionStart", "untrusted", true},
		{"UserPromptSubmit", "untrusted", true},
		{"PostToolUse", "untrusted", true},
		{"PostToolUseFailure", "untrusted", true},
		{"Stop", "untrusted", true},
		{"PreCompact", "untrusted", true},
		{"SessionEnd", "untrusted", true},
	})
	traexBin := writeFakeHookTrustCLI(t, filepath.Join(binDir, "traecli"), traexRespFile, "")

	eng := newContractMockEngine()
	store, err := domain.OpenStore(filepath.Join(t.TempDir(), "memlane.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertAgent(ctx, domain.Agent{ID: "agent-codex", Kind: agents.HostCodex, Name: "Codex", PrincipalID: "u-codex", DefaultAccess: domain.AccessMaintain}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAgent(ctx, domain.Agent{ID: "agent-traex", Kind: agents.HostTraeX, Name: "TRAE CLI", PrincipalID: "u-traex", DefaultAccess: domain.AccessMaintain}); err != nil {
		t.Fatal(err)
	}
	// Seed historical saved receipts for BOTH hosts to prove needs_hook_trust never masks as healthy.
	for _, item := range []struct{ key, user, sess string }{
		{"hist-codex", "u-codex", "sess-codex-old"},
		{"hist-traex", "u-traex", "sess-traex-old"},
	} {
		if _, _, err := store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
			CaptureKey: item.key, UserID: item.user, DatasetID: "ds-1", SessionID: item.sess,
			MessageID: "m-1", PayloadSHA256: "sha-1", EntryType: "capture_qa",
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkCaptureReceiptSaved(ctx, item.key, "entry-"+item.key); err != nil {
			t.Fatal(err)
		}
	}

	srv, err := gateway.NewServer(gateway.Config{
		Store:           store,
		Engine:          eng,
		Projector:       eng,
		ManagementToken: contractMgmtToken,
		DiscoveryOptions: agents.DiscoveryOptions{
			Home:  home,
			Kinds: []string{agents.HostCodex, agents.HostTraeX},
			LookPath: func(name string) (string, error) {
				p := filepath.Join(binDir, name)
				if _, err := os.Stat(p); err == nil {
					return p, nil
				}
				return "", errors.New("not found")
			},
			Environ: func(string) string { return "" },
			RuntimeOverrides: map[string]agents.HostOverride{
				agents.HostCodex: {Executable: codexBin, ProfileDir: codexProfile},
				agents.HostTraeX: {Executable: traexBin, ProfileDir: traexProfile},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	fetch := func(path string) map[string]map[string]any {
		t.Helper()
		code, resp := doHTTPJSON(t, srv, http.MethodGet, path, "", mgmtHeaders())
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d: %+v", path, code, resp)
		}
		byID := map[string]map[string]any{}
		for _, raw := range resp["connections"].([]any) {
			row := raw.(map[string]any)
			byID[row["instance_id"].(string)] = row
		}
		return byID
	}
	countCodexCalls := func() int {
		raw, err := os.ReadFile(codexCountFile)
		if err != nil {
			return 0
		}
		return len(strings.Fields(string(raw)))
	}

	// 1. First request probes app-server and overrides historical healthy receipt with needs_hook_trust.
	byID := fetch("/v1/connections")
	codexRow := byID["agent-codex"]
	codexCap, _ := codexRow["capture"].(map[string]any)
	if codexRow["needs_hook_trust"] != true ||
		codexRow["untrusted_hook_count"] != float64(3) ||
		codexRow["modified_hook_count"] != float64(3) ||
		codexRow["environment_verified"] != nil ||
		codexCap == nil || codexCap["state"] != "needs_hook_trust" {
		t.Fatalf("codex row should be needs_hook_trust (3 untrusted + 3 modified), got %+v", codexRow)
	}
	traexRow := byID["agent-traex"]
	traexCap, _ := traexRow["capture"].(map[string]any)
	if traexRow["needs_hook_trust"] != true ||
		traexRow["untrusted_hook_count"] != float64(7) ||
		traexRow["environment_verified"] != nil ||
		traexCap == nil || traexCap["state"] != "needs_hook_trust" {
		t.Fatalf("traex row should be needs_hook_trust (7 untrusted), got %+v", traexRow)
	}
	if calls := countCodexCalls(); calls != 1 {
		t.Fatalf("codex probe calls = %d, want 1", calls)
	}

	// 2. Second non-forced request hits in-memory cache (no new subprocess launch).
	_ = fetch("/v1/connections")
	if calls := countCodexCalls(); calls != 1 {
		t.Fatalf("cached GET /v1/connections spawned extra probe: calls = %d, want 1", calls)
	}

	// 3. force=1 bypasses cache and re-probes.
	_ = fetch("/v1/connections?force=1")
	if calls := countCodexCalls(); calls != 2 {
		t.Fatalf("GET /v1/connections?force=1 did not refresh probe: calls = %d, want 2", calls)
	}

	// 4. Updating config.toml (simulating user running /hooks -> trust all) invalidates cache automatically without force=1.
	writeHooksListJSON(t, codexRespFile, codexHooks, agents.HostCodex, []struct {
		event, trustStatus string
		enabled            bool
	}{
		{"SessionStart", "trusted", true},
		{"UserPromptSubmit", "trusted", true},
		{"PostToolUse", "trusted", true},
		{"PostToolUseFailure", "trusted", true},
		{"Stop", "trusted", true},
		{"PreCompact", "trusted", true},
	})
	if err := os.WriteFile(codexTOML, []byte("model = \"gpt-5.4\"\n# trusted in CLI\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	byID = fetch("/v1/connections")
	if calls := countCodexCalls(); calls != 3 {
		t.Fatalf("config.toml change did not invalidate hook trust cache: calls = %d, want 3", calls)
	}
	codexRow = byID["agent-codex"]
	codexCap, _ = codexRow["capture"].(map[string]any)
	if codexRow["needs_hook_trust"] != nil || codexRow["environment_verified"] != true || codexCap == nil || codexCap["state"] != "healthy" {
		t.Fatalf("codex row did not recover to healthy after trusting all hooks: %+v", codexRow)
	}
}

func TestConnectionsStaleReceiptDowngradesAfterHookRewrite(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	appsDir := filepath.Join(home, "Applications")

	traeApp := filepath.Join(appsDir, "Trae CN.app")
	writeSyntheticExecutable(t, filepath.Join(traeApp, "Contents", "MacOS", "Electron"))
	if err := os.WriteFile(filepath.Join(traeApp, "Contents", "Info.plist"), []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>CFBundleShortVersionString</key><string>1.9.8</string></dict></plist>`), 0o600); err != nil {
		t.Fatal(err)
	}

	traeProfile := filepath.Join(home, ".trae-cn")
	traeHooks := filepath.Join(traeProfile, "hooks.json")
	if err := os.MkdirAll(traeProfile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(traeHooks, []byte(`{"version":1,"hooks":{"Stop":[{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook trae-ide Stop --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'","_memlane_managed":"memlane-hook"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	codexProfile := filepath.Join(home, ".codex")
	codexHooks := filepath.Join(codexProfile, "hooks.json")
	if err := os.MkdirAll(codexProfile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexHooks, []byte(`{"hooks":{"Stop":[{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook codex Stop --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'","_memlane_managed":"memlane-hook"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	codexBin := writeSyntheticExecutable(t, filepath.Join(binDir, "codex"))

	eng := newContractMockEngine()
	store, err := domain.OpenStore(filepath.Join(t.TempDir(), "memlane.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertAgent(ctx, domain.Agent{ID: "agent-trae", Kind: agents.HostTraeIDE, Name: "TRAE CN", PrincipalID: "u-trae", DefaultAccess: domain.AccessMaintain}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAgent(ctx, domain.Agent{ID: "agent-codex", Kind: agents.HostCodex, Name: "Codex", PrincipalID: "u-codex", DefaultAccess: domain.AccessMaintain}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ key, user, sess string }{
		{"stale-trae", "u-trae", "sess-trae-old"},
		{"stale-codex", "u-codex", "sess-codex-old"},
	} {
		if _, _, err := store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
			CaptureKey: item.key, UserID: item.user, DatasetID: "ds-1", SessionID: item.sess,
			MessageID: "m-1", PayloadSHA256: "sha-1", EntryType: "capture_qa",
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkCaptureReceiptSaved(ctx, item.key, "entry-"+item.key); err != nil {
			t.Fatal(err)
		}
	}
	// Backdate receipts by 10 minutes so hooks.json mtime is clearly newer than last receipt.
	oldTimestamp := domain.FormatUTCNS(time.Now().Add(-10 * time.Minute))
	if _, err := store.DB().ExecContext(ctx, `UPDATE capture_receipts SET updated_at = ?`, oldTimestamp); err != nil {
		t.Fatal(err)
	}

	srv, err := gateway.NewServer(gateway.Config{
		Store:           store,
		Engine:          eng,
		Projector:       eng,
		ManagementToken: contractMgmtToken,
		DiscoveryOptions: agents.DiscoveryOptions{
			Home:    home,
			AppDirs: []string{appsDir},
			Kinds:   []string{agents.HostTraeIDE, agents.HostCodex},
			LookPath: func(name string) (string, error) {
				if name == "codex" {
					return codexBin, nil
				}
				return "", errors.New("not found")
			},
			Environ: func(string) string { return "" },
			RuntimeOverrides: map[string]agents.HostOverride{
				agents.HostCodex: {Executable: codexBin, ProfileDir: codexProfile},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	code, resp := doHTTPJSON(t, srv, http.MethodGet, "/v1/connections", "", mgmtHeaders())
	if code != http.StatusOK {
		t.Fatalf("GET /v1/connections = %d: %+v", code, resp)
	}
	byID := map[string]map[string]any{}
	for _, raw := range resp["connections"].([]any) {
		row := raw.(map[string]any)
		byID[row["instance_id"].(string)] = row
	}
	for _, id := range []string{"agent-trae", "agent-codex"} {
		row := byID[id]
		capState, _ := row["capture"].(map[string]any)
		if capState == nil || capState["state"] != "waiting_session" ||
			capState["reason"] != "hook_config_updated_after_receipt" ||
			row["environment_verified"] != nil {
			t.Fatalf("%s with hook config newer than receipt must fall back to waiting_session, got %+v", id, row)
		}
	}

	// Once a real codex app-server confirms all managed hooks are trusted, codex recovers to healthy.
	codexRespFile := filepath.Join(home, "codex_trusted.json")
	writeHooksListJSON(t, codexRespFile, codexHooks, agents.HostCodex, []struct {
		event, trustStatus string
		enabled            bool
	}{
		{"Stop", "trusted", true},
	})
	writeFakeHookTrustCLI(t, codexBin, codexRespFile, "")
	code, resp = doHTTPJSON(t, srv, http.MethodGet, "/v1/connections?force=1", "", mgmtHeaders())
	if code != http.StatusOK {
		t.Fatalf("GET /v1/connections?force=1 = %d: %+v", code, resp)
	}
	for _, raw := range resp["connections"].([]any) {
		row := raw.(map[string]any)
		if row["instance_id"] == "agent-codex" {
			capState, _ := row["capture"].(map[string]any)
			if capState == nil || capState["state"] != "healthy" || row["environment_verified"] != true {
				t.Fatalf("app-server verified codex should be healthy, got %+v", row)
			}
		}
	}
}

func TestConnectionsHookTrustDoesNotCacheCanceledOrUnevaluatedProbe(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	binDir := filepath.Join(home, "bin")

	codexProfile := filepath.Join(home, ".codex")
	codexHooks := filepath.Join(codexProfile, "hooks.json")
	codexTOML := filepath.Join(codexProfile, "config.toml")
	if err := os.MkdirAll(codexProfile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexHooks, []byte(`{"hooks":{"Stop":[{"_memlane_managed":"memlane-hook","hooks":[{"type":"command","command":"'/bin/memlane' hook codex Stop --gateway-url 'http://127.0.0.1:18174' --key-file '/tmp/k'","_memlane_managed":"memlane-hook"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexTOML, []byte("model = \"gpt-5.4\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// External mode/response/marker files outside hookTrustFilesStamp so the cache key stays
	// byte-for-byte identical between requests without force=1.
	modeFile := filepath.Join(home, "probe_mode.txt")
	startedFile := filepath.Join(home, "slow_started.txt")
	respFile := filepath.Join(home, "codex_untrusted.json")
	countFile := filepath.Join(home, "codex_calls.log")
	writeHooksListJSON(t, respFile, codexHooks, agents.HostCodex, []struct {
		event, trustStatus string
		enabled            bool
	}{
		{"Stop", "untrusted", true},
	})
	codexBin := filepath.Join(binDir, "codex")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" != "app-server" ]; then exit 0; fi
echo 1 >> %q
mode=""
if [ -f %q ]; then
  IFS= read -r mode < %q
fi
case "$mode" in
  slow)
    : > %q
    sleep 30
    exit 0
    ;;
  fail)
    exit 0
    ;;
esac
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*) printf '%%s\n' '{"id":1,"result":{}}' ;;
    *'"method":"hooks/list"'*) IFS= read -r resp < %q; printf '%%s\n' "$resp" ;;
  esac
done
`, countFile, modeFile, modeFile, startedFile, respFile)
	if err := os.WriteFile(codexBin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	eng := newContractMockEngine()
	store, err := domain.OpenStore(filepath.Join(t.TempDir(), "memlane.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertAgent(ctx, domain.Agent{ID: "agent-codex", Kind: agents.HostCodex, Name: "Codex", PrincipalID: "u-codex", DefaultAccess: domain.AccessMaintain}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ReserveCaptureReceipt(ctx, domain.CaptureReceipt{
		CaptureKey: "hist-codex-cache", UserID: "u-codex", DatasetID: "ds-1", SessionID: "sess-old",
		MessageID: "m-1", PayloadSHA256: "sha-1", EntryType: "capture_qa",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCaptureReceiptSaved(ctx, "hist-codex-cache", "entry-1"); err != nil {
		t.Fatal(err)
	}

	srv, err := gateway.NewServer(gateway.Config{
		Store:           store,
		Engine:          eng,
		Projector:       eng,
		ManagementToken: contractMgmtToken,
		DiscoveryOptions: agents.DiscoveryOptions{
			Home:  home,
			Kinds: []string{agents.HostCodex},
			LookPath: func(name string) (string, error) {
				if name == "codex" {
					return codexBin, nil
				}
				return "", errors.New("not found")
			},
			Environ: func(string) string { return "" },
			RuntimeOverrides: map[string]agents.HostOverride{
				agents.HostCodex: {Executable: codexBin, ProfileDir: codexProfile},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	countCalls := func() int {
		raw, err := os.ReadFile(countFile)
		if err != nil {
			return 0
		}
		return len(strings.Fields(string(raw)))
	}
	getCodexRow := func(reqCtx context.Context) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/connections", nil).WithContext(reqCtx)
		for k, v := range mgmtHeaders() {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /v1/connections = %d: %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		for _, raw := range body["connections"].([]any) {
			row := raw.(map[string]any)
			if row["instance_id"] == "agent-codex" {
				return row
			}
		}
		t.Fatalf("missing agent-codex in response: %+v", body)
		return nil
	}

	// 1. Canceled request aborts mid-probe (Source:"none", ctx.Err()!=nil) -> must NOT pollute cache.
	if err := os.WriteFile(modeFile, []byte("slow\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	rowCh := make(chan map[string]any, 1)
	go func() {
		rowCh <- getCodexRow(cancelCtx)
	}()
	startDeadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(startedFile); err == nil {
			break
		}
		if time.Now().After(startDeadline) {
			cancel()
			t.Fatal("timed out waiting for fake codex probe to start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	row := <-rowCh
	if row["needs_hook_trust"] != nil {
		t.Fatalf("canceled probe should not falsely flag needs_hook_trust, got %+v", row)
	}
	if calls := countCalls(); calls != 1 {
		t.Fatalf("expected 1 probe call after canceled request, got %d", calls)
	}

	// 2. Non-canceled probe that exits without response (Evaluated:false, Source:"none") -> must NOT pollute cache.
	if err := os.WriteFile(modeFile, []byte("fail\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	row = getCodexRow(ctx)
	if row["needs_hook_trust"] != nil {
		t.Fatalf("unevaluated probe should not flag needs_hook_trust, got %+v", row)
	}
	if calls := countCalls(); calls != 2 {
		t.Fatalf("expected second probe call (first was canceled, not cached), got %d", calls)
	}

	// 3. Immediate next normal request (no force=1, identical file stamp) MUST re-probe and return real untrusted state.
	if err := os.WriteFile(modeFile, []byte("untrusted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	row = getCodexRow(ctx)
	capState, _ := row["capture"].(map[string]any)
	if row["needs_hook_trust"] != true || row["untrusted_hook_count"] != float64(1) || capState == nil || capState["state"] != "needs_hook_trust" {
		t.Fatalf("expected immediate recovery to needs_hook_trust without force=1, got %+v", row)
	}
	if calls := countCalls(); calls != 3 {
		t.Fatalf("expected third probe call (unevaluated result must not be cached), got %d", calls)
	}

	// 4. Now that an evaluated result was returned, subsequent normal request hits the cache.
	row = getCodexRow(ctx)
	if row["needs_hook_trust"] != true {
		t.Fatalf("cached evaluated result mismatch: %+v", row)
	}
	if calls := countCalls(); calls != 3 {
		t.Fatalf("evaluated result should be cached, got %d calls want 3", calls)
	}
}
