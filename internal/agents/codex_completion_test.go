//go:build darwin || linux

package agents

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const codexCompletionFixture = `#!/usr/bin/python3
import os,sys,json
if '--proposal-guard' in sys.argv:
 os.read(3,1);os.execv(sys.argv[2],sys.argv[2:])
if '--version' in sys.argv: print('codex-cli 0.167.0');sys.exit(0)
if 'mcp' in sys.argv: print('COMMAND --url');sys.exit(0)
MODE='__MODE__'
def send(v): print(json.dumps(v),flush=True)
profile=open(os.path.join(os.environ['CODEX_HOME'],'config.toml')).read()
assert os.environ['HOME']!=os.environ['CODEX_HOME']
assert os.environ.get('MEMLANE_FAKE_UNRELATED_SECRET') is None
assert 'mcp_servers' not in profile and 'hooks = false' in profile
assert os.path.basename(os.getcwd())=='task'
pending=None
for line in sys.stdin:
 m=json.loads(line);p=m.get('params',{});method=m.get('method')
 if method=='initialize': send({'id':m['id'],'result':{}})
 elif method=='thread/start':
  assert p['ephemeral'] and not p['allowProviderModelFallback'] and p['dynamicTools']==[] and p['runtimeWorkspaceRoots']==[]
  if 'permissions' not in p:
   send({'id':m['id'],'result':{'model':p.get('model') or 'fixture-model','modelProvider':'fixture'}});continue
  assert p['permissions']=='memlane_completion' and p['approvalPolicy']=='never'
  for feature in ['shell_tool','unified_exec','plugins','apps','hooks','memories','code_mode_host','view_image']:
   assert feature+' = false' in profile
  assert 'network' in profile and 'enabled = false' in profile
  assert open(os.path.join(os.environ['CODEX_HOME'],'auth.json')).read()=='synthetic-auth-snapshot'
  open(os.path.join(os.environ['CODEX_HOME'],'auth.json'),'w').write('private-refresh-must-not-change-source')
  send({'id':m['id'],'result':{'thread':{'id':'thread'},'model':'wrong' if MODE=='model' else p['model'],'modelProvider':'fixture','runtimeWorkspaceRoots':[],'activePermissionProfile':{'id':'memlane_completion'}}})
 elif method=='turn/start':
  assert p['outputSchema']['additionalProperties']==False and p['outputSchema']['required']==['marker','value']
  assert p['outputSchema']['properties']['value']['const']==7
  send({'id':m['id'],'result':{'turn':{'id':'turn'}}})
  if MODE in ['cancel','late']:continue
  if MODE=='request':send({'id':70,'method':'item/tool/call','params':{'threadId':'thread','turnId':'turn','tool':'shell'}});continue
  if MODE=='tool':send({'method':'item/started','params':{'threadId':'thread','turnId':'turn','item':{'type':'commandExecution'}}});continue
  if MODE=='tool-delta':send({'method':'item/commandExecution/outputDelta','params':{'threadId':'thread','turnId':'turn','delta':'must not run'}});continue
  if MODE=='cross-start':send({'method':'turn/started','params':{'threadId':'foreign','turn':{'id':'turn'}}});continue
  final='not json' if MODE=='json' else json.dumps({'marker':'memlane-model-probe','value':8 if MODE=='schema' else 7})
  item={'method':'item/completed','params':{'threadId':'foreign' if MODE=='cross' else 'thread','turnId':'turn','item':{'type':'agentMessage','phase':'final_answer','text':final}}}
  send(item)
  if MODE=='multiple':send(item)
  send({'method':'turn/completed','params':{'threadId':'thread','turn':{'id':'turn','status':'failed' if MODE=='failed' else 'completed','error':None}}})
 elif method=='turn/interrupt':
  send({'id':m['id'],'result':{}})
  if MODE=='late':send({'method':'item/completed','params':{'threadId':'thread','turnId':'turn','item':{'type':'agentMessage','text':json.dumps({'marker':'memlane-model-probe','value':7})}}})
  send({'method':'turn/completed','params':{'threadId':'thread','turn':{'id':'turn','status':'completed' if MODE=='late' else 'interrupted','error':None}}})
`

