package supervisor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/zwhu/memlane/internal/bootstrap"
	"github.com/zwhu/memlane/internal/supervisor"
)

func TestSupervisorHelperProcess(t *testing.T) {
	if os.Getenv("MEMLANE_SUPERVISOR_HELPER") != "1" {
		return
	}
	mode := os.Getenv("MEMLANE_HELPER_MODE")
	if mode == "grandchild" {
		for {
			time.Sleep(time.Second)
		}
	}

	role := os.Getenv("MEMLANE_HELPER_ROLE")
	orderFile := os.Getenv("MEMLANE_HELPER_ORDER_FILE")
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
	appendOrder("start")

	if gf := os.Getenv("MEMLANE_HELPER_GRANDCHILD_FILE"); gf != "" {
		exe, err := os.Executable()
		if err == nil {
			child := exec.Command(exe, "-test.run=^TestSupervisorHelperProcess$")
			child.Env = []string{
				"MEMLANE_SUPERVISOR_HELPER=1",
				"MEMLANE_HELPER_MODE=grandchild",
			}
			if child.Start() == nil {
				_ = os.WriteFile(gf, []byte(strconv.Itoa(child.Process.Pid)), 0o600)
			}
		}
	}

	addr := os.Getenv("MEMLANE_HELPER_ADDR")
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper listen error: %v\n", err)
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
		_ = json.NewEncoder(w).Encode(map[string]any{"revision": 0})
	})

	srv := &http.Server{Handler: mux}
	go func() {
		_ = srv.Serve(ln)
	}()

	if mode == "crash" {
		time.Sleep(150 * time.Millisecond)
		fmt.Fprintln(os.Stderr, "Authorization: Bearer super-secret-token-value")
		fmt.Fprintln(os.Stderr, "fatal: simulated crash")
		os.Exit(42)
	}

	for sig := range sigCh {
		appendOrder(sig.String())
		if mode == "ignore_sigterm" && sig == syscall.SIGTERM {
			continue
		}
		_ = srv.Close()
		os.Exit(0)
	}
}

func reserveLoopbackEndpoint(t *testing.T) (addr string, origin string) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr = ln.Addr().String()
	_ = ln.Close()
	return addr, "http://" + addr
}

func canonicalTestDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(resolved, 0o700); err != nil {
		t.Fatal(err)
	}
	return resolved
}

func writePrivateJSON(t *testing.T, path string, value any) {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func helperSpec(t *testing.T, role, mode, addr, logPath string, extraEnv ...string) supervisor.ServiceSpec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := []string{
		"MEMLANE_SUPERVISOR_HELPER=1",
		"MEMLANE_HELPER_ROLE=" + role,
		"MEMLANE_HELPER_MODE=" + mode,
		"MEMLANE_HELPER_ADDR=" + addr,
	}
	env = append(env, extraEnv...)
	return supervisor.ServiceSpec{
		Role:         role,
		Command:      exe,
		Args:         []string{"-test.run=^TestSupervisorHelperProcess$"},
		Env:          env,
		LogPath:      logPath,
		BindURL:      "http://" + addr,
		HealthURL:    "http://" + addr + "/health",
		ReadyTimeout: 5 * time.Second,
		StopGrace:    2 * time.Second,
		KillWait:     2 * time.Second,
	}
}

func waitForPIDDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("process %d still alive after stop", pid)
}

