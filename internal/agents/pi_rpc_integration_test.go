//go:build darwin || linux

package agents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Explicit NO MODEL subprocess protocol fixture, never real pi acceptance.
const piRPCFixture = `import json,sys
mode=sys.argv[1]
def send(v): print(json.dumps(v,ensure_ascii=False),flush=True)
def response(c,data=None):
 v={'type':'response','id':c['id'],'command':c['type'],'success':True}
 if data is not None:v['data']=data
 send(v)
def end(reason='stop'):
 body=json.dumps({'note':'a'+chr(0x2028)+'b'+chr(0x2029)+'c'},ensure_ascii=False)
 if mode in ('fenced','fence-extra','fence-double'):body=chr(96)*3+'json\n'+body+'\n'+chr(96)*3
 if mode=='fence-extra':body+='\nclaim passed'
 if mode=='fence-double':body+='\n'+body
 send({'type':'message_end','message':{'role':'assistant','stopReason':reason,'content':[{'type':'text','text':body}]}})
 send({'type':'turn_end'});send({'type':'agent_end','willRetry':False})
 if mode!='missing-settle':send({'type':'agent_settled'})
for line in sys.stdin:
 c=json.loads(line)
 if c['type']=='get_state':
  if mode=='wrong-id':c['id']='foreign'
  response(c,{'model':{'provider':'synthetic','id':'fixture'},'isStreaming':False})
 elif c['type'] in ('set_auto_retry','set_auto_compaction'):response(c)
 elif c['type']=='prompt':
  response(c)
  if mode=='accepted-eof':break
  send({'type':'agent_start'});send({'type':'turn_start'})
  if mode=='tool':send({'type':'tool_execution_start'});continue
  if mode in ('cancel','abort-missing'):continue
  end('error' if mode=='model-error' else 'stop')
 elif c['type']=='abort':
  end('aborted')
  if mode!='abort-missing':response(c)
`

func TestPiRPCProtocolCompletionAndAbortFixtures(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"normal", "fenced", "fence-extra", "fence-double", "accepted-eof", "model-error", "tool", "wrong-id", "missing-settle", "cancel", "abort-missing"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "rpc-synthetic.py")
			if err := os.WriteFile(script, []byte(piRPCFixture), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := runPiRPCProcessOwned(ctx, python, []string{script, mode}, []string{"PATH=/usr/bin:/bin"}, dir, "synthetic only", "", nil, func(event string) {
				if (mode == "cancel" || mode == "abort-missing") && event == "agent_start" {
					cancel()
				}
			}, nil, "")
			if !out.Started || !out.Reaped || !out.GroupGone {
				t.Fatal("fixture process lifecycle incomplete", out, err)
			}
			if mode == "normal" || mode == "fenced" {
				if err != nil || !out.Accepted || !out.Terminal || out.ResolvedProvider != "synthetic" || !strings.Contains(string(out.Final), "a"+string(rune(0x2028))+"b"+string(rune(0x2029))+"c") {
					t.Fatal("LF Unicode or completion failed", out, string(out.Final), err)
				}
			} else {
				if err == nil || out.Final != nil || out.Terminal {
					t.Fatal("failed/cancelled protocol released a result", out, err)
				}
				if mode == "cancel" && (!out.Accepted || !out.Cancelled || !out.AbortSent || !out.AbortAcknowledged) {
					t.Fatal("abort receipt incomplete", out, err)
				}
				if mode == "abort-missing" && (!out.AbortSent || out.AbortAcknowledged) {
					t.Fatal("missing abort was not explicit", out, err)
				}
			}
		})
	}
}
