package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
	owners "github.com/zwhu/memlane/internal/supervisor"
	"github.com/zwhu/memlane/kit/daemon"
)

func TestDaemonOwnerConfigDualFilenameAndUnifiedCodec(t *testing.T) {
	// 1. Daily/dev store (first_install == nil) keeps .daemon-owner.json.
	dailyStore := t.TempDir()
	dailyCfg := runtimeConfig{
		Path:       filepath.Join(dailyStore, "runtime.json"),
		StoreRoot:  dailyStore,
		GatewayURL: "http://127.0.0.1:18791",
		raw:        []byte(`{"store_root":"` + dailyStore + `"}`),
	}
	dcfg, err := daemonOwnerConfig(dailyCfg, []string{dailyCfg.GatewayURL})
	require(t, err)
	if dcfg.LockConfig.OwnerReceiptFilename != "" && dcfg.LockConfig.OwnerReceiptFilename != ".daemon-owner.json" {
		t.Fatalf("unexpected daily receipt filename: %q", dcfg.LockConfig.OwnerReceiptFilename)
	}

	// 2. First-install store (first_install != nil) keeps .memlane-dev-owner.json
	//    and preserves all 5 metadata fields through unified owners.OwnerCodec.
	firstStore := t.TempDir()
	resourcesDir := filepath.Join(firstStore, "runtime-0123456789abcdef")
	require(t, os.MkdirAll(resourcesDir, 0o700))
	rawFirst := []byte(`{
		"first_install": {"schema": 1, "store_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "setup": true},
		"completion_url": "http://127.0.0.1:18792",
		"generation_mode": "agent_bridge",
		"generation_reference": {"id": "codex-default", "revision": 4}
	}`)
	firstCfg := runtimeConfig{
		Path:          filepath.Join(firstStore, "runtime.json"),
		StoreRoot:     firstStore,
		EngineURL:     "http://127.0.0.1:18793",
		GatewayURL:    "http://127.0.0.1:18792",
		EngineWorkDir: resourcesDir,
		raw:           rawFirst,
	}
	fcfg, err := daemonOwnerConfig(firstCfg, nil)
	require(t, err)
	if fcfg.LockConfig.OwnerReceiptFilename != ".memlane-dev-owner.json" {
		t.Fatalf("expected .memlane-dev-owner.json, got %q", fcfg.LockConfig.OwnerReceiptFilename)
	}
	sup := daemon.New(fcfg)
	require(t, sup.StartOwner(context.Background()))
	defer func() { _ = sup.Stop(context.Background()) }()

	read, _, err := owners.ReadOwnerRecord(firstStore)
	require(t, err)
	if read.ResourcesDir != resourcesDir ||
		read.ResourcesHash != "0123456789abcdef" ||
		read.RuntimeConfig != firstCfg.Path ||
		read.GenerationMode != "agent_bridge" ||
		read.GenerationReference == nil ||
		read.GenerationReference.ID != "codex-default" ||
		read.GenerationReference.Revision != 4 {
		t.Fatalf("first-install owner metadata lost on round-trip: %+v", read)
	}
}

func TestCleanupStaleDaemonRunDirsPreservesActiveDirs(t *testing.T) {
	storeRoot := t.TempDir()
	stale1, err := canonicalStoreRuntimeDir(storeRoot, "memlane-engine-")
	require(t, err)
	stale2, err := canonicalStoreRuntimeDir(storeRoot, "memlane-completion-")
	require(t, err)
	activeCompletion, err := canonicalStoreRuntimeDir(storeRoot, "memlane-completion-")
	require(t, err)
	activeCode, err := canonicalStoreRuntimeDir(storeRoot, "memlane-engine-code-")
	require(t, err)
	activeEngine, err := canonicalStoreRuntimeDir(storeRoot, "memlane-engine-")
	require(t, err)

	cleanupStaleDaemonRunDirs(storeRoot, activeCompletion, activeCode, activeEngine)

	for _, stale := range []string{stale1, stale2} {
		if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected stale dir %s to be removed, err=%v", stale, err)
		}
	}
	for _, keep := range []string{activeCompletion, activeCode, activeEngine} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("expected active dir %s to be preserved, err=%v", keep, err)
		}
	}
}

