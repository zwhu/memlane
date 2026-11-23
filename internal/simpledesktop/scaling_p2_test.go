package simpledesktop

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestScalingP2ProvenanceBackfillSameMachineAndStopLocalWrite(t *testing.T) {
	token := p1TestTokenFile(t)
	var backfillCalls atomic.Int32
	var receivedRaw map[string]any
	var mu sync.Mutex

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureProvenanceBackfill},
			})
		case "/v1/provenance/backfill-legacy-dates":
			backfillCalls.Add(1)
			var raw map[string]any
			_ = json.NewDecoder(r.Body).Decode(&raw)
			mu.Lock()
			receivedRaw = raw
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(v2ProvenanceBackfillResponse{
				Status:     "migrated",
				Matched:    5,
				Imported:   5,
				Skipped:    0,
				BackupPath: "/tmp/knowledge-source-dates-x.json.migrated.bak",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	stateDir := t.TempDir()
	client := Client{
		Config: Config{
			ServiceURL:      control.URL,
			ClientStateFile: filepath.Join(stateDir, "client-state.json"),
			ProjectControl:  &ProjectControlConfig{URL: control.URL, ManagementTokenFile: token},
		},
	}

	serverAuth, err := client.backfillLegacyKnowledgeDatesV2(context.Background())
	if err != nil || !serverAuth {
		t.Fatalf("expected serverAuth=true err=nil, got auth=%v err=%v", serverAuth, err)
	}
	if backfillCalls.Load() != 1 {
		t.Fatalf("expected 1 backfill call, got %d", backfillCalls.Load())
	}
	mu.Lock()
	_, hasDays := receivedRaw["days"]
	gotIdentity, _ := receivedRaw["reading_identity"].(string)
	mu.Unlock()
	if hasDays || gotIdentity == "" {
		t.Fatalf("same-machine request must send ONLY reading_identity (no days), got %v", receivedRaw)
	}

	// Subsequent rememberKnowledgeDays must NOT write any local knowledge-source-dates-*.json
	// and must NOT send duplicate backfill calls.
	docs := []Document{{
		ID:       "k-new-1",
		Dataset:  "ds-1",
		Created:  "2026-11-21T10:00:00Z",
		Metadata: map[string]any{"node_set": []any{"session_learnings"}},
	}}
	if err := client.rememberKnowledgeDays(docs); err != nil {
		t.Fatalf("rememberKnowledgeDays failed: %v", err)
	}
	if backfillCalls.Load() != 1 {
		t.Fatalf("expected idempotent single backfill call, got %d", backfillCalls.Load())
	}
	entries, _ := os.ReadDir(stateDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "knowledge-source-dates-") {
			t.Fatalf("client must stop writing %s once provenance_backfill_v2 is active", e.Name())
		}
	}
}

