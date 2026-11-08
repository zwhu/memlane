#!/usr/bin/env python3
"""No-model config integration: actual IDE probe and isolated owned JSONC edits."""
import json,subprocess,tempfile,uuid
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-ide';f=Path(tempfile.mkdtemp(prefix='memlane-p2-ide-config-'));(f/'repo/.trae').mkdir(parents=True)
p=f/'repo/.trae/mcp.json';baseline='// synthetic root comment\n{\n "mcpServers": {\n  /* foreign comment */ "other": {"command":"/usr/bin/false","disabled":true,},\n },\n "external":"keep",\n}\n'
c={'id':str(uuid.uuid4()),'agent':'trae','name':'synthetic','project':str(uuid.uuid4()),'generation':1,'enabled':True,'allowed_scopes':[]}
fixture=Path((OUT/'fixture-path.txt').read_text().strip());app=Path(json.loads((fixture/'source-info.json').read_text())['app'])
o={'ClientRoot':str(f/'client'),'ServiceRoot':str(f/'data'),'Bridge':str(app/'Contents/Resources/bin/memlane'),'AgentPath':'/Applications/Trae CN.app/Contents/Resources/app/bin/trae-cn','Workspace':str(f/'repo')}
def run(action,expected=True,**kw):
 r=subprocess.run([str(OUT/'p2-agent-check')],input=json.dumps({'action':action,'connection':c,'options':o,**kw}),capture_output=True,text=True,timeout=30);j=json.loads(r.stdout);assert j['ok']==expected,(action,j);return j
report={'status':'failed','synthetic_only':True,'model_called':False,'integration_fixture':str(f),'cases':[]}
try:
 p.write_text(baseline);i=run('install')['result'];assert Path(i['backup_path']).read_text()==baseline;assert '/* foreign comment */ "other": {"command":"/usr/bin/false","disabled":true,}' in p.read_text();run('check_installation');report['cases'].append('actual_probe_jsonc_install_backup')
 d=run('ide_begin')['result'];assert run('ide_begin')['result']==d;run('ide_finish');assert run('ide_begin')['result']['nonce']!=d['nonce'];report['cases'].append('stable_challenge_cancel_fresh_nonce')
 owned=p.read_bytes();p.write_text(p.read_text().replace('"external":"keep"','"external":"external_edit"'));run('check_installation');c['generation']=2;run('install');run('check_installation');run('uninstall');assert 'external_edit' in p.read_text() and 'foreign comment' in p.read_text() and '"memlane"' not in p.read_text();report['cases'].append('generation_reinstall_foreign_edit_revoke')
 p.write_text(baseline);c['generation']=3;i=run('install')['result'];changed=p.read_text().replace('"disabled":false','"disabled":true');assert changed!=p.read_text();p.write_text(changed);run('check_installation',False);run('uninstall',False);assert p.read_text()==changed;report['cases'].append('owned_external_edit_fail_closed')
 # Preserve independent damaged/foreign JSONC inputs byte-for-byte.
 for name,bad in [('foreign','{"mcpServers":{"memlane":{"command":"other"}}}'),('duplicate','{"mcpServers":{},"mcpServers":{}}'),('duplicate_server','{"mcpServers":{"other":{},"other":{}}}'),('malformed','{"mcpServers":[')]:
  c['id']=str(uuid.uuid4());p.write_text(bad);run('install',False);assert p.read_text()==bad;report['cases'].append(name+'_preserved')
 report['status']='passed'
except Exception as e:report['error']=repr(e);raise
finally:(OUT/'config-integration.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
