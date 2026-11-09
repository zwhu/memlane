package desktop

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
)

func TestProcessingSettingsActionGatesFollowDurableLedger(t *testing.T) {
	spec := records.ProcessingSpec{Mode: "agent", Agent: "pi", Provider: "deepseek", Model: "deepseek-flash", Connection: "connection", ConnectionGeneration: 2}
	s := ProcessingUI{Mode: "agent", Drafts: map[string]records.ProcessingSpec{"agent": spec}, Configuration: records.ProcessingConfiguration{Saved: map[string]records.ProcessingRevision{}}}
	if !s.CanSave() || s.CanValidate() || s.CanActivate() || s.CanCancel() {
		t.Fatal("an unsaved candidate bypassed the save-first ledger boundary")
	}

	s.Configuration.Saved["agent"] = records.ProcessingRevision{ID: "revision", Spec: spec}
	if s.CanSave() || !s.CanValidate() || s.CanActivate() || s.CanCancel() {
		t.Fatal("a saved unverified revision has incorrect action gates")
	}

	s.Configuration.Saved["agent"] = records.ProcessingRevision{ID: "revision", Spec: spec, Validation: &records.ProcessingValidation{ID: "validation", Revision: "revision", State: "validating"}}
	if s.CanValidate() || s.CanActivate() || !s.CanCancel() {
		t.Fatal("a running validation did not expose cancellation exclusively")
	}

	s.Configuration.Saved["agent"] = records.ProcessingRevision{ID: "revision", Spec: spec, Validation: &records.ProcessingValidation{ID: "validation", Revision: "revision", State: "succeeded"}}
	if s.CanSave() || s.CanValidate() || !s.CanActivate() || s.CanCancel() {
		t.Fatal("a verified saved revision did not require explicit activation")
	}

	s.Configuration.Current = records.ProcessingSelection{Active: true, Generation: 1, Revision: &records.ProcessingRevision{ID: "revision", Spec: spec}}
	if s.CanActivate() {
		t.Fatal("the active revision remained activatable")
	}
}

func TestSettingsMemoryProcessingNativeFrameKeepsUnavailableModesHonest(t *testing.T) {
	a := New("test", nil)
	a.page = "设置"
	a.settingsSection = "processing"
	a.processing = ProcessingUI{Mode: "agent", Drafts: map[string]records.ProcessingSpec{}, Configuration: records.ProcessingConfiguration{Saved: map[string]records.ProcessingRevision{}}}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, label := range []string{"环境与组件", "Agent 连接", "记忆处理", "LLM provider", "使用已连接 Agent", "使用 Magpie"} {
		if !tt.HasText(label) {
			t.Fatalf("missing accepted settings label %q", label)
		}
	}
	if tt.HasText("当前生产边界") || tt.HasText("隔离进程与终态回收") {
		t.Fatal("implementation details leaked into the daily processing page")
	}
	if err := tt.Click("LLM provider"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("LLM provider 暂不可用") {
		t.Fatal("provider became available without a real backend")
	}
	if err := tt.Click("使用 Magpie"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Magpie 暂不可用") {
		t.Fatal("Magpie became available without a real backend")
	}
	_ = tt.Click("设为当前使用")
	if a.processing.Configuration.Current.Active || a.processing.Busy {
		t.Fatal("an unsaved and unverified candidate was activatable")
	}
	tt.SetSize(980, 620)
	for _, label := range []string{"设为当前使用", "取消修改", "保存配置", "验证配置"} {
		node, ok := tt.Find(label)
		if !ok || node.X < 0 || node.Y < 0 || node.X+node.W > 980 || node.Y+node.H > 620 {
			t.Fatalf("minimum window hid fixed processing action %q: %#v", label, node)
		}
	}
}

func processingUIFixture(t *testing.T, handle func(serviceipc.Request) serviceipc.Response) (*App, chan func()) {
	t.Helper()
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
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			var request serviceipc.Request
			if json.NewDecoder(connection).Decode(&request) == nil {
				response := handle(request)
				response.Protocol, response.ID = 1, request.ID
				_ = json.NewEncoder(connection).Encode(response)
			}
			connection.Close()
		}
	}()
	app := New("test", nil)
	app.libraryContext = context.Background()
	app.library = serviceipc.Library{Root: root}
	dispatched := make(chan func(), 12)
	app.dispatch = func(fn func()) { dispatched <- fn }
	return app, dispatched
}

func awaitProcessingDispatch(t *testing.T, dispatched chan func()) {
	t.Helper()
	select {
	case fn := <-dispatched:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("processing IPC callback absent")
	}
}

