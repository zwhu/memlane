# Synthetic local protocol only; never imports an Agent or invokes a model.
import json,sys
mode=sys.argv[1]
schema_digest=sys.argv[2] if len(sys.argv)>2 else 'fixed-schema'
provider=sys.argv[3] if len(sys.argv)>3 else 'synthetic'
model=sys.argv[4] if len(sys.argv)>4 else 'fixture'
def send(v):print(json.dumps(v),flush=True)
def metadata(v):print(json.dumps(v),file=sys.stderr,flush=True)
def response(c,data=None):
 v={'type':'response','id':c['id'],'command':c['type'],'success':True}
 if data is not None:v['data']=data
 send(v)
def end(reason='stop'):
 value='{' if mode=='invalid-json' else json.dumps({'note':5 if mode=='wrong-output-schema' else 'synthetic'})
 send({'type':'message_end','message':{'role':'assistant','stopReason':reason,'content':[{'type':'text','text':value}]}})
 send({'type':'agent_end','willRetry':False})
 if mode!='missing-settle':send({'type':'agent_settled'})
for line in sys.stdin:
 c=json.loads(line)
 if c['type']=='get_state':response(c,{'model':{'provider':provider,'id':model},'isStreaming':False})
 elif c['type'] in ('set_auto_retry','set_auto_compaction'):response(c)
 elif c['type']=='prompt':
  response(c);send({'type':'agent_start'})
  metadata({'type':'memlane_schema_bound','schema_sha256':schema_digest})
  metadata({'type':'memlane_schema_response','schema_sha256':schema_digest,'status':400 if mode=='schema-http400' else 200})
  if mode=='eof':break
  if mode=='deadline':continue
  end('length' if mode=='truncated' else 'error' if mode=='schema-http400' else 'stop')
 elif c['type']=='abort':
  if mode!='missing-settle':end('aborted')
  response(c)
