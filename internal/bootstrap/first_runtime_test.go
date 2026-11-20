package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/zwhu/memlane/internal/supervisor"
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

// TestFirstRuntimeHelperProcess acts as a deterministic synthetic launcher or service
// child process for bootstrap-supervisor lifecycle tests.
func TestFirstRuntimeHelperProcess(t *testing.T) {
	if os.Getenv("MEMLANE_BOOTSTRAP_HELPER") != "1" {
		return
	}
	mode := os.Getenv("MEMLANE_BOOTSTRAP_MODE")
	if mode == "grandchild" {
		for {
			time.Sleep(time.Second)
		}
	}

	role := os.Getenv("MEMLANE_BOOTSTRAP_ROLE")
	orderFile := os.Getenv("MEMLANE_BOOTSTRAP_ORDER_FILE")
	appendOrder := func(event string) {
		if orderFile == "" {
			return
		}
		f, err := os.OpenFile(orderFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%s:%s\n", role, event)
			_ = f.Close()
		}
	}

	if mode == "crash" {
		fmt.Fprintln(os.Stderr, "Authorization: Bearer leak-me-never-token")
		fmt.Fprintln(os.Stderr, "api_key=super-secret-value")
		fmt.Fprintln(os.Stderr, "fatal: synthetic launcher crash")
		os.Exit(17)
	}

	if mode == "service" || mode == "service_ignore_sigterm" {
		appendOrder("start")
		if gf := os.Getenv("MEMLANE_BOOTSTRAP_GRANDCHILD_FILE"); gf != "" {
			exe, err := os.Executable()
			if err == nil {
				child := exec.Command(exe, "-test.run=^TestFirstRuntimeHelperProcess$")
				child.Env = []string{"MEMLANE_BOOTSTRAP_HELPER=1", "MEMLANE_BOOTSTRAP_MODE=grandchild"}
				if child.Start() == nil {
					_ = os.WriteFile(gf, []byte(strconv.Itoa(child.Process.Pid)), 0o600)
				}
			}
		}
		addr := os.Getenv("MEMLANE_BOOTSTRAP_ADDR")
		ln, err := net.Listen("tcp4", addr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "listen error: %v\n", err)
			os.Exit(2)
		}
		var unhealthy atomic.Bool
		sigCh := make(chan os.Signal, 4)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

		mux := http.NewServeMux()
		mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
			if unhealthy.Load() {
				http.Error(w, "unhealthy", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok")
		})
		mux.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) {
			unhealthy.Store(r.URL.Query().Get("state") == "1")
			w.WriteHeader(http.StatusOK)
		})
		mux.HandleFunc("/api/v1/users/me", func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Api-Key") != "synthetic-owner-token" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "synthetic-owner"})
		})
		mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer synthetic-management-token" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"revision": 1})
		})
		srv := &http.Server{Handler: mux}
		go func() { _ = srv.Serve(ln) }()

		for sig := range sigCh {
			appendOrder(sig.String())
			if mode == "service_ignore_sigterm" && sig == syscall.SIGTERM {
				continue
			}
			_ = srv.Close()
			os.Exit(0)
		}
	}

	// Launcher modes: emulate scripts/dev.py or first_install_runtime.py activation contract.
	storeRoot := os.Getenv("MEMLANE_BOOTSTRAP_STORE")
	configPath := os.Getenv("MEMLANE_BOOTSTRAP_CONFIG")
	serviceAddr := os.Getenv("MEMLANE_BOOTSTRAP_SERVICE_ADDR")
	controlAddr := os.Getenv("MEMLANE_BOOTSTRAP_CONTROL_ADDR")
	triggerFile := os.Getenv("MEMLANE_BOOTSTRAP_TRIGGER_FILE")
	grandchildFile := os.Getenv("MEMLANE_BOOTSTRAP_GRANDCHILD_FILE")
	spawnLogFile := os.Getenv("MEMLANE_BOOTSTRAP_SPAWN_COUNT_FILE")
	logNoiseBytes, _ := strconv.Atoi(os.Getenv("MEMLANE_BOOTSTRAP_NOISE_BYTES"))

	if spawnLogFile != "" {
		if f, err := os.OpenFile(spawnLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
		}
	}

	lock, err := supervisor.AcquireOwner(storeRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "acquire owner failed: %v\n", err)
		os.Exit(3)
	}
	defer func() { _ = lock.Release() }()

	started, err := supervisor.ProcessStartTime(context.Background(), os.Getpid())
	if err != nil {
		fmt.Fprintf(os.Stderr, "lstart failed: %v\n", err)
		os.Exit(4)
	}

	if grandchildFile != "" {
		exe, err := os.Executable()
		if err == nil {
			child := exec.Command(exe, "-test.run=^TestFirstRuntimeHelperProcess$")
			child.Env = []string{"MEMLANE_BOOTSTRAP_HELPER=1", "MEMLANE_BOOTSTRAP_MODE=grandchild"}
			if child.Start() == nil {
				_ = os.WriteFile(grandchildFile, []byte(strconv.Itoa(child.Process.Pid)), 0o600)
			}
		}
	}

	rec := supervisor.OwnerRecord{
		PID:               os.Getpid(),
		Started:           started,
		Phase:             "starting",
		Config:            configPath,
		OfficialData:      storeRoot,
		ServiceURL:        "http://" + serviceAddr,
		ProjectControlURL: "http://" + controlAddr,
		Children:          []int{},
		ServiceChildren:   map[string]int{},
	}
	if err := lock.WriteRecord(rec); err != nil {
		fmt.Fprintf(os.Stderr, "write starting owner failed: %v\n", err)
		os.Exit(5)
	}

	svcLn, err := net.Listen("tcp4", serviceAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "service listen error: %v\n", err)
		os.Exit(6)
	}
	ctlLn, err := net.Listen("tcp4", controlAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "control listen error: %v\n", err)
		os.Exit(7)
	}

	svcMux := http.NewServeMux()
	svcMux.HandleFunc("/api/v1/users/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "synthetic-owner-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "synthetic-owner"})
	})
	ctlMux := http.NewServeMux()
	ctlMux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-management-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"revision": 1})
	})

	svcSrv := &http.Server{Handler: svcMux}
	ctlSrv := &http.Server{Handler: ctlMux}
	go func() { _ = svcSrv.Serve(svcLn) }()
	go func() { _ = ctlSrv.Serve(ctlLn) }()

	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	if triggerFile != "" {
		for {
			if b, readErr := os.ReadFile(triggerFile); readErr == nil {
				if strings.TrimSpace(string(b)) == "crash" {
					_ = svcSrv.Close()
					_ = ctlSrv.Close()
					_ = lock.Release()
					os.Exit(23)
				}
				break
			}
			select {
			case <-sigCh:
				_ = svcSrv.Close()
				_ = ctlSrv.Close()
				_ = lock.RemoveRecord()
				_ = lock.Release()
				os.Exit(0)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}

	if mode == "launcher_activate" {
		// Flip setup=false, write applied generation.json & client-state.json so FirstInitializationComplete passes.
		rawCfg, err := os.ReadFile(configPath)
		if err != nil {
			os.Exit(8)
		}
		var cfgMap map[string]any
		if err := json.Unmarshal(rawCfg, &cfgMap); err != nil {
			os.Exit(9)
		}
		firstMap := cfgMap["first_install"].(map[string]any)
		firstMap["setup"] = false
		cfgMap["generation_mode"] = "provider"
		cfgMap["model"] = "synthetic-model"
		cfgMap["generation_file"] = filepath.Join(storeRoot, "generation.json")
		updatedCfg, _ := json.Marshal(cfgMap)
		_ = os.WriteFile(configPath, updatedCfg, 0o600)

		gen := map[string]any{
			"schema_version": 2,
			"active":         map[string]any{"id": "cand", "revision": 1},
			"profiles": map[string]any{
				"cand": []any{map[string]any{
					"revision": 1,
					"profile":  map[string]any{"mode": "provider", "model": "synthetic-model"},
				}},
			},
		}
		genBytes, _ := json.Marshal(gen)
		_ = os.WriteFile(filepath.Join(storeRoot, "generation.json"), genBytes, 0o600)

		clientState := map[string]any{
			"schema_version":              1,
			"initialization_completed_at": "2026-11-20T05:00:00Z",
			"first_used_date":             "2026-11-20",
			"first_used_timezone":         "UTC",
		}
		csBytes, _ := json.Marshal(clientState)
		_ = os.WriteFile(filepath.Join(storeRoot, "client-state.json"), csBytes, 0o600)
		rec.Phase = "ready"
	} else {
		rec.Phase = "first_install_setup"
	}

	if err := lock.WriteRecord(rec); err != nil {
		fmt.Fprintf(os.Stderr, "write ready owner failed: %v\n", err)
		os.Exit(10)
	}

	if logNoiseBytes > 0 {
		fmt.Fprintln(os.Stderr, "Authorization: Bearer super-secret-runtime-token")
		fmt.Fprintln(os.Stderr, strings.Repeat("L", logNoiseBytes))
	}

	<-sigCh
	_ = svcSrv.Close()
	_ = ctlSrv.Close()
	_ = lock.RemoveRecord()
	_ = lock.Release()
	os.Exit(0)
}

type syntheticFirstRuntimeFixture struct {
	parent       string
	storeRoot    string
	resourcesDir string
	hostHome     string
	configPath   string
	wrapperPath  string
	serviceAddr  string
	controlAddr  string
	storeID      string
}

func reserveTestLoopback(t *testing.T) (addr, origin string) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr = ln.Addr().String()
	_ = ln.Close()
	return addr, "http://" + addr
}

