package simpledesktop

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zwhu/memlane/internal/controlplane"
)

func generationUpdate(t *testing.T, updates <-chan func()) {
	t.Helper()
	select {
	case f := <-updates:
		f()
	case <-time.After(5 * time.Second):
		t.Fatal("missing generation update")
	}
}

func TestGenerationUnknownOperationsSurviveReopenAndOnlyReadOriginalReceipts(t *testing.T) {
	for _, kind := range []string{"save", "next", "validations"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			token := filepath.Join(root, "manager.token")
			if err := os.WriteFile(token, []byte(strings.Repeat("m", 40)), 0600); err != nil {
				t.Fatal(err)
			}
			row := GenerationRow{Revision: 1, Digest: "digest", Profile: GenerationProfile{Mode: "provider", Provider: "custom", Model: "model", Endpoint: "https://example.test/v1", CredentialConfigured: true, CredentialVersion: "v1"}}
			directory := GenerationDirectory{Revision: 2, Active: &GenerationRef{ID: "old", Revision: 1}, Profiles: map[string][]GenerationRow{"api": {row}}}
			if kind == "next" {
				directory.Validations = map[string]GenerationValidation{
					"probe": {Digest: row.Digest, Status: "passed", Plain: true, Schema: true, Quality: true},
				}
			}
			cfg := Config{GenerationFile: filepath.Join(root, "generation.json")}
			var mutations atomic.Int32
			var confirmed atomic.Bool
			transport := queueTestTransport(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != "GET" {
					mutations.Add(1)
					pending, err := New(cfg, nil).loadGenerationOperations()
					if err != nil || len(pending) != 1 || pending[0].Kind != kind {
						t.Error("request preceded durable receipt", pending, err)
					}
					if len(pending) == 1 {
						b, _ := os.ReadFile(filepath.Join(cfg.GenerationFile+".ui-operations", pending[0].Operation+".json"))
						if strings.Contains(string(b), "synthetic-secret") || strings.Contains(string(b), "api_key") || strings.Contains(string(b), "endpoint") {
							t.Error("request body leaked into journal")
						}
					}
					w.WriteHeader(503)
					fmt.Fprint(w, `{"error":"result_unknown"}`)
					return
				}
				switch {
				case r.URL.Path == "/v1/connections":
					fmt.Fprint(w, `{"revision":2,"connections":[]}`)
				case r.URL.Path == "/v1/generation":
					json.NewEncoder(w).Encode(directory)
				case strings.HasPrefix(r.URL.Path, "/v1/generation/operations/"):
					if kind == "save" {
						fmt.Fprint(w, `{"recorded":true,"result":{"id":"api","revision":1}}`)
					} else {
						fmt.Fprint(w, `{"recorded":true,"result":{"next":{"id":"api","revision":1}}}`)
					}
				case strings.HasPrefix(r.URL.Path, "/v1/generation/validations/"):
					status := "result_unknown"
					if confirmed.Load() {
						status = "passed"
					}
					json.NewEncoder(w).Encode(GenerationValidation{Status: status})
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			cfg.ProjectControl = &ProjectControlConfig{URL: coldStartTestURL, ManagementTokenFile: token}
			a := New(cfg, nil)
			updates := make(chan func(), 16)
			a.Client.HTTP = &http.Client{Transport: transport}
			a.Update = func(f func()) { updates <- f }
			a.integrations.Generation = directory
			a.integrations.editProfile("api", row)
			if kind == "save" {
				a.integrations.APIKey = "synthetic-secret"
				a.saveGeneration()
			} else {
				a.generationAction(kind)
			}
			generationUpdate(t, updates)
			if kind == "validations" {
				generationUpdate(t, updates)
			}
			if mutations.Load() != 1 {
				t.Fatal("initial request missing")
			}
			b := New(cfg, nil)
			reopened := make(chan func(), 16)
			b.Client.HTTP = &http.Client{Transport: transport}
			b.Update = func(f func()) { reopened <- f }
			b.RefreshIntegrations()
			generationUpdate(t, reopened)
			if len(b.integrations.PendingOperations) != 1 {
				t.Fatal("reopen lost the unknown operation", b.integrations.PendingOperations)
			}
			pending := b.integrations.PendingOperations[0]
			confirmed.Store(true)
			b.reviewPendingGeneration(pending)
			generationUpdate(t, reopened)
			if mutations.Load() != 1 {
				t.Fatal("review repeated a mutation")
			}
			recovered, err := b.loadGenerationOperations()
			if err != nil || len(recovered) != 0 {
				t.Fatal("confirmed receipt not cleared", recovered, err)
			}
			if b.integrations.Generation.Active.ID != "old" {
				t.Fatal("review switched active configuration")
			}
			if kind == "save" && (b.integrations.Saved == nil || b.integrations.Saved.ID != "api" || b.integrations.APIKey != "") {
				t.Fatal("save receipt did not restore safe profile")
			}
		})
	}
}