func TestDaemonRunSymlinkTraversalBlockedAndPrefixScoped(t *testing.T) {
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "victim.txt")
	require(t, os.WriteFile(sentinel, []byte("keep"), 0o600))

	// 1. Symlinked cache/daemon-run directory must be rejected by both creation and cleanup.
	storeWithSymlinkBase := t.TempDir()
	require(t, os.MkdirAll(filepath.Join(storeWithSymlinkBase, "cache"), 0o700))
	require(t, os.Symlink(outside, filepath.Join(storeWithSymlinkBase, "cache", "daemon-run")))
	if _, err := canonicalStoreRuntimeDir(storeWithSymlinkBase, "memlane-engine-"); err == nil {
		t.Fatal("expected canonicalStoreRuntimeDir to reject symlinked daemon-run base")
	}
	cleanupStaleDaemonRunDirs(storeWithSymlinkBase)
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("symlinked daemon-run cleanup touched outside target: %v", err)
	}

	// 2. Inside a valid daemon-run dir, symlinked entries and non-managed entries must NOT be deleted.
	store := t.TempDir()
	base := filepath.Join(store, "cache", "daemon-run")
	require(t, os.MkdirAll(base, 0o700))
	symlinkEntry := filepath.Join(base, "memlane-engine-symlink")
	require(t, os.Symlink(outside, symlinkEntry))
	unmanagedDir := filepath.Join(base, "user-custom-dir")
	require(t, os.MkdirAll(unmanagedDir, 0o700))
	staleManaged := filepath.Join(base, "memlane-engine-stale123")
	require(t, os.MkdirAll(staleManaged, 0o700))

	cleanupStaleDaemonRunDirs(store)

	if _, err := os.Lstat(symlinkEntry); err != nil {
		t.Fatalf("expected symlink entry to be skipped, err=%v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("outside target must remain untouched: %v", err)
	}
	if _, err := os.Stat(unmanagedDir); err != nil {
		t.Fatalf("unmanaged directory must not be deleted: %v", err)
	}
	if _, err := os.Stat(staleManaged); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale managed directory should be removed, err=%v", err)
	}
}

func TestGatewayReadyForOwnerRejectsForeignOwnerPID(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "mgmt.token")
	require(t, os.WriteFile(tokenFile, []byte("test-management-token-0000000001\n"), 0o600))

	includeHealthPID := true
	monitorPID := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			resp := map[string]any{
				"ok":         false,
				"gateway_ok": true,
				"engine_ok":  false,
				"status":     "starting",
			}
			if includeHealthPID {
				resp["owner_pid"] = 7777
			}
			_ = json.NewEncoder(w).Encode(resp)
		case "/v1/runtime/monitor":
			if monitorPID == 0 {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"owner_pid": monitorPID})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := runtimeConfig{
		GatewayURL:          srv.URL,
		ManagementTokenFile: tokenFile,
	}
	if gatewayReadyForOwner(context.Background(), cfg, 8888) {
		t.Fatal("expected gatewayReadyForOwner to reject mismatched owner_pid")
	}
	if !gatewayReadyForOwner(context.Background(), cfg, 7777) {
		t.Fatal("expected gatewayReadyForOwner to accept matching owner_pid")
	}

	// Legacy /api/health omitting owner_pid must fall back to /v1/runtime/monitor and reject if missing/mismatched.
	includeHealthPID = false
	monitorPID = 0
	if gatewayReadyForOwner(context.Background(), cfg, 7777) {
		t.Fatal("expected gatewayReadyForOwner to reject legacy /api/health when monitor owner_pid is unavailable")
	}
	monitorPID = 8888
	if gatewayReadyForOwner(context.Background(), cfg, 7777) {
		t.Fatal("expected gatewayReadyForOwner to reject mismatched monitor owner_pid")
	}
	monitorPID = 7777
	if !gatewayReadyForOwner(context.Background(), cfg, 7777) {
		t.Fatal("expected gatewayReadyForOwner to accept matching fallback monitor owner_pid")
	}
}

