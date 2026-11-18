package bootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFirstRuntimeReadinessRequiresLiveOwnerAndBothAuthenticatedAPIs(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "library")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	var ownerIdentity atomic.Value
	ownerIdentity.Store("synthetic-owner")
	var requests atomic.Int64
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("X-Api-Key") != "synthetic-owner-token" {
			http.Error(w, "unauthorized", 401)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id": ownerIdentity.Load().(string)})
	}))
	defer service.Close()
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-management-token" {
			http.Error(w, "unauthorized", 401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"revision": 0})
	}))
	defer control.Close()
	token := filepath.Join(root, "owner.token")
	management := filepath.Join(root, "management.token")
	for name, value := range map[string]string{token: "synthetic-owner-token", management: "synthetic-management-token"} {
		if err := os.WriteFile(name, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name string, value any) {
		t.Helper()
		b, _ := json.Marshal(value)
		if err := os.WriteFile(name, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "store-identity.json"), StoreReceipt{Schema: 1, StoreID: strings.Repeat("a", 32), SourceCommit: "fixed", OwnerID: "synthetic-owner", KeyID: "synthetic-key", StorageInitialized: true})
	configPath := filepath.Join(parent, "runtime.json")
	write(configPath, map[string]any{"first_install": FirstInstallation{Schema: 1, StoreID: strings.Repeat("a", 32), Setup: true}, "official_data": root, "service_commit": "fixed", "token_file": token, "service_url": service.URL, "project_control": map[string]string{"url": control.URL, "management_token_file": management}})
	started, err := exec.Command("/bin/ps", "-p", strconv.Itoa(os.Getpid()), "-o", "lstart=").Output()
	if err != nil {
		t.Fatal(err)
	}
	owner := map[string]any{"pid": os.Getpid(), "started": strings.TrimSpace(string(started)), "config": configPath, "official_data": root, "phase": "starting"}
	ownerPath := filepath.Join(root, ".memlane-dev-owner.json")
	write(ownerPath, owner)
	if ready, err := FirstRuntimeReady(context.Background(), configPath); err != nil || ready || requests.Load() != 0 {
		t.Fatal(ready, err, requests.Load())
	}
	owner["phase"] = "first_install_setup"
	write(ownerPath, owner)
	if ready, err := FirstRuntimeReady(context.Background(), configPath); err != nil || !ready {
		t.Fatal(ready, err)
	}
	ownerIdentity.Store("another-owner")
	if ready, err := FirstRuntimeReady(context.Background(), configPath); err == nil || ready {
		t.Fatal("foreign owner accepted", ready, err)
	}
	before := requests.Load()
	owner["started"] = "different kernel identity"
	write(ownerPath, owner)
	if ready, err := FirstRuntimeReady(context.Background(), configPath); err == nil || ready || requests.Load() != before {
		t.Fatal("stale process accepted", ready, err)
	}
}

func TestFirstRuntimeDoesNotReplaceExistingOrBrokenConfigurations(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(parent, "runtime.json")
	environment := &Environment{}
	for _, content := range []string{`{"official_data":"/synthetic/daily"}`, `{"first_install":{"schema":1,"store_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","setup":true},"official_data":"/synthetic/missing-store"}`} {
		if err := os.WriteFile(config, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := environment.PrepareFirstRuntime(context.Background(), config, nil); err == nil {
			t.Fatal("existing configuration accepted as new")
		}
		if b, _ := os.ReadFile(config); string(b) != content {
			t.Fatal("existing configuration modified")
		}
		if _, err := os.Stat(filepath.Join(parent, "library")); !os.IsNotExist(err) {
			t.Fatal("replacement library created", err)
		}
	}
}

func TestFirstRuntimeRefusesRemoteOriginsBeforeReadingCredentials(t *testing.T) {
	if _, err := loopbackJSON(context.Background(), "https://example.invalid:443", "/api/v1/users/me", "/nonexistent/private-key", "X-Api-Key", ""); err == nil || strings.Contains(err.Error(), "no such file") {
		t.Fatal("remote origin not rejected first", err)
	}
	if err := StartFirstRuntime(context.Background(), FirstRuntime{}); err == nil {
		t.Fatal("incomplete runtime accepted")
	}
}
