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
	s.Configuration.Runtime = map[string]records.ProcessingReadiness{"revision": {State: "ready"}}
	if s.CanSave() || !s.CanValidate() || !s.CanActivate() || s.CanCancel() {
		t.Fatal("a verified saved revision did not permit explicit revalidation/activation")
	}

	s.Configuration.Current = records.ProcessingSelection{Active: true, Generation: 1, Revision: &records.ProcessingRevision{ID: "revision", Spec: spec}}
	if s.CanActivate() {
		t.Fatal("the active revision remained activatable")
	}
}

func TestProcessingCodexCandidateDoesNotActivateOnSelection(t *testing.T) {
	a := New("test", nil)
	a.processing.Mode = "agent"
	current := records.ProcessingRevision{ID: uuid.NewString(), Spec: records.ProcessingSpec{Mode: "agent", Agent: "pi", Provider: "deepseek", Model: "deepseek-flash"}}
	a.processing.Configuration.Current = records.ProcessingSelection{Active: true, Generation: 4, Revision: &current}
	candidate := ProcessingAgentCandidate{Agent: "codex", Connection: uuid.NewString(), Generation: 2, Provider: "explicit-provider", Model: "explicit-model"}
	a.selectProcessingAgent(candidate)
	if a.processing.draft().Connection == candidate.Connection {
		t.Fatal("unavailable Codex candidate was selectable")
	}
	candidate.Available = true
	a.selectProcessingAgent(candidate)
	draft := a.processing.draft()
	if draft.Agent != "codex" || draft.Connection != candidate.Connection || draft.Provider != candidate.Provider || draft.Model != candidate.Model || !a.processing.CanSave() {
		t.Fatal("Codex selection lost its verified identity or current model")
	}
	if a.processing.Configuration.Current.Revision.ID != current.ID || a.processing.Configuration.Current.Generation != 4 || a.processing.CanValidate() || a.processing.CanActivate() {
		t.Fatal("selecting a Codex candidate bypassed save, verification or explicit activation")
	}
}

func TestSettingsMemoryProcessingNativeFrameKeepsUnreadModelsHonest(t *testing.T) {
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
	if !tt.HasText("先配置供应商并读取模型目录") {
		t.Fatal("provider displayed a fabricated model catalog")
	}
	if err := tt.Click("使用 Magpie"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("等待本机记忆服务") {
		t.Fatal("Magpie claimed detection without a real backend")
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

func TestProcessingPollingKeepsFailedOperationVisible(t *testing.T) {
	spec := records.ProcessingSpec{Mode: "agent", Agent: "pi", Connection: uuid.NewString(), ConnectionGeneration: 1, Provider: "deepseek", Model: "deepseek-flash"}
	revision := records.ProcessingRevision{ID: uuid.NewString(), Spec: spec}
	configuration := records.ProcessingConfiguration{Saved: map[string]records.ProcessingRevision{"agent": revision}}
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "processing_validate":
			return serviceipc.Response{Code: "invalid_request", Error: "connection_changed"}
		case "processing_get":
			return serviceipc.Response{OK: true, Result: configuration}
		case "connections":
			return serviceipc.Response{OK: true, Result: []records.Connection{}}
		default:
			return serviceipc.Response{Error: "unexpected request " + r.Op}
		}
	})
	a.processing = ProcessingUI{Mode: "agent", Drafts: map[string]records.ProcessingSpec{"agent": spec}, Configuration: configuration}
	a.validateProcessing()
	awaitProcessingDispatch(t, updates)
	failure := a.processing.Error
	if failure == "" {
		t.Fatal("validation failure not displayed")
	}
	a.processing.ReadError = "old read failure"
	a.refreshProcessing()
	awaitProcessingDispatch(t, updates)
	if a.processing.Error != failure || a.processing.ReadError != "" || a.processing.Polling {
		t.Fatal("polling erased failed operation or fabricated running validation", a.processing)
	}
}

