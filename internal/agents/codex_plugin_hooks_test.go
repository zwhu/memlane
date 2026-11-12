//go:build darwin || linux

package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexPluginHooksRequireExactSourceTrustAndCompleteCatalog(t *testing.T) {
	o := CodexPluginHookOptions{PluginID: "cognee@cognee", Directory: "/synthetic", RequiredEvents: []string{"stop"}}
	for _, tt := range []struct {
		name, body, want string
		fails            bool
	}{
		{"installed_but_untrusted", `{"data":[{"cwd":"/synthetic","hooks":[{"eventName":"stop","currentHash":"h","trustStatus":"untrusted","source":"plugin","pluginId":"cognee@cognee","enabled":true}]}]}`, "review_required", false},
		{"trusted", `{"data":[{"cwd":"/synthetic","hooks":[{"eventName":"stop","currentHash":"h","trustStatus":"trusted","source":"plugin","pluginId":"cognee@cognee","enabled":true}]}]}`, "hooks_ready", false},
		{"changed_script", `{"data":[{"cwd":"/synthetic","hooks":[{"eventName":"stop","currentHash":"new","trustStatus":"modified","source":"plugin","pluginId":"cognee@cognee","enabled":true}]}]}`, "review_required", false},
		{"disabled", `{"data":[{"cwd":"/synthetic","hooks":[{"eventName":"stop","currentHash":"h","trustStatus":"trusted","source":"plugin","pluginId":"cognee@cognee","enabled":false}]}]}`, "hooks_disabled", false},
		{"wrong_plugin", `{"data":[{"cwd":"/synthetic","hooks":[{"eventName":"stop","currentHash":"h","trustStatus":"trusted","source":"plugin","pluginId":"other@cognee","enabled":true}]}]}`, "hooks_missing", false},
		{"system_cannot_stand_in_for_plugin", `{"data":[{"cwd":"/synthetic","hooks":[{"eventName":"stop","currentHash":"h","trustStatus":"trusted","source":"system","pluginId":"cognee@cognee","enabled":true}]}]}`, "hooks_missing", false},
		{"missing_event", `{"data":[{"cwd":"/synthetic","hooks":[{"eventName":"sessionStart","currentHash":"h","trustStatus":"trusted","source":"plugin","pluginId":"cognee@cognee","enabled":true}]}]}`, "hooks_incomplete", false},
		{"partial_errors", `{"data":[{"cwd":"/synthetic","errors":[{}],"hooks":[{"eventName":"stop","currentHash":"h","trustStatus":"trusted","source":"plugin","pluginId":"cognee@cognee","enabled":true}]}]}`, "catalog_incomplete", false},
		{"other_scope", `{"data":[{"cwd":"/other","hooks":[]}]}`, "", true},
		{"no_scope_receipt", `{"data":[]}`, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var catalog codexHookCatalog
			if err := json.Unmarshal([]byte(tt.body), &catalog); err != nil {
				t.Fatal(err)
			}
			got, err := summarizeCodexPluginHooks(catalog, o)
			if (err != nil) != tt.fails || !tt.fails && got.State != tt.want {
				t.Fatalf("state=%q error=%v", got.State, err)
			}
		})
	}
}

// Opt-in real host probe: use only the explicitly prepared synthetic home.
// The probe never starts a model turn or reads a transcript.
func TestCodexPluginHooksActualIsolatedHost(t *testing.T) {
	executable, home := os.Getenv("MEMLANE_HOOK_PROBE_CODEX"), os.Getenv("MEMLANE_HOOK_PROBE_HOME")
	if executable == "" || home == "" {
		t.Skip("explicit synthetic host required")
	}
	out, err := InspectCodexPluginHooks(context.Background(), CodexPluginHookOptions{Executable: executable, Home: home, CodexDirectory: filepath.Join(home, ".codex"), Directory: home, PluginID: "cognee@cognee", RequiredEvents: []string{"sessionStart", "userPromptSubmit", "postToolUse", "stop", "sessionEnd", "preCompact"}})
	if err != nil || out.State != "review_required" || len(out.Hooks) != 8 || out.Errors != 0 {
		t.Fatalf("unexpected isolated hook state: %+v, %v", out, err)
	}
	if _, err = os.Stat(filepath.Join(home, ".codex", "auth.json")); !os.IsNotExist(err) {
		t.Fatal("probe home must not contain auth.json")
	}
	t.Logf("actual host state=%s plugin_hooks=%d other_hooks=%d", out.State, len(out.Hooks), out.OtherHooks)
}
