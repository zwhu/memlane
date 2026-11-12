package desktop

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
)

func TestSettingsRemovalRejectsChangedConnectionAfterReview(t *testing.T) {
	a := New("test", nil)
	a.page, a.settingsSection = "设置", "agents"
	a.agents.Context = context.Background()
	a.agents.Connections = []records.Connection{{ID: "connection", Agent: "codex", Name: "Codex", Enabled: true, Generation: 2}}
	a.agents.Selection = "connection-connection"
	a.agents.Found = []agents.Discovery{{Agent: "codex", Name: "Codex", Path: "/synthetic/codex"}}
	tt := ui.NewTester(a.View, 800, 620)
	if err := tt.Click("撤除接入"); err != nil {
		t.Fatal(err)
	}
	a.agents.Connections[0].Generation = 3
	if err := tt.Click("确认撤除接入"); err != nil {
		t.Fatal(err)
	}
	if a.agents.Busy || !a.agents.Connections[0].Enabled || a.agents.SettingsRemoveConfirmation != "" || !strings.Contains(a.agents.Notice, "连接已变化") {
		t.Fatal("stale review revoked changed connection")
	}
}

func TestSettingsNavigationPreservesDraftAndShowsAllMaintenanceEntrances(t *testing.T) {
	a := New("test", nil)
	a.page = "设置"
	a.settingsSection = "processing"
	a.processing.ensure()
	draft := records.ProcessingSpec{Mode: "agent", Agent: "pi", Connection: "existing", Model: "draft-model"}
	a.processing.Drafts["agent"] = draft
	a.processing.Configuration.Current = records.ProcessingSelection{Active: true, Generation: 7, Revision: &records.ProcessingRevision{ID: "active", Spec: records.ProcessingSpec{Mode: "provider", Model: "active-model"}}}
	current := a.processing.Configuration.Current
	for _, size := range [][2]int{{1200, 820}, {800, 620}} {
		tt := ui.NewTester(a.View, size[0], size[1])
		for _, section := range settingsSections {
			if err := tt.Click(section.label); err != nil {
				t.Fatal(err)
			}
			if a.selectedSettingsSection() != section.key {
				t.Fatal("wrong section", section.key)
			}
		}
		a.selectSettingsSection("processing")
		tt.Frame()
		for _, tab := range []string{"自配 LLM", "Magpie", "已连接 Agent"} {
			if err := tt.Click(tab); err != nil {
				t.Fatal(err)
			}
			activate, ok := tt.Find("设为当前使用")
			if !ok || activate.X+activate.W > float32(size[0]) {
				t.Fatal("activation outside viewport", activate)
			}
		}
		if !reflect.DeepEqual(a.processing.draft(), draft) || !reflect.DeepEqual(a.processing.Configuration.Current, current) {
			t.Fatal("navigation changed draft or active revision")
		}
	}
}

func TestSettingsSharedConnectionRemovalRequiresReviewAndCanCancel(t *testing.T) {
	a := New("test", nil)
	a.page, a.settingsSection = "设置", "agents"
	conn := records.Connection{ID: "connection", Agent: "codex", Name: "Codex", Enabled: true, Generation: 2}
	a.agents.Context = context.Background()
	a.agents.Connections = []records.Connection{conn}
	a.agents.Selection = "connection-connection"
	a.agents.Found = []agents.Discovery{{Agent: "codex", Name: "Codex", Path: "/synthetic/codex"}}
	tt := ui.NewTester(a.View, 800, 620)
	if tt.HasText("确认撤除接入") {
		t.Fatal("destructive confirmation offered before review")
	}
	if err := tt.Click("撤除接入"); err != nil {
		t.Fatal(err)
	}
	if a.agents.SettingsRemoveConfirmation != conn.ID || !tt.HasText("确认撤除接入") || !reflect.DeepEqual(a.agents.Connections[0], conn) || a.agents.Busy {
		t.Fatal("review changed connection or missing confirmation")
	}
	if err := tt.Click("取消撤除"); err != nil {
		t.Fatal(err)
	}
	if a.agents.SettingsRemoveConfirmation != "" || !reflect.DeepEqual(a.agents.Connections[0], conn) {
		t.Fatal("cancel changed connection")
	}
}

func TestSettingsProviderEditsGateActivationAndPreserveSupplierDrafts(t *testing.T) {
	a := New("test", nil)
	a.processing.ensure()
	a.processing.ensureHTTP()
	s := &a.processing
	s.Mode = "provider"
	profile := records.ProcessingSpec{Mode: "provider", Provider: "deepseek", Endpoint: "https://example.invalid/v1", Protocol: "openai-chat", Credential: "synthetic-credential", CredentialRevision: 1, Model: "exact-model"}
	s.HTTP.Profiles["deepseek"] = profile
	s.Drafts["provider"] = profile
	s.Configuration.Saved["provider"] = records.ProcessingRevision{ID: "revision", Spec: profile, Validation: &records.ProcessingValidation{State: "succeeded"}}
	s.Configuration.Runtime = map[string]records.ProcessingReadiness{"revision": {State: "ready"}}
	a.selectSettingsSupplier("deepseek")
	if !s.CanActivate() {
		t.Fatal("unchanged verified configuration blocked")
	}
	s.HTTP.Endpoint = "https://changed.invalid/v1"
	s.HTTP.Secret = "synthetic-only-secret"
	if s.CanActivate() || s.CanValidate() {
		t.Fatal("edited address or credential inherited validation")
	}
	a.selectSettingsSupplier("openai")
	a.selectSettingsSupplier("deepseek")
	if s.HTTP.Endpoint != "https://changed.invalid/v1" || s.HTTP.Secret != "synthetic-only-secret" {
		t.Fatal("supplier switch lost unsaved draft")
	}
	if s.Configuration.Current.Active {
		t.Fatal("supplier switch enabled processing")
	}
	if !a.processingHasDraftChanges() {
		t.Fatal("edited provider form cannot cancel")
	}
	a.cancelProcessingChanges()
	if s.HTTP.Secret != "" || len(s.HTTP.SettingsDrafts) != 0 || s.draft() != profile {
		t.Fatal("cancel did not restore saved provider and discard transient secret")
	}
}

func TestSettingsSavedProviderPreservesCustomAddressBeforeSupplierRefresh(t *testing.T) {
	a := New("test", nil)
	a.page, a.settingsSection = "设置", "processing"
	s := &a.processing
	s.ensure()
	s.Mode = "provider"
	spec := records.ProcessingSpec{Mode: "provider", Provider: "deepseek", Endpoint: "https://saved-custom.invalid/v1", Protocol: "openai-responses", Credential: "saved-reference", CredentialRevision: 2, Model: "saved-model"}
	s.Drafts["provider"] = spec
	s.Configuration.Saved["provider"] = records.ProcessingRevision{ID: "saved", Spec: spec}
	ui.NewTester(a.View, 800, 620)
	if s.HTTP.Endpoint != spec.Endpoint || s.HTTP.Protocol != spec.Protocol || s.draft() != spec {
		t.Fatal("saved custom endpoint replaced by supplier template while refreshing", s.HTTP.Endpoint, s.HTTP.Protocol)
	}
}