func TestProcessingSettingsSaveValidateAndExplicitActivateUsePrivateIPC(t *testing.T) {
	connection, revisionID, validationID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	spec := records.ProcessingSpec{Mode: "agent", Agent: "pi", Provider: "deepseek", Model: "deepseek-flash", Connection: connection, ConnectionGeneration: 3}
	operations := make(chan string, 12)
	activated := false
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
			activated = true
			return serviceipc.Response{OK: true, Result: records.ProcessingActivationReceipt{Revision: revisionID, Generation: 1}}
		case "processing_get":
			verified := records.ProcessingValidation{ID: validationID, Revision: revisionID, State: "succeeded"}
			revision := records.ProcessingRevision{ID: revisionID, Spec: spec, Validation: &verified}
			return serviceipc.Response{OK: true, Result: records.ProcessingConfiguration{Saved: map[string]records.ProcessingRevision{"agent": revision}, Runtime: map[string]records.ProcessingReadiness{revisionID: {State: "ready"}}, Current: records.ProcessingSelection{Active: activated, Generation: 1, Revision: &revision}}}
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
	awaitProcessingDispatch(t, dispatched)
	if !app.processing.CanActivate() || app.processing.Progress == nil || !app.processing.Progress.GroupGone {
		t.Fatal("terminal daemon evidence did not gate explicit activation")
	}
	app.activateProcessing()
	awaitProcessingDispatch(t, dispatched)
	if app.processing.CanActivate() || !app.processing.Configuration.Current.Active || app.processing.Configuration.Current.Revision.ID != revisionID {
		t.Fatal("activation receipt did not refresh the current selection")
	}
	for _, want := range []string{"processing_save", "processing_validate", "processing_validation", "processing_get", "processing_activate", "processing_get"} {
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

func TestProcessingOldSuccessWithChangedRuntimeExposesExplicitRevalidation(t *testing.T) {
	a := New("test", nil)
	spec := records.ProcessingSpec{Mode: "agent", Agent: "pi", Provider: "deepseek", Model: "deepseek-flash", Connection: uuid.NewString(), ConnectionGeneration: 3}
	revision := records.ProcessingRevision{ID: uuid.NewString(), Spec: spec, Validation: &records.ProcessingValidation{ID: uuid.NewString(), State: "succeeded"}}
	a.page, a.settingsSection = "设置", "processing"
	a.processing = ProcessingUI{Mode: "agent", Loaded: true, Drafts: map[string]records.ProcessingSpec{"agent": spec}, Configuration: records.ProcessingConfiguration{Saved: map[string]records.ProcessingRevision{"agent": revision}, Runtime: map[string]records.ProcessingReadiness{revision.ID: {State: "dependency_changed"}}, Current: records.ProcessingSelection{Active: true, Generation: 2, Revision: &revision}}}
	if !a.processing.CanValidate() || a.processing.CanActivate() || a.processing.CanSave() || a.processing.currentRuntimeReady() {
		t.Fatal("stale success blocked revalidation or authorized activation")
	}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("依赖已变化，请重新验证") || !tt.HasText("重新验证") {
		t.Fatal("runtime drift recovery missing")
	}
	if err := tt.Click("重新验证"); err != nil {
		t.Fatal("explicit revalidation is disabled", err)
	}
	if a.processing.Configuration.Current.Generation != 2 || a.processing.Configuration.Saved["agent"].Validation.State != "succeeded" {
		t.Fatal("read-only UI changed historical receipt or selection")
	}
}

func TestProcessingInitialReadAndFailureNeverClaimUnconfigured(t *testing.T) {
	s := ProcessingUI{}
	if s.currentLabel() != "记忆处理待读取" {
		t.Fatal("unread zero state appeared unconfigured")
	}
	s.ReadError = "explicit read failure"
	if s.currentLabel() != "记忆处理读取失败" {
		t.Fatal("read failure appeared unconfigured")
	}
	s.Loaded = true
	s.ReadError = ""
	if s.currentLabel() != "记忆处理未配置" {
		t.Fatal("confirmed empty configuration lacks distinct state")
	}
}

// Reproduce 009: a legitimate runtime metadata read takes longer than the
// ordinary five-second UI timeout, but no model task is started by this read.
func TestProcessingRuntimeReadAcceptsSlowReadyMetadata(t *testing.T) {
	revision := records.ProcessingRevision{ID: uuid.NewString(), Spec: records.ProcessingSpec{Mode: "agent", Agent: "pi", Connection: uuid.NewString(), ConnectionGeneration: 1, Provider: "synthetic", Model: "synthetic"}}
	revision.Validation = &records.ProcessingValidation{ID: uuid.NewString(), Revision: revision.ID, State: "succeeded"}
	configuration := records.ProcessingConfiguration{Saved: map[string]records.ProcessingRevision{"agent": revision}, Runtime: map[string]records.ProcessingReadiness{revision.ID: {State: "ready"}}}
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		switch r.Op {
		case "processing_get":
			time.Sleep(6200 * time.Millisecond)
			return serviceipc.Response{OK: true, Result: configuration}
		case "connections":
			return serviceipc.Response{OK: true, Result: []records.Connection{}}
		default:
			return serviceipc.Response{Code: "unexpected_read", Error: "unexpected read"}
		}
	})
	a.readProcessing(false)
	select {
	case apply := <-updates:
		apply()
	case <-time.After(9 * time.Second):
		t.Fatal("runtime metadata read never completed")
	}
	if a.processing.ReadError != "" || !a.processing.Loaded || a.processing.Configuration.Runtime[revision.ID].State != "ready" {
		t.Fatalf("valid slow ready metadata was replaced by timeout: error=%q loaded=%v", a.processing.ReadError, a.processing.Loaded)
	}
}

