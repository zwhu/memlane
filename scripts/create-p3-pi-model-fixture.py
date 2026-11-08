#!/usr/bin/env python3
"""Reuse only pi's selected existing qwen3:0.6b blobs for a temporary server."""
import json,os,shutil,socket,tempfile
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-pi-rpc';OUT.mkdir(parents=True,exist_ok=True)
source=Path(os.environ.get('OLLAMA_MODELS',str(Path.home()/'.ollama/models')));manifest=source/'manifests/registry.ollama.ai/library/qwen3/0.6b';data=json.loads(manifest.read_text())
f=Path(tempfile.mkdtemp(prefix='memlane-p3-pi-model-'));models=f/'models';target=models/'manifests/registry.ollama.ai/library/qwen3/0.6b';target.parent.mkdir(parents=True);shutil.copy2(manifest,target);(models/'blobs').mkdir()
for v in [data['config'],*data['layers']]:
 name=v['digest'].replace(':','-');blob=source/'blobs'/name;assert blob.is_file() and blob.stat().st_size==v['size'];(models/'blobs'/name).symlink_to(blob)
(f/'home').mkdir();(f/'tmp').mkdir();(f/'pi-agent').mkdir()
pi_source=Path(os.environ.get('PI_CODING_AGENT_DIR',str(Path.home()/'.pi/agent')))
selection=json.loads((pi_source/'settings.json').read_text());assert selection['defaultProvider']=='ollama' and selection['defaultModel']=='qwen3:0.6b', 'fixture only reuses the actually selected model'
provider=json.loads((pi_source/'models.json').read_text())['providers']['ollama'];selected=next(m for m in provider['models'] if m['id']=='qwen3:0.6b');api=provider.get('api',selected.get('api'));assert api=='openai-completions'
safe={k:v for k,v in selected.items() if k in ['id','name','reasoning','input','contextWindow','maxTokens','cost']}
with socket.socket() as probe:probe.bind(('127.0.0.1',0));port=probe.getsockname()[1]
(f/'pi-agent/settings.json').write_text(json.dumps({'defaultProvider':'ollama','defaultModel':'qwen3:0.6b'}))
(f/'pi-agent/models.json').write_text(json.dumps({'providers':{'ollama':{'api':api,'baseUrl':'http://127.0.0.1:'+str(port)+'/v1','apiKey':'ollama','models':[safe]}}}))
(OUT/'private-model-route.json').write_text(json.dumps({'port':port,'auth_home':str(f/'pi-agent'),'private_route_only':True,'selection_from_actual_defaults':True,'credentials_copied':False,'public_local_dummy_auth':'ollama','user_config_changed':False,'model':'qwen3:0.6b'},indent=2)+'\n')
(OUT/'model-fixture-path.txt').write_text(str(f)+'\n');(OUT/'model-fixture.json').write_text(json.dumps({'fixture':str(f),'selected_model':'qwen3:0.6b','model_changed':False,'download_bytes':0,'existing_model_blob_bytes':sum(v['size'] for v in [data['config'],*data['layers']]),'ordinary_model_files_copied':False,'user_model_files_are_read_only_links':True,'user_agent_config_changed':False,'model_server_bind':'127.0.0.1:'+str(port)},indent=2)+'\n');print(f)