func TestSupervisorOrderedStartReverseShutdownAndNoOrphans(t *testing.T) {
	rootParent := canonicalTestDir(t)
	storeRoot := filepath.Join(rootParent, "library")
	logsDir := filepath.Join(rootParent, "runtime-logs")
	if err := os.Mkdir(storeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(logsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	serviceAddr, serviceOrigin := reserveLoopbackEndpoint(t)
	controlAddr, controlOrigin := reserveLoopbackEndpoint(t)

	ownerToken := filepath.Join(storeRoot, "owner.token")
	mgmtToken := filepath.Join(storeRoot, "management.token")
	if err := os.WriteFile(ownerToken, []byte("synthetic-owner-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mgmtToken, []byte("synthetic-management-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	storeID := strings.Repeat("a", 32)
	writePrivateJSON(t, filepath.Join(storeRoot, "store-identity.json"), bootstrap.StoreReceipt{
		Schema:             1,
		StoreID:            storeID,
		SourceCommit:       "fixed",
		OwnerID:            "synthetic-owner",
		KeyID:              "synthetic-key",
		StorageInitialized: true,
	})
	configPath := filepath.Join(rootParent, "runtime.json")
	writePrivateJSON(t, configPath, map[string]any{
		"first_install":  bootstrap.FirstInstallation{Schema: 1, StoreID: storeID, Setup: true},
		"official_data":  storeRoot,
		"service_commit": "fixed",
		"token_file":     ownerToken,
		"service_url":    serviceOrigin,
		"project_control": map[string]string{
			"url":                   controlOrigin,
			"management_token_file": mgmtToken,
		},
	})

	grandchildFile := filepath.Join(rootParent, "grandchild.pid")
	orderFile := filepath.Join(rootParent, "order.log")

	sup := supervisor.New(supervisor.Config{
		StoreRoot:       storeRoot,
		ConfigPath:      configPath,
		ServiceURL:      serviceOrigin,
		ControlURL:      controlOrigin,
		MonitorInterval: 100 * time.Millisecond,
	})

	specs := []supervisor.ServiceSpec{
		helperSpec(t, "official", "service", serviceAddr, filepath.Join(logsDir, "official.log"),
			"MEMLANE_HELPER_GRANDCHILD_FILE="+grandchildFile,
			"MEMLANE_HELPER_ORDER_FILE="+orderFile,
		),
		helperSpec(t, "project_control", "service", controlAddr, filepath.Join(logsDir, "project_control.log"),
			"MEMLANE_HELPER_ORDER_FILE="+orderFile,
		),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := sup.Start(ctx, specs); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = sup.Stop(context.Background()) }()

	// Second concurrent owner acquisition must fail with ErrOwnerLocked.
	if _, err := supervisor.AcquireOwner(storeRoot); !errors.Is(err, supervisor.ErrOwnerLocked) {
		t.Fatalf("expected ErrOwnerLocked, got %v", err)
	}

	// Receipt file must be private 0600 and carry phase=ready + service_children.
	receiptPath := filepath.Join(storeRoot, ".memlane-dev-owner.json")
	info, err := os.Lstat(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 receipt, got %v", info.Mode().Perm())
	}
	rec, _, err := supervisor.ReadOwnerRecord(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Phase != "ready" || rec.ServiceChildren["official"] <= 1 || rec.ServiceChildren["project_control"] <= 1 {
		t.Fatalf("unexpected owner record: %+v", rec)
	}

	// Cross-package check: internal/bootstrap.FirstRuntimeReady must accept the live Go supervisor stack.
	ready, err := bootstrap.FirstRuntimeReady(ctx, configPath)
	if err != nil || !ready {
		t.Fatalf("expected FirstRuntimeReady=true, got ready=%v err=%v", ready, err)
	}

	// Wait briefly until grandchild PID file is populated by official child.
	var grandchildPID int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b, readErr := os.ReadFile(grandchildFile); readErr == nil && len(bytes.TrimSpace(b)) > 0 {
			grandchildPID, err = strconv.Atoi(strings.TrimSpace(string(b)))
			if err == nil && grandchildPID > 1 {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if grandchildPID <= 1 {
		t.Fatal("grandchild PID was not recorded")
	}

	officialPID := rec.ServiceChildren["official"]
	controlPID := rec.ServiceChildren["project_control"]

	if err := sup.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if _, err := os.Stat(receiptPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected receipt removed after clean Stop, got %v", err)
	}
	waitForPIDDead(t, officialPID)
	waitForPIDDead(t, controlPID)
	waitForPIDDead(t, grandchildPID)

	// Re-acquiring owner lock must succeed now that owner & receipt were cleaned up.
	relock, err := supervisor.AcquireOwner(storeRoot)
	if err != nil {
		t.Fatalf("expected AcquireOwner success after Stop, got %v", err)
	}
	_ = relock.Release()

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
		t.Fatalf("unexpected start/stop sequence: %v", lines)
	}
}

func TestSupervisorSigtermFallbackToSigkill(t *testing.T) {
	rootParent := canonicalTestDir(t)
	storeRoot := filepath.Join(rootParent, "library")
	logsDir := filepath.Join(rootParent, "runtime-logs")
	if err := os.Mkdir(storeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(logsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	addr, origin := reserveLoopbackEndpoint(t)
	configPath := filepath.Join(rootParent, "runtime.json")
	writePrivateJSON(t, configPath, map[string]any{"official_data": storeRoot})

	sup := supervisor.New(supervisor.Config{
		StoreRoot:       storeRoot,
		ConfigPath:      configPath,
		ServiceURL:      origin,
		MonitorInterval: 100 * time.Millisecond,
	})
	spec := helperSpec(t, "stubborn", "ignore_sigterm", addr, filepath.Join(logsDir, "stubborn.log"))
	spec.StopGrace = 250 * time.Millisecond
	spec.KillWait = 2 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sup.Start(ctx, []supervisor.ServiceSpec{spec}); err != nil {
		t.Fatal(err)
	}

	snap := sup.Snapshot()
	if len(snap.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(snap.Services))
	}
	pid := snap.Services[0].PID

	if err := sup.Stop(context.Background()); err != nil {
		t.Fatalf("expected SIGKILL fallback to stop cleanly, got %v", err)
	}
	waitForPIDDead(t, pid)
	if _, err := os.Stat(filepath.Join(storeRoot, ".memlane-dev-owner.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected owner receipt removed after SIGKILL fallback, got %v", err)
	}
}

func TestSupervisorDetectsCrashWithoutAutoRestart(t *testing.T) {
	rootParent := canonicalTestDir(t)
	storeRoot := filepath.Join(rootParent, "library")
	logsDir := filepath.Join(rootParent, "runtime-logs")
	if err := os.Mkdir(storeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(logsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	addr, origin := reserveLoopbackEndpoint(t)
	configPath := filepath.Join(rootParent, "runtime.json")
	writePrivateJSON(t, configPath, map[string]any{"official_data": storeRoot})

	sup := supervisor.New(supervisor.Config{
		StoreRoot:       storeRoot,
		ConfigPath:      configPath,
		ServiceURL:      origin,
		MonitorInterval: 50 * time.Millisecond,
	})
	spec := helperSpec(t, "official", "crash", addr, filepath.Join(logsDir, "official.log"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sup.Start(ctx, []supervisor.ServiceSpec{spec}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sup.Stop(context.Background()) }()

	initialPID := sup.Snapshot().Services[0].PID

	var crashStatus supervisor.ServiceStatus
	select {
	case crashStatus = <-sup.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for unexpected crash event")
	}

	if crashStatus.State != supervisor.StateExited || crashStatus.ExitCode == nil || *crashStatus.ExitCode != 42 {
		t.Fatalf("unexpected crash status: %+v", crashStatus)
	}
	joined := strings.Join(crashStatus.TailLines, "\n")
	if strings.Contains(joined, "super-secret-token-value") {
		t.Fatalf("secret leaked in crash tail lines: %q", joined)
	}
	if !strings.Contains(joined, "Bearer [REDACTED]") || !strings.Contains(joined, "fatal: simulated crash") {
		t.Fatalf("expected redacted token and crash line, got %q", joined)
	}

	// Confirm supervisor never auto-restarts crashed services.
	time.Sleep(250 * time.Millisecond)
	after := sup.Snapshot()
	if after.State != supervisor.StateExited || len(after.Services) != 1 || after.Services[0].PID != initialPID {
		t.Fatalf("expected exited state without restart, got %+v", after)
	}

	if err := sup.Stop(context.Background()); err != nil {
		t.Fatalf("Stop after crash failed: %v", err)
	}
}

func TestSupervisorHealthDebounceDegradedAndRecovery(t *testing.T) {
	rootParent := canonicalTestDir(t)
	storeRoot := filepath.Join(rootParent, "library")
	logsDir := filepath.Join(rootParent, "runtime-logs")
	if err := os.Mkdir(storeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(logsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	addr, origin := reserveLoopbackEndpoint(t)
	configPath := filepath.Join(rootParent, "runtime.json")
	writePrivateJSON(t, configPath, map[string]any{"official_data": storeRoot})

	sup := supervisor.New(supervisor.Config{
		StoreRoot:        storeRoot,
		ConfigPath:       configPath,
		ServiceURL:       origin,
		MonitorInterval:  40 * time.Millisecond,
		HealthFailureMax: 3,
		HealthTimeout:    250 * time.Millisecond,
	})
	spec := helperSpec(t, "official", "service", addr, filepath.Join(logsDir, "official.log"))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sup.Start(ctx, []supervisor.ServiceSpec{spec}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sup.Stop(context.Background()) }()

	resp, err := http.Get(origin + "/fail?state=1")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	waitState := func(want supervisor.ServiceState) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if sup.Snapshot().State == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for state %q (current=%+v)", want, sup.Snapshot())
	}

	waitState(supervisor.StateDegraded)

	resp, err = http.Get(origin + "/fail?state=0")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	waitState(supervisor.StateReady)
}

func TestSupervisorRefusesUnrecoveredReceiptAndRecoversDeadOwner(t *testing.T) {
	rootParent := canonicalTestDir(t)
	storeRoot := filepath.Join(rootParent, "library")
	if err := os.Mkdir(storeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(rootParent, "runtime.json")
	writePrivateJSON(t, configPath, map[string]any{"official_data": storeRoot})
	_, serviceOrigin := reserveLoopbackEndpoint(t)

	receiptPath := filepath.Join(storeRoot, ".memlane-dev-owner.json")
	receipt := supervisor.OwnerRecord{
		PID:          999991,
		Started:      "synthetic-lstart",
		Phase:        "ready",
		Config:       configPath,
		OfficialData: storeRoot,
		ServiceURL:   serviceOrigin,
		Children:     []int{999992},
		ServiceChildren: map[string]int{
			"official": 999992,
		},
	}
	writePrivateJSON(t, receiptPath, receipt)
	originalBytes, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}

	// AcquireOwner & Supervisor.Start must refuse an unrecovered receipt.
	if _, err := supervisor.AcquireOwner(storeRoot); !errors.Is(err, supervisor.ErrRecoveryRequired) {
		t.Fatalf("expected ErrRecoveryRequired from AcquireOwner, got %v", err)
	}
	sup := supervisor.New(supervisor.Config{StoreRoot: storeRoot, ConfigPath: configPath})
	if err := sup.Start(context.Background(), nil); !errors.Is(err, supervisor.ErrRecoveryRequired) {
		t.Fatalf("expected ErrRecoveryRequired from Start, got %v", err)
	}

	// Recovery must refuse while any recorded PID still exists in the inspected table.
	if _, err := supervisor.RecoverStaleOwner(context.Background(), supervisor.RecoverOptions{
		StoreRoot:  storeRoot,
		ConfigPath: configPath,
		ProcessTable: func(context.Context) (map[int]supervisor.ProcessInfo, error) {
			return map[int]supervisor.ProcessInfo{
				999992: {PID: 999992, PPID: 999991, PGID: 999992, Started: "synthetic-lstart"},
			}, nil
		},
	}); !errors.Is(err, supervisor.ErrRecoveryRefused) {
		t.Fatalf("expected ErrRecoveryRefused with live process, got %v", err)
	}

	// Empty process table + free port allows archival recovery.
	res, err := supervisor.RecoverStaleOwner(context.Background(), supervisor.RecoverOptions{
		StoreRoot:  storeRoot,
		ConfigPath: configPath,
		ProcessTable: func(context.Context) (map[int]supervisor.ProcessInfo, error) {
			return map[int]supervisor.ProcessInfo{}, nil
		},
	})
	if err != nil {
		t.Fatalf("RecoverStaleOwner failed: %v", err)
	}
	if res.Status != "released" || res.Archive == "" {
		t.Fatalf("unexpected recover result: %+v", res)
	}
	archivedBytes, err := os.ReadFile(res.Archive)
	if err != nil || !bytes.Equal(archivedBytes, originalBytes) {
		t.Fatalf("archived receipt mismatch (err=%v)", err)
	}

	lock, err := supervisor.AcquireOwner(storeRoot)
	if err != nil {
		t.Fatalf("expected AcquireOwner success after recovery, got %v", err)
	}
	_ = lock.Release()
}

func TestTrimServiceLogPreservesInodeAndCreatesPrevious(t *testing.T) {
	rootParent := canonicalTestDir(t)
	logPath := filepath.Join(rootParent, "logs", "service.log")

	f, err := supervisor.OpenPrivateLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	payload := bytes.Repeat([]byte("A"), 150)
	tailExpected := bytes.Repeat([]byte("Z"), 64)
	if _, err := f.Write(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(tailExpected); err != nil {
		t.Fatal(err)
	}
	infoBefore, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	inodeBefore := infoBefore.Sys().(*syscall.Stat_t).Ino

	if err := supervisor.TrimServiceLog(logPath, 64); err != nil {
		t.Fatalf("TrimServiceLog failed: %v", err)
	}

	prevInfo, err := os.Lstat(logPath + ".previous")
	if err != nil {
		t.Fatal(err)
	}
	if prevInfo.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 previous log, got %v", prevInfo.Mode().Perm())
	}
	prevBytes, err := os.ReadFile(logPath + ".previous")
	if err != nil || !bytes.Equal(prevBytes, tailExpected) {
		t.Fatalf("unexpected .previous content: got=%q want=%q err=%v", prevBytes, tailExpected, err)
	}

	infoAfter, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if infoAfter.Size() != 0 {
		t.Fatalf("expected truncated size 0, got %d", infoAfter.Size())
	}
	if infoAfter.Sys().(*syscall.Stat_t).Ino != inodeBefore {
		t.Fatal("expected live log inode preserved across trim")
	}

	// Active O_APPEND descriptor must write at offset 0 on the same inode.
	if _, err := f.Write([]byte("after-trim\n")); err != nil {
		t.Fatal(err)
	}
	activeBytes, err := os.ReadFile(logPath)
	if err != nil || string(activeBytes) != "after-trim\n" {
		t.Fatalf("expected write to truncated inode, got %q (err=%v)", activeBytes, err)
	}

	// Symlink logs must always be rejected.
	symlinkLog := filepath.Join(rootParent, "logs", "sym.log")
	if err := os.Symlink(logPath, symlinkLog); err != nil {
		t.Fatal(err)
	}
	if err := supervisor.TrimServiceLog(symlinkLog, 64); err == nil {
		t.Fatal("expected TrimServiceLog to reject symlink log")
	}
}
