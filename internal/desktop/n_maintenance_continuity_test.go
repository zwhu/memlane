package desktop

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/platform"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func nMaintenanceFrame(a *App, section string) {
	a.visualFixture = true
	a.page, a.settingsSection, a.maintenanceSection = "设置", "environment", section
	a.runtimeStatus = RuntimeStatus{Phase: "unavailable", Installed: true}
	a.serviceState = "failed"
}
func TestN03ExplicitRetryUsesRealIPCAndNeverClaimsReadyFromACK(t *testing.T) {
	var calls atomic.Int64
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		if r.Op != "retry" || !r.UserConfirmed {
			t.Error("unexpected recovery mutation", r.Op)
		}
		calls.Add(1)
		return serviceipc.Response{OK: true, Result: map[string]any{"retry_requested": true, "recovery_owner": "actual-daemon-owner", "recovery_generation": 2}}
	})
	nMaintenanceFrame(a, "service")
	a.ApplyRuntimeAvailability(RuntimeAvailability{Phase: "unavailable", State: "failed", Recovery: "exhausted", RecoveryAttempts: 3, RecoveryLimit: 3, RecoveryWindowSeconds: 600})
	a.prepareRuntime = func() { t.Fatal("recovery invoked component prepare") }
	tt := ui.NewTester(a.View, 800, 720)
	if !tt.HasText("服务恢复失败") || !tt.HasText("原文处理组件在最近 10 分钟内已恢复失败 3 次，达到 3 次自动恢复上限。请处理原因后明确重试。") {
		t.Fatal("actual recovery ceiling omitted")
	}
	writeScreenshot(t, tt, "n03-exhausted-800.png")
	if err := tt.Click("明确重试服务"); err != nil {
		t.Fatal(err)
	}
	awaitProcessingDispatch(t, updates)
	tt.Frame()
	if calls.Load() != 1 || a.serviceState == "ready" || !a.maintenanceNext.Awaiting {
		t.Fatal("ACK fabricated recovery or duplicate request")
	}
	_ = tt.Click("明确重试服务")
	if calls.Load() != 1 {
		t.Fatal("pending recovery allowed duplicate")
	}
	a.ApplyRuntimeAvailability(RuntimeAvailability{Phase: "ready", State: "ready"})
	if !a.maintenanceNext.Awaiting || a.serviceState == "ready" {
		t.Fatal("proof-less observation overwrote pending recovery cycle")
	}
	a.ApplyRuntimeAvailability(RuntimeAvailability{Phase: "unavailable", State: "failed", Recovery: "exhausted", RecoveryOwner: "actual-daemon-owner", RecoveryGeneration: 1})
	if !a.maintenanceNext.Awaiting {
		t.Fatal("late pre-retry observation released duplicate gate")
	}
	a.ApplyRuntimeAvailability(RuntimeAvailability{Phase: "ready", State: "ready", RecoveryOwner: "actual-daemon-owner", RecoveryGeneration: 2})
	tt.Frame()
	if a.maintenanceNext.Awaiting || a.serviceState != "ready" {
		t.Fatal("actual ready receipt not adopted")
	}
}
func TestN04SettingsOpenDoesNotGrantAndRecheckOnlyObserves(t *testing.T) {
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		t.Error("permission recheck unexpectedly called service mutation", r.Op)
		return serviceipc.Response{OK: false}
	})
	nMaintenanceFrame(a, "service")
	a.runtimeStatus.Phase = "authorization"
	a.runtimeStatus.Service = platform.RequiresApproval
	var opens, observes atomic.Int64
	a.ConfigureMaintenanceRecovery(func(context.Context) (RuntimeAvailability, error) {
		observes.Add(1)
		return RuntimeAvailability{Phase: "authorization", Service: platform.RequiresApproval}, nil
	}, func() error { opens.Add(1); return nil }, nil)
	tt := ui.NewTester(a.View, 800, 720)
	if err := tt.Click("查看系统设置入口"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if opens.Load() != 1 || a.runtimeStatus.Phase != "authorization" {
		t.Fatal("opening settings fabricated permission")
	}
	if err := tt.Click("恢复许可后重新验证"); err != nil {
		t.Fatal(err)
	}
	awaitProcessingDispatch(t, updates)
	tt.Frame()
	if observes.Load() != 1 || a.runtimeStatus.Phase != "authorization" || !strings.Contains(a.maintenanceNext.Notice, "仍需系统批准") {
		t.Fatal("permission recheck fabricated success")
	}
	writeScreenshot(t, tt, "n04-permission-800.png")
	a.maintenanceNext.Observe = func(context.Context) (RuntimeAvailability, error) {
		return RuntimeAvailability{Phase: "ready", State: "ready", Service: platform.Enabled}, nil
	}
	a.recheckMaintenancePermission()
	awaitProcessingDispatch(t, updates)
	if a.runtimeStatus.Phase != "ready" {
		t.Fatal("actual injected ready observation not adopted")
	}
}
func TestN10RestoredSummaryUsesReceiptAndNeverAutoEnables(t *testing.T) {
	for _, width := range []int{1200, 800} {
		a := New("test", nil)
		nMaintenanceFrame(a, "backups")
		a.runtimeStatus.Phase = "stopped"
		a.serviceState = "stopped"
		a.backups.LastRestore = records.RestoreReceipt{ID: "real-receipt-shaped-fixture", State: "restored_stopped", Stats: records.RestoreStats{Memories: 7, Connections: 2, Plans: 3}}
		starts, reopens := 0, 0
		a.resumeRuntime = func() { starts++ }
		a.ConfigureMaintenanceRecovery(nil, nil, func() { reopens++ })
		tt := ui.NewTester(a.View, width, 720)
		if !tt.HasText("知识已恢复，服务保持停止") || starts != 0 || !tt.HasText("当次退休 2 个连接、停用 3 个计划") {
			t.Fatal("restore summary lacks actual receipt or auto-started")
		}
		writeScreenshot(t, tt, fmt.Sprintf("n10-restored-%d.png", width))
		if err := tt.Click("重新打开窗口"); err != nil {
			t.Fatal(err)
		}
		if reopens != 1 || starts != 0 || a.runtimeStatus.Phase != "stopped" {
			t.Fatal("reopen enabled service or repeated restore")
		}
		if err := tt.Click("重建检索索引"); err != nil {
			t.Fatal(err)
		}
		if a.maintenanceSection != "index" || starts != 0 {
			t.Fatal("index action started service automatically")
		}
		a.maintenanceSection = "backups"
		a.backups.LastRestore.State = "recovery_required"
		tt.Frame()
		if tt.HasText("知识已恢复，服务保持停止") {
			t.Fatal("incomplete restore shown as success")
		}
	}
}
func TestN12TypedStorageFailureRetainsDraftAndOriginalRequest(t *testing.T) {
	for _, code := range []string{"storage_full", "storage_read_only", "storage_permission"} {
		var requests atomic.Int64
		a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
			if r.Op != "apply" {
				t.Error(r.Op)
			}
			requests.Add(1)
			return serviceipc.Response{Code: code, Error: "storage write did not complete"}
		})
		a.visualFixture = true
		a.browsing.View = "memories"
		a.page, a.scope, a.detailMode = "项目", "project", "edit"
		a.editing = true
		item := MemoryItem{ID: "one", Dataset: "project", Origin: "knowledge", State: "active", Revision: 1}
		a.items = []MemoryItem{item}
		a.selectedID = "one"
		a.selectedItem = &item
		a.draftTitle, a.draft = "未保存标题", "保留完整草稿"
		a.drafts["one"] = Draft{Revision: 1, Text: a.draft}
		q := records.Mutation{Kind: "edit", OperationID: "same-operation", Memory: "one", Dataset: "project", ExpectedRevision: 1, Text: a.draft}
		a.pendingMutations["one"] = q
		a.applyMutation("one", q)
		awaitProcessingDispatch(t, updates)
		if requests.Load() != 1 || a.draft != "保留完整草稿" || a.drafts["one"].Revision != 1 || a.pendingMutations["one"].OperationID != "same-operation" || a.maintenanceNext.Storage == nil {
			t.Fatal("storage failure dropped draft/request or retried")
		}
		tt := ui.NewTester(a.View, 800, 720)
		if tt.HasText("查看服务状态") {
			t.Fatal("storage failure offered unrelated service recovery")
		}
		if !tt.HasText(maintenanceStorageMessage(code)) || strings.Contains(a.message, "恢复服务后") {
			t.Fatal("storage failure misdiagnosed as service recovery")
		}
		if code == "storage_full" {
			writeScreenshot(t, tt, "n12-diskfull-800.png")
		}
		a.libraryEpoch++
		tt.Frame()
		if tt.HasText(maintenanceStorageMessage(code)) {
			t.Fatal("old library fault leaked into new library")
		}
	}
}