func newSyntheticFirstRuntimeFixture(t *testing.T, mode string, extraEnv map[string]string) (syntheticFirstRuntimeFixture, FirstRuntime) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	storeRoot := filepath.Join(parent, "library")
	resourcesDir := filepath.Join(parent, "resources")
	hostHome := filepath.Join(parent, "home")
	for _, dir := range []string{storeRoot, resourcesDir, hostHome} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	serviceAddr, serviceOrigin := reserveTestLoopback(t)
	controlAddr, controlOrigin := reserveTestLoopback(t)

	ownerToken := filepath.Join(storeRoot, "owner.token")
	mgmtToken := filepath.Join(storeRoot, "management.token")
	if err := os.WriteFile(ownerToken, []byte("synthetic-owner-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mgmtToken, []byte("synthetic-management-token"), 0o600); err != nil {
		t.Fatal(err)
	}

	storeID := strings.Repeat("b", 32)
	receiptBytes, _ := json.Marshal(StoreReceipt{
		Schema:             1,
		StoreID:            storeID,
		SourceCommit:       "fixed",
		OwnerID:            "synthetic-owner",
		KeyID:              "synthetic-key",
		StorageInitialized: true,
	})
	if err := os.WriteFile(filepath.Join(storeRoot, "store-identity.json"), receiptBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(parent, "runtime.json")
	wrapperPath := filepath.Join(parent, "fake-python.sh")

	var envLines strings.Builder
	envLines.WriteString("#!/bin/sh\n")
	envLines.WriteString("export MEMLANE_BOOTSTRAP_HELPER=1\n")
	fmt.Fprintf(&envLines, "export MEMLANE_BOOTSTRAP_MODE=%q\n", mode)
	fmt.Fprintf(&envLines, "export MEMLANE_BOOTSTRAP_STORE=%q\n", storeRoot)
	fmt.Fprintf(&envLines, "export MEMLANE_BOOTSTRAP_CONFIG=%q\n", configPath)
	fmt.Fprintf(&envLines, "export MEMLANE_BOOTSTRAP_SERVICE_ADDR=%q\n", serviceAddr)
	fmt.Fprintf(&envLines, "export MEMLANE_BOOTSTRAP_CONTROL_ADDR=%q\n", controlAddr)
	for k, v := range extraEnv {
		fmt.Fprintf(&envLines, "export %s=%q\n", k, v)
	}
	fmt.Fprintf(&envLines, "exec %q -test.run=^TestFirstRuntimeHelperProcess$\n", exe)
	if err := os.WriteFile(wrapperPath, []byte(envLines.String()), 0o700); err != nil {
		t.Fatal(err)
	}

	cfgBytes, _ := json.Marshal(map[string]any{
		"first_install":   FirstInstallation{Schema: 1, StoreID: storeID, Setup: true},
		"official_data":   storeRoot,
		"service_commit":  "fixed",
		"python":          wrapperPath,
		"token_file":      ownerToken,
		"service_url":     serviceOrigin,
		"generation_file": filepath.Join(storeRoot, "generation.json"),
		"generation_mode": "unconfigured",
		"project_control": map[string]string{
			"url":                   controlOrigin,
			"registry_file":         filepath.Join(storeRoot, "projects.json"),
			"management_token_file": mgmtToken,
			"plugin_dir":            filepath.Join(parent, "plugin"),
		},
	})
	if err := os.WriteFile(configPath, cfgBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	fix := syntheticFirstRuntimeFixture{
		parent:       parent,
		storeRoot:    storeRoot,
		resourcesDir: resourcesDir,
		hostHome:     hostHome,
		configPath:   configPath,
		wrapperPath:  wrapperPath,
		serviceAddr:  serviceAddr,
		controlAddr:  controlAddr,
		storeID:      storeID,
	}
	prepared := FirstRuntime{
		ConfigPath: configPath,
		StoreRoot:  storeRoot,
		Resources:  resourcesDir,
		Python:     wrapperPath,
		HostHome:   hostHome,
	}
	return fix, prepared
}

func waitPIDDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("process %d still alive after shutdown", pid)
}

func TestStartFirstRuntimeDelegatesLifecycleLogRotationAndGracefulShutdown(t *testing.T) {
	t.Cleanup(func() { _ = ShutdownAllFirstRuntimes(context.Background()) })

	tempMeta, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	grandchildFile := filepath.Join(tempMeta, "grandchild.pid")
	spawnCountFile := filepath.Join(tempMeta, "spawns.log")

	fix, prepared := newSyntheticFirstRuntimeFixture(t, "launcher", map[string]string{
		"MEMLANE_BOOTSTRAP_GRANDCHILD_FILE":  grandchildFile,
		"MEMLANE_BOOTSTRAP_SPAWN_COUNT_FILE": spawnCountFile,
		"MEMLANE_BOOTSTRAP_NOISE_BYTES":      "600",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	handle, err := StartFirstRuntimeHandle(
		ctx,
		prepared,
		WithFirstRuntimeLogLimit(256),
		WithFirstRuntimeMonitorInterval(40*time.Millisecond),
		WithFirstRuntimeReadyPollInterval(25*time.Millisecond),
		WithFirstRuntimeStopTimeouts(2*time.Second, 2*time.Second),
	)
	if err != nil {
		t.Fatalf("StartFirstRuntimeHandle failed: %v", err)
	}
	if !handle.Owned() || handle.Supervisor() == nil {
		t.Fatalf("expected owned handle with backing supervisor")
	}
	if got, ok := FirstRuntimeSupervisor(fix.configPath); !ok || got != handle {
		t.Fatalf("expected FirstRuntimeSupervisor to return active handle")
	}
	if ready, err := FirstRuntimeReady(ctx, fix.configPath); err != nil || !ready {
		t.Fatalf("expected FirstRuntimeReady=true, got ready=%v err=%v", ready, err)
	}

	launcherPID := handle.Snapshot().Services[0].PID
	if launcherPID <= 1 {
		t.Fatalf("unexpected launcher PID %d", launcherPID)
	}

	// Second StartFirstRuntimeHandle while running must reuse the live handle without spawning another process.
	handle2, err := StartFirstRuntimeHandle(ctx, prepared, WithFirstRuntimeReadyPollInterval(25*time.Millisecond))
	if err != nil {
		t.Fatalf("second StartFirstRuntimeHandle failed: %v", err)
	}
	if handle2 != handle {
		t.Fatalf("expected second start to reuse live owned handle")
	}
	spawns, err := os.ReadFile(spawnCountFile)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(spawns)), "\n"); len(lines) != 1 {
		t.Fatalf("expected exactly 1 launcher spawn, got %v", lines)
	}

	// Wait for background supervisor monitor to rotate launcher.log while preserving live inode.
	logPath := filepath.Join(fix.parent, "runtime-logs", "launcher.log")
	prevPath := logPath + ".previous"
	infoBefore, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	inodeBefore := infoBefore.Sys().(*syscall.Stat_t).Ino

	deadline := time.Now().Add(3 * time.Second)
	rotated := false
	for time.Now().Before(deadline) {
		if prevInfo, statErr := os.Lstat(prevPath); statErr == nil {
			if prevInfo.Mode().Perm() != 0o600 {
				t.Fatalf("expected 0600 previous log, got %v", prevInfo.Mode().Perm())
			}
			rotated = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !rotated {
		t.Fatal("expected supervisor monitor to rotate launcher.log into .previous")
	}
	infoAfter, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if infoAfter.Sys().(*syscall.Stat_t).Ino != inodeBefore {
		t.Fatal("expected launcher.log rotation to preserve live inode")
	}

	// Verify grandchild was spawned in the same Setsid process group.
	var grandchildPID int
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b, readErr := os.ReadFile(grandchildFile); readErr == nil && len(bytes.TrimSpace(b)) > 0 {
			grandchildPID, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			if grandchildPID > 1 {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if grandchildPID <= 1 {
		t.Fatal("expected grandchild PID recorded")
	}

	// Explicit graceful shutdown hook must stop launcher + grandchild cleanly and release .service-owner.
	if err := ShutdownFirstRuntime(context.Background(), fix.configPath); err != nil {
		t.Fatalf("ShutdownFirstRuntime failed: %v", err)
	}
	// Second shutdown call must be a clean idempotent no-op.
	if err := handle.Shutdown(context.Background()); err != nil {
		t.Fatalf("idempotent handle.Shutdown failed: %v", err)
	}
	if _, ok := FirstRuntimeSupervisor(fix.configPath); ok {
		t.Fatal("expected handle unregistered after shutdown")
	}
	waitPIDDead(t, launcherPID)
	waitPIDDead(t, grandchildPID)

	lock, err := supervisor.AcquireOwner(fix.storeRoot)
	if err != nil {
		t.Fatalf("expected .service-owner cleanly released after shutdown, got %v", err)
	}
	_ = lock.Release()
}

func TestStartFirstRuntimeRecoversStaleOwnerAndRefusesForeignOrLiveReceipt(t *testing.T) {
	t.Cleanup(func() { _ = ShutdownAllFirstRuntimes(context.Background()) })

	fix, prepared := newSyntheticFirstRuntimeFixture(t, "launcher", nil)
	receiptPath := filepath.Join(fix.storeRoot, ".memlane-dev-owner.json")

	// Case A: foreign config receipt must be refused and preserved untouched.
	foreign, _ := json.Marshal(supervisor.OwnerRecord{
		PID:          999991,
		Started:      "synthetic-dead",
		Phase:        "ready",
		Config:       filepath.Join(fix.parent, "other-runtime.json"),
		OfficialData: fix.storeRoot,
	})
	if err := os.WriteFile(receiptPath, foreign, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := StartFirstRuntimeHandle(context.Background(), prepared); err == nil {
		t.Fatal("expected foreign owner receipt to refuse startup")
	}
	if got, err := os.ReadFile(receiptPath); err != nil || !bytes.Equal(got, foreign) {
		t.Fatalf("foreign owner receipt must remain untouched (err=%v)", err)
	}

	// Case B: receipt pointing to a still-live process (current PID) without flock must refuse recovery.
	started, err := supervisor.ProcessStartTime(context.Background(), os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	liveUnowned, _ := json.Marshal(supervisor.OwnerRecord{
		PID:          os.Getpid(),
		Started:      started,
		Phase:        "ready",
		Config:       fix.configPath,
		OfficialData: fix.storeRoot,
	})
	if err := os.WriteFile(receiptPath, liveUnowned, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := StartFirstRuntimeHandle(context.Background(), prepared); !errors.Is(err, supervisor.ErrRecoveryRefused) {
		t.Fatalf("expected ErrRecoveryRefused for live unowned PID, got %v", err)
	}
	if got, err := os.ReadFile(receiptPath); err != nil || !bytes.Equal(got, liveUnowned) {
		t.Fatalf("live unowned receipt must remain untouched (err=%v)", err)
	}

	// Case C: stale dead-PID receipt with matching config/store and free ports is archived and recovered automatically.
	stale, _ := json.Marshal(supervisor.OwnerRecord{
		PID:               999992,
		Started:           "synthetic-dead",
		Phase:             "ready",
		Config:            fix.configPath,
		OfficialData:      fix.storeRoot,
		ServiceURL:        "http://" + fix.serviceAddr,
		ProjectControlURL: "http://" + fix.controlAddr,
	})
	if err := os.WriteFile(receiptPath, stale, 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	handle, err := StartFirstRuntimeHandle(
		ctx,
		prepared,
		WithFirstRuntimeReadyPollInterval(25*time.Millisecond),
		WithFirstRuntimeStopTimeouts(2*time.Second, 2*time.Second),
	)
	if err != nil {
		t.Fatalf("expected stale receipt to auto-recover and start cleanly, got %v", err)
	}
	defer func() { _ = handle.Shutdown(context.Background()) }()

	// Verify exact archived stale receipt exists.
	archives, err := filepath.Glob(filepath.Join(fix.storeRoot, ".memlane-dev-owner-recovered-*.json"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("expected 1 archived stale receipt, got %v (err=%v)", archives, err)
	}
	archivedBytes, err := os.ReadFile(archives[0])
	if err != nil || !bytes.Equal(archivedBytes, stale) {
		t.Fatalf("archived receipt content mismatch (err=%v)", err)
	}
}

func TestStartFirstRuntimeTimeoutPreservesInFlightOwnerAndRetryAdoptsHandle(t *testing.T) {
	t.Cleanup(func() { _ = ShutdownAllFirstRuntimes(context.Background()) })

	metaDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	triggerFile := filepath.Join(metaDir, "go-ready")
	spawnCountFile := filepath.Join(metaDir, "spawns.log")

	_, prepared := newSyntheticFirstRuntimeFixture(t, "launcher", map[string]string{
		"MEMLANE_BOOTSTRAP_TRIGGER_FILE":     triggerFile,
		"MEMLANE_BOOTSTRAP_SPAWN_COUNT_FILE": spawnCountFile,
	})

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 140*time.Millisecond)
	defer shortCancel()
	inFlight, err := StartFirstRuntimeHandle(
		shortCtx,
		prepared,
		WithFirstRuntimeReadyPollInterval(25*time.Millisecond),
		WithFirstRuntimeStopTimeouts(2*time.Second, 2*time.Second),
	)
	if err == nil || inFlight == nil {
		t.Fatalf("expected timeout error with preserved in-flight handle, got handle=%v err=%v", inFlight, err)
	}
	inFlightPID := inFlight.Snapshot().Services[0].PID
	if err := syscall.Kill(inFlightPID, 0); err != nil {
		t.Fatalf("expected in-flight launcher %d preserved after timeout, got %v", inFlightPID, err)
	}

	// Release the in-flight launcher to ready phase and retry StartFirstRuntimeHandle.
	if err := os.WriteFile(triggerFile, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	retryCtx, retryCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer retryCancel()
	adopted, err := StartFirstRuntimeHandle(retryCtx, prepared, WithFirstRuntimeReadyPollInterval(25*time.Millisecond))
	if err != nil {
		t.Fatalf("retry after timeout failed: %v", err)
	}
	if adopted != inFlight {
		t.Fatal("expected retry to adopt the same in-flight handle without duplicating launcher")
	}
	spawns, err := os.ReadFile(spawnCountFile)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(spawns)), "\n"); len(lines) != 1 {
		t.Fatalf("expected exactly 1 spawn across timeout + retry, got %v", lines)
	}
	if err := adopted.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown adopted handle failed: %v", err)
	}
	waitPIDDead(t, inFlightPID)
}

func TestStartFirstRuntimeCrashRedactsSecretsAndLeavesNoLiveHandle(t *testing.T) {
	t.Cleanup(func() { _ = ShutdownAllFirstRuntimes(context.Background()) })

	fix, prepared := newSyntheticFirstRuntimeFixture(t, "crash", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	handle, err := StartFirstRuntimeHandle(ctx, prepared, WithFirstRuntimeReadyPollInterval(20*time.Millisecond))
	if err == nil || handle != nil {
		t.Fatalf("expected crash failure, got handle=%v err=%v", handle, err)
	}
	if !strings.Contains(err.Error(), "首次运行入口退出") {
		t.Fatalf("unexpected crash error: %v", err)
	}
	if _, ok := FirstRuntimeSupervisor(fix.configPath); ok {
		t.Fatal("crashed launcher must not remain registered")
	}

	logPath := filepath.Join(fix.parent, "runtime-logs", "launcher.log")
	tail, err := supervisor.ReadLogTail(logPath, 50, 64*1024)
	if err != nil {
		t.Fatalf("ReadLogTail failed: %v", err)
	}
	joined := strings.Join(tail, "\n")
	if strings.Contains(joined, "leak-me-never-token") || strings.Contains(joined, "super-secret-value") {
		t.Fatalf("secrets leaked in redacted tail: %q", joined)
	}
	if !strings.Contains(joined, "Bearer [REDACTED]") || !strings.Contains(joined, "api_key=[REDACTED]") {
		t.Fatalf("expected redacted credentials in tail, got %q", joined)
	}
}

func TestStartFirstRuntimeSupervisedReverseShutdownHealthDebounceAndRaceSafety(t *testing.T) {
	t.Cleanup(func() { _ = ShutdownAllFirstRuntimes(context.Background()) })

	fix, _ := newSyntheticFirstRuntimeFixture(t, "launcher", nil)
	logsDir := filepath.Join(fix.parent, "runtime-logs")
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	orderFile := filepath.Join(fix.parent, "order.log")
	grandchildFile := filepath.Join(fix.parent, "grandchild.pid")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	specs := []supervisor.ServiceSpec{
		{
			Role:    "official",
			Command: exe,
			Args:    []string{"-test.run=^TestFirstRuntimeHelperProcess$"},
			Env: []string{
				"MEMLANE_BOOTSTRAP_HELPER=1",
				"MEMLANE_BOOTSTRAP_MODE=service",
				"MEMLANE_BOOTSTRAP_ROLE=official",
				"MEMLANE_BOOTSTRAP_ADDR=" + fix.serviceAddr,
				"MEMLANE_BOOTSTRAP_ORDER_FILE=" + orderFile,
				"MEMLANE_BOOTSTRAP_GRANDCHILD_FILE=" + grandchildFile,
			},
			LogPath:      filepath.Join(logsDir, "official.log"),
			BindURL:      "http://" + fix.serviceAddr,
			HealthURL:    "http://" + fix.serviceAddr + "/health",
			ReadyTimeout: 5 * time.Second,
		},
		{
			Role:    "project_control",
			Command: exe,
			Args:    []string{"-test.run=^TestFirstRuntimeHelperProcess$"},
			Env: []string{
				"MEMLANE_BOOTSTRAP_HELPER=1",
				"MEMLANE_BOOTSTRAP_MODE=service_ignore_sigterm",
				"MEMLANE_BOOTSTRAP_ROLE=project_control",
				"MEMLANE_BOOTSTRAP_ADDR=" + fix.controlAddr,
				"MEMLANE_BOOTSTRAP_ORDER_FILE=" + orderFile,
			},
			LogPath:      filepath.Join(logsDir, "project_control.log"),
			BindURL:      "http://" + fix.controlAddr,
			HealthURL:    "http://" + fix.controlAddr + "/health",
			ReadyTimeout: 5 * time.Second,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	handle, err := StartFirstRuntimeSupervised(
		ctx,
		fix.configPath,
		specs,
		WithFirstRuntimeMonitorInterval(40*time.Millisecond),
		WithFirstRuntimeHealthPolicy(3, 250*time.Millisecond),
		WithFirstRuntimeStopTimeouts(250*time.Millisecond, 2*time.Second),
	)
	if err != nil {
		t.Fatalf("StartFirstRuntimeSupervised failed: %v", err)
	}

	// Single-instance .service-owner lock must reject a second supervised start.
	if _, err := StartFirstRuntimeSupervised(ctx, fix.configPath, specs); !errors.Is(err, supervisor.ErrOwnerLocked) {
		t.Fatalf("expected ErrOwnerLocked on duplicate supervised start, got %v", err)
	}

	// FirstRuntimeReady must accept the Go-supervised stack.
	if ready, err := FirstRuntimeReady(ctx, fix.configPath); err != nil || !ready {
		t.Fatalf("expected FirstRuntimeReady=true on supervised stack, got ready=%v err=%v", ready, err)
	}

	// Debounced health check: failing project_control transitions to degraded, then recovers to ready.
	resp, err := http.Get("http://" + fix.controlAddr + "/fail?state=1")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	waitState := func(want supervisor.ServiceState) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if handle.Snapshot().State == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for state %q (current=%+v)", want, handle.Snapshot())
	}
	waitState(supervisor.StateDegraded)

	resp, err = http.Get("http://" + fix.controlAddr + "/fail?state=0")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	waitState(supervisor.StateReady)

	var grandchildPID int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b, readErr := os.ReadFile(grandchildFile); readErr == nil && len(bytes.TrimSpace(b)) > 0 {
			grandchildPID, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			if grandchildPID > 1 {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if grandchildPID <= 1 {
		t.Fatal("expected grandchild PID recorded")
	}

	snap := handle.Snapshot()
	officialPID := snap.Services[0].PID
	controlPID := snap.Services[1].PID

	// Concurrent Snapshot reads + ShutdownAllFirstRuntimes under -race.
	var wg sync.WaitGroup
	stopReaders := make(chan struct{})
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopReaders:
					return
				default:
					_ = handle.Snapshot()
				}
			}
		}()
	}
	if err := ShutdownAllFirstRuntimes(context.Background()); err != nil {
		close(stopReaders)
		wg.Wait()
		t.Fatalf("ShutdownAllFirstRuntimes failed: %v", err)
	}
	close(stopReaders)
	wg.Wait()

	waitPIDDead(t, officialPID)
	waitPIDDead(t, controlPID)
	waitPIDDead(t, grandchildPID)

	if _, err := os.Stat(filepath.Join(fix.storeRoot, ".memlane-dev-owner.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected owner receipt removed after clean reverse shutdown, got %v", err)
	}

	orderBytes, err := os.ReadFile(orderFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(orderBytes)), "\n")
	if len(lines) != 4 ||
		lines[0] != "official:start" ||
		lines[1] != "project_control:start" ||
		!strings.HasPrefix(lines[2], "project_control:") ||
		!strings.HasPrefix(lines[3], "official:") {
		t.Fatalf("expected strict reverse shutdown order, got %v", lines)
	}
}

func TestStartFirstRuntimeOwnerLockHandoffFromSupervisedToActivationLauncher(t *testing.T) {
	t.Cleanup(func() { _ = ShutdownAllFirstRuntimes(context.Background()) })

	fix, prepared := newSyntheticFirstRuntimeFixture(t, "launcher_activate", nil)
	logsDir := filepath.Join(fix.parent, "runtime-logs")
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	// Start in first_install_setup with direct Go-owned supervision holding .service-owner.
	specs := []supervisor.ServiceSpec{
		{
			Role:    "official",
			Command: exe,
			Args:    []string{"-test.run=^TestFirstRuntimeHelperProcess$"},
			Env: []string{
				"MEMLANE_BOOTSTRAP_HELPER=1",
				"MEMLANE_BOOTSTRAP_MODE=service",
				"MEMLANE_BOOTSTRAP_ROLE=official",
				"MEMLANE_BOOTSTRAP_ADDR=" + fix.serviceAddr,
			},
			LogPath:      filepath.Join(logsDir, "official.log"),
			BindURL:      "http://" + fix.serviceAddr,
			HealthURL:    "http://" + fix.serviceAddr + "/health",
			ReadyTimeout: 5 * time.Second,
		},
		{
			Role:    "project_control",
			Command: exe,
			Args:    []string{"-test.run=^TestFirstRuntimeHelperProcess$"},
			Env: []string{
				"MEMLANE_BOOTSTRAP_HELPER=1",
				"MEMLANE_BOOTSTRAP_MODE=service",
				"MEMLANE_BOOTSTRAP_ROLE=project_control",
				"MEMLANE_BOOTSTRAP_ADDR=" + fix.controlAddr,
			},
			LogPath:      filepath.Join(logsDir, "project_control.log"),
			BindURL:      "http://" + fix.controlAddr,
			HealthURL:    "http://" + fix.controlAddr + "/health",
			ReadyTimeout: 5 * time.Second,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	setupHandle, err := StartFirstRuntimeSupervised(
		ctx,
		fix.configPath,
		specs,
		WithFirstRuntimeStopTimeouts(2*time.Second, 2*time.Second),
	)
	if err != nil {
		t.Fatalf("setup StartFirstRuntimeSupervised failed: %v", err)
	}
	setupPIDs := []int{setupHandle.Snapshot().Services[0].PID, setupHandle.Snapshot().Services[1].PID}

	// Hand off to activation launcher: Go-owned setup supervisor must release .service-owner first,
	// allowing the activation launcher to acquire .service-owner and transition to ready + complete.
	prepared.ActivationFile = filepath.Join(fix.storeRoot, "first-install-activation.json")
	activatedHandle, err := StartFirstRuntimeHandle(
		ctx,
		prepared,
		WithFirstRuntimeReadyPollInterval(25*time.Millisecond),
		WithFirstRuntimeStopTimeouts(2*time.Second, 2*time.Second),
	)
	if err != nil {
		t.Fatalf("activation handoff failed: %v", err)
	}
	for _, pid := range setupPIDs {
		waitPIDDead(t, pid)
	}
	if complete, err := FirstInitializationComplete(fix.configPath); err != nil || !complete {
		t.Fatalf("expected FirstInitializationComplete=true after activation handoff, got complete=%v err=%v", complete, err)
	}
	if ready, err := FirstRuntimeReady(ctx, fix.configPath); err != nil || !ready {
		t.Fatalf("expected FirstRuntimeReady=true after activation handoff, got ready=%v err=%v", ready, err)
	}
	activatedPID := activatedHandle.Snapshot().Services[0].PID

	if err := ShutdownAllFirstRuntimes(context.Background()); err != nil {
		t.Fatalf("final shutdown failed: %v", err)
	}
	waitPIDDead(t, activatedPID)
}

func TestStartFirstRuntimeRetryCrashCleansUpHandleAndMonitor(t *testing.T) {
	t.Cleanup(func() { _ = ShutdownAllFirstRuntimes(context.Background()) })

	metaDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	triggerFile := filepath.Join(metaDir, "trigger")
	fix, prepared := newSyntheticFirstRuntimeFixture(t, "launcher", map[string]string{
		"MEMLANE_BOOTSTRAP_TRIGGER_FILE": triggerFile,
	})

	shortCtx, shortCancel := context.WithTimeout(context.Background(), 140*time.Millisecond)
	defer shortCancel()
	inFlight, err := StartFirstRuntimeHandle(
		shortCtx,
		prepared,
		WithFirstRuntimeMonitorInterval(30*time.Millisecond),
		WithFirstRuntimeReadyPollInterval(25*time.Millisecond),
		WithFirstRuntimeStopTimeouts(2*time.Second, 2*time.Second),
	)
	if err == nil || inFlight == nil {
		t.Fatalf("expected timeout with preserved in-flight handle, got handle=%v err=%v", inFlight, err)
	}
	inFlightPID := inFlight.Snapshot().Services[0].PID

	// Trigger crash during retry adoption.
	go func() {
		time.Sleep(60 * time.Millisecond)
		_ = os.WriteFile(triggerFile, []byte("crash"), 0o600)
	}()

	retryCtx, retryCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer retryCancel()
	handle, err := StartFirstRuntimeHandle(retryCtx, prepared, WithFirstRuntimeReadyPollInterval(25*time.Millisecond))
	if err == nil || handle != nil {
		t.Fatalf("expected retry crash error, got handle=%v err=%v", handle, err)
	}
	if !strings.Contains(err.Error(), "首次运行入口退出") {
		t.Fatalf("unexpected retry crash error: %v", err)
	}
	if _, ok := FirstRuntimeSupervisor(fix.configPath); ok {
		t.Fatal("crashed in-flight handle must be unregistered immediately")
	}
	if inFlight.Snapshot().State != supervisor.StateStopped {
		t.Fatalf("expected crashed handle monitor stopped cleanly, got %+v", inFlight.Snapshot())
	}
	waitPIDDead(t, inFlightPID)
}