func TestGatewayFirstSurvivesEngineFailureAndAcceptsIngest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	storage := t.TempDir()
	workspace := t.TempDir()
	mgmtToken := "management-token-gateway-first-test"
	agentToken := "agent-token-gateway-first-test-0001"
	mgmtFile := filepath.Join(storage, "management.key")
	agentFile := filepath.Join(storage, "agent.key")
	require(t, os.WriteFile(mgmtFile, []byte(mgmtToken+"\n"), 0o600))
	require(t, os.WriteFile(agentFile, []byte(agentToken+"\n"), 0o600))

	// Reserve two free loopback ports (gateway + engine).
	ln1, err := net.Listen("tcp4", "127.0.0.1:0")
	require(t, err)
	gatewayURL := "http://" + ln1.Addr().String()
	require(t, ln1.Close())
	ln2, err := net.Listen("tcp4", "127.0.0.1:0")
	require(t, err)
	engineURL := "http://" + ln2.Addr().String()
	require(t, ln2.Close())

	// Use a shell script that exits immediately as the "python" executable so cognee-engine fails.
	failingPython := filepath.Join(storage, "fail-python.sh")
	require(t, os.WriteFile(failingPython, []byte("#!/bin/sh\necho simulated engine crash >&2\nexit 1\n"), 0o700))

	cfgPath := filepath.Join(storage, "runtime.json")
	rawCfg, err := json.Marshal(map[string]any{
		"store_root":            storage,
		"gateway_url":           gatewayURL,
		"engine_url":            engineURL,
		"management_token_file": mgmtFile,
		"python":                failingPython,
	})
	require(t, err)
	require(t, os.WriteFile(cfgPath, rawCfg, 0o600))

	rt := runtimeConfig{
		Path:                cfgPath,
		StoreRoot:           storage,
		OfficialData:        storage,
		GatewayURL:          gatewayURL,
		EngineURL:           engineURL,
		ManagementTokenFile: mgmtFile,
		Python:              failingPython,
		EngineWorkDir:       storage,
		raw:                 rawCfg,
	}

	daemonCtx, stopDaemon := context.WithCancel(ctx)
	var outBuf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- serveDaemon(daemonCtx, rt, false, &outBuf)
	}()
	defer func() {
		stopDaemon()
		select {
		case err := <-done:
			require(t, err)
		case <-time.After(10 * time.Second):
			t.Error("daemon did not shut down cleanly")
		}
	}()

	client := &http.Client{Timeout: 2 * time.Second}
	var healthBody map[string]any
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayURL+"/api/health", nil)
		require(t, err)
		req.Header.Set("Authorization", "Bearer "+mgmtToken)
		if resp, err := client.Do(req); err == nil {
			_ = json.NewDecoder(resp.Body).Decode(&healthBody)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && healthBody["gateway_ok"] == true && healthBody["status"] == "exited" {
				break
			}
		}
		select {
		case err := <-done:
			t.Fatalf("daemon exited prematurely when engine failed: %v", err)
		case <-time.After(25 * time.Millisecond):
		}
	}
	if healthBody["gateway_ok"] != true || healthBody["engine_ok"] != false || healthBody["status"] != "exited" {
		t.Fatalf("expected gateway alive with exited engine, got %+v", healthBody)
	}
	if !strings.Contains(outBuf.String(), "Memlane gateway listening on") {
		t.Fatalf("missing listening line in stdout: %q", outBuf.String())
	}

	// gatewayReady must treat gateway_ok=true as ready even when engine_ok=false.
	if !gatewayReady(ctx, rt) {
		t.Fatal("expected gatewayReady=true when gateway_ok=true and engine_ok=false")
	}

	// dev status --json must expose gateway_ok, engine_ok, and status.
	code, statusOut, stderrOut := invoke(t, "", "dev", "status", "--config", cfgPath, "--json")
	if code != 0 || stderrOut != "" {
		t.Fatalf("dev status failed: code=%d out=%q err=%q", code, statusOut, stderrOut)
	}
	var statusJSON map[string]any
	require(t, json.Unmarshal([]byte(statusOut), &statusJSON))
	if statusJSON["gateway_ok"] != true || statusJSON["engine_ok"] != false || statusJSON["status"] != "exited" {
		t.Fatalf("unexpected dev status JSON: %+v", statusJSON)
	}

	// Gateway must still accept project creation, agent registration, ACL, and POST /ingest while engine is down.
	manageReq := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		return liveRequest(t, ctx, client, gatewayURL, method, path, "Authorization", "Bearer "+mgmtToken, body, want)
	}
	proj := manageReq(http.MethodPost, "/api/projects", map[string]any{
		"name": "Offline Engine Project", "aliases": []string{workspace}, "auto_capture": true,
	}, http.StatusCreated)
	projID, _ := proj["id"].(string)
	if projID == "" {
		t.Fatalf("missing project id: %+v", proj)
	}
	manageReq(http.MethodPost, "/api/agents", map[string]any{
		"id": "codex-test", "kind": "codex", "name": "Codex", "key_file": agentFile,
	}, http.StatusCreated)
	manageReq(http.MethodPut, "/api/projects/"+projID+"/acl/codex-test", map[string]any{
		"access": "maintain", "capture": true,
	}, http.StatusOK)

	accepted := liveRequest(t, ctx, client, gatewayURL, http.MethodPost, "/ingest", "X-Api-Key", agentToken, map[string]any{
		"kind": "capture_qa", "workspace": workspace, "session_id": "s1", "message_id": "m1",
		"content": map[string]string{"question": "q", "answer": "a"},
	}, http.StatusAccepted)
	inboxID, _ := accepted["inbox_id"].(string)
	if inboxID == "" {
		t.Fatalf("expected inbox_id on /ingest while engine is down, got %+v", accepted)
	}
	db, err := domain.OpenStore(filepath.Join(storage, "memlane.sqlite3"))
	require(t, err)
	defer func() { _ = db.Close() }()
	item, err := db.GetInboxItem(ctx, inboxID)
	require(t, err)
	if item.ID != inboxID {
		t.Fatalf("expected persisted inbox item %s, got %+v", inboxID, item)
	}
}