func TestScalingP2ProvenanceBackfillCustomDirDaysAndBackup(t *testing.T) {
	token := p1TestTokenFile(t)
	var calls atomic.Int32
	var secondDays map[string]string
	var mu sync.Mutex

	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(ControlCapabilities{
				APIVersions: []string{"v1"}, ReadingContract: readingContract,
				Features: []string{v2FeatureProvenanceBackfill},
			})
		case "/v1/provenance/backfill-legacy-dates":
			n := calls.Add(1)
			var req v2ProvenanceBackfillRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if n == 1 {
				// Simulate control server state_dir != client custom ClientStateFile dir.
				_ = json.NewEncoder(w).Encode(v2ProvenanceBackfillResponse{Status: "absent"})
				return
			}
			mu.Lock()
			secondDays = req.Days
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(v2ProvenanceBackfillResponse{
				Status:   "migrated",
				Matched:  len(req.Days),
				Imported: len(req.Days),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer control.Close()

	stateDir := t.TempDir()
	client := Client{
		Config: Config{
			ServiceURL:      control.URL,
			ClientStateFile: filepath.Join(stateDir, "client-state.json"),
			ProjectControl:  &ProjectControlConfig{URL: control.URL, ManagementTokenFile: token},
		},
	}
	identity, err := readingIdentity(client)
	if err != nil {
		t.Fatal(err)
	}
	ownPath, ok := knowledgeSourceDatesPath(client.Config, identity)
	if !ok {
		t.Fatal("missing own knowledge source dates path")
	}
	key1 := fmt.Sprintf("%x", sha256.Sum256([]byte("ds-1/convention/k-1")))
	ownPayload, _ := json.Marshal(map[string]any{
		"schema_version": 1,
		"days":           map[string]string{key1: "2026-10-15"},
	})
	if err := os.WriteFile(ownPath, ownPayload, 0600); err != nil {
		t.Fatal(err)
	}
	// Plant an unrelated identity file in the same directory to verify strict single-identity isolation.
	otherPath := filepath.Join(stateDir, fmt.Sprintf("knowledge-source-dates-%x.json", sha256.Sum256([]byte("other-identity/"))))
	if err := os.WriteFile(otherPath, []byte(`{"schema_version":1,"days":{"`+strings.Repeat("a", 64)+`":"2026-01-01"}}`), 0600); err != nil {
		t.Fatal(err)
	}

	serverAuth, err := client.backfillLegacyKnowledgeDatesV2(context.Background())
	if err != nil || !serverAuth {
		t.Fatalf("custom dir backfill failed: auth=%v err=%v", serverAuth, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls (absent probe + explicit days), got %d", calls.Load())
	}
	mu.Lock()
	gotDays := secondDays
	mu.Unlock()
	if len(gotDays) != 1 || gotDays[key1] != "2026-10-15" {
		t.Fatalf("unexpected explicit days payload: %v", gotDays)
	}
	// Own file must be atomically renamed to .migrated.bak (never deleted).
	if _, err := os.Lstat(ownPath); !os.IsNotExist(err) {
		t.Fatalf("expected original file renamed away, stat err=%v", err)
	}
	if _, err := os.Lstat(ownPath + ".migrated.bak"); err != nil {
		t.Fatalf("expected .migrated.bak backup preserved, err=%v", err)
	}
	// Unrelated identity file must remain completely untouched.
	if _, err := os.Lstat(otherPath); err != nil {
		t.Fatalf("unrelated identity file was touched: %v", err)
	}
}

func TestScalingP2ProvenanceBackfillLegacyFallback(t *testing.T) {
	token := p1TestTokenFile(t)
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer control.Close()

	stateDir := t.TempDir()
	client := Client{
		Config: Config{
			ServiceURL:      control.URL,
			ClientStateFile: filepath.Join(stateDir, "client-state.json"),
			ProjectControl:  &ProjectControlConfig{URL: control.URL, ManagementTokenFile: token},
		},
	}
	docs := []Document{{
		ID:       "k-legacy-1",
		Dataset:  "ds-legacy",
		Created:  "2026-11-21T08:00:00Z",
		Metadata: map[string]any{"node_set": []any{"session_learnings"}, "session_date": "2026-11-18"},
	}}
	if err := client.rememberKnowledgeDays(docs); err != nil {
		t.Fatalf("legacy fallback rememberKnowledgeDays failed: %v", err)
	}
	if docs[0].FirstSourceDate != "2026-11-18" {
		t.Fatalf("expected legacy fallback FirstSourceDate=2026-11-18, got %q", docs[0].FirstSourceDate)
	}
	identity, _ := readingIdentity(client)
	path, _ := knowledgeSourceDatesPath(client.Config, identity)
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("expected legacy knowledge-source-dates file created when capability absent, err=%v", err)
	}
}

func TestScalingP2ColdPendingWatchPersistenceZeroSubprocessOnRestart(t *testing.T) {
	prevDebounce := coldPendingStore.debounce
	coldPendingStore.debounce = 10 * time.Millisecond
	t.Cleanup(func() {
		waitColdPendingStoreFlush()
		configureColdPendingStore("")
		resetColdPendingCachesForTest()
		coldPendingStore.debounce = prevDebounce
	})

	dir := t.TempDir()
	watchStorePath := filepath.Join(dir, "cold-pending-watch.json")
	resetColdPendingCachesForTest()
	configureColdPendingStore(watchStorePath)

	pluginDir := filepath.Join(dir, "plugin")
	scriptsDir := filepath.Join(pluginDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0700); err != nil {
		t.Fatal(err)
	}
	policyBytes := []byte("# synthetic policy\n")
	commonBytes := []byte("# synthetic common\n")
	if err := os.WriteFile(filepath.Join(scriptsDir, "_memlane_policy.py"), policyBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "_plugin_common.py"), commonBytes, 0600); err != nil {
		t.Fatal(err)
	}
	policySHA := fmt.Sprintf("%x", sha256.Sum256(policyBytes))
	commonSHA := fmt.Sprintf("%x", sha256.Sum256(commonBytes))
	manifestBytes, _ := json.Marshal(map[string]any{
		"files": map[string]string{
			"scripts/_memlane_policy.py": policySHA,
			"scripts/_plugin_common.py":  commonSHA,
		},
	})
	manifestPath := filepath.Join(pluginDir, ".memlane-project-access.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0600); err != nil {
		t.Fatal(err)
	}
	manifestSHA := fmt.Sprintf("%x", sha256.Sum256(manifestBytes))

	homeDir := filepath.Join(dir, "home")
	if err := os.MkdirAll(homeDir, 0700); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(dir, "agent.env")
	if err := os.WriteFile(envFile, []byte("SYNTHETIC=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mapPath := filepath.Join(dir, "session-map.json") // absent when cold
	bridgePath := filepath.Join(dir, "bridge.json")
	if err := os.WriteFile(bridgePath, []byte(`{"state":"idle"}`), 0600); err != nil {
		t.Fatal(err)
	}
	bInfo, err := os.Lstat(bridgePath)
	if err != nil {
		t.Fatal(err)
	}

	counterFile := filepath.Join(dir, "py-invocations.txt")
	fakePython := filepath.Join(dir, "fake-python.sh")
	script := fmt.Sprintf(`#!/bin/sh
echo x >> %q
cat <<'JSON'
{"pending":0,"unknown_outcomes":0,"signature":"sig-cold-1","session_id":"sess-1","session_inactive":true,"cold_watch":{"map_path":%q,"bridge_path":%q,"bridge_exists":true,"bridge_size":%d,"bridge_mtime_ns":%d}}
JSON
`, counterFile, mapPath, bridgePath, bInfo.Size(), bInfo.ModTime().UnixNano())
	if err := os.WriteFile(fakePython, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}

	inst := AgentInstallation{
		HostKind:    "codex",
		PluginDir:   pluginDir,
		EnvFile:     envFile,
		Home:        homeDir,
		ManifestSHA: manifestSHA,
		HelperSHA:   policySHA,
	}
	binding := ProjectBinding{AgentID: "ag-1", ProjectID: "p-1", HostSessionKey: "hk-1"}

	// 1. Cold miss executes subprocess once and schedules debounced atomic persistence.
	res1, err := runPendingMaterials(context.Background(), fakePython, inst, binding, "ds-1", "show", "")
	if err != nil || res1.Signature != "sig-cold-1" {
		t.Fatalf("first show failed: res=%+v err=%v", res1, err)
	}
	waitColdPendingStoreFlush()

	// Verify 0600 permissions & zero message body/details leakage on disk.
	info, err := os.Lstat(watchStorePath)
	if err != nil {
		t.Fatalf("expected %s persisted, err=%v", watchStorePath, err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected 0600 permissions on cold-pending-watch.json, got %o", info.Mode().Perm())
	}
	rawDisk, _ := os.ReadFile(watchStorePath)
	if strings.Contains(string(rawDisk), `"details"`) || strings.Contains(string(rawDisk), `"summary"`) || strings.Contains(string(rawDisk), `"unknown_entry_ids"`) {
		t.Fatalf("persisted cold-pending-watch.json must never contain message content/details: %s", string(rawDisk))
	}

	// 2. Simulate full process restart: clear in-memory caches and reload from disk.
	resetColdPendingCachesForTest()
	configureColdPendingStore(watchStorePath)

	res2, err := runPendingMaterials(context.Background(), fakePython, inst, binding, "ds-1", "show", "")
	if err != nil || res2.Signature != "sig-cold-1" {
		t.Fatalf("post-restart show failed: res=%+v err=%v", res2, err)
	}
	countBytes, _ := os.ReadFile(counterFile)
	if lines := len(strings.Split(strings.TrimSpace(string(countBytes)), "\n")); lines != 1 {
		t.Fatalf("expected 0 subprocess spawns after restart (total 1), got %d", lines)
	}

	// 3. Mutate watched bridge file -> must invalidate persisted cold watch and invoke subprocess again.
	if err := os.WriteFile(bridgePath, []byte(`{"state":"changed-with-new-bytes"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runPendingMaterials(context.Background(), fakePython, inst, binding, "ds-1", "show", ""); err != nil {
		t.Fatalf("post-mutation show failed: %v", err)
	}
	waitColdPendingStoreFlush()
	countBytes, _ = os.ReadFile(counterFile)
	if lines := len(strings.Split(strings.TrimSpace(string(countBytes)), "\n")); lines != 2 {
		t.Fatalf("expected bridge mutation to invalidate cold watch (total 2), got %d", lines)
	}

	// 4. Corrupt file & loose-permission file must safely fall back to empty cache without panicking.
	configureColdPendingStore("")
	if err := os.WriteFile(watchStorePath, []byte("{corrupt json"), 0600); err != nil {
		t.Fatal(err)
	}
	resetColdPendingCachesForTest()
	loadColdPendingStore(watchStorePath)
	coldPendingCache.mu.Lock()
	nEntries := len(coldPendingCache.entries)
	coldPendingCache.mu.Unlock()
	if nEntries != 0 {
		t.Fatalf("corrupt watch file should yield empty cache, got %d", nEntries)
	}

	if err := os.WriteFile(watchStorePath, rawDisk, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(watchStorePath, 0644); err != nil {
		t.Fatal(err)
	}
	resetColdPendingCachesForTest()
	loadColdPendingStore(watchStorePath)
	coldPendingCache.mu.Lock()
	nEntries = len(coldPendingCache.entries)
	coldPendingCache.mu.Unlock()
	if nEntries != 0 {
		t.Fatalf("0644 watch file must be rejected, got %d entries", nEntries)
	}
}

func TestScalingP2OpenCodeColdWatchPersistenceZeroSubprocessOnRestart(t *testing.T) {
	prevDebounce := coldPendingStore.debounce
	coldPendingStore.debounce = 10 * time.Millisecond
	t.Cleanup(func() {
		waitColdPendingStoreFlush()
		configureColdPendingStore("")
		resetColdPendingCachesForTest()
		coldPendingStore.debounce = prevDebounce
	})

	dir := t.TempDir()
	watchStorePath := filepath.Join(dir, "cold-pending-watch.json")
	resetColdPendingCachesForTest()
	configureColdPendingStore(watchStorePath)

	tokenFile := filepath.Join(dir, "mgr.token")
	if err := os.WriteFile(tokenFile, []byte("synthetic-opencode-token-0123456789012345"), 0600); err != nil {
		t.Fatal(err)
	}
	outboxDir := filepath.Join(dir, "outbox")
	bindingsDir := filepath.Join(dir, "bindings")
	if err := os.MkdirAll(outboxDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bindingsDir, 0700); err != nil {
		t.Fatal(err)
	}

	verifyEntry := openCodeVerifyEntry{
		OutboxDir:   outboxDir,
		BindingsDir: bindingsDir,
	}
	watchEntry, ok := captureOpenCodeColdWatch(verifyEntry, tokenFile, "oc-sess-1")
	if !ok {
		t.Fatal("failed to capture OpenCode cold watch")
	}
	watchEntry.Result = PendingMaterials{
		Pending:   0,
		Unknown:   0,
		Signature: "oc-sig-1",
		SessionID: "oc-sess-1",
	}
	key := openCodeColdKey{
		AgentID:        "ag-oc",
		ProjectID:      "p-1",
		HostSessionKey: "oc-sess-1",
		Dataset:        "ds-1",
		PluginDir:      filepath.Join(dir, "oc-plugin"),
		TokenFile:      tokenFile,
	}

	openCodeColdPut(key, watchEntry)
	waitColdPendingStoreFlush()

	// Simulate restart: wipe memory and reload from cold-pending-watch.json.
	resetColdPendingCachesForTest()
	configureColdPendingStore(watchStorePath)

	reloaded, found := openCodeColdGet(key)
	if !found || reloaded.Result.Signature != "oc-sig-1" {
		t.Fatalf("expected OpenCode cold watch reloaded from disk, found=%v res=%+v", found, reloaded.Result)
	}
	if !openCodeColdEntryUnchanged(reloaded) {
		t.Fatalf("reloaded OpenCode cold watch should validate unchanged via Lstat")
	}

	// Creating a new outbox file must invalidate the reloaded watch immediately!
	if err := os.WriteFile(filepath.Join(outboxDir, "pending-1.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if openCodeColdEntryUnchanged(reloaded) {
		t.Fatalf("expected new outbox entry to invalidate persisted OpenCode cold watch")
	}
}