func TestProcessingSlowMetadataActionRoundTrips(t *testing.T) {
	for _, action := range []string{"begin", "activate"} {
		t.Run(action, func(t *testing.T) {
			t.Parallel()
			revision := records.ProcessingRevision{ID: uuid.NewString(), Spec: records.ProcessingSpec{Mode: "agent", Agent: "pi", Connection: uuid.NewString(), ConnectionGeneration: 1, Provider: "synthetic", Model: "synthetic"}}
			validation := records.ProcessingValidation{ID: uuid.NewString(), Revision: revision.ID, State: "succeeded"}
			revision.Validation = &validation
			configuration := records.ProcessingConfiguration{Saved: map[string]records.ProcessingRevision{"agent": revision}, Runtime: map[string]records.ProcessingReadiness{revision.ID: {State: "ready"}}}
			a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				switch r.Op {
				case "processing_validate":
					time.Sleep(11 * time.Second)
					return serviceipc.Response{OK: true, Result: validation}
				case "processing_validation":
					return serviceipc.Response{OK: true, Result: records.ProcessingValidationStatus{Validation: validation}}
				case "processing_activate":
					time.Sleep(5600 * time.Millisecond)
					return serviceipc.Response{OK: true, Result: records.ProcessingActivationReceipt{Revision: revision.ID, Generation: 1}}
				case "processing_get":
					if action == "activate" {
						time.Sleep(5600 * time.Millisecond)
					}
					out := configuration
					if action == "activate" {
						out.Current = records.ProcessingSelection{Active: true, Generation: 1, Revision: &revision}
					}
					return serviceipc.Response{OK: true, Result: out}
				default:
					return serviceipc.Response{Code: "unexpected", Error: "unexpected action"}
				}
			})
			a.processing = ProcessingUI{Mode: "agent", Loaded: true, Configuration: configuration, Drafts: map[string]records.ProcessingSpec{"agent": revision.Spec}}
			if action == "begin" {
				a.validateProcessing()
			} else {
				a.activateProcessing()
			}
			select {
			case apply := <-updates:
				apply()
			case <-time.After(15 * time.Second):
				t.Fatal("slow action never returned", action)
			}
			if a.processing.Error != "" || a.processing.Busy {
				t.Fatal("valid metadata action timed out", action, a.processing.Error)
			}
			if action == "activate" {
				if !a.processing.Configuration.Current.Active || a.processing.Configuration.Current.Revision.ID != revision.ID {
					t.Fatal("activation receipt/readback not applied")
				}
			} else {
				// Finish only the synthetic ledger poll/read, never a model execution.
				deadline := time.After(3 * time.Second)
				for a.processing.Polling || a.processing.Loading {
					select {
					case apply := <-updates:
						apply()
					case <-deadline:
						t.Fatal("synthetic status poll did not finish")
					}
				}
				if a.processing.Configuration.Current.Active {
					t.Fatal("begin implicitly activated configuration")
				}
			}
		})
	}
}