func TestGenerationOperationJournalRejectsDuplicateAndDamagedMetadata(t *testing.T) {
	a := New(Config{GenerationFile: filepath.Join(t.TempDir(), "generation.json")}, nil)
	first := generationUIOperation{Operation: "ui-original", Kind: "save", Profile: "api"}
	if err := a.recordGenerationOperation(first); err != nil {
		t.Fatal(err)
	}
	if err := a.recordGenerationOperation(generationUIOperation{Operation: "ui-retry", Kind: "validations", Profile: "api"}); err == nil {
		t.Fatal("unknown profile operation implicitly repeated")
	}
	if err := a.recordGenerationOperation(generationUIOperation{Operation: "ui-other", Kind: "save", Profile: "other"}); err != nil {
		t.Fatal("unrelated profile blocked", err)
	}
	path := filepath.Join(a.generationOperationsDir(), first.Operation+".json")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.loadGenerationOperations(); err == nil {
		t.Fatal("public journal accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.recordGenerationOperation(generationUIOperation{Operation: "ui-another", Kind: "save", Profile: "third"}); err == nil {
		t.Fatal("corrupt recovery metadata ignored before a mutation")
	}
}

func TestConnectionAndProviderPendingOperationsRemainIndependent(t *testing.T) {
	a := New(Config{GenerationFile: filepath.Join(t.TempDir(), "generation.json")}, nil)
	if err := a.recordGenerationOperation(generationUIOperation{Operation: "ui-agent", Kind: "connection_prepare", Profile: "same-id"}); err != nil {
		t.Fatal(err)
	}
	if a.integrations.pendingGenerationProfile("same-id") || a.integrations.hasPendingGeneration() {
		t.Fatal("Agent pending operation blocked provider configuration")
	}
	if err := a.recordGenerationOperation(generationUIOperation{Operation: "ui-provider", Kind: "save", Profile: "same-id"}); err != nil {
		t.Fatal("Provider blocked by unrelated Agent receipt", err)
	}
	if a.integrations.pendingConnection("same-id") == nil || !a.integrations.pendingGenerationProfile("same-id") {
		t.Fatal("shared journal lost distinct pending operations")
	}
	if err := a.recordGenerationOperation(generationUIOperation{Operation: "ui-agent-again", Kind: "connection_activate", Profile: "same-id"}); err == nil {
		t.Fatal("unknown Agent operation implicitly replaced")
	}
}

func TestGenerationOperationRecoveryProcess(t *testing.T) {
	if file := os.Getenv("MEMLANE_TEST_GENERATION_JOURNAL"); file != "" {
		rows, err := New(Config{GenerationFile: file}, nil).loadGenerationOperations()
		if err != nil || len(rows) != 1 || rows[0].Operation != "ui-crashed" {
			t.Fatal("process recovery failed", rows, err)
		}
		fmt.Println("independent-process-recovered-original-operation")
		return
	}
	file := filepath.Join(t.TempDir(), "generation.json")
	a := New(Config{GenerationFile: file}, nil)
	if err := a.recordGenerationOperation(generationUIOperation{Operation: "ui-crashed", Kind: "next", Profile: "api", Revision: 2}); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestGenerationOperationRecoveryProcess$")
	cmd.Env = append(os.Environ(), "MEMLANE_TEST_GENERATION_JOURNAL="+file)
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "independent-process-recovered-original-operation") {
		t.Fatal("independent process lost recovery", string(output), err)
	}
}

func TestGenerationJournalSharesAtomicRepairRecordsWithControlPlane(t *testing.T) {
	a := New(Config{GenerationFile: filepath.Join(t.TempDir(), "generation.json")}, nil)
	op := generationUIOperation{Operation: "ui-trae-repair", Kind: "connection_repair_trae_hooks", Profile: "trae"}
	if !op.valid() || !op.connection() {
		t.Fatal("repair journal kind must be recognized as a connection operation")
	}
	if err := a.recordGenerationOperation(op); err != nil {
		t.Fatal(err)
	}
	root := a.generationOperationsDir()
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != op.Operation+".json" {
		t.Fatalf("expected only the committed journal entry, got %v, %v", entries, err)
	}
	shared, err := controlplane.LoadUIOperations(root)
	if err != nil || len(shared) != 1 || shared[0] != controlplane.UIOperation(op) {
		t.Fatalf("shared journal could not recover repair: %v, %v", shared, err)
	}
	if !a.finishGenerationOperation(op.Operation) {
		t.Fatal(a.integrations.Error)
	}
	shared, err = controlplane.LoadUIOperations(root)
	if err != nil || len(shared) != 0 {
		t.Fatalf("finished entry remains: %v, %v", shared, err)
	}
}
