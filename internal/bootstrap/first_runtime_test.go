package bootstrap

import (
	"runtime"
	"testing/fstest"

	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	"testing/synctest"
	"time"

	"github.com/zwhu/memlane/internal/supervisor"
)

func TestFirstRuntimeReadinessRequiresLiveOwnerAndBothAuthenticatedAPIs(t *testing.T) {
	t.Parallel()
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
		if r.URL.Path != "/v1/projects" {
			http.NotFound(w, r)
			return
		}
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
	owner["phase"] = "ready"
	owner["generation_reference"] = map[string]any{"id": "synthetic-pi", "revision": 1}
	write(ownerPath, owner)
	if ready, err := FirstRuntimeReady(context.Background(), configPath); err != nil || !ready {
		t.Fatal("Python activation receipt should pass readiness", ready, err)
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
	t.Parallel()
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
	t.Parallel()
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

	// Launcher modes: emulate engines/cognee/runner.py or first_install_runtime.py activation contract.
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
	if resources, err := os.Getwd(); err == nil {
		if hash, managed := RuntimeHashFromDir(resources); managed {
			rec.ResourcesDir, rec.ResourcesHash = resources, hash
		}
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

		generationPath := filepath.Join(storeRoot, "generation.json")
		if _, err := os.Stat(generationPath); err == nil {
			if err := updateActivationJSON(generationPath, map[string]any{"active": FirstGenerationReference{ID: "cand", Revision: 1}}); err != nil {
				os.Exit(9)
			}
		} else {
			gen := map[string]any{
				"schema_version": 2, "active": FirstGenerationReference{ID: "cand", Revision: 1},
				"profiles": map[string]any{"cand": []any{map[string]any{
					"revision": 1, "profile": map[string]string{"mode": "provider", "model": "synthetic-model"},
				}}},
			}
			if err := writeFirstRuntimeJSON(generationPath, gen); err != nil {
				os.Exit(9)
			}
		}

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
	envLines.WriteString("export MEMLANE_BOOTSTRAP_HELPER=1\n")
	fmt.Fprintf(&envLines, "export MEMLANE_BOOTSTRAP_MODE=%q\n", mode)
	fmt.Fprintf(&envLines, "export MEMLANE_BOOTSTRAP_STORE=%q\n", storeRoot)
	fmt.Fprintf(&envLines, "export MEMLANE_BOOTSTRAP_CONFIG=%q\n", configPath)
	fmt.Fprintf(&envLines, "export MEMLANE_BOOTSTRAP_SERVICE_ADDR=%q\n", serviceAddr)
	fmt.Fprintf(&envLines, "export MEMLANE_BOOTSTRAP_CONTROL_ADDR=%q\n", controlAddr)
	for k, v := range extraEnv {
		fmt.Fprintf(&envLines, "export %s=%q\n", k, v)
	}
	fmt.Fprintf(&envLines, "exec %q -test.run=^TestFirstRuntimeHelperProcess$", exe)
	writeWarmBootstrapScript(t, wrapperPath, envLines.String())

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
		Executable: wrapperPath,
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
	t.Parallel()

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
	t.Cleanup(func() { _ = ShutdownFirstRuntime(context.Background(), fix.configPath) })

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
	t.Parallel()

	fix, prepared := newSyntheticFirstRuntimeFixture(t, "launcher", nil)
	t.Cleanup(func() { _ = ShutdownFirstRuntime(context.Background(), fix.configPath) })
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
	t.Parallel()

	metaDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	triggerFile := filepath.Join(metaDir, "go-ready")
	spawnCountFile := filepath.Join(metaDir, "spawns.log")

	fix, prepared := newSyntheticFirstRuntimeFixture(t, "launcher", map[string]string{
		"MEMLANE_BOOTSTRAP_TRIGGER_FILE":     triggerFile,
		"MEMLANE_BOOTSTRAP_SPAWN_COUNT_FILE": spawnCountFile,
	})
	t.Cleanup(func() { _ = ShutdownFirstRuntime(context.Background(), fix.configPath) })

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
	t.Parallel()

	fix, prepared := newSyntheticFirstRuntimeFixture(t, "crash", nil)
	t.Cleanup(func() { _ = ShutdownFirstRuntime(context.Background(), fix.configPath) })

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
	writeBootstrapTestJSON(t, prepared.ActivationFile, FirstActivationRequest{
		Schema: 1, StoreID: fix.storeID, Operation: "first-handoff", Status: "approved",
		Reference: FirstGenerationReference{ID: "cand", Revision: 1}, Digest: strings.Repeat("a", 64), Revision: 1,
	})
	writeBootstrapTestJSON(t, filepath.Join(fix.storeRoot, "generation.json"), map[string]any{
		"schema_version": 2, "revision": 1, "next": FirstGenerationReference{ID: "cand", Revision: 1},
		"profiles": map[string]any{"cand": []any{map[string]any{
			"revision": 1, "digest": strings.Repeat("a", 64),
			"profile": map[string]string{"mode": "provider", "model": "synthetic-model"},
		}}},
		"validations": map[string]any{"validated": map[string]any{
			"digest": strings.Repeat("a", 64), "status": "passed", "plain_passed": true, "schema_passed": true, "quality_passed": true,
		}},
	})
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

func TestStartFirstRuntimeCompletedActivationReplay(t *testing.T) {
	t.Parallel()
	requireRuntimeOwnerTestSupport(t)
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprintf("owner running=%t", running), func(t *testing.T) {
			t.Parallel()
			fix, prepared := newSyntheticFirstRuntimeFixture(t, "launcher_activate", nil)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := ShutdownFirstRuntime(cleanup, fix.configPath); err != nil {
					t.Error(err)
				}
			})
			if err := StartFirstRuntimeDetached(ctx, prepared); err != nil {
				t.Fatal(err)
			}
			owner, err := supervisor.VerifyOwnerRecord(ctx, fix.storeRoot, fix.configPath)
			if err != nil {
				t.Fatal(err)
			}
			if !running {
				if err := ShutdownFirstRuntime(ctx, fix.configPath); err != nil {
					t.Fatal(err)
				}
				waitPIDDead(t, owner.PID)
			}
			prepared.ActivationFile = filepath.Join(fix.storeRoot, "first-install-activation.json")
			writeBootstrapTestJSON(t, prepared.ActivationFile, FirstActivationRequest{
				Schema: 1, StoreID: fix.storeID, Operation: "first-replay", Status: "completed",
			})
			handle, err := StartFirstRuntimeHandle(ctx, prepared, WithFirstRuntimeReadyPollInterval(25*time.Millisecond))
			if err != nil || handle == nil || handle.Attached() != running || handle.Owned() == running {
				t.Fatalf("completed replay handle = %+v, %v", handle, err)
			}
			post, err := supervisor.VerifyOwnerRecord(ctx, fix.storeRoot, fix.configPath)
			if err != nil || (supervisor.OwnerFingerprint(post) == supervisor.OwnerFingerprint(owner)) != running {
				t.Fatalf("owner after completed replay = %+v, %v", post, err)
			}
			if ready, err := FirstRuntimeReady(ctx, fix.configPath); err != nil || !ready {
				t.Fatalf("runtime after completed replay ready = %t, %v", ready, err)
			}
		})
	}
}