func completionFixture(t *testing.T, mode string) (CodexCompletionOptions, string) {
	t.Helper()
	root := t.TempDir()
	program := codexCompletionFixtureProgram(t, root, mode)
	profile := filepath.Join(root, "source")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "config.toml"), []byte("model = 'fixture-model'\nmodel_provider = 'fixture'\n[mcp_servers.daily]\ncommand = 'never-copy-daily-collector'\n[model_providers.fixture]\nname = 'Fixture'\nbase_url = 'http://127.0.0.1:9/v1'\nwire_api = 'responses'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "auth.json"), []byte("synthetic-auth-snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	return CodexCompletionOptions{Executable: program, AuthHome: profile, Provider: "fixture", Model: "fixture-model", SystemPrompt: "Synthetic isolated completion", Prompt: "Return the synthetic marker/value.", OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["marker","value"],"properties":{"marker":{"type":"string","const":"memlane-model-probe"},"value":{"type":"integer","const":7}}}`)}, root
}

// The fixture entry runs before testing flags are parsed and keeps the same
// process/descriptor identity through the guard and protocol interpreter exec.
func init() {
	if filepath.Base(os.Args[0]) != "memlane-codex-completion-fixture" {
		return
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version":
			_, _ = os.Stdout.WriteString("codex-cli 0.167.0\n")
			syscall.Exit(0)
		case "mcp":
			_, _ = os.Stdout.WriteString("COMMAND --url\n")
			syscall.Exit(0)
		case "--proposal-guard":
			control := os.NewFile(3, "fixture-owner-control")
			var permit [1]byte
			n, err := control.Read(permit[:])
			_ = control.Close()
			if err != nil || n != 1 || permit[0] != 'S' || len(os.Args) < 3 {
				syscall.Exit(2)
			}
			_ = syscall.Exec(os.Args[2], os.Args[2:], os.Environ())
			syscall.Exit(2)
		}
	}
	protocol := filepath.Join(filepath.Dir(os.Args[0]), "codex-protocol.py")
	args := append([]string{"/usr/bin/python3", "-S", protocol}, os.Args[1:]...)
	_ = syscall.Exec(args[0], args, os.Environ())
	syscall.Exit(2)
}

func codexCompletionFixtureProgram(t *testing.T, root, mode string) string {
	t.Helper()
	program := filepath.Join(root, "memlane-codex-completion-fixture")
	protocol := filepath.Join(root, "codex-protocol.py")
	if err := os.WriteFile(protocol, []byte(strings.ReplaceAll(codexCompletionFixture, "__MODE__", mode)), 0600); err != nil {
		t.Fatal(err)
	}
	// Reuse the already running native test executable. Fresh script exec can
	// stall before its first instruction on macOS; invoking the established
	// executable avoids that unrelated fixture startup path.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, program); err != nil {
		t.Fatal(err)
	}
	return program
}

func TestCodexFixtureProbeDoesNotInitializePython(t *testing.T) {
	root := t.TempDir()
	program := codexCompletionFixtureProgram(t, root, "ok")
	for _, args := range [][]string{{"--version"}, {"mcp", "add", "--help"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		start := time.Now()
		command := exec.CommandContext(ctx, program, args...)
		command.Env = []string{"HOME=" + root, "CODEX_HOME=" + filepath.Join(root, "profile"), "PATH=/usr/bin:/bin", "PYTHONHOME=" + filepath.Join(root, "unavailable-python")}
		output, err := command.Output()
		cancel()
		t.Logf("fixture metadata args=%v elapsed=%s", args, time.Since(start))
		if err != nil {
			t.Fatalf("fixture native metadata failed: %v", err)
		}
		if args[0] == "--version" && strings.TrimSpace(string(output)) != "codex-cli 0.167.0" || args[0] == "mcp" && !strings.Contains(string(output), "COMMAND --url") {
			t.Fatal("fixture metadata response changed")
		}
	}
}

func TestCodexCompletionStrictProtocolAndPrivateConfiguration(t *testing.T) {
	t.Setenv("MEMLANE_FAKE_UNRELATED_SECRET", "must-not-inherit")
	for _, mode := range []string{"ok", "model", "request", "tool", "tool-delta", "cross-start", "json", "schema", "cross", "multiple", "failed"} {
		t.Run(mode, func(t *testing.T) {
			o, _ := completionFixture(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := RunCodexCompletion(ctx, o)
			if (err == nil) != (mode == "ok") {
				t.Fatal(mode, err, out.FailureCode)
			}
			if out.Started && (!out.Reaped || !out.GroupGone) {
				t.Fatal("unreaped process", out)
			}
			if mode == "ok" {
				if out.Codex == nil || !out.Codex.OutputSchemaAccepted || !out.Codex.FinalSchemaValid || !out.Codex.ToolFree || !out.Terminal || out.NativeSchemaResponseStatus != 0 {
					t.Fatal("independent Codex proof missing", out)
				}
			} else if len(out.Final) != 0 {
				t.Fatal("failed result released")
			}
			before, err := os.ReadFile(filepath.Join(o.AuthHome, "auth.json"))
			if err != nil || string(before) != "synthetic-auth-snapshot" {
				t.Fatal("private refresh rewrote source auth", err)
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), "synthetic-auth-snapshot") || strings.Contains(string(encoded), "never-copy-daily-collector") {
				t.Fatal("configuration leaked into receipt")
			}
		})
	}
}

func TestCodexCompletionCancelRejectsLateResultAndWaitsTerminal(t *testing.T) {
	for _, mode := range []string{"cancel", "late"} {
		t.Run(mode, func(t *testing.T) {
			o, _ := completionFixture(t, mode)
			ctx, cancel := context.WithCancel(context.Background())
			o.OnEvent = func(event string) {
				if event == "model_request_sent" {
					cancel()
				}
			}
			out, err := RunCodexCompletion(ctx, o)
			if err == nil || len(out.Final) != 0 || !out.AbortSent || !out.AbortAcknowledged || !out.Terminal || !out.Reaped || !out.GroupGone || out.FailureCode != "codex_cancelled" {
				t.Fatal("cancellation boundary not proven", out, err)
			}
		})
	}
}

func TestCodexCompletionFrozenConfigurationAndNativeProvider(t *testing.T) {
	o, _ := completionFixture(t, "ok")
	plan, err := ResolveCodexCompletion(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	o.Binding = &plan
	f, err := os.OpenFile(filepath.Join(o.AuthHome, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\n# externally changed\n")
	f.Close()
	out, err := RunCodexCompletion(context.Background(), o)
	if err == nil || out.Started || CodexCompletionErrorCode(err) != "codex_dependency_changed" {
		t.Fatal("configuration drift incurred model execution", err, out)
	}
	o, _ = completionFixture(t, "ok")
	path := filepath.Join(o.AuthHome, "config.toml")
	data, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(data), "model_provider = 'fixture'\n", "", 1)), 0600)
	plan, err = ResolveCodexCompletion(context.Background(), o)
	if err != nil || plan.Provider != "fixture" {
		t.Fatal("native provider was guessed or unresolved", err)
	}
	data, _ = os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(data), "model = 'fixture-model'\n", "", 1)), 0600)
	plan, err = ResolveCodexCompletion(context.Background(), o)
	if err != nil || plan.Model != "fixture-model" || plan.Provider != "fixture" {
		t.Fatal("default selection was guessed or unavailable", err)
	}
}

func TestCodexCompletionMachineErrorsNeverUseProviderText(t *testing.T) {
	for _, test := range []struct{ info, code string }{{`"unauthorized"`, "codex_authentication_failed"}, {`"usageLimitExceeded"`, "codex_quota_exceeded"}, {`"rateLimitExceeded"`, "codex_rate_limited"}, {`{"responseStreamDisconnected":{"httpStatusCode":503}}`, "codex_provider_unavailable"}, {`{"httpConnectionFailed":{"httpStatusCode":401}}`, "codex_authentication_failed"}, {`"other"`, "codex_model_failed"}} {
		raw := json.RawMessage(`{"message":"private upstream diagnostic must not be used","codexErrorInfo":` + test.info + `}`)
		if actual := codexCompletionRemoteCode(raw); actual != test.code {
			t.Fatal(actual, test.code)
		}
	}
}

func TestCodexCompletionCredentialEnvironmentCannotOverrideIsolation(t *testing.T) {
	for _, name := range []string{"HOME", "CODEX_HOME", "PATH", "NODE_OPTIONS", "DYLD_INSERT_LIBRARIES", "MEMLANE_TOOL_MODE"} {
		t.Run(name, func(t *testing.T) {
			o, _ := completionFixture(t, "ok")
			file, err := os.OpenFile(filepath.Join(o.AuthHome, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			file.WriteString("env_key = '" + name + "'\n")
			file.Close()
			out, err := RunCodexCompletion(context.Background(), o)
			if err == nil || out.Started || CodexCompletionErrorCode(err) != "codex_credential_environment_unsupported" {
				t.Fatal("provider environment overrode isolation", err)
			}
		})
	}
}
