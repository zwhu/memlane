package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zwhu/memlane/internal/platform"
)

func TestRuntimeAvailabilityRevokesReadyWithoutChangingKnowledge(t *testing.T) {
	a := New("test", nil)
	a.runtimeStatus = RuntimeStatus{Phase: "ready", Installed: true, EngineBytes: 100, ModelBytes: 200, Downloaded: 300, Total: 300}
	a.items = []MemoryItem{{ID: "original", Text: "已保存原文", Revision: 1}}
	a.ApplyRuntimeAvailability(RuntimeAvailability{Phase: "authorization", Message: "需要系统批准", Service: platform.RequiresApproval})
	if a.runtimeStatus.Phase != "authorization" || a.runtimeStatus.Service != platform.RequiresApproval || a.runtimeStatus.ModelBytes != 200 || a.runtimeStatus.Downloaded != 300 {
		t.Fatal("approval change retained ready or lost prepared component state")
	}
	if a.items[0].Text != "已保存原文" || a.items[0].Revision != 1 {
		t.Fatal("availability observation changed effective knowledge")
	}
	a.ApplyRuntimeAvailability(RuntimeAvailability{Phase: "ready", Message: "服务已就绪", Service: platform.Enabled})
	if a.runtimeStatus.Phase != "ready" {
		t.Fatal("restored actual availability did not clear authorization state")
	}
}

func TestRuntimeAvailabilityCannotOverrideActiveLifecycleOrStop(t *testing.T) {
	for _, phase := range []string{"checking", "downloading", "installing", "preparing", "stopped", "failed"} {
		a := &App{runtimeStatus: RuntimeStatus{Phase: phase, Installed: true}}
		a.ApplyRuntimeAvailability(RuntimeAvailability{Phase: "ready", Message: "late observation"})
		if a.runtimeStatus.Phase != phase {
			t.Fatalf("late observation replaced %s", phase)
		}
	}
	a := &App{runtimeStatus: RuntimeStatus{Phase: "ready", Installed: true}, backups: BackupUI{RestoreLocked: true}}
	a.ApplyRuntimeAvailability(RuntimeAvailability{Phase: "authorization"})
	if a.runtimeStatus.Phase != "ready" {
		t.Fatal("observation replaced an active offline restore")
	}
}

func TestRuntimeObservationRespectsPersistentStopAndLifecycleLock(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	state := []byte(`{"stopped":true}`)
	path := filepath.Join(data, "service-state.json")
	if err := os.WriteFile(path, state, 0600); err != nil {
		t.Fatal(err)
	}
	r := &RuntimeController{Config: RuntimeConfig{Root: root, ServicePlist: "invalid-would-fail-if-observed"}}
	status, err := r.ObserveAvailability(context.Background())
	if err != nil || status.Phase != "stopped" {
		t.Fatalf("observation bypassed persistent stop: %#v %v", status, err)
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != string(state) {
		t.Fatal("observation wrote the stop state")
	}
	r.lifecycle.Lock()
	_, err = r.ObserveAvailability(context.Background())
	r.lifecycle.Unlock()
	if !errors.Is(err, ErrRuntimeChanging) {
		t.Fatal("observer competed with an active lifecycle operation")
	}
}