func TestStartFirstRuntimeRetryCrashCleansUpHandleAndMonitor(t *testing.T) {
	t.Parallel()

	metaDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	triggerFile := filepath.Join(metaDir, "trigger")
	fix, prepared := newSyntheticFirstRuntimeFixture(t, "launcher", map[string]string{
		"MEMLANE_BOOTSTRAP_TRIGGER_FILE": triggerFile,
	})
	t.Cleanup(func() { _ = ShutdownFirstRuntime(context.Background(), fix.configPath) })

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

func TestAttachedExternalOwnerShutdownStopsVerifiedOwnerAndRefusesForeign(t *testing.T) {
	t.Cleanup(func() { _ = ShutdownAllFirstRuntimes(context.Background()) })

	metaDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	grandchildFile := filepath.Join(metaDir, "grandchild.pid")
	fix, prepared := newSyntheticFirstRuntimeFixture(t, "launcher", map[string]string{
		"MEMLANE_BOOTSTRAP_GRANDCHILD_FILE": grandchildFile,
	})

	// Launch an external owner process (simulating a prior Memlane.app session that kept
	// services alive on quit, or an external dev.py owner holding .service-owner).
	extCmd := exec.Command(fix.wrapperPath)
	extCmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := extCmd.Start(); err != nil {
		t.Fatalf("start external owner failed: %v", err)
	}
	extPID := extCmd.Process.Pid
	extDone := make(chan error, 1)
	go func() { extDone <- extCmd.Wait() }()
	t.Cleanup(func() {
		_ = syscall.Kill(-extPID, syscall.SIGKILL)
		select {
		case <-extDone:
		case <-time.After(time.Second):
		}
	})

	// Wait until the external owner acquires .service-owner and reaches ready state.
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer waitCancel()
	for {
		insp, inspErr := supervisor.InspectOwner(fix.storeRoot)
		ready, _ := FirstRuntimeReady(waitCtx, fix.configPath)
		if inspErr == nil && insp.Locked && ready {
			break
		}
		if waitCtx.Err() != nil {
			t.Fatalf("external owner did not become ready: insp=%+v err=%v", insp, inspErr)
		}
		time.Sleep(30 * time.Millisecond)
	}

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

	// Re-opening Memlane attaches to the locked external owner (owned=false, attached=true).
	handle, err := StartFirstRuntimeHandle(
		waitCtx,
		prepared,
		WithFirstRuntimeReadyPollInterval(25*time.Millisecond),
		WithFirstRuntimeStopTimeouts(2*time.Second, 2*time.Second),
	)
	if err != nil {
		t.Fatalf("StartFirstRuntimeHandle attach failed: %v", err)
	}
	if handle.Owned() {
		t.Fatal("expected attached handle to report Owned()=false")
	}
	if !handle.Attached() {
		t.Fatal("expected attached handle to report Attached()=true")
	}
	if got, ok := FirstRuntimeSupervisor(fix.configPath); !ok || got != handle {
		t.Fatalf("expected attached handle registered in FirstRuntimeSupervisor, got %v %v", got, ok)
	}

	// Foreign config must never stop this external owner.
	foreignCfg := filepath.Join(fix.parent, "foreign-runtime.json")
	if err := supervisor.StopVerifiedOwner(waitCtx, fix.storeRoot, foreignCfg, time.Second, time.Second); !errors.Is(err, supervisor.ErrOwnerMismatch) {
		t.Fatalf("expected ErrOwnerMismatch for foreign config, got %v", err)
	}
	if err := syscall.Kill(extPID, 0); err != nil {
		t.Fatalf("foreign config must not have signaled external owner %d: %v", extPID, err)
	}

	// Explicit quit (ShutdownAllFirstRuntimes) must genuinely stop the attached external owner.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := ShutdownAllFirstRuntimes(stopCtx); err != nil {
		t.Fatalf("ShutdownAllFirstRuntimes failed on attached handle: %v", err)
	}
	select {
	case <-extDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("external owner %d did not exit after ShutdownAllFirstRuntimes", extPID)
	}
	waitPIDDead(t, extPID)
	if grandchildPID > 1 {
		_ = syscall.Kill(grandchildPID, syscall.SIGKILL)
	}

	lock, err := supervisor.AcquireOwner(fix.storeRoot)
	if err != nil {
		t.Fatalf("expected .service-owner cleanly released after attached shutdown, got %v", err)
	}
	_ = lock.Release()
	if _, ok := FirstRuntimeSupervisor(fix.configPath); ok {
		t.Fatal("expected attached handle unregistered after shutdown")
	}
}

