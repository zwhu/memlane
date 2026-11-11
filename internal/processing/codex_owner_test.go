package processing

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

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/agents"
	"github.com/zwhu/memlane/internal/records"
	"github.com/zwhu/memlane/internal/store"
)

// This executable is a protocol fixture, not Codex or a real model. It proves
// Owner/SQLite/strict candidate gates over the production completion adapter.
const codexOwnerProtocolFixture = `#!/usr/bin/python3
import os,sys,json
if '--proposal-guard' in sys.argv:
 os.read(3,1);os.execv(sys.argv[2],sys.argv[2:])
if '--version' in sys.argv: print('codex-cli 0.167.0');sys.exit(0)
if 'mcp' in sys.argv:print('COMMAND --url');sys.exit(0)
MODE='__MODE__'
def send(v):print(json.dumps(v),flush=True)
for line in sys.stdin:
 m=json.loads(line);p=m.get('params',{});method=m.get('method')
 if method=='initialize':send({'id':m['id'],'result':{}})
 elif method=='thread/start':
  assert p['runtimeWorkspaceRoots']==[] and p['dynamicTools']==[] and p['ephemeral'] and not p['allowProviderModelFallback']
  cfg=open(os.path.join(os.environ['CODEX_HOME'],'config.toml')).read();assert 'mcp_servers' not in cfg and 'hooks = false' in cfg and 'shell_tool = false' in cfg
  send({'id':m['id'],'result':{'thread':{'id':'fixture-thread'},'model':p['model'],'modelProvider':p['modelProvider'],'runtimeWorkspaceRoots':[],'activePermissionProfile':{'id':'memlane_completion'}}})
 elif method=='turn/start':
  send({'id':m['id'],'result':{'turn':{'id':'fixture-turn'}}})
  if MODE=='cancel':continue
  schema=p['outputSchema'];prompt=p['input'][0]['text']
  if 'marker' in schema['properties']:result={'marker':'memlane-model-probe','value':7}
  else:
   snapshot=json.loads(prompt.split('冻结授权材料：\n')[1]);source=snapshot['inputs'][0]
   props=schema['properties'];result={'schema':snapshot['schema'],'task':snapshot['task'],'attempt':snapshot['attempt'],'input_digest':snapshot['input_digest'],'result_id':props['result_id']['const'],'explanation':'synthetic Codex protocol fixture','items':[{'index':1,'action':'summary','title':'Fixture summary','text':source['text'],'target':'','tags':[],'sources':[{'memory':source['memory'],'revision':source['revision'],'quote':'fabricated absent quote' if MODE=='quote' else source['text']}]}]}
  send({'method':'item/completed','params':{'threadId':'fixture-thread','turnId':'fixture-turn','item':{'type':'agentMessage','phase':'final_answer','text':json.dumps(result)}}})
  send({'method':'turn/completed','params':{'threadId':'fixture-thread','turn':{'id':'fixture-turn','status':'completed','error':None}}})
 elif method=='turn/interrupt':
  send({'id':m['id'],'result':{}});send({'method':'turn/completed','params':{'threadId':'fixture-thread','turn':{'id':'fixture-turn','status':'interrupted','error':None}}})
`