func TestAppInstanceLockIsolationAndQuitGuard(t *testing.T) {
	storeA := t.TempDir()
	storeB := t.TempDir()
	cfgA := filepath.Join(storeA, "runtime.json")
	cfgB := filepath.Join(storeB, "runtime.json")

	lockA1, acquiredA1, err := acquireAppInstanceLock(cfgA, storeA)
	require(t, err)
	if !acquiredA1 || lockA1 == nil {
		t.Fatal("expected first instance lock on storeA to succeed")
	}
	defer func() { _ = lockA1.Release() }()

	// Second instance for same storeA must fail to acquire and return cleanly without error.
	lockA2, acquiredA2, err := acquireAppInstanceLock(cfgA, storeA)
	require(t, err)
	if acquiredA2 || lockA2 != nil {
		t.Fatal("expected second instance lock on same storeA to be rejected")
	}

	// Different storeB must be completely isolated and acquire cleanly.
	lockB, acquiredB, err := acquireAppInstanceLock(cfgB, storeB)
	require(t, err)
	if !acquiredB || lockB == nil {
		t.Fatal("expected instance lock on distinct storeB to succeed")
	}
	require(t, lockB.Release())

	// Release storeA and verify re-acquisition succeeds.
	require(t, lockA1.Release())
	lockA3, acquiredA3, err := acquireAppInstanceLock(cfgA, storeA)
	require(t, err)
	if !acquiredA3 || lockA3 == nil {
		t.Fatal("expected re-acquiring storeA lock after release to succeed")
	}
	require(t, lockA3.Release())

	// Brand-new first install: .app starts before library/ exists, wizard creates
	// library/, then a second launch via --config must hit the same configDir anchor lock.
	firstDir := t.TempDir()
	firstCfg := filepath.Join(firstDir, "runtime.json")
	firstStore := resolveAppStoreRoot(firstCfg, "", "", true)
	if firstStore != filepath.Join(firstDir, "library") {
		t.Fatalf("unexpected deterministic first-install store root: %q", firstStore)
	}
	appLock, ok, err := acquireAppInstanceLock(firstCfg, firstStore)
	require(t, err)
	if !ok || appLock == nil {
		t.Fatal("expected brand-new first-install .app lock to succeed")
	}
	defer func() { _ = appLock.Release() }()
	require(t, os.MkdirAll(firstStore, 0o700))
	secondLock, ok2, err := acquireAppInstanceLock(firstCfg, firstStore)
	require(t, err)
	if ok2 || secondLock != nil {
		t.Fatal("expected --config launch targeting same first-install store to be blocked by dual-anchor lock")
	}

	// KeepAliveOnQuit==false shutdown guard: non-owning instance must NEVER stop runtime/daemon.
	var firstCalls, explicitCalls, externalCalls int
	err = shutdownAppRuntimeOnQuit(
		context.Background(),
		false, // ownsInstance == false
		true,
		filepath.Join(storeA, "runtime.json"),
		func(context.Context) error { firstCalls++; return nil },
		func(context.Context, string) error { explicitCalls++; return nil },
		func(context.Context, string) error { externalCalls++; return nil },
	)
	require(t, err)
	if firstCalls != 0 || explicitCalls != 0 || externalCalls != 0 {
		t.Fatalf("non-owning instance must not stop daemon/runtime: first=%d explicit=%d external=%d", firstCalls, explicitCalls, externalCalls)
	}

	// Owning instance with explicit --config calls shutdownFirstRuntimes and stopExplicitOwner.
	err = shutdownAppRuntimeOnQuit(
		context.Background(),
		true,
		true,
		filepath.Join(storeA, "runtime.json"),
		func(context.Context) error { firstCalls++; return nil },
		func(context.Context, string) error { explicitCalls++; return nil },
		func(context.Context, string) error { externalCalls++; return nil },
	)
	require(t, err)
	if firstCalls != 1 || explicitCalls != 1 || externalCalls != 0 {
		t.Fatalf("unexpected owning explicit-config shutdown calls: first=%d explicit=%d external=%d", firstCalls, explicitCalls, externalCalls)
	}
}