func TestShutdownAllFirstRuntimesCancelsInFlightStartupWithoutOrphan(t *testing.T) {
	t.Cleanup(func() { _ = ShutdownAllFirstRuntimes(context.Background()) })

	metaDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	triggerFile := filepath.Join(metaDir, "never-ready")
	grandchildFile := filepath.Join(metaDir, "grandchild.pid")
	fix, prepared := newSyntheticFirstRuntimeFixture(t, "launcher", map[string]string{
		"MEMLANE_BOOTSTRAP_TRIGGER_FILE":    triggerFile,
		"MEMLANE_BOOTSTRAP_GRANDCHILD_FILE": grandchildFile,
	})

	// Also verify TrackStartupContext is canceled by ShutdownAllFirstRuntimes.
	trackedCtx, releaseTracked := TrackStartupContext(context.Background())
	defer releaseTracked()

	startDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := StartFirstRuntimeHandle(
			ctx,
			prepared,
			WithFirstRuntimeReadyPollInterval(25*time.Millisecond),
			WithFirstRuntimeStopTimeouts(2*time.Second, 2*time.Second),
		)
		startDone <- err
	}()

	// Wait until the Setsid launcher & grandchild have spawned and written starting owner receipt,
	// while StartFirstRuntimeHandle is still blocked waiting for readiness.
	var launcherPID, grandchildPID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if rec, _, readErr := supervisor.ReadOwnerRecord(fix.storeRoot); readErr == nil && rec.PID > 1 {
			launcherPID = rec.PID
		}
		if b, readErr := os.ReadFile(grandchildFile); readErr == nil && len(bytes.TrimSpace(b)) > 0 {
			grandchildPID, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		if launcherPID > 1 && grandchildPID > 1 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if launcherPID <= 1 || grandchildPID <= 1 {
		t.Fatalf("expected in-flight launcher (%d) and grandchild (%d) to spawn before readiness", launcherPID, grandchildPID)
	}

	// Simulate keep-alive-disabled quit during the first-startup window.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := ShutdownAllFirstRuntimes(stopCtx); err != nil {
		t.Fatalf("ShutdownAllFirstRuntimes during in-flight startup failed: %v", err)
	}

	if trackedCtx.Err() == nil {
		t.Fatal("expected TrackStartupContext to be canceled by ShutdownAllFirstRuntimes")
	}
	select {
	case err := <-startDone:
		if err == nil {
			t.Fatal("expected canceled StartFirstRuntimeHandle to return error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StartFirstRuntimeHandle hung after ShutdownAllFirstRuntimes")
	}

	waitPIDDead(t, launcherPID)
	waitPIDDead(t, grandchildPID)

	if _, ok := FirstRuntimeSupervisor(fix.configPath); ok {
		t.Fatal("in-flight handle must not remain registered after shutdown")
	}
	lock, err := supervisor.AcquireOwner(fix.storeRoot)
	if err != nil {
		t.Fatalf("expected .service-owner released after in-flight startup shutdown, got %v", err)
	}
	_ = lock.Release()
}

func writeBootstrapTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func newReadOnlyRuntimeFixture(t *testing.T) FirstRuntime {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(home, "library")
	if err := os.Mkdir(store, 0700); err != nil {
		t.Fatal(err)
	}
	storeID := strings.Repeat("c", 32)
	configPath := filepath.Join(home, "runtime.json")
	writeBootstrapTestJSON(t, filepath.Join(store, "store-identity.json"), StoreReceipt{
		Schema: 1, StoreID: storeID, SourceCommit: "fixed", OwnerID: "owner", KeyID: "key", StorageInitialized: true,
	})
	writeBootstrapTestJSON(t, configPath, firstRuntimeConfig{
		Root: store, Commit: "fixed", Python: "/bin/sh", TokenFile: filepath.Join(store, "owner.token"),
		First: &FirstInstallation{Schema: 1, StoreID: storeID, Setup: true},
	})
	return FirstRuntime{
		ConfigPath: configPath, StoreRoot: store, Resources: home, Python: "/bin/sh", HostHome: home,
		Executable: filepath.Join(home, "nonexistent-launcher"),
	}
}

