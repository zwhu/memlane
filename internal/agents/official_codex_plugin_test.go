//go:build darwin || linux

package agents

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zwhu/memlane/internal/components"
)

func officialPluginFixture(t *testing.T, mode string) (OfficialCodexPluginOptions, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	profile := filepath.Join(home, ".codex")
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	managerRoot := filepath.Join(root, "runtime")
	source := filepath.Join(managerRoot, "components/cognee-codex-plugin/1.7.7")
	files := map[string]string{".agents/plugins/marketplace.json": `{"name":"cognee"}`, "integrations/codex/plugins/cognee/.codex-plugin/plugin.json": `{"name":"cognee","version":"1.7.7"}`, "integrations/codex/plugins/cognee/hooks.json": `{"hooks":{"Stop":[]}}`}
	metadata := components.Metadata{ID: "cognee-codex-plugin", Version: "1.7.7", Platform: runtime.GOOS + "-" + runtime.GOARCH, ProtocolMin: 1, ProtocolMax: 1}
	for name, text := range files {
		p := filepath.Join(source, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(text))
		metadata.Files = append(metadata.Files, components.Asset{Path: name, Bytes: int64(len(text)), SHA256: hex.EncodeToString(sum[:])})
	}
	raw, _ := json.Marshal(metadata)
	if err := os.WriteFile(filepath.Join(source, "component.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	var signed []byte
	ready := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-ready; w.Write(signed) }))
	t.Cleanup(server.Close)
	descriptor := components.Descriptor{ID: metadata.ID, Version: metadata.Version, Platform: metadata.Platform, ProtocolMin: 1, ProtocolMax: 1, URL: server.URL + "/archive", Bytes: 1, UnpackedBytes: 1, SHA256: strings.Repeat("0", 64), ManifestSHA256: hex.EncodeToString(sum[:])}
	payload, _ := json.Marshal(components.Feed{Version: 1, Components: []components.Descriptor{descriptor}})
	signed, _ = json.Marshal(components.SignedFeed{Payload: payload, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))})
	close(ready)
	manager, err := components.New(managerRoot, pub, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.Feed(context.Background(), server.URL); err != nil {
		t.Fatal(err)
	}
	script := `#!/usr/bin/python3
import sys,os,json,shutil
from pathlib import Path
p=Path(os.environ['CODEX_HOME']);a=sys.argv[1:]
with (p/'cli-calls').open('a') as f:f.write(json.dumps(a)+'\n')
m=p/'synthetic-marketplace'
if a[:3]==['plugin','marketplace','list']:
 print(json.dumps({'marketplaces':[{'name':'cognee','root':m.read_text()}] if m.exists() else []}))
elif a[:3]==['plugin','marketplace','add']:
 m.write_text(a[3]);print('{}')
elif a[:2]==['plugin','list']:
 target=p/'plugins/cache/cognee/cognee/1.7.7'
 print(json.dumps({'installed':[{'pluginId':'cognee@cognee','version':'1.7.7','installed':True,'enabled':not (p/'disabled').exists(),'source':{'source':'local','path':str(Path(m.read_text())/'integrations/codex/plugins/cognee')}}] if target.exists() else []}))
elif a[:2]==['plugin','add']:
 target=p/'plugins/cache/cognee/cognee/1.7.7';target.parent.mkdir(parents=True,exist_ok=True)
 shutil.copytree(Path(m.read_text())/'integrations/codex/plugins/cognee',target,dirs_exist_ok=True)
 if MODE=='tampered':(target/'hooks.json').write_text('changed')
 if MODE=='command_error':print('SYNTHETIC_SECRET_DO_NOT_RETURN',file=sys.stderr);sys.exit(1)
 print(json.dumps({'pluginId':'wrong' if MODE=='mismatch' else 'cognee@cognee','version':'1.7.7','installedPath':str(target)}))
else:sys.exit(3)
`
	script = strings.Replace(script, "import sys,os,json,shutil", "import sys,os,json,shutil\nMODE="+strconvQuote(mode), 1)
	binary := filepath.Join(root, "synthetic-codex")
	if err = os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"config.toml": "model = 'unrelated-model'\n[mcp_servers.unrelated]\ncommand = 'synthetic-tool'\n", "hooks.json": "{\"hooks\":{\"Unrelated\":[]}}"} {
		if err = os.WriteFile(filepath.Join(profile, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return OfficialCodexPluginOptions{Executable: binary, Home: home, CodexDirectory: profile, Manager: manager, Component: descriptor, Confirmed: true}, source
}
func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestOfficialCodexPluginInstallUsesVerifiedComponentAndPreservesHost(t *testing.T) {
	o, _ := officialPluginFixture(t, "normal")
	before, err := pluginHostSnapshot(o.CodexDirectory)
	if err != nil {
		t.Fatal(err)
	}
	r, err := InstallOfficialCodexPlugin(context.Background(), o)
	if err != nil || r.State != "installed_pending_verification" || r.Version != "1.7.7" || r.Reused {
		t.Fatal(r, err)
	}
	after, err := pluginHostSnapshot(o.CodexDirectory)
	if err != nil || after != before {
		t.Fatal("unrelated configuration changed", err)
	}
	if _, err = os.Stat(filepath.Join(o.CodexDirectory, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("installer accessed/copied authentication")
	}
	again, err := InstallOfficialCodexPlugin(context.Background(), o)
	if err != nil || !again.Reused || again.State != r.State {
		t.Fatal("install replay failed", again, err)
	}
	if err = os.WriteFile(filepath.Join(o.CodexDirectory, "disabled"), []byte("disabled"), 0600); err != nil {
		t.Fatal(err)
	}
	disabled, err := InstallOfficialCodexPlugin(context.Background(), o)
	if err != nil || disabled.State != "installed_disabled" || !disabled.Reused {
		t.Fatal("disabled installation was not preserved", disabled, err)
	}
	calls, _ := os.ReadFile(filepath.Join(o.CodexDirectory, "cli-calls"))
	if strings.Count(string(calls), `["plugin", "add",`) != 1 {
		t.Fatal("replay repeated installation", string(calls))
	}
}

func TestOfficialCodexPluginInstallRejectsScopeAndIntegrityConflicts(t *testing.T) {
	for _, mode := range []string{"unconfirmed", "legacy", "source_changed", "foreign_marketplace", "cache_symlink", "config_symlink", "mismatch", "tampered", "command_error"} {
		t.Run(mode, func(t *testing.T) {
			o, source := officialPluginFixture(t, mode)
			switch mode {
			case "unconfirmed":
				o.Confirmed = false
			case "legacy":
				o.Legacy = []Installation{{Options: InstallOptions{CodexDirectory: o.CodexDirectory}, CaptureConsent: &CodexCaptureConsent{Enabled: true}}}
			case "source_changed":
				if err := os.WriteFile(filepath.Join(source, "integrations/codex/plugins/cognee/hooks.json"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "foreign_marketplace":
				if err := os.WriteFile(filepath.Join(o.CodexDirectory, "synthetic-marketplace"), []byte(t.TempDir()), 0600); err != nil {
					t.Fatal(err)
				}
			case "cache_symlink":
				if err := os.Symlink(t.TempDir(), filepath.Join(o.CodexDirectory, "plugins")); err != nil {
					t.Fatal(err)
				}
			case "config_symlink":
				path := filepath.Join(o.CodexDirectory, "config.toml")
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".original", path); err != nil {
					t.Fatal(err)
				}
			}
			r, err := InstallOfficialCodexPlugin(context.Background(), o)
			if err == nil || r.State == "installed_pending_verification" || strings.Contains(err.Error(), "SYNTHETIC_SECRET") {
				t.Fatal("invalid installation accepted or leaked output", r, err)
			}
			if mode == "unconfirmed" || mode == "legacy" || mode == "source_changed" {
				if _, err = os.Stat(filepath.Join(o.CodexDirectory, "cli-calls")); !os.IsNotExist(err) {
					t.Fatal("mutated host before preflight", mode)
				}
			}
			if mode == "foreign_marketplace" {
				calls, _ := os.ReadFile(filepath.Join(o.CodexDirectory, "cli-calls"))
				if strings.Contains(string(calls), `"add"`) {
					t.Fatal("replaced another marketplace")
				}
			}
		})
	}
}

func TestOfficialCodexPluginInspectionDoesNotInstallOrEnable(t *testing.T) {
	o, _ := officialPluginFixture(t, "normal")
	o.Confirmed = false
	r, err := InspectOfficialCodexPlugin(context.Background(), o)
	if err != nil || r.State != "not_installed" {
		t.Fatal(r, err)
	}
	calls, _ := os.ReadFile(filepath.Join(o.CodexDirectory, "cli-calls"))
	if strings.Contains(string(calls), `"add"`) {
		t.Fatal("inspection installed plugin")
	}
	o.Confirmed = true
	if _, err = InstallOfficialCodexPlugin(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(o.CodexDirectory, "disabled"), []byte("disabled"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(o.CodexDirectory, "cli-calls"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	o.Confirmed = false
	r, err = InspectOfficialCodexPlugin(context.Background(), o)
	if err != nil || r.State != "installed_disabled" || !r.Reused {
		t.Fatal(r, err)
	}
	calls, _ = os.ReadFile(filepath.Join(o.CodexDirectory, "cli-calls"))
	if strings.Contains(string(calls), `"add"`) || strings.Contains(string(calls), `"enable"`) {
		t.Fatal("inspection changed installation")
	}
	if err = os.WriteFile(filepath.Join(r.Directory, "hooks.json"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = InspectOfficialCodexPlugin(context.Background(), o); err == nil {
		t.Fatal("modified persisted plugin accepted")
	}
}