func TestProcessingSettingsSaveValidateAndExplicitActivateUsePrivateIPC(t *testing.T) {
	connection, revisionID, validationID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	spec := records.ProcessingSpec{Mode: "agent", Agent: "pi", Provider: "deepseek", Model: "deepseek-flash", Connection: connection, ConnectionGeneration: 3}
	operations := make(chan string, 8)
	app, dispatched := processingUIFixture(t, func(request serviceipc.Request) serviceipc.Response {
		operations <- request.Op
		switch request.Op {
		case "processing_save":
			if !request.UserConfirmed || request.ProcessingSave == nil || request.ProcessingSave.Spec.Connection != connection {
				return serviceipc.Response{OK: false, Error: "invalid save fixture"}
			}
			return serviceipc.Response{OK: true, Result: records.ProcessingRevision{ID: revisionID, Spec: spec}}
		case "processing_validate":
			if !request.UserConfirmed || request.ProcessingValidation == nil || request.ProcessingValidation.Revision != revisionID {
				return serviceipc.Response{OK: false, Error: "invalid validation fixture"}
			}
			return serviceipc.Response{OK: true, Result: records.ProcessingValidation{ID: validationID, Revision: revisionID, State: "validating"}}
		case "processing_validation":
			return serviceipc.Response{OK: true, Result: records.ProcessingValidationStatus{Validation: records.ProcessingValidation{ID: validationID, Revision: revisionID, State: "succeeded"}, Progress: &records.ProcessingValidationProgress{Started: true, Terminal: true, Reaped: true, GroupGone: true, NativeSchemaRequested: true}}}
		case "processing_activate":
			if !request.UserConfirmed || request.ProcessingActivation == nil || request.ProcessingActivation.Revision != revisionID {
				return serviceipc.Response{OK: false, Error: "invalid activation fixture"}
			}
			return serviceipc.Response{OK: true, Result: records.ProcessingActivationReceipt{Revision: revisionID, Generation: 1}}
		case "processing_get":
			verified := records.ProcessingValidation{ID: validationID, Revision: revisionID, State: "succeeded"}
			revision := records.ProcessingRevision{ID: revisionID, Spec: spec, Validation: &verified}
			return serviceipc.Response{OK: true, Result: records.ProcessingConfiguration{Saved: map[string]records.ProcessingRevision{"agent": revision}, Current: records.ProcessingSelection{Active: true, Generation: 1, Revision: &revision}}}
		default:
			return serviceipc.Response{OK: false, Error: "unexpected operation"}
		}
	})
	app.processing.Drafts["agent"] = spec
	app.saveProcessing()
	awaitProcessingDispatch(t, dispatched)
	if !app.processing.CanValidate() {
		t.Fatal("saved revision did not become the only validation target")
	}
	app.validateProcessing()
	awaitProcessingDispatch(t, dispatched)
	awaitProcessingDispatch(t, dispatched)
	if !app.processing.CanActivate() || app.processing.Progress == nil || !app.processing.Progress.GroupGone {
		t.Fatal("terminal daemon evidence did not gate explicit activation")
	}
	app.activateProcessing()
	awaitProcessingDispatch(t, dispatched)
	if app.processing.CanActivate() || !app.processing.Configuration.Current.Active || app.processing.Configuration.Current.Revision.ID != revisionID {
		t.Fatal("activation receipt did not refresh the current selection")
	}
	for _, want := range []string{"processing_save", "processing_validate", "processing_validation", "processing_activate", "processing_get"} {
		select {
		case got := <-operations:
			if got != want {
				t.Fatalf("operation order = %q, want %q", got, want)
			}
		default:
			t.Fatalf("missing operation %q", want)
		}
	}
}

func TestProcessingSettingsCancelUsesDaemonOwnedValidation(t *testing.T) {
	validationID := uuid.NewString()
	requests := make(chan serviceipc.Request, 1)
	app, dispatched := processingUIFixture(t, func(request serviceipc.Request) serviceipc.Response {
		requests <- request
		return serviceipc.Response{OK: true}
	})
	spec := records.ProcessingSpec{Mode: "agent", Agent: "pi", Provider: "deepseek", Model: "deepseek-flash", Connection: uuid.NewString(), ConnectionGeneration: 1}
	validation := records.ProcessingValidation{ID: validationID, Revision: uuid.NewString(), State: "validating"}
	app.processing.Drafts["agent"] = spec
	app.processing.Configuration.Saved["agent"] = records.ProcessingRevision{ID: validation.Revision, Spec: spec, Validation: &validation}
	app.cancelProcessingValidation()
	awaitProcessingDispatch(t, dispatched)
	request := <-requests
	if request.Op != "processing_validation_cancel" || !request.UserConfirmed || request.ProcessingValidationID != validationID {
		t.Fatal("cancel did not use the daemon-owned validation action")
	}
	if !app.processing.Configuration.Saved["agent"].Validation.CancelRequested || app.processing.Configuration.Current.Active {
		t.Fatal("cancel acknowledgement changed activation or lost pending terminal state")
	}
}