func newRuntimePreparationFixture(t *testing.T, probeScript string) (FirstRuntime, *Environment, fstest.MapFS) {
	t.Helper()
	prepared := newReadOnlyRuntimeFixture(t)
	root := filepath.Join(prepared.HostHome, "components")
	statePath, err := WorkerStatePath(root)
	if err != nil {
		t.Fatal(err)
	}
	probeScript = strings.NewReplacer(
		"@WORKER_STATE@", fmt.Sprintf("%q", statePath),
		"@WORKER_MARKER@", fmt.Sprintf("%q", filepath.Join(prepared.StoreRoot, "preflight-started")),
	).Replace(probeScript)
	python := fakeDependencyPython(t, probeScript)
	config, err := loadFirstRuntimeConfig(prepared.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	config.Python = python
	config.GenerationFile = filepath.Join(prepared.StoreRoot, "generation.json")
	prepared.Python = python
	writeBootstrapTestJSON(t, prepared.ConfigPath, config)

	writeFirstStoreInputFixture(t, prepared, config)
	env := Environment{
		Options: EnvironmentOptions{Root: root, ProtectedStore: prepared.StoreRoot},
		Runtime: RuntimeDriver{ExistingPython: python, Lock: []byte("locked==1\n")},
		Source:  sourceLocations{Managed: SourceDriver{Spec: SourceSpec{Commit: config.Commit}}},
	}
	env.requirements = []Requirement{
		{ID: "runtime", Name: "Runtime", Purpose: "Dependencies", Version: "fixed", Source: "https://example.test/lock",
			Digest: env.Runtime.Digest(), Destination: filepath.Join(root, "runtime")},
	}
	assets := fstest.MapFS{
		"engines/cognee/runner.py": {Data: []byte("# upgraded runtime")},
	}
	return prepared, &env, assets
}

func TestPrepareFirstRuntimeForUpgradePreservesConfig(t *testing.T) {
	t.Parallel()
	prepared, env, assets := newRuntimePreparationFixture(t, `echo '{"mismatch":[]}'`)
	wantConfig, err := os.ReadFile(prepared.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := env.PrepareFirstRuntimeForUpgrade(t.Context(), prepared.ConfigPath, assets)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := BundledRuntimeHash(assets)
	if err != nil {
		t.Fatal(err)
	}
	if got.Resources != filepath.Join(env.Installer().Root, "runtime-"+hash) || !verifyPreparedRuntimeResources(got.Resources, assets) ||
		got.ConfigPath != prepared.ConfigPath || got.StoreRoot != prepared.StoreRoot || got.Python != prepared.Python || got.HostHome == "" || !got.Plan.Complete() {
		t.Fatalf("unexpected upgrade preparation: %+v", got)
	}
	assertConfig := func(t *testing.T) {
		t.Helper()
		if data, err := os.ReadFile(prepared.ConfigPath); err != nil || !bytes.Equal(data, wantConfig) {
			t.Fatalf("upgrade preparation changed runtime.json: %v", err)
		}
	}
	assertConfig(t)
	if err := env.Installer().withLock(func() error {
		reused, err := env.PrepareFirstRuntimeForUpgrade(t.Context(), prepared.ConfigPath, assets)
		if err != nil || reused.Resources != got.Resources {
			t.Fatalf("upgrade preflight with installation.lock held = %+v, %v", reused, err)
		}
		assertConfig(t)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Run("missing inputs", func(t *testing.T) {
		if err := os.Remove(filepath.Join(prepared.StoreRoot, "first-install-input.json")); err != nil {
			t.Fatal(err)
		}
		if _, err := env.PrepareFirstRuntimeForUpgrade(t.Context(), prepared.ConfigPath, assets); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("upgrade accepted a store without its inputs: %v", err)
		}
		assertConfig(t)
	})
	if err := os.WriteFile(filepath.Join(got.Resources, "unexpected.py"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if verifyPreparedRuntimeResources(got.Resources, assets) {
		t.Fatal("verified runtime generation with an unexpected module")
	}
}

func TestEvaluateZeroDownloads(t *testing.T) {
	t.Parallel()
	const currentHash, oldHash = "new-runtime", "old-runtime"
	plan := Plan{Items: []Item{{Observation: Observation{Status: Available}}}}
	zero := &RuntimeUpgradeReceipt{ToRuntimeHash: currentHash, ZeroDownloads: true}
	used := &RuntimeUpgradeReceipt{ToRuntimeHash: currentHash}
	oldReceipt := &RuntimeUpgradeReceipt{ToRuntimeHash: oldHash}
	historical := InstallWorkerState{State: "completed", RuntimeHash: oldHash, NetworkBytesDownloaded: 123456, RuntimeSyncRan: true}

	for _, tc := range []struct {
		name       string
		bundled    string
		plan       Plan
		journal    *Journal
		worker     *InstallWorkerState
		receipt    *RuntimeUpgradeReceipt
		wantReason string
		wantBytes  int64
		wantSync   bool
	}{
		{name: "beta.2 to beta.3 without worker", bundled: currentHash, plan: plan, receipt: zero},
		{name: "beta.3 to beta.3.1 with historical downloads", bundled: currentHash, plan: plan, worker: &historical, receipt: zero},
		{name: "fresh install with downloads", bundled: currentHash, plan: plan,
			worker:     &InstallWorkerState{State: "completed", RuntimeHash: currentHash, NetworkBytesDownloaded: 42},
			wantReason: "current_generation_downloaded", wantBytes: 42},
		{name: "current pip sync without reported HTTP bytes", bundled: currentHash, plan: plan,
			worker:     &InstallWorkerState{State: "completed", RuntimeHash: currentHash, RuntimeSyncRan: true},
			wantReason: "current_generation_downloaded", wantSync: true},
		{name: "upgrade receipt reports downloads", bundled: currentHash, plan: plan, receipt: used, wantReason: "upgrade_used_downloads"},
		{name: "unknown worker generation", bundled: currentHash, plan: plan,
			worker:     &InstallWorkerState{State: "completed", NetworkBytesDownloaded: 19},
			wantReason: "current_generation_downloaded", wantBytes: 19},
		{name: "unknown bundled generation", plan: plan, worker: &historical,
			wantReason: "current_generation_downloaded", wantBytes: historical.NetworkBytesDownloaded, wantSync: true},
		{name: "historical upgrade receipt", bundled: currentHash, plan: plan, receipt: oldReceipt},
		{name: "active historical worker", bundled: currentHash, plan: plan,
			worker: &InstallWorkerState{State: "starting", RuntimeHash: oldHash}, wantReason: "install_worker_active"},
		{name: "running worker", bundled: currentHash, plan: plan,
			worker: &InstallWorkerState{State: "running"}, wantReason: "install_worker_active"},
		{name: "installing journal", bundled: currentHash, plan: plan,
			journal: &Journal{Status: Installing}, wantReason: "installation_in_progress"},
		{name: "active journal step", bundled: currentHash, plan: plan,
			journal: &Journal{ActiveStep: "runtime"}, wantReason: "installation_in_progress"},
		{name: "incomplete plan", bundled: currentHash, wantReason: "components_incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var journal Journal
			var worker InstallWorkerState
			if tc.journal != nil {
				journal = *tc.journal
			}
			if tc.worker != nil {
				worker = *tc.worker
			}
			check := EvaluateZeroDownloads(tc.plan, journal, tc.journal != nil, worker, tc.worker != nil, tc.bundled, tc.receipt)
			want := ZeroDownloadCheck{
				Satisfied: tc.wantReason == "", Reason: tc.wantReason, CurrentRuntimeHash: tc.bundled,
				NetworkBytesDownloaded: tc.wantBytes, RuntimeSyncRan: tc.wantSync, UpgradeReceipt: tc.receipt,
			}
			if check != want {
				t.Fatalf("zero-download check = %+v, want %+v", check, want)
			}
		})
	}
}

func TestInspectOwnerRuntimeStatusReadOnly(t *testing.T) {
	t.Parallel()
	prepared := newReadOnlyRuntimeFixture(t)
	assets := fstest.MapFS{"engines/cognee/runner.py": {Data: []byte("new runtime")}}
	config, err := loadFirstRuntimeConfig(prepared.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	component := filepath.Join(prepared.HostHome, "missing components")
	old := filepath.Join(component, "installation", "runtime-1111111111111111")
	config.GraphAssets, _ = json.Marshal(filepath.Join(old, graphAssetsSuffix))
	writeBootstrapTestJSON(t, prepared.ConfigPath, config)
	before, err := os.ReadFile(prepared.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	status, err := InspectOwnerRuntimeStatus(context.Background(), prepared.ConfigPath, component, assets)
	if err != nil || status.State != "stopped" || !status.GraphAssetsStale || status.OwnerLocked || status.HasReceipt {
		t.Fatal(status, err)
	}
	if _, err := os.Stat(component); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if after, err := os.ReadFile(prepared.ConfigPath); !bytes.Equal(before, after) || err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(prepared.StoreRoot); err != nil || len(entries) != 1 || entries[0].Name() != "store-identity.json" {
		t.Fatal(entries, err)
	}
	lock, err := supervisor.AcquireOwner(prepared.StoreRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	status, err = InspectOwnerRuntimeStatus(context.Background(), prepared.ConfigPath, component, assets)
	if err != nil || status.State != "starting" || !status.OwnerLocked || status.HasReceipt {
		t.Fatal(status, err)
	}
}

func TestInspectOwnerRuntimeStatusFiltersUpgradeReceipt(t *testing.T) {
	t.Parallel()
	prepared := newReadOnlyRuntimeFixture(t)
	root := filepath.Join(prepared.HostHome, "components")
	config, err := loadFirstRuntimeConfig(prepared.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	path, err := RuntimeUpgradeReceiptPath(root, prepared.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareInstallationDir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	assets := fstest.MapFS{"engines/cognee/runner.py": {Data: []byte("new runtime")}}
	hash, err := BundledRuntimeHash(assets)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, storeID, hash string
		assets              fs.FS
		attached            bool
	}{
		{"matching generation and store", config.First.StoreID, hash, assets, true},
		{"unknown bundled generation", config.First.StoreID, hash, nil, true},
		{"different generation", config.First.StoreID, "previous", assets, false},
		{"different store", "another-store", hash, assets, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := RuntimeUpgradeReceipt{
				Schema: 1, StoreID: tc.storeID, ToRuntimeHash: tc.hash, OwnerFingerprint: "verified-owner",
			}
			writeBootstrapTestJSON(t, path, receipt)
			status, err := InspectOwnerRuntimeStatus(t.Context(), prepared.ConfigPath, root, tc.assets)
			if err != nil || (status.UpgradeReceipt != nil) != tc.attached ||
				(tc.attached && *status.UpgradeReceipt != receipt) {
				t.Fatalf("attached upgrade receipt = %+v, %v; want attached=%t", status.UpgradeReceipt, err, tc.attached)
			}
			got, err := ReadRuntimeUpgradeReceipt(root, prepared.ConfigPath)
			if err != nil || got != receipt {
				t.Fatalf("retained upgrade receipt = %+v, %v; want %+v", got, err, receipt)
			}
		})
	}
}

func TestInspectOwnerRuntimeStatusSurvivesCorruptUpgradeReceipt(t *testing.T) {
	t.Parallel()
	const token = "sk-upgrade-receipt-fixture"
	const oldHash = "1111111111111111"
	assets := fstest.MapFS{"engines/cognee/runner.py": {Data: []byte("new runtime")}}
	for _, mode := range []string{"corrupt JSON", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			prepared := newReadOnlyRuntimeFixture(t)
			component := filepath.Join(prepared.HostHome, "components-"+token)
			path, err := RuntimeUpgradeReceiptPath(component, prepared.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			oldRuntime := filepath.Join(filepath.Dir(path), "runtime-"+oldHash)
			if err := os.MkdirAll(oldRuntime, 0700); err != nil {
				t.Fatal(err)
			}
			config, err := loadFirstRuntimeConfig(prepared.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			graphAssets := filepath.Join(oldRuntime, graphAssetsSuffix)
			config.GraphAssets, _ = json.Marshal(graphAssets)
			writeBootstrapTestJSON(t, prepared.ConfigPath, config)
			if mode == "corrupt JSON" {
				if err := os.WriteFile(path, []byte(`{"token":"`+token+`",`), 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(filepath.Join(prepared.HostHome, "missing-receipt"), path); err != nil {
				t.Fatal(err)
			}
			readStatus := func(t *testing.T) OwnerRuntimeStatus {
				t.Helper()
				status, err := InspectOwnerRuntimeStatus(t.Context(), prepared.ConfigPath, component, assets)
				if err != nil {
					t.Fatalf("inspect owner status: %v", err)
				}
				if status.UpgradeReceipt != nil || status.UpgradeReceiptError == "" || strings.Contains(status.UpgradeReceiptError, token) {
					t.Fatalf("unexpected upgrade receipt diagnostics: receipt=%+v error=%q", status.UpgradeReceipt, status.UpgradeReceiptError)
				}
				if status.GraphAssetsPath != graphAssets || !status.GraphAssetsStale {
					t.Fatal("graph assets status was not populated")
				}
				return status
			}
			status := readStatus(t)
			if status.State != "stopped" || status.OwnerLocked || status.HasReceipt || status.OwnerPID != 0 ||
				status.OwnerFingerprint != "" || status.RunningRuntimeHash != "" || status.StaleVersionDetected {
				t.Fatalf("unexpected stopped owner status: %+v", status)
			}
			t.Run("live owner", func(t *testing.T) {
				pid := os.Getpid()
				started, err := supervisor.ProcessStartTime(t.Context(), pid)
				if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
					t.Skipf("ps unavailable in sandbox: %v", err)
				}
				if err != nil {
					t.Fatal(err)
				}
				lock, err := supervisor.AcquireOwner(prepared.StoreRoot)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Release()
				defer lock.RemoveRecord()
				record := supervisor.OwnerRecord{
					PID: pid, Started: started, Phase: "ready", Config: prepared.ConfigPath,
					OfficialData: prepared.StoreRoot, ResourcesDir: oldRuntime, ResourcesHash: oldHash,
				}
				if err := lock.WriteRecord(record); err != nil {
					t.Fatal(err)
				}
				status := readStatus(t)
				if status.State != "stale_runtime" || !status.OwnerLocked || !status.HasReceipt || status.OwnerPID != pid ||
					status.OwnerFingerprint != supervisor.OwnerFingerprint(record) || status.RunningRuntimeHash != oldHash || !status.StaleVersionDetected {
					t.Fatalf("unexpected live owner status: %+v", status)
				}
			})
		})
	}
}

func TestInspectOwnerRuntimeStatusLiveBeta2Argv(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("KERN_PROCARGS2 requires macOS")
	}
	prepared := newReadOnlyRuntimeFixture(t)
	component := filepath.Join(prepared.HostHome, "components with spaces")
	install := filepath.Join(component, "installation")
	old := filepath.Join(install, "runtime-1111111111111111")
	assets := fstest.MapFS{"engines/cognee/runner.py": {Data: []byte("new runtime")}}
	lock, err := supervisor.AcquireOwner(prepared.StoreRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	defer lock.RemoveRecord()

	for _, tc := range []struct {
		name, script string
		developer    bool
	}{
		{"managed", filepath.Join(old, "engines/cognee/runner.py"), false},
		{"checkout", filepath.Join(prepared.HostHome, "developer repo", "engines/cognee/service.py"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("/bin/sh", "-c", "read -r line", "fixture", tc.script)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
			argv, err := supervisor.ProcessArgs(cmd.Process.Pid)
			if errors.Is(err, syscall.EPERM) {
				t.Skipf("kernel argv unavailable in sandbox: %v", err)
			}
			if err != nil || len(argv) < 5 || argv[4] != tc.script {
				t.Fatal(argv, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			started, err := supervisor.ProcessStartTime(ctx, cmd.Process.Pid)
			if errors.Is(err, syscall.EPERM) {
				t.Skipf("ps unavailable in sandbox: %v", err)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := lock.WriteRecord(supervisor.OwnerRecord{
				PID: cmd.Process.Pid, Started: started, Phase: "ready", Config: prepared.ConfigPath, OfficialData: prepared.StoreRoot,
			}); err != nil {
				t.Fatal(err)
			}
			status, err := InspectOwnerRuntimeStatus(ctx, prepared.ConfigPath, component, assets)
			if err != nil || status.DeveloperCheckout != tc.developer || status.StaleVersionDetected == tc.developer || status.OwnerFingerprint == "" {
				t.Fatal(status, err)
			}
			if !tc.developer && (status.State != "stale_runtime" || status.RunningRuntimeDir != old || status.RunningRuntimeHash != "1111111111111111") {
				t.Fatal(status)
			}
		})
	}
}

func TestDetectOwnerRuntimeUsesServiceChildAndRejectsCheckout(t *testing.T) {
	prepared := newReadOnlyRuntimeFixture(t)
	install := filepath.Join(prepared.HostHome, "installation with spaces")
	old := filepath.Join(install, "runtime-1111111111111111")
	if err := os.MkdirAll(old, 0700); err != nil {
		t.Fatal(err)
	}
	rec := supervisor.OwnerRecord{PID: 42, ServiceChildren: map[string]int{"control": 43}}
	original := processArgsFn
	t.Cleanup(func() { processArgsFn = original })
	processArgsFn = func(pid int) ([]string, error) {
		if pid == 43 {
			return []string{"/bin/python3", filepath.Join(old, "engines/cognee/control.py")}, nil
		}
		return nil, os.ErrNotExist
	}
	var status OwnerRuntimeStatus
	detectOwnerRuntime(&status, install, rec)
	if status.RunningRuntimeDir != old || status.RunningRuntimeHash != "1111111111111111" || status.DeveloperCheckout {
		t.Fatal(status)
	}

	rec.ResourcesDir = old
	processArgsFn = func(int) ([]string, error) { return nil, os.ErrNotExist }
	status = OwnerRuntimeStatus{}
	detectOwnerRuntime(&status, install, rec)
	if status.RunningRuntimeDir != old {
		t.Fatal("receipt fallback", status)
	}

	processArgsFn = func(int) ([]string, error) {
		return []string{"/bin/python3", filepath.Join(prepared.HostHome, "installation with spaces-other", "engines/cognee/runner.py")}, nil
	}
	status = OwnerRuntimeStatus{}
	detectOwnerRuntime(&status, install, rec)
	if !status.DeveloperCheckout || status.RunningRuntimeDir != "" {
		t.Fatal(status)
	}
}

func TestRestartStaleRuntimeOwnerRejectsUnboundOwner(t *testing.T) {
	t.Parallel()
	prepared := newReadOnlyRuntimeFixture(t)
	install := filepath.Join(prepared.HostHome, "installation")
	prepared.Resources = filepath.Join(install, "runtime-1111111111111111")
	if err := os.Mkdir(install, 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := RestartStaleRuntimeOwner(ctx, prepared, "", nil); err == nil {
		t.Fatal("empty fingerprint accepted")
	}
	lock, err := supervisor.AcquireUpgradeLock(prepared.StoreRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := RestartStaleRuntimeOwnerDetached(ctx, prepared, "fingerprint", nil); !errors.Is(err, supervisor.ErrUpgradeLocked) {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := RestartStaleRuntimeOwner(ctx, prepared, "fingerprint", nil); !errors.Is(err, supervisor.ErrOwnerFingerprintMismatch) {
		t.Fatal(err)
	}
	if _, err := os.Stat(prepared.Resources); !os.IsNotExist(err) {
		t.Fatal("resources unexpectedly changed", err)
	}
}

func TestStartFirstRuntimeRefusesHeldUpgradeLock(t *testing.T) {
	t.Parallel()
	prepared := newReadOnlyRuntimeFixture(t)
	lock, err := supervisor.AcquireUpgradeLock(prepared.StoreRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	ctx := t.Context()
	if _, err := StartFirstRuntimeHandle(ctx, prepared); !errors.Is(err, supervisor.ErrUpgradeLocked) {
		t.Fatalf("handle startup returned %v", err)
	}
	if err := StartFirstRuntimeDetached(ctx, prepared); !errors.Is(err, supervisor.ErrUpgradeLocked) {
		t.Fatalf("detached startup returned %v", err)
	}
	if _, err := StartFirstRuntimeSupervised(ctx, prepared.ConfigPath, nil); !errors.Is(err, supervisor.ErrUpgradeLocked) {
		t.Fatalf("supervised startup returned %v", err)
	}
}

func TestVerifyRestartedRuntimeGeneration(t *testing.T) {
	t.Parallel()
	const bundledHash = "2222222222222222"
	for _, tc := range []struct {
		name string
		post OwnerRuntimeStatus
		ok   bool
	}{
		{name: "matching hash", post: OwnerRuntimeStatus{OwnerLocked: true, HasReceipt: true, State: "ready", RunningRuntimeHash: bundledHash}, ok: true},
		{name: "starting", post: OwnerRuntimeStatus{OwnerLocked: true, HasReceipt: true, State: "starting", RunningRuntimeHash: bundledHash}},
		{name: "stale graph assets", post: OwnerRuntimeStatus{OwnerLocked: true, HasReceipt: true, State: "ready", GraphAssetsStale: true, RunningRuntimeHash: bundledHash}},
		{name: "wrong hash", post: OwnerRuntimeStatus{OwnerLocked: true, HasReceipt: true, State: "ready", RunningRuntimeHash: "1111111111111111"}},
		{name: "missing hash", post: OwnerRuntimeStatus{OwnerLocked: true, HasReceipt: true, State: "ready"}},
		{name: "unlocked", post: OwnerRuntimeStatus{HasReceipt: true, State: "ready", RunningRuntimeHash: bundledHash}},
		{name: "missing receipt", post: OwnerRuntimeStatus{OwnerLocked: true, State: "ready", RunningRuntimeHash: bundledHash}},
		{name: "developer checkout", post: OwnerRuntimeStatus{OwnerLocked: true, HasReceipt: true, State: "ready", DeveloperCheckout: true, RunningRuntimeHash: bundledHash}},
		{name: "stale generation", post: OwnerRuntimeStatus{OwnerLocked: true, HasReceipt: true, State: "ready", StaleVersionDetected: true, RunningRuntimeHash: bundledHash}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyRestartedRuntimeGeneration(tc.post, bundledHash)
			if tc.ok {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "restarted runtime generation mismatch") {
				t.Fatalf("expected generation mismatch, got %v", err)
			}
		})
	}
}

func TestStartFirstRuntimeDetachedKeepsLauncherOnTimeout(t *testing.T) {
	t.Parallel()
	prepared := newReadOnlyRuntimeFixture(t)
	script := filepath.Join(prepared.HostHome, "launcher.sh")
	childPID := filepath.Join(prepared.HostHome, "launcher.pid")
	writeWarmBootstrapScript(t, script, "echo $$ > \"$HOME/launcher.pid\"\necho launcher-ready-to-wait\nexec /bin/sleep 30\n")
	prepared.Executable = script
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := StartFirstRuntimeDetached(ctx, prepared)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(childPID)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		t.Fatal(pid, err)
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatal("launcher stopped on timeout", err)
	}
	if group, err := syscall.Getpgid(pid); group != pid || err != nil {
		t.Fatal("launcher not detached", group, err)
	}
	logPath := filepath.Join(prepared.HostHome, "runtime-logs", "launcher.log")
	if info, err := os.Stat(logPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	if log, err := os.ReadFile(logPath); err != nil || !strings.Contains(string(log), "launcher-ready-to-wait") {
		t.Fatal(string(log), err)
	}
}

func TestStartFirstRuntimeDetachedReportsEarlyExit(t *testing.T) {
	t.Parallel()
	prepared := newReadOnlyRuntimeFixture(t)
	script := filepath.Join(prepared.HostHome, "launcher.sh")
	writeWarmBootstrapScript(t, script, "exit 7\n")
	prepared.Executable = script
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var exitErr *exec.ExitError
	if err := StartFirstRuntimeDetached(ctx, prepared); !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatal(err)
	}
}

func TestStartFirstRuntimeDetachedObservesOwnerAfterLauncherExit(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"releases lock", "different config"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			// FIFO rendezvous uses wall time while the launcher is blocked in a syscall.
			signalCtx, stopSignal := context.WithTimeout(t.Context(), 3*time.Second)
			defer stopSignal()
			signalDeadline := signalCtx.Done()
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			synctest.Test(t, func(t *testing.T) {
				prepared := newReadOnlyRuntimeFixture(t)
				fifo := filepath.Join(prepared.HostHome, "launcher-exit")
				if err := syscall.Mkfifo(fifo, 0600); err != nil {
					t.Fatal(err)
				}
				script := filepath.Join(prepared.HostHome, "launcher.sh")
				writeWarmBootstrapScript(t, script, "exec 3<> \"$HOME/launcher-exit\"\nread -r -t 5 line <&3\nexit 1\n")
				prepared.Executable = script
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- StartFirstRuntimeDetached(ctx, prepared) }()
				signal := openDetachedTestSignal(t, signalDeadline, fifo, done, ticker.C)
				owner, err := supervisor.AcquireOwner(prepared.StoreRoot)
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Release()
				if _, err := signal.WriteString("\n"); err != nil {
					t.Fatal(err)
				}
				if err := signal.Close(); err != nil {
					t.Fatal(err)
				}
				// The launcher has exited and readiness is waiting on the next tick.
				synctest.Wait()
				select {
				case err := <-done:
					t.Fatalf("launcher exit ended observation of the competing owner: %v", err)
				default:
				}
				if mode == "releases lock" {
					if err := owner.Release(); err != nil {
						t.Fatal(err)
					}
					if err := <-done; err == nil || err.Error() != "competing runtime owner exited before readiness" {
						t.Fatalf("detached startup = %v, want competing owner exit", err)
					}
				} else {
					writeBootstrapTestJSON(t, filepath.Join(prepared.StoreRoot, ".memlane-dev-owner.json"), supervisor.OwnerRecord{
						PID: os.Getpid(), Config: filepath.Join(prepared.HostHome, "foreign.json"),
						OfficialData: prepared.StoreRoot, Phase: "starting",
					})
					if err := <-done; !errors.Is(err, supervisor.ErrOwnerMismatch) {
						t.Fatalf("detached startup = %v, want owner mismatch", err)
					}
				}
				upgrade, err := supervisor.AcquireUpgradeLock(prepared.StoreRoot)
				if err != nil {
					t.Fatalf("upgrade lock retained: %v", err)
				}
				upgrade.Release()
			})
		})
	}
}

func TestStartFirstRuntimeDetachedCompetingOwner(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"releases lock", "different config", "different process", "invalid receipt", "unexpected phase"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			prepared := newReadOnlyRuntimeFixture(t)
			lock, err := supervisor.AcquireOwner(prepared.StoreRoot)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { lock.Release() })
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()

			receiptPath := filepath.Join(prepared.StoreRoot, ".memlane-dev-owner.json")
			var want string
			switch mode {
			case "different config":
				writeBootstrapTestJSON(t, receiptPath, supervisor.OwnerRecord{
					PID: os.Getpid(), Config: filepath.Join(prepared.HostHome, "foreign.json"),
					OfficialData: prepared.StoreRoot, Phase: "starting",
				})
				want = supervisor.ErrOwnerMismatch.Error()
			case "different process":
				writeBootstrapTestJSON(t, receiptPath, supervisor.OwnerRecord{
					PID: os.Getpid(), Started: "different kernel identity", Config: prepared.ConfigPath,
					OfficialData: prepared.StoreRoot, Phase: "starting",
				})
				want = supervisor.ErrOwnerProcessChanged.Error()
			case "invalid receipt":
				if err := os.WriteFile(receiptPath, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
				want = "unexpected end of JSON input"
			case "unexpected phase":
				writeBootstrapTestJSON(t, receiptPath, supervisor.OwnerRecord{
					PID: os.Getpid(), Config: prepared.ConfigPath,
					OfficialData: prepared.StoreRoot, Phase: "stopping",
				})
				want = `competing runtime owner entered unexpected phase "stopping" before readiness`
			default:
				want = "competing runtime owner exited before readiness"
			}
			done := make(chan error, 1)
			go func() { done <- StartFirstRuntimeDetached(ctx, prepared) }()
			if mode == "releases lock" {
				for {
					locked, _, err := supervisor.ProbeFileLockedReadOnly(filepath.Join(prepared.StoreRoot, ".memlane-upgrade.lock"))
					if err != nil {
						t.Fatal(err)
					}
					if locked {
						break
					}
					select {
					case err := <-done:
						t.Fatalf("detached startup exited before attaching: %v", err)
					case <-ctx.Done():
						t.Fatal("detached startup never acquired the upgrade lock")
					case <-time.After(5 * time.Millisecond):
					}
				}
				lock.Release()
			}
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("detached startup = %v, want %q", err, want)
				}
			case <-ctx.Done():
				t.Fatal("detached startup did not return promptly")
			}
			upgrade, err := supervisor.AcquireUpgradeLock(prepared.StoreRoot)
			if err != nil {
				t.Fatalf("upgrade lock retained: %v", err)
			}
			upgrade.Release()
		})
	}
}

func TestWaitFirstRuntimeDetachedRejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	prepared := newReadOnlyRuntimeFixture(t)
	if err := os.WriteFile(prepared.ConfigPath, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err := waitFirstRuntimeDetached(ctx, prepared.StoreRoot, prepared.ConfigPath, make(chan error))
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("detached readiness = %v, want invalid configuration error", err)
	}
}

func requireRuntimeOwnerTestSupport(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skipf("loopback listen unavailable in sandbox: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	if _, err := supervisor.ProcessStartTime(context.Background(), os.Getpid()); errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skipf("ps unavailable in sandbox: %v", err)
	} else if err != nil {
		t.Fatal(err)
	}
}

func TestRestartStaleRuntimeOwnerReplacesVerifiedOwner(t *testing.T) {
	requireRuntimeOwnerTestSupport(t)
	_, prepared := newSyntheticFirstRuntimeFixture(t, "launcher", nil)
	install := filepath.Join(prepared.HostHome, "components", "installation")
	oldAssets := fstest.MapFS{"engines/cognee/runner.py": {Data: []byte("# old runtime")}}
	newAssets := fstest.MapFS{"engines/cognee/runner.py": {Data: []byte("# new runtime")}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	oldResources, err := PrepareRuntimeResources(ctx, install, oldAssets)
	if err != nil {
		t.Fatal(err)
	}
	oldHash, err := BundledRuntimeHash(oldAssets)
	if err != nil {
		t.Fatal(err)
	}
	historical := InstallWorkerState{
		Schema: 1, State: "completed", PID: 42, Started: "historical worker start", RuntimeHash: oldHash,
		NetworkBytesDownloaded: 123456, RuntimeSyncRan: true,
	}
	writeBootstrapTestJSON(t, filepath.Join(install, "worker-state.json"), historical)
	prepared.Resources = oldResources
	if _, err := RefreshManagedGraphAssets(prepared.ConfigPath, install, prepared.Resources); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = ShutdownFirstRuntime(cleanup, prepared.ConfigPath)
	})
	if err := StartFirstRuntimeDetached(ctx, prepared); err != nil {
		t.Fatal(err)
	}
	oldOwner, err := supervisor.VerifyOwnerRecord(ctx, prepared.StoreRoot, prepared.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	original := processArgsFn
	t.Cleanup(func() { processArgsFn = original })
	processArgsFn = func(pid int) ([]string, error) {
		if pid == oldOwner.PID {
			return []string{prepared.Python, filepath.Join(oldResources, "engines/cognee/runner.py")}, nil
		}
		return nil, os.ErrNotExist
	}

	fingerprint := supervisor.OwnerFingerprint(oldOwner)
	installer := Installer{Root: install}
	bundledHash, err := BundledRuntimeHash(newAssets)
	if err != nil {
		t.Fatal(err)
	}
	prepared.Resources = filepath.Join(install, "runtime-"+bundledHash)
	if err := installer.withLock(func() error {
		handle, err := RestartStaleRuntimeOwner(ctx, prepared, fingerprint, newAssets)
		if handle != nil || !errors.Is(err, ErrInstallWorkerBusy) {
			t.Fatalf("restart without staging while installation.lock held = %v, %v", handle, err)
		}
		owner, err := supervisor.VerifyOwnerRecord(ctx, prepared.StoreRoot, prepared.ConfigPath)
		if err != nil || supervisor.OwnerFingerprint(owner) != fingerprint {
			t.Fatalf("unstaged restart disturbed existing owner: %+v, %v", owner, err)
		}
		if ready, err := FirstRuntimeReady(ctx, prepared.ConfigPath); err != nil || !ready {
			t.Fatalf("old owner readiness after blocked restart = %t, %v", ready, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareRuntimeResources(ctx, install, newAssets); err != nil {
		t.Fatal(err)
	}
	if err := installer.withLock(func() error {
		handle, err := RestartStaleRuntimeOwner(ctx, prepared, fingerprint, newAssets,
			WithFirstRuntimeStopTimeouts(2*time.Second, 2*time.Second))
		if err != nil || handle == nil || !handle.Owned() {
			t.Fatalf("restart with staged resources while installation.lock held = %v, %v", handle, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	newOwner, err := supervisor.VerifyOwnerRecord(ctx, prepared.StoreRoot, prepared.ConfigPath)
	if err != nil || supervisor.OwnerFingerprint(newOwner) == supervisor.OwnerFingerprint(oldOwner) {
		t.Fatal(newOwner, err)
	}
	status, err := InspectOwnerRuntimeStatus(ctx, prepared.ConfigPath, filepath.Dir(install), newAssets)
	if err != nil || status.RunningRuntimeHash != status.BundledRuntimeHash || status.GraphAssetsStale ||
		status.GraphAssetsPath != filepath.Join(install, "runtime-"+status.BundledRuntimeHash, graphAssetsSuffix) {
		t.Fatal(status, err)
	}
	receipt, err := ReadRuntimeUpgradeReceipt(filepath.Dir(install), prepared.ConfigPath)
	if err != nil || !receipt.ZeroDownloads || receipt.FromRuntimeHash != oldHash || receipt.ToRuntimeHash != bundledHash ||
		receipt.OwnerFingerprint != supervisor.OwnerFingerprint(newOwner) || receipt.CompletedAt == "" ||
		receipt.PriorWorkerRuntime != oldHash || receipt.PriorWorkerPID != historical.PID || receipt.PriorWorkerStarted != historical.Started ||
		receipt.PriorWorkerNetBytes != historical.NetworkBytesDownloaded || !receipt.PriorWorkerSyncRan {
		t.Fatalf("upgrade receipt = %+v, %v", receipt, err)
	}
	if status.UpgradeReceipt == nil || *status.UpgradeReceipt != receipt {
		t.Fatalf("status upgrade receipt = %+v, want %+v", status.UpgradeReceipt, receipt)
	}
	plan := Plan{Items: []Item{{Observation: Observation{Status: Available}}}}
	if check := EvaluateZeroDownloads(plan, Journal{}, false, historical, true, bundledHash, status.UpgradeReceipt); !check.Satisfied {
		t.Fatalf("zero-download check = %+v", check)
	}
}

func writeFirstStoreInputFixture(t *testing.T, prepared FirstRuntime, config firstRuntimeConfig) {
	t.Helper()
	writeBootstrapTestJSON(t, filepath.Join(prepared.StoreRoot, "first-install-input.json"), firstStoreInput{
		StoreIdentity: StoreIdentity{Schema: 1, StoreID: config.First.StoreID, SourceCommit: config.Commit},
		Root:          prepared.StoreRoot, Source: filepath.Join(prepared.HostHome, "source"),
		Archive: filepath.Join(prepared.HostHome, "source.tar.gz"), ModelCache: filepath.Join(prepared.HostHome, "model-cache"),
	})
}