func codexOwnerFixture(t *testing.T, mode string) (*Owner, *store.Store, records.ProcessingRevision, agents.Installation) {
	t.Helper()
	root := t.TempDir()
	program := codexOwnerFixtureProgram(t, root, mode)
	db, err := store.Open(filepath.Join(root, "memory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	o := &Owner{ClientRoot: root, ServiceRoot: filepath.Join(root, "service"), Helper: program, Ledger: db, Lifecycle: ctx, Acquire: func(ctx context.Context) (func(), error) { return func() {}, ctx.Err() }}
	t.Cleanup(func() { cancel(); o.Close(); db.Close() })
	c, err := db.ConfigureConnection(ctx, records.ConnectionDraft{Agent: "codex", Name: "Synthetic Codex processing", ConnectionOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(root, "profile")
	if err = os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(profile, "config.toml"), []byte("model = 'fixture-model'\nmodel_provider = 'fixture'\n[model_providers.fixture]\nname = 'Synthetic'\nbase_url = 'http://127.0.0.1:9/v1'\nwire_api = 'responses'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	i, err := agents.Install(ctx, c, agents.InstallOptions{ComputerLevel: true, ClientRoot: root, ServiceRoot: o.ServiceRoot, Bridge: program, AgentPath: program, CodexDirectory: profile})
	if err != nil {
		t.Fatal(err)
	}
	if err = agents.SaveProtocolVerification(root, c.ID, c.Generation, ""); err != nil {
		t.Fatal(err)
	} // explicitly synthetic connection proof
	i, err = agents.LoadInstallation(root, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := db.SaveProcessing(ctx, records.ProcessingSave{OperationID: uuid.NewString(), Spec: records.ProcessingSpec{Mode: "agent", Agent: "codex", Provider: "fixture", Model: "fixture-model", Connection: c.ID, ConnectionGeneration: c.Generation}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return o, db, revision, i
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

func codexOwnerFixtureProgram(t *testing.T, root, mode string) string {
	t.Helper()
	program := filepath.Join(root, "memlane-codex-completion-fixture")
	protocol := filepath.Join(root, "codex-protocol.py")
	if err := os.WriteFile(protocol, []byte(strings.ReplaceAll(codexOwnerProtocolFixture, "__MODE__", mode)), 0600); err != nil {
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
	program := codexOwnerFixtureProgram(t, root, "ok")
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

func TestCodexOwnerValidationExplicitActivationAndFrozenTask(t *testing.T) {
	for _, mode := range []string{"ok", "quote"} {
		t.Run(mode, func(t *testing.T) {
			o, db, revision, i := codexOwnerFixture(t, mode)
			ctx := context.Background()
			if cfg, _ := o.Configuration(ctx); cfg.Current.Active {
				t.Fatal("save activated model")
			}
			if _, err := o.Activate(ctx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: revision.ID}); err == nil {
				t.Fatal("connection proof substituted for inference proof")
			}
			operation := uuid.NewString()
			validation, err := o.Begin(ctx, operation, revision.ID)
			if err != nil {
				t.Fatal(err)
			}
			if terminal := waitValidation(t, db, validation.ID); terminal.State != "succeeded" {
				t.Fatal("Codex validation failed", terminal)
			}
			status, err := o.Status(ctx, validation.ID)
			if err != nil || status.Progress == nil || !status.Progress.Terminal || !status.Progress.Reaped || !status.Progress.GroupGone || !status.Progress.NativeSchemaRequested {
				t.Fatal("process proof missing", status, err)
			}
			if replay, err := o.Begin(ctx, operation, revision.ID); err != nil || replay.ID != validation.ID || replay.State != "succeeded" {
				t.Fatal("terminal replay reran", replay, err)
			}
			if cfg, _ := o.Configuration(ctx); cfg.Current.Active {
				t.Fatal("validation activated selection")
			}
			active, err := o.Activate(ctx, records.ProcessingActivation{OperationID: uuid.NewString(), Revision: revision.ID})
			if err != nil {
				t.Fatal(err)
			}
			project := uuid.NewString()
			db.DB.Exec(`INSERT INTO scopes(id,kind,name,path) VALUES(?,'project','Synthetic completion','/generated/codex')`, project)
			source, err := db.Apply(ctx, records.Mutation{Kind: "save", OperationID: uuid.NewString(), Memory: uuid.NewString(), Dataset: project, Text: "Synthetic project retries at most three times."})
			if err != nil {
				t.Fatal(err)
			}
			auth := records.ProcessingTaskAuthorization{Revision: revision.ID, Generation: active.Generation}
			binding, err := o.ResolveCurrentTask(ctx, auth)
			if err != nil {
				t.Fatal(err)
			}
			task, _, err := db.StartProcessingOrganization(ctx, records.OrganizationStart{OperationID: uuid.NewString(), Project: project, Inputs: []records.MergeTarget{{Memory: source.Memory, Revision: source.Revision}}, Processing: &auth}, binding.Dependency)
			if err != nil {
				t.Fatal(err)
			}
			// Changing the saved candidate alone cannot replace the frozen task.
			next := revision.Spec
			next.Model = "another-saved-candidate"
			if _, err = db.SaveProcessing(ctx, records.ProcessingSave{OperationID: uuid.NewString(), ExpectedSaved: revision.ID, Spec: next}, time.Now()); err != nil {
				t.Fatal(err)
			}
			result, err := o.RunCodexOrganization(ctx, task.Snapshot, nil, nil)
			if mode == "quote" {
				if err == nil || result.Result != nil {
					t.Fatal("fabricated schema-valid citation accepted")
				}
			} else {
				if err != nil || result.Result == nil || !CodexCompletionConfirmed(result.PiCompletionExecution, binding) {
					t.Fatal(result, err)
				}
				if err = db.ClaimOrganization(ctx, task.ID, task.Attempt); err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(result.Result)
				staged, err := db.StageOrganization(ctx, task.ID, task.Attempt, raw, result, "")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.ApplyOrganizationItem(ctx, staged.ID, staged.Attempt, 1); err != nil {
					t.Fatal(err)
				}
			}
			current, err := db.Current(ctx, source.Memory)
			if err != nil || current.Revision != source.Revision || current.Text != source.Text {
				t.Fatal("completion overwrote source", err)
			}
			file, err := os.OpenFile(filepath.Join(i.Options.CodexDirectory, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			file.WriteString("\n# changed dependency\n")
			file.Close()
			if _, err = o.ResolveTask(ctx, *task.Snapshot.Processing); err == nil {
				t.Fatal("old model proof survived configuration drift")
			}
		})
	}
}

func TestCodexOwnerRealProcessCancelAndQueuedDependencyDrift(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		o, db, r, _ := codexOwnerFixture(t, "cancel")
		v, err := o.Begin(context.Background(), uuid.NewString(), r.ID)
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			status, _ := o.Status(context.Background(), v.ID)
			if status.Progress != nil && status.Progress.Events["model_request_sent"] > 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("model fixture not active")
			}
			time.Sleep(time.Millisecond)
		}
		if err = o.Cancel(context.Background(), v.ID); err != nil {
			t.Fatal(err)
		}
		if terminal := waitValidation(t, db, v.ID); terminal.State != "cancelled" || terminal.ReceiptDigest != "" {
			t.Fatal("cancelled validation became success", terminal)
		}
		status, _ := o.Status(context.Background(), v.ID)
		if status.Progress == nil || !status.Progress.Terminal || !status.Progress.Reaped || !status.Progress.GroupGone || !status.Progress.AbortAcknowledged {
			t.Fatal("cancel ACK replaced process termination", status)
		}
	})
	t.Run("queue-drift", func(t *testing.T) {
		o, db, r, i := codexOwnerFixture(t, "ok")
		entered, release := make(chan struct{}), make(chan struct{})
		o.Acquire = func(ctx context.Context) (func(), error) {
			close(entered)
			select {
			case <-release:
				return func() {}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		v, err := o.Begin(context.Background(), uuid.NewString(), r.ID)
		if err != nil {
			t.Fatal(err)
		}
		<-entered
		file, _ := os.OpenFile(filepath.Join(i.Options.CodexDirectory, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
		file.WriteString("\n# queued drift\n")
		file.Close()
		close(release)
		terminal := waitValidation(t, db, v.ID)
		if terminal.State != "failed" || terminal.ErrorCode != "dependency_changed" || terminal.Diagnostics == nil || terminal.Diagnostics.Stage != "preflight" {
			t.Fatal(terminal)
		}
		status, _ := o.Status(context.Background(), v.ID)
		if status.Progress != nil && status.Progress.Started {
			t.Fatal("queued drift incurred execution")
		}
	})
}
