package gateway

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/zwhu/memlane/core/domain"
	"github.com/zwhu/memlane/core/engine"
	daemonkit "github.com/zwhu/memlane/kit/daemon"
)

type controllableHealthEngine struct {
	fakeEngine
	mu      sync.Mutex
	delay   time.Duration
	status  engine.HealthStatus
	err     error
	probes  int
}

func (e *controllableHealthEngine) Health(ctx context.Context) (engine.HealthStatus, error) {
	e.mu.Lock()
	delay := e.delay
	st := e.status
	err := e.err
	e.probes++
	e.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return engine.HealthStatus{}, ctx.Err()
		}
	}
	return st, err
}

func TestHealthEndpointDecouplesGatewayAndEngine(t *testing.T) {
	store := openTestStore(t)
	eng := &controllableHealthEngine{
		status: engine.HealthStatus{OK: true, Engine: "cognee", Capabilities: engine.Capabilities{HybridRecall: true}},
	}
	var statusMu sync.Mutex
	currentServices := []daemonkit.ServiceStatus{
		{Role: "cognee-engine", PID: 4242, State: daemonkit.StateStarting},
	}
	srv, err := NewServer(Config{
		Store:           store,
		Engine:          eng,
		ManagementToken: testManagement,
		DaemonStatus: func(context.Context) map[string]any {
			statusMu.Lock()
			defer statusMu.Unlock()
			return map[string]any{"owner_pid": 100, "phase": "starting", "state": daemonkit.StateStarting, "services": currentServices}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Engine starting -> fast non-blocking response with gateway_ok=true, engine_ok=false, status=starting.
	body := decodeResponse[map[string]any](t, manage(t, srv, http.MethodGet, "/api/health", "", http.StatusOK))
	if body["gateway_ok"] != true || body["gateway_ready"] != true || body["engine_ok"] != false || body["engine_ready"] != false || body["status"] != "starting" || body["ok"] != false {
		t.Fatalf("unexpected starting /api/health: %+v", body)
	}

	// 2. Engine exited -> status=exited with engine_error, no probe required.
	now := time.Now().UTC()
	statusMu.Lock()
	currentServices = []daemonkit.ServiceStatus{
		{Role: "cognee-engine", PID: 4242, State: daemonkit.StateExited, ExitedAt: &now, LastError: "python exited status 1"},
	}
	statusMu.Unlock()
	body = decodeResponse[map[string]any](t, manage(t, srv, http.MethodGet, "/api/health", "", http.StatusOK))
	if body["gateway_ok"] != true || body["engine_ok"] != false || body["status"] != "exited" || body["engine_error"] == nil {
		t.Fatalf("unexpected exited /api/health: %+v", body)
	}

	// 3. Engine ready -> ok=true, gateway_ok=true, engine_ok=true, status=ready.
	statusMu.Lock()
	currentServices = []daemonkit.ServiceStatus{
		{Role: "cognee-engine", PID: 4242, State: daemonkit.StateReady},
	}
	statusMu.Unlock()
	body = decodeResponse[map[string]any](t, manage(t, srv, http.MethodGet, "/api/health", "", http.StatusOK))
	if body["gateway_ok"] != true || body["engine_ok"] != true || body["engine_ready"] != true || body["ok"] != true || body["status"] != "ready" {
		t.Fatalf("unexpected ready /api/health: %+v", body)
	}
}

func TestCapabilitiesEndpointNonBlockingAndEpochRefresh(t *testing.T) {
	store := openTestStore(t)
	eng := &controllableHealthEngine{
		delay:  2 * time.Second,
		status: engine.HealthStatus{OK: false},
		err:    errors.New("engine booting"),
	}
	srv, err := NewServer(Config{
		Store:           store,
		Engine:          eng,
		ManagementToken: testManagement,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Must not block 3 seconds waiting on a slow/unready engine.
	started := time.Now()
	first := decodeResponse[map[string]any](t, manage(t, srv, http.MethodGet, "/v1/capabilities", "", http.StatusOK))
	elapsed := time.Since(started)
	if elapsed > 300*time.Millisecond {
		t.Fatalf("/v1/capabilities blocked for %s (budget <300ms)", elapsed)
	}
	if first["engine_ready"] != false {
		t.Fatalf("expected engine_ready=false while engine is slow/unready, got %+v", first)
	}
	initialEpoch, _ := first["capabilities_epoch"].(float64)

	// Engine recovers and daemon notifies gateway -> capabilities and epoch update.
	eng.mu.Lock()
	eng.delay = 0
	eng.err = nil
	eng.status = engine.HealthStatus{OK: true, Engine: "cognee", Capabilities: engine.Capabilities{HybridRecall: true, ToolGraph: true}}
	eng.mu.Unlock()

	srv.NotifyEngineStatus(true, "")

	deadline := time.Now().Add(2 * time.Second)
	var second map[string]any
	for time.Now().Before(deadline) {
		second = decodeResponse[map[string]any](t, manage(t, srv, http.MethodGet, "/v1/capabilities", "", http.StatusOK))
		caps, _ := second["capabilities"].(map[string]any)
		if second["engine_ready"] == true && caps["hybrid_recall"] == true {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	caps, _ := second["capabilities"].(map[string]any)
	nextEpoch, _ := second["capabilities_epoch"].(float64)
	if second["engine_ready"] != true || caps["hybrid_recall"] != true || nextEpoch <= initialEpoch {
		t.Fatalf("expected refreshed capabilities and incremented epoch, initial=%v second=%+v", initialEpoch, second)
	}
}

func TestRuntimeMonitorExposesGatewayEnginePhaseStartupErrorAndInboxPause(t *testing.T) {
	store := openTestStore(t)
	eng := &controllableHealthEngine{
		status: engine.HealthStatus{OK: true, Engine: "cognee"},
	}
	var statusMu sync.Mutex
	currentServices := []daemonkit.ServiceStatus{
		{Role: "cognee-engine", PID: 9001, State: daemonkit.StateStarting},
	}
	srv, err := NewServer(Config{
		Store:           store,
		Engine:          eng,
		ManagementToken: testManagement,
		DaemonStatus: func(context.Context) map[string]any {
			statusMu.Lock()
			defer statusMu.Unlock()
			return map[string]any{"owner_pid": 100, "phase": "starting", "state": daemonkit.StateStarting, "services": currentServices}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Starting phase.
	mon := decodeResponse[map[string]any](t, manage(t, srv, http.MethodGet, "/v1/runtime/monitor", "", http.StatusOK))
	if mon["gateway_ok"] != true || mon["engine_ok"] != false || mon["engine_phase"] != "starting" || mon["state"] != "starting" {
		t.Fatalf("unexpected starting monitor payload: %+v", mon)
	}
	inbox, _ := mon["inbox"].(map[string]any)
	if inbox["extraction_paused"] != true || inbox["pause_reason"] != "engine_starting" {
		t.Fatalf("expected extraction_paused=true/engine_starting, got %+v", inbox)
	}
	if _, ok := inbox["awaiting_publish"]; !ok {
		t.Fatalf("missing awaiting_publish in monitor inbox: %+v", inbox)
	}
	if _, ok := inbox["publish_failed"]; !ok {
		t.Fatalf("missing publish_failed in monitor inbox: %+v", inbox)
	}

	// 2. Startup engine failure recorded while exited.
	now := time.Now().UTC()
	statusMu.Lock()
	currentServices = []daemonkit.ServiceStatus{
		{Role: "cognee-engine", PID: 9001, State: daemonkit.StateExited, ExitedAt: &now, LastError: "cognee_boot_failed"},
	}
	statusMu.Unlock()
	srv.RecordStartupEngineFailure(context.Background(), errors.New("cognee_boot_failed"))

	mon = decodeResponse[map[string]any](t, manage(t, srv, http.MethodGet, "/v1/runtime/monitor", "", http.StatusOK))
	if mon["gateway_ok"] != true || mon["engine_ok"] != false || mon["engine_phase"] != "exited" || mon["state"] != "exited" {
		t.Fatalf("unexpected exited monitor payload: %+v", mon)
	}
	if mon["startup_error"] != "cognee_boot_failed" {
		t.Fatalf("expected startup_error=cognee_boot_failed, got %+v", mon["startup_error"])
	}
	inbox, _ = mon["inbox"].(map[string]any)
	if inbox["extraction_paused"] != true || inbox["pause_reason"] != "cognee_boot_failed" {
		t.Fatalf("expected extraction_paused with startup_error reason, got %+v", inbox)
	}
}

func TestHealthEndpointBoundedWaitNotStalledBySlowProbe(t *testing.T) {
	store := openTestStore(t)
	eng := &controllableHealthEngine{
		delay:  2 * time.Second,
		status: engine.HealthStatus{OK: false},
		err:    errors.New("slow engine"),
	}
	srv, err := NewServer(Config{
		Store:           store,
		Engine:          eng,
		ManagementToken: testManagement,
		DaemonStatus: func(context.Context) map[string]any {
			return map[string]any{"owner_pid": 54321}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	body := decodeResponse[map[string]any](t, manage(t, srv, http.MethodGet, "/api/health", "", http.StatusOK))
	elapsed := time.Since(started)
	if elapsed > 400*time.Millisecond {
		t.Fatalf("/api/health stalled for %s waiting on slow probe", elapsed)
	}
	if body["gateway_ok"] != true || body["owner_pid"] != float64(54321) {
		t.Fatalf("unexpected bounded /api/health response: %+v", body)
	}
}

func TestStartupPrepareErrorSurvivesEngineHealthRecovery(t *testing.T) {
	store := openTestStore(t)
	eng := &controllableHealthEngine{
		status: engine.HealthStatus{OK: true, Engine: "cognee"},
	}
	srv, err := NewServer(Config{
		Store:           store,
		Engine:          eng,
		ManagementToken: testManagement,
	})
	if err != nil {
		t.Fatal(err)
	}

	srv.RecordStartupPrepareFailure(context.Background(), errors.New("initial catalog projection failed"))
	srv.NotifyEngineStatus(true, "")

	mon := decodeResponse[map[string]any](t, manage(t, srv, http.MethodGet, "/v1/runtime/monitor", "", http.StatusOK))
	if mon["startup_error"] != "initial catalog projection failed" {
		t.Fatalf("engine health recovery must not clear startupPrepareErr: %+v", mon)
	}
	srv.ClearStartupPrepareFailure()
	mon = decodeResponse[map[string]any](t, manage(t, srv, http.MethodGet, "/v1/runtime/monitor", "", http.StatusOK))
	if mon["startup_error"] == "initial catalog projection failed" {
		t.Fatalf("ClearStartupPrepareFailure did not clear prepare error: %+v", mon)
	}
}

func TestManagedEngineGateHoldsInboxPendingUntilReady(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	ws := t.TempDir()
	proj := domain.Project{ID: "p-gate", Name: "Gate", DatasetID: "11111111-1111-4111-8111-111111111111", Aliases: []string{ws}, AutoCapture: true}
	if err := store.UpsertProject(ctx, proj); err != nil {
		t.Fatal(err)
	}
	item, err := store.EnqueueInboxItem(ctx, domain.CaptureInboxItem{
		ID:          "gate-item-1",
		ProjectID:   proj.ID,
		DatasetID:   proj.DatasetID,
		AgentID:     "codex",
		SessionID:   "gate-s1",
		MessageID:   "gate-m1",
		Kind:        string(engine.IngestKindCaptureQA),
		CaptureKey:  "gate-k1",
		PayloadJSON: `{"question":"q","answer":"a"}`,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	eng := &controllableHealthEngine{status: engine.HealthStatus{OK: true, Engine: "cognee"}}
	srv, err := NewServer(Config{
		Store:           store,
		Engine:          eng,
		ManagementToken: testManagement,
	})
	if err != nil {
		t.Fatal(err)
	}

	srv.SetManagedEngineGate(false)
	worked, err := srv.Inbox().ProcessOnce(ctx)
	if err != nil || worked {
		t.Fatalf("expected gated ProcessOnce to skip claim, worked=%v err=%v", worked, err)
	}
	got, err := store.GetInboxItem(ctx, item.ID)
	if err != nil || got.Status != "pending" {
		t.Fatalf("expected item to remain pending while gated, got %+v err=%v", got, err)
	}
}
