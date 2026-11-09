//go:build darwin || linux

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zwhu/memlane/internal/records"
)

func TestProjectSourcesFreezeManyFilesAndRejectEscapes(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	r, err := openProjectSourceReader(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.root.Close()
	text := strings.Repeat("project rule\n", 20000)
	for i := 0; i < 20; i++ {
		path := fmt.Sprintf("source-%d.data", i)
		if err = os.WriteFile(filepath.Join(root, path), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		source, e := r.read(context.Background(), path)
		if e != nil || source.Text != text {
			t.Fatalf("read %d: %v", i, e)
		}
		if err = os.WriteFile(filepath.Join(root, path), []byte("changed"), 0600); err != nil {
			t.Fatal(err)
		}
		frozen, e := r.read(context.Background(), path)
		if e != nil || frozen != source {
			t.Fatal("source version was replaced", e)
		}
		absolute, e := r.read(context.Background(), filepath.Join(root, path))
		if e != nil || absolute != source {
			t.Fatal("authorized absolute source did not keep identity", e)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	os.WriteFile(outside, []byte("outside"), 0600)
	os.Symlink(outside, filepath.Join(root, "escape.txt"))
	for _, path := range []string{"../outside.txt", outside, "escape.txt", ".codex/session.jsonl", ".env", "dir/.env.local", "auth.json"} {
		if _, e := r.read(context.Background(), path); e == nil {
			t.Fatal("source accepted", path)
		}
	}
	os.Mkdir(filepath.Join(root, "real"), 0700)
	os.WriteFile(filepath.Join(root, "real", "data.txt"), []byte("inside"), 0600)
	os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "linked"))
	if _, e := r.read(context.Background(), "linked/data.txt"); e == nil {
		t.Fatal("symlinked source directory accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := r.read(ctx, "source-0.data"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

const codexProjectProtocolFixture = `#!/usr/bin/python3
import sys,json,time,os
if '--version' in sys.argv: print('codex-cli 0.167.0-alpha.3.2');sys.exit(0)
if 'mcp' in sys.argv: print('COMMAND --url');sys.exit(0)
MODE='__MODE__'
def send(v): print(json.dumps(v),flush=True)
thread='fixture-thread';turn='fixture-turn';pending=None
for line in sys.stdin:
 m=json.loads(line);method=m.get('method');p=m.get('params',{})
 if method=='initialize': send({'id':m['id'],'result':{}})
 elif method=='config/read': send({'id':m['id'],'result':{'config':{'model':'bound-model','model_provider':None if MODE=='native' else 'fixture','model_providers':{'fixture':{'name':'fixture','base_url':'https://fixture.invalid','env_key':'SYNTHETIC_PROVIDER_KEY','request_max_retries':4}}}}})
 elif method=='thread/start':
  assert 'request_max_retries = 4\n' in open(os.path.join(os.environ['CODEX_HOME'],'config.toml')).read()
  if MODE=='native' and 'permissions' not in p:
   assert p['dynamicTools']==[] and p['ephemeral'] and not p['allowProviderModelFallback']
   send({'id':m['id'],'result':{'model':p['model'],'modelProvider':'fixture'}});continue
  assert p['permissions']=='memlane_project' and p['ephemeral'] and not p['allowProviderModelFallback']
  assert p['cwd']!=p['runtimeWorkspaceRoots'][0]
  expected=['memlane_project_source']+(['memlane_project_knowledge'] if MODE.startswith('context') else [])
  assert [x['name'] for x in p['dynamicTools']]==expected
  send({'id':m['id'],'result':{'thread':{'id':thread},'model':'wrong-model' if MODE=='model' else p['model'],'modelProvider':p['modelProvider'],'runtimeWorkspaceRoots':p['runtimeWorkspaceRoots'],'activePermissionProfile':{'id':p['permissions']}}})
 elif method=='turn/start':
  schema=p['outputSchema'];pending=(schema['properties']['task']['enum'][0],schema['properties']['attempt']['enum'][0])
  send({'id':m['id'],'result':{'turn':{'id':turn}}})
  if MODE=='cancel':
   send({'method':'item/reasoning/textDelta','params':{'threadId':thread,'turnId':turn,'delta':'fixture active'}})
  elif MODE=='unexpected': send({'id':700,'method':'item/commandExecution/requestApproval','params':{'threadId':thread,'turnId':turn}})
  elif MODE.startswith('context') and MODE!='context_missing':
   send({'id':698,'method':'item/tool/call','params':{'threadId':thread,'turnId':turn,'tool':'memlane_project_knowledge','arguments':{'cursor':'','project':'foreign'} if MODE=='context_cross' else {'cursor':''}}})
  else: send({'id':700,'method':'item/tool/call','params':{'threadId':thread,'turnId':turn if MODE!='cross' else 'other-turn','tool':'memlane_project_source','arguments':{'path':'README.md'}}})
 elif method=='turn/interrupt':
  send({'id':m['id'],'result':{}});send({'method':'turn/completed','params':{'threadId':thread,'turn':{'id':turn,'status':'interrupted','error':None}}})
 elif m.get('id')==698 and 'result' in m:
  page=json.loads(m['result']['contentItems'][0]['text']);assert page['total']==2 and page['items'][0]['revision']==2
  send({'id':699,'method':'item/tool/call','params':{'threadId':thread,'turnId':turn,'tool':'memlane_project_knowledge','arguments':{'cursor':page['next']}}})
 elif m.get('id')==699 and 'result' in m:
  page=json.loads(m['result']['contentItems'][0]['text']);assert page['total']==2 and not page.get('next')
  send({'id':700,'method':'item/tool/call','params':{'threadId':thread,'turnId':turn,'tool':'memlane_project_source','arguments':{'path':'README.md'}}})
 elif m.get('id')==700 and 'result' in m:
  source=json.loads(m['result']['contentItems'][0]['text'])
  proposal={'task':pending[0],'attempt':pending[1],'items':[{'title':'项目规则','text':'本项目使用 pnpm。','sources':[{'source':source['id'] if MODE!='forged' else '0'*64,'quote':source['text']}]}],'explanation':'已核对本轮合成材料。'}
  send({'method':'item/completed','params':{'threadId':thread,'turnId':turn,'item':{'type':'commandExecution','id':'command','status':'completed'}}})
  send({'method':'item/completed','params':{'threadId':thread,'turnId':turn,'item':{'type':'agentMessage','id':'answer','phase':'final_answer','text':json.dumps(proposal)}}})
  send({'method':'turn/completed','params':{'threadId':thread,'turn':{'id':turn,'status':'failed' if MODE=='failed' else 'completed','error':None}}})
`

func codexProjectFixture(t *testing.T, mode string) (Installation, string, string) {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	work := filepath.Join(root, "repo")
	os.Mkdir(work, 0700)
	os.WriteFile(filepath.Join(work, "README.md"), []byte("本项目使用 pnpm。"), 0600)
	program := filepath.Join(root, "codex-fixture")
	os.WriteFile(program, []byte(strings.ReplaceAll(codexProjectProtocolFixture, "__MODE__", mode)), 0700)
	config := filepath.Join(root, "connected.toml")
	os.WriteFile(config, []byte("# synthetic installed block\n"), 0600)
	i := Installation{Connection: records.Connection{ID: uuid.NewString(), Agent: "codex", Generation: 1}, AgentVersion: "codex-cli 0.167.0-alpha.3.2", Options: InstallOptions{AgentPath: program}, ConfigPath: config, Block: "# synthetic installed block", VerifiedGeneration: 1, VerifiedAt: "synthetic verification fixture"}
	return i, work, root
}

func TestCodexProjectActualProtocolSeam(t *testing.T) {
	for _, mode := range []string{"normal", "native", "model", "cross", "forged", "failed", "unexpected", "cancel", "context", "context_missing", "context_cross", "context_cancel"} {
		t.Run(mode, func(t *testing.T) {
			i, work, home := codexProjectFixture(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			plan, err := ResolveCodexProject(ctx, i, work, home, "")
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(plan)
			if strings.Contains(string(encoded), "base_url") || strings.Contains(string(encoded), "SYNTHETIC_PROVIDER_KEY") {
				t.Fatal("private configuration exposed")
			}
			task := records.ProjectTask{ID: uuid.NewString(), Attempt: 1, Snapshot: records.ProjectTaskSnapshot{Workspace: work, Executor: plan.Executor}}
			if strings.HasPrefix(mode, "context") {
				plan.KnowledgeReader = func(_ context.Context, cursor string) (records.ProjectKnowledgeContextPage, error) {
					if mode == "context_cancel" {
						cancel()
					}
					if cursor == "" {
						return records.ProjectKnowledgeContextPage{Items: []records.ProjectKnowledgeContextItem{{Memory: "00000000-0000-4000-8000-000000000001", Revision: 2, Title: "已有v2", Text: "本轮已有知识"}}, Next: "00000000-0000-4000-8000-000000000001", Total: 2}, nil
					}
					return records.ProjectKnowledgeContextPage{Items: []records.ProjectKnowledgeContextItem{{Memory: "00000000-0000-4000-8000-000000000002", Revision: 1, Text: "保留当前项目约定"}}, Total: 2}, nil
				}
			}
			out, err := RunCodexProject(ctx, plan, task, nil, func(event string) {
				if mode == "cancel" && event == "model_update" {
					cancel()
				}
			})
			if !out.Reaped || !out.GroupGone {
				t.Fatalf("process remains: %+v %v", out, err)
			}
			if mode == "normal" || mode == "native" || mode == "context" {
				if err != nil || out.Proposal == nil || len(out.Sources) != 1 || out.Items["commandExecution"] != 1 || !out.Terminal {
					t.Fatalf("normal: %+v %v", out, err)
				}
				if mode == "context" && (out.KnowledgeContext == nil || !out.KnowledgeContext.Complete || out.KnowledgeContext.Pages != 2 || out.KnowledgeContext.SentPages != 2 || out.KnowledgeContext.Items != 2) {
					t.Fatal("context delivery receipt missing", out.KnowledgeContext)
				}
			} else {
				if err == nil || out.Proposal != nil || len(out.Sources) != 0 || len(out.Final) != 0 {
					t.Fatalf("invalid task released result: %+v %v", out, err)
				}
			}
			if mode == "cancel" && (!errors.Is(err, context.Canceled) || !out.InterruptSent || !out.InterruptAcknowledged || !out.Cancelled) {
				t.Fatalf("cancel: %+v %v", out, err)
			}
		})
	}
}

func TestCodexProjectRequiresCurrentInstallationAndTask(t *testing.T) {
	i, work, home := codexProjectFixture(t, "normal")
	i.VerifiedGeneration = 0
	if _, err := ResolveCodexProject(context.Background(), i, work, home, ""); err == nil {
		t.Fatal("unverified installation accepted")
	}
	i.VerifiedGeneration = 1
	plan, err := ResolveCodexProject(context.Background(), i, work, home, "")
	if err != nil {
		t.Fatal(err)
	}
	task := records.ProjectTask{ID: uuid.NewString(), Attempt: 1, Snapshot: records.ProjectTaskSnapshot{Workspace: work, Executor: plan.Executor, IncludeHistory: true}}
	if out, e := RunCodexProject(context.Background(), plan, task, nil, nil); e == nil || out.Started {
		t.Fatal("unadapted history started")
	}
	task.Snapshot.IncludeHistory = false
	task.Snapshot.Executor.Model = "other"
	if out, e := RunCodexProject(context.Background(), plan, task, nil, nil); e == nil || out.Started {
		t.Fatal("changed model started")
	}
	task.Snapshot.Executor = plan.Executor
	os.Rename(work, work+"-old")
	os.Mkdir(work, 0700)
	if out, e := RunCodexProject(context.Background(), plan, task, nil, nil); e == nil || out.Started {
		t.Fatal("replacement repository started")
	}
}
