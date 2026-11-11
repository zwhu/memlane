package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

// Real Unix socket/strict decoder/SQLite; the organizer slot is held throughout,
// so these checks cannot start a model or a fixture process.
func TestProjectStartMinimalAndGoWireReplayAndFailureStages(t *testing.T) {
	s, d := projectDaemonFixture(t)
	root := t.TempDir()
	path := serviceipc.SocketPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close(); os.RemoveAll(filepath.Dir(path)) })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); s.handle(s.lifecycle, conn) }()
		}
	}()
	release, err := s.acquireOrganizer(s.lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var executions atomic.Int64
	s.projectExecute = func(context.Context, agents.CodexProjectPlan, records.ProjectTask, func(int) error, func(string)) (agents.CodexProjectExecution, error) {
		executions.Add(1)
		return agents.CodexProjectExecution{}, errors.New("must not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	minimal := map[string]any{"protocol": 1, "id": uuid.NewString(), "op": "project_task_start", "user_confirmed": true, "project_task_start": map[string]any{"operation_id": d.OperationID, "project": d.Project, "binding_revision": d.BindingRevision, "include_history": false, "sessions": []any{}}}
	if err = json.NewEncoder(conn).Encode(minimal); err != nil {
		t.Fatal(err)
	}
	var response struct {
		OK     bool                `json:"ok"`
		Error  string              `json:"error"`
		Result records.ProjectTask `json:"result"`
	}
	if err = json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if !response.OK || response.Result.ID == "" {
		t.Fatal(response)
	}
	client := serviceipc.ProjectTaskClient{Root: root}
	task, err := client.Start(ctx, d)
	if err != nil || task.ID != response.Result.ID {
		t.Fatal("Go nil sessions differs from minimal empty sessions", task, err)
	}
	d.Sessions = []records.ProjectSessionSelection{}
	replay, err := client.Start(ctx, d)
	if err != nil || replay.ID != task.ID {
		t.Fatal("Go empty sessions duplicated task", replay, err)
	}
	originalResolve := s.projectResolve
	s.projectResolve = func(context.Context, string) (agents.CodexProjectPlan, error) {
		return agents.CodexProjectPlan{}, &records.AccessError{Code: "connection_changed", Message: "explicit runtime invalidation"}
	}
	// A lost-reply recovery must not require today's runtime to be valid.
	replay, err = client.Start(ctx, d)
	if err != nil || replay.ID != task.ID {
		t.Fatal("replay was blocked by mutable executor", replay, err)
	}
	bad := d
	bad.OperationID = uuid.NewString()
	_, err = client.Start(ctx, bad)
	var remote *serviceipc.ServiceError
	if !errors.As(err, &remote) || remote.Code != "connection_changed" || remote.Stage != "executor" {
		t.Fatal("executor stage/code lost", err)
	}
	s.projectResolve = originalResolve
	bad.OperationID = uuid.NewString()
	bad.BindingRevision++
	_, err = client.Start(ctx, bad)
	if !errors.As(err, &remote) || remote.Code != "binding_changed" || remote.Stage != "persist" {
		t.Fatal("persistence stage/code lost", err)
	}
	conflict := d
	conflict.BindingRevision++
	_, err = client.Start(ctx, conflict)
	if !errors.As(err, &remote) || remote.Code != "conflict" || remote.Stage != "request" {
		t.Fatal("intent conflict stage/code lost", err)
	}
	if _, err = client.Cancel(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if executions.Load() != 0 {
		t.Fatal("wire diagnostic invoked model", executions.Load())
	}
}
