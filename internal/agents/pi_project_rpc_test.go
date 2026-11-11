//go:build darwin || linux

package agents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// No model. Replay the real RPC tool lifecycle and verify that only the separate
// project path accepts it; ordinary model-only processing still rejects tools.
const piProjectRPCFixture = `import json,sys
mode=sys.argv[1]
def send(v): print(json.dumps(v),flush=True)
def response(c,data=None):
 v={'type':'response','id':c['id'],'command':c['type'],'success':True}
 if data is not None:v['data']=data
 send(v)
for line in sys.stdin:
 c=json.loads(line)
 if c['type']=='get_state':response(c,{'model':{'provider':'synthetic','id':'fixture'},'isStreaming':False})
 elif c['type'] in ('set_auto_retry','set_auto_compaction'):response(c)
 elif c['type']=='prompt':
  response(c);send({'type':'agent_start'});send({'type':'turn_start'})
  name='bash' if mode=='forbidden' else 'memlane_project_tree'
  send({'type':'message_end','message':{'role':'assistant','stopReason':'toolUse','content':[{'type':'toolCall','id':'one','name':name,'arguments':{}}]}})
  send({'type':'tool_execution_start','toolName':name,'toolCallId':'one'})
  if mode!='missing-end':send({'type':'tool_execution_end','toolName':name,'toolCallId':'one'})
  if mode=='duplicate-end':send({'type':'tool_execution_end','toolName':name,'toolCallId':'one'})
  if mode!='missing-result':send({'type':'message_end','message':{'role':'toolResult','toolName':name,'toolCallId':'one','content':[{'type':'text','text':'[]'}]}})
  print(json.dumps({'type':'memlane_schema_bound','schema_sha256':'fixture-digest'}),file=sys.stderr,flush=True)
  print(json.dumps({'type':'memlane_schema_response','schema_sha256':'fixture-digest','status':200}),file=sys.stderr,flush=True)
  send({'type':'message_end','message':{'role':'assistant','stopReason':'stop','content':[{'type':'text','text':'{}'}]}})
  send({'type':'turn_end'});send({'type':'agent_end','willRetry':False});send({'type':'agent_settled'})
`

func TestPiProjectRPCRequiresDeclaredCompleteReadToolsAndKeepsModelOnlyStrict(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"normal", "forbidden", "missing-end", "missing-result", "duplicate-end", "model-only"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			script := filepath.Join(root, "fixture.py")
			if err := os.WriteFile(script, []byte(piProjectRPCFixture), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			boundary := &piProjectTools{finished: true}
			if mode == "model-only" {
				boundary = nil
			}
			execution, err := runPiRPCProcessWithProject(ctx, python, []string{script, mode}, []string{"PATH=/usr/bin:/bin"}, root, "synthetic", "", nil, nil, nil, "fixture-digest", boundary)
			if !execution.Started || !execution.Reaped || !execution.GroupGone {
				t.Fatal("fixture process leak", execution, err)
			}
			if (err == nil) != (mode == "normal") {
				t.Fatal("tool transcript accepted incorrectly", mode, err)
			}
			if mode == "normal" && (!execution.NativeSchemaRequested || !execution.Terminal) {
				t.Fatal("final schema/lifecycle missing", execution)
			}
		})
	}
}
