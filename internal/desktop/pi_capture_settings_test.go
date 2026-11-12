package desktop

import (
	"context"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/serviceipc"
	"testing"
)

func TestPiCaptureSettingsRequiresConfirmationRetriesSameIntentAndStops(t *testing.T) {
	for _, size := range [][2]int{{1200, 820}, {800, 620}} {
		t.Run(string(rune(size[0])), func(t *testing.T) {
			cid, project := uuid.NewString(), uuid.NewString()
			connection := records.Connection{ID: cid, Agent: "pi", Name: "pi", Generation: 1, Enabled: true, AllowedScopes: []string{project}}
			binding := records.CogneeBinding{Operation: uuid.NewString(), Project: project, Instance: uuid.NewString(), Dataset: uuid.NewString(), ConnectionID: cid, Generation: 1, MaterialSubmission: true}
			row := records.PiCaptureSetting{Binding: binding, Ready: true}
			calls := 0
			operation := ""
			loseFirst := true
			a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				switch r.Op {
				case "pi_capture_settings":
					return serviceipc.Response{OK: true, Result: []records.PiCaptureSetting{row}}
				case "pi_capture_consent_review":
					calls++
					q := r.PiCaptureConsentReview
					if !r.UserConfirmed || q == nil || q.Binding != binding {
						t.Error("confirmation lost scope")
					}
					if operation == "" {
						operation = q.Operation
					} else if operation != q.Operation {
						t.Error("unknown consent result got a new operation")
					}
					g := records.PiCaptureConsent{Operation: q.Operation, Binding: q.Binding, PreviousKey: q.PreviousKey, Key: "saved-grant", Enabled: true}
					row.Consent = &g
					row.PreviousKey = g.Key
					if loseFirst {
						loseFirst = false
						return serviceipc.Response{Code: "timeout", Error: "synthetic lost acknowledgement"}
					}
					return serviceipc.Response{OK: true, Result: g}
				case "pi_capture_consent_disable":
					if !r.UserConfirmed || r.PiCaptureConsentDisable.ExpectedKey != row.Consent.Key {
						t.Error("stop scope changed")
					}
					g := *row.Consent
					g.Enabled = false
					row.Consent = &g
					return serviceipc.Response{OK: true}
				}
				return serviceipc.Response{Code: "unsupported"}
			})
			a.page, a.settingsSection = "设置", "agents"
			a.agents.Context = context.Background()
			a.agents.Dispatch = a.dispatch
			a.agents.Selection = "connection-" + cid
			a.agents.Connections = []records.Connection{connection}
			a.agents.Installs = map[string]agents.Installation{cid: {Connection: connection, PiCaptureSupported: true}}
			a.refreshPiCaptureSettings(connection)
			awaitProcessingDispatch(t, updates)
			tt := ui.NewTester(func(c *ui.Context) { a.piCaptureSettings(c, connection) }, size[0], size[1])
			if err := tt.Click("开启记录"); err != nil {
				t.Fatal(err)
			}
			if calls != 0 || a.agents.PiCapture.Review == nil {
				t.Fatal("opening review enabled capture")
			}
			if err := tt.Click("确认开启记录"); err != nil {
				t.Fatal(err)
			}
			awaitProcessingDispatch(t, updates)
			tt.Frame()
			if a.agents.PiCapture.Review == nil {
				t.Fatal("unknown receipt discarded frozen review")
			}
			if err := tt.Click("确认开启记录"); err != nil {
				t.Fatal(err)
			}
			awaitProcessingDispatch(t, updates)
			awaitProcessingDispatch(t, updates)
			tt.Frame()
			if calls != 2 || !tt.HasText("已允许新对话") {
				t.Fatal("explicit retry not reflected")
			}
			if err := tt.Click("停止记录"); err != nil {
				t.Fatal(err)
			}
			awaitProcessingDispatch(t, updates)
			awaitProcessingDispatch(t, updates)
			tt.Frame()
			if !tt.HasText("已停止记录") || row.Consent.Enabled {
				t.Fatal("stop did not preserve explicit disabled state")
			}
		})
	}
}

func TestPiCaptureSettingsRejectsLateNavigationAndGeneration(t *testing.T) {
	connection := records.Connection{ID: uuid.NewString(), Agent: "pi", Generation: 1, Enabled: true}
	a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
		return serviceipc.Response{OK: true, Result: []records.PiCaptureSetting{{Ready: true}}}
	})
	a.page, a.settingsSection = "设置", "agents"
	a.agents.Dispatch = a.dispatch
	a.agents.Connections = []records.Connection{connection}
	a.agents.Selection = "connection-" + connection.ID
	a.refreshPiCaptureSettings(connection)
	a.agents.Connections[0].Generation = 2
	awaitProcessingDispatch(t, updates)
	if a.agents.PiCapture.Loaded || a.agents.PiCapture.Loading {
		t.Fatal("late old-generation settings adopted or locked refresh")
	}
}

func TestPiCaptureSettingsFailureCannotKeepEnabledPresentation(t *testing.T) {
	for _, code := range []string{"timeout", "scope_changed", "connection_revoked"} {
		t.Run(code, func(t *testing.T) {
			connection := records.Connection{ID: uuid.NewString(), Agent: "pi", Generation: 1, Enabled: true}
			binding := records.CogneeBinding{Project: uuid.NewString(), ConnectionID: connection.ID, Generation: 1}
			fail := false
			a, updates := processingUIFixture(t, func(r serviceipc.Request) serviceipc.Response {
				if fail {
					return serviceipc.Response{Code: code}
				}
				return serviceipc.Response{OK: true, Result: []records.PiCaptureSetting{{Binding: binding, Ready: true, Consent: &records.PiCaptureConsent{Enabled: true}}}}
			})
			a.page, a.settingsSection = "设置", "agents"
			a.agents.Dispatch = a.dispatch
			a.agents.Selection = "connection-" + connection.ID
			a.agents.Connections = []records.Connection{connection}
			a.refreshPiCaptureSettings(connection)
			awaitProcessingDispatch(t, updates)
			fail = true
			a.refreshPiCaptureSettings(connection)
			awaitProcessingDispatch(t, updates)
			s := a.agents.PiCapture
			if s.Loaded {
				t.Fatal("failed read kept current consent state")
			}
			if code == "timeout" && len(s.Rows) != 1 {
				t.Fatal("temporary failure discarded prior settings")
			}
			if code != "timeout" && len(s.Rows) != 0 {
				t.Fatal("revoked settings still present")
			}
			tt := ui.NewTester(func(c *ui.Context) { a.piCaptureSettings(c, connection) }, 800, 620)
			if tt.HasText("已允许新对话") {
				t.Fatal("failed authority check still presented enabled")
			}
		})
	}
}
