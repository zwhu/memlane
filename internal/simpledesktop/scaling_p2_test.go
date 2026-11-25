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
)

func TestScalingP2ProvenanceBackfillSameMachineAndStopLocalWrite(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
