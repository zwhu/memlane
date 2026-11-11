package desktop

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestRuntimeReuseOnlyObservesExistingService(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.MkdirAll(data, 0700); err != nil {
		t.Fatal(err)
	}
	path := serviceipc.SocketPath(data)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close(); os.Remove(path) })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var q serviceipc.Request
			if json.NewDecoder(conn).Decode(&q) == nil {
				response := serviceipc.Response{Protocol: 1, ID: q.ID, OK: q.Op == "health", Result: map[string]any{"state": "ready", "service_version": "0.0.1"}}
				_ = json.NewEncoder(conn).Encode(response)
			}
			conn.Close()
		}
	}()
	r := &RuntimeController{Config: RuntimeConfig{Root: root, ReuseRunningService: true, ServicePlist: "invalid-no-system-registration-allowed"}}
	var status RuntimeStatus
	if err := r.Prepare(context.Background(), func(s RuntimeStatus) { status = s }); err != nil {
		t.Fatal(err)
	}
	if status.Phase != "ready" || !status.Installed || status.Service != "" {
		t.Fatal("reuse must prove health without claiming system service ownership", status)
	}
	if _, err := os.Stat(filepath.Join(root, "runtime")); !os.IsNotExist(err) {
		t.Fatal("reuse prepared a second component environment")
	}
	if err := r.StopService(context.Background()); err == nil {
		t.Fatal("reuse attempted to stop the owning service")
	}
	if err := r.ResumeService(context.Background(), func(RuntimeStatus) {}); err == nil {
		t.Fatal("reuse attempted to restart the owning helper")
	}
	if _, err := r.QuiesceForUpdate(context.Background()); err == nil {
		t.Fatal("reuse attempted an update")
	}
}

func TestRuntimeReuseUnavailableNeverBecomesReady(t *testing.T) {
	r := &RuntimeController{Config: RuntimeConfig{Root: t.TempDir(), ReuseRunningService: true, ServicePlist: "invalid"}}
	var status RuntimeStatus
	if err := r.Prepare(context.Background(), func(s RuntimeStatus) { status = s }); err != nil {
		t.Fatal(err)
	}
	if status.Phase != "unavailable" {
		t.Fatal("missing existing service was presented as ready", status)
	}
}
