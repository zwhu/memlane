package desktop

import (
	"context"
	"encoding/json"
	"github.com/zwhu/memlane/internal/serviceipc"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateCancellationKeepsPriorRunningProofAfterQuiesceRequest(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	path := serviceipc.SocketPath(data)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	defer listener.Close()
	defer os.RemoveAll(filepath.Dir(path))
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			var request serviceipc.Request
			if json.NewDecoder(c).Decode(&request) == nil {
				if request.Op == "quiesce" {
					close(entered)
					<-release
				} else {
					_ = json.NewEncoder(c).Encode(serviceipc.Response{Protocol: 1, ID: request.ID, OK: true, Result: map[string]string{"state": "ready"}})
				}
			}
			c.Close()
		}
	}()
	controller := &RuntimeController{Config: RuntimeConfig{Root: root, ServicePlist: "must-not-reach-system-registration"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		running bool
		err     error
	}
	done := make(chan result, 1)
	go func() { running, e := controller.QuiesceForUpdate(ctx); done <- result{running, e} }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("quiesce not requested")
	}
	cancel()
	select {
	case r := <-done:
		if !r.running || r.err == nil {
			t.Fatalf("cancel discarded recovery proof after quiesce: running=%v err=%v", r.running, r.err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not release update preparation")
	}
}
