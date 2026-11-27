package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSupervisorStartOwnerAndAsyncServicesReady(t *testing.T) {
	store := t.TempDir()
	cfgPath := filepath.Join(store, "runtime.json")
	sup := New(Config{
		StoreRoot:         store,
		ConfigPath:        cfgPath,
		PhaseOnReady:      "ready",
		MonitorInterval:   50 * time.Millisecond,
		ReadyPollInterval: 20 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := sup.StartOwner(ctx); err != nil {
		t.Fatalf("StartOwner: %v", err)
	}
	defer func() { _ = sup.Stop(context.Background()) }()

	insp, err := InspectOwnerReadOnly(store)
	if err != nil || !insp.Locked || !insp.HasReceipt || insp.Record.Phase != "starting" {
		t.Fatalf("expected locked starting receipt after StartOwner, got %+v err=%v", insp, err)
	}

	readyCh := make(chan ServiceStatus, 4)
	spec := ServiceSpec{
		Role:    "cognee-engine",
		Command: "/bin/sleep",
		Args:    []string{"30"},
		LogPath: filepath.Join(store, "logs", "engine.log"),
		ReadyCheck: func(context.Context) (bool, error) {
			return true, nil
		},
		ReadyTimeout: 5 * time.Second,
		StopGrace:    2 * time.Second,
		KillWait:     time.Second,
	}
	if err := sup.StartServicesAsync(ctx, []ServiceSpec{spec}, func(st ServiceStatus) {
		if st.State == StateReady {
			select {
			case readyCh <- st:
			default:
			}
		}
	}); err != nil {
		t.Fatalf("StartServicesAsync: %v", err)
	}

	select {
	case st := <-readyCh:
		if st.Role != "cognee-engine" || st.State != StateReady || st.PID <= 0 {
			t.Fatalf("unexpected ready status: %+v", st)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for async service ready; snap=%+v", sup.Snapshot())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		insp, err = InspectOwnerReadOnly(store)
		if err == nil && insp.Record.Phase == "ready" && insp.Record.ServiceChildren["cognee-engine"] > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if insp.Record.Phase != "ready" || insp.Record.ServiceChildren["cognee-engine"] <= 0 {
		t.Fatalf("owner record not updated to ready: %+v", insp.Record)
	}

	if err := sup.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	insp, err = InspectOwnerReadOnly(store)
	if err != nil || insp.Locked || insp.HasReceipt {
		t.Fatalf("expected clean unlock and receipt removal after Stop, got %+v err=%v", insp, err)
	}
}

func TestSupervisorStartServicesAsyncFailureKeepsOwnerAliveNoAutoRestart(t *testing.T) {
	store := t.TempDir()
	cfgPath := filepath.Join(store, "runtime.json")
	sup := New(Config{
		StoreRoot:         store,
		ConfigPath:        cfgPath,
		PhaseOnReady:      "ready",
		MonitorInterval:   50 * time.Millisecond,
		ReadyPollInterval: 20 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := sup.StartOwner(ctx); err != nil {
		t.Fatalf("StartOwner: %v", err)
	}
	defer func() { _ = sup.Stop(context.Background()) }()

	var mu sync.Mutex
	var events []ServiceStatus
	exitedCh := make(chan ServiceStatus, 4)
	spec := ServiceSpec{
		Role:         "cognee-engine",
		Command:      "/bin/sh",
		Args:         []string{"-c", "echo engine boom >&2; exit 2"},
		LogPath:      filepath.Join(store, "logs", "engine.log"),
		ReadyCheck:   func(context.Context) (bool, error) { return false, nil },
		ReadyTimeout: 5 * time.Second,
		StopGrace:    time.Second,
		KillWait:     time.Second,
	}
	if err := sup.StartServicesAsync(ctx, []ServiceSpec{spec}, func(st ServiceStatus) {
		mu.Lock()
		events = append(events, st)
		mu.Unlock()
		if st.State == StateExited {
			select {
			case exitedCh <- st:
			default:
			}
		}
	}); err != nil {
		t.Fatalf("StartServicesAsync: %v", err)
	}

	select {
	case st := <-exitedCh:
		if st.State != StateExited || st.Role != "cognee-engine" {
			t.Fatalf("unexpected exited status: %+v", st)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for exited event; snap=%+v", sup.Snapshot())
	}

	// Owner lock must remain held so the gateway stays alive, and child must not auto-restart.
	time.Sleep(150 * time.Millisecond)
	insp, err := InspectOwnerReadOnly(store)
	if err != nil || !insp.Locked {
		t.Fatalf("expected owner lock to remain held after child exit, got %+v err=%v", insp, err)
	}
	snap := sup.Snapshot()
	if snap.State != StateExited || len(snap.Services) != 1 || snap.Services[0].State != StateExited {
		t.Fatalf("expected single exited service without restart, got %+v", snap)
	}
}

func TestSupervisorStopCancelsInFlightAsyncStartup(t *testing.T) {
	store := t.TempDir()
	cfgPath := filepath.Join(store, "runtime.json")
	sup := New(Config{
		StoreRoot:         store,
		ConfigPath:        cfgPath,
		ReadyPollInterval: 20 * time.Millisecond,
	})
	if err := sup.StartOwner(context.Background()); err != nil {
		t.Fatalf("StartOwner: %v", err)
	}
	startedCh := make(chan struct{}, 1)
	spec := ServiceSpec{
		Role:    "cognee-engine",
		Command: "/bin/sleep",
		Args:    []string{"30"},
		LogPath: filepath.Join(store, "logs", "engine.log"),
		ReadyCheck: func(context.Context) (bool, error) {
			select {
			case startedCh <- struct{}{}:
			default:
			}
			return false, nil
		},
		ReadyTimeout: 30 * time.Second,
		StopGrace:    time.Second,
		KillWait:     time.Second,
	}
	if err := sup.StartServicesAsync(context.Background(), []ServiceSpec{spec}, nil); err != nil {
		t.Fatalf("StartServicesAsync: %v", err)
	}
	select {
	case <-startedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("async startup never reached ReadyCheck")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sup.Stop(stopCtx); err != nil {
		t.Fatalf("Stop during in-flight async startup: %v", err)
	}
	insp, err := InspectOwnerReadOnly(store)
	if err != nil || insp.Locked || insp.HasReceipt {
		t.Fatalf("expected clean unlock after Stop, got %+v err=%v", insp, err)
	}
}

func TestSupervisorRejectsMixingSyncAndAsyncStart(t *testing.T) {
	store := t.TempDir()
	cfgPath := filepath.Join(store, "runtime.json")

	// 1. StartServicesAsync without prior StartOwner must fail.
	sup1 := New(Config{StoreRoot: store, ConfigPath: cfgPath})
	if err := sup1.StartServicesAsync(context.Background(), nil, nil); err == nil {
		t.Fatal("expected StartServicesAsync without StartOwner to fail")
	}

	// 2. StartServicesAsync after sync Start must fail.
	sup2 := New(Config{StoreRoot: store, ConfigPath: cfgPath})
	if err := sup2.Start(context.Background(), nil); err != nil {
		t.Fatalf("sync Start: %v", err)
	}
	defer func() { _ = sup2.Stop(context.Background()) }()
	if err := sup2.StartServicesAsync(context.Background(), nil, nil); err == nil {
		t.Fatal("expected StartServicesAsync after sync Start to fail")
	}
	if err := sup2.StartOwner(context.Background()); err == nil {
		t.Fatal("expected StartOwner after sync Start to fail")
	}
}

func TestSupervisorUpdateOwnerMetadataPersists(t *testing.T) {
	store := t.TempDir()
	cfgPath := filepath.Join(store, "runtime.json")
	sup := New(Config{StoreRoot: store, ConfigPath: cfgPath})
	if err := sup.StartOwner(context.Background()); err != nil {
		t.Fatalf("StartOwner: %v", err)
	}
	defer func() { _ = sup.Stop(context.Background()) }()

	meta := json.RawMessage(`{"ResourcesHash":"deadbeef"}`)
	if err := sup.UpdateOwnerMetadata(meta); err != nil {
		t.Fatalf("UpdateOwnerMetadata: %v", err)
	}
	rec, _, err := ReadOwnerRecord(store)
	if err != nil {
		t.Fatalf("ReadOwnerRecord: %v", err)
	}
	if string(rec.Metadata) != string(meta) {
		t.Fatalf("metadata = %s, want %s", rec.Metadata, meta)
	}
}

func TestManagedProcessStopGroupSkipsClearedAndRecycledPID(t *testing.T) {
	// 1. After clean exit, groupCleared is set and StopGroup returns nil immediately.
	logBase, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	proc, err := StartManagedProcess(ServiceSpec{
		Role:    "short-lived",
		Command: "/bin/sh",
		Args:    []string{"-c", "exit 0"},
		LogPath: filepath.Join(logBase, "logs", "short.log"),
	}, nil)
	if err != nil {
		t.Fatalf("StartManagedProcess: %v", err)
	}
	<-proc.done
	proc.mu.Lock()
	cleared := proc.groupCleared
	proc.mu.Unlock()
	if !cleared {
		t.Fatal("expected groupCleared=true after process group exited")
	}
	if err := proc.StopGroup(context.Background(), time.Second, time.Second); err != nil {
		t.Fatalf("StopGroup on cleared process: %v", err)
	}

	// 2. Simulated PID reuse: done is closed, pid points to current live process
	//    with a different startIdentity -> StopGroup must NOT signal it and return nil.
	done := make(chan struct{})
	close(done)
	recycled := &ManagedProcess{
		pid:           os.Getpid(),
		startIdentity: "stale-kernel-start-identity-never-matches",
		done:          done,
		exited:        true,
	}
	if err := recycled.StopGroup(context.Background(), time.Second, time.Second); err != nil {
		t.Fatalf("StopGroup on recycled PID must return nil without signalling: %v", err)
	}
}
