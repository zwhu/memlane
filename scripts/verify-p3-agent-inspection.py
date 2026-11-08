#!/usr/bin/env python3
"""当前合成根的无模型隔离证据；检查项目优先并绑定连接/实际CLI/hash。"""
import hashlib,json,os,subprocess,tempfile
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-agents';f=Path((OUT/'fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app'])
G=SourceFileLoader('ainspectguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,phase='agents')
pre=json.loads((OUT/'preflight-app.json').read_text());assert pre['fixture']==str(f) and pre['identifier']==meta['identifier'];assert json.loads((f/'client/data/service-state.json').read_text())['stopped']
p=f/'repo-a/.trae/traecli.toml';G.ensure_inside(p,f);original=p.read_bytes()
def inspect(model='',env=None):
 request={'action':'run','connection':pre['connection'],'options':pre['options'],'run':{'InspectInput':True,'Prompt':'仅核对隔离Memlane工具清单，不调用模型。','Workspace':str(f/'repo-a'),'TraeAuthHome':str(Path.home()/'.trae/cli'),'Model':model}}
 r=subprocess.run([str(OUT/'p2-agent-check')],input=json.dumps(request),env=env,capture_output=True,text=True,timeout=60);result=json.loads(r.stdout);assert result['ok'],result;return result['result']
try:
 with tempfile.TemporaryDirectory(prefix='memlane-agent-selection-') as shared:
  Path(shared,'traecli.toml').write_text('model = "GPT-6.1-Sol"\nmodel_provider = "trae"\n')
  p.write_bytes(b'model = "GPT-5.4"\n'+original)
  selection=inspect(env=dict(os.environ,TRAE_HOME=shared));assert selection['selected_model']=='GPT-5.4',selection
  (OUT/'project-model-precedence.json').write_text(json.dumps({'status':'passed','model_called':False,'fixture':str(f),'shared_model':'GPT-6.1-Sol','project_model':selection['selected_model'],'actual_cli_version':selection['version']},indent=2)+'\n')
finally:
 p.write_bytes(original)
inspection=inspect('GPT-6.1-Sol');raw=json.dumps(inspection,ensure_ascii=False,indent=2)+'\n';(OUT/'traex-prompt-input.json').write_text(raw)
text='\n'.join(c.get('text','') for m in inspection['inspection'] for c in m.get('content',[]));assert 'mcp__memlane__' in text and '`read-only`' in text
for forbidden in ('Available skills','<INSTRUCTIONS>','# Memlane','functions.exec_command','mcp__filesystem__'):assert forbidden not in text,forbidden
proof={'status':'passed','fixture':str(f),'identifier':meta['identifier'],'connection':pre['connection']['id'],'generation':pre['connection']['generation'],'inspection_sha256':hashlib.sha256((OUT/'traex-prompt-input.json').read_bytes()).hexdigest(),'agent':inspection['agent'],'version':inspection['version'],'channel':inspection['channel'],'selected_model':inspection['selected_model'],'model_called':False,'note':'debug展示当前上下文/明确sandbox_mode与配置覆盖；不声明与exec的ignore-rules/ephemeral/strict所有启动语义完全等价。实际模型另验收。'}
(OUT/'prompt-inspection-check.json').write_text(json.dumps(proof,ensure_ascii=False,indent=2)+'\n');print(json.dumps(proof,ensure_ascii=False))