func TestN12SocketPermissionCannotMasqueradeAsStorageFailure(t *testing.T) {
	a := New("test", nil)
	if a.noteMaintenanceStorageError("manual", "one", "op", &os.PathError{Op: "dial", Path: "/synthetic/socket", Err: syscall.EACCES}) {
		t.Fatal("socket access failure became database write failure")
	}
	if a.maintenanceNext.Storage != nil {
		t.Fatal("untrusted local network errno produced storage card")
	}
	if !a.noteLocalMaintenanceStorageError("manual", "one", "op", fmt.Errorf("actual local write: %w", syscall.EROFS)) {
		t.Fatal("actual local read-only write not classified")
	}
}
func TestN12LocalDraftWriteFailureRetainsStrictBarrierAndTypedCause(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := t.TempDir()
	a, updates := manualTestApp(t, root, uuid.NewString(), uuid.NewString())
	a.newMemory()
	a.draft = "本地草稿不可丢"
	directory := filepath.Join(root, "client-drafts")
	if err := os.Chmod(directory, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(directory, 0700)
	a.saveMemory()
	manualAwait(t, updates)
	if a.maintenanceNext.Storage == nil || a.maintenanceNext.Storage.Code != "storage_permission" || a.draft != "本地草稿不可丢" || a.pendingMutations[a.newDraftKey()].OperationID == "" {
		t.Fatal("actual local permission failure lost request or typed cause")
	}
	if err := manualFlush(t, a, updates); err == nil {
		t.Fatal("failed local storage bypassed quit barrier")
	}
}
