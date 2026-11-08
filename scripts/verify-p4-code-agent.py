#!/usr/bin/env python3
"""Actual traex code tool against the isolated G1 App and current Rust graph."""
import hashlib
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import socket
import shutil
import subprocess
import uuid

ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p4-code';f=Path((OUT/'app-fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app'])
G=SourceFileLoader('codeagentguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,'code')
U=SourceFileLoader('codeagentui',str(ROOT/'scripts/p1-ui.py')).load_module()
data=f/'client/data';ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def call(op,**kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(125);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline());assert r['ok'],r;return r.get('result')
def choose(ui,key,label):
    before={n['id'] for n in ui.nodes()};ui.press_key(key)
    def option():
        nodes=ui.nodes();popup={n['id'] for n in nodes if n.get('role')=='popup' and n['id'] not in before};return next((n for n in nodes if n.get('label')==label and n.get('parent') in popup),None)
    n=U.wait(option);b=n['bounds'];w=ui.req('windows')['windows'][0]['id'];ui.req('input',window=w,events=[{'type':'click','x':b['x']+b['width']/2,'y':b['y']+b['height']/2}]);ui.req('idle',timeout=1500)
ui=None;conn=None;report={'status':'failed','synthetic_only':True,'actual_app':False,'actual_traex_attempted':False,'actual_traex_passed':False,'actual_rust':False,'model_protocol_fixture':False,'support':'pending','cases':[]}
try:
    ui=U.UI(app,f,ROOT/'build/p4-code-screenshots');report['actual_app']=True;ui.press('设置');U.wait(lambda:'启动服务' in ui.labels() or '停止服务' in ui.labels(),timeout=95)
    if '启动服务' in ui.labels():ui.press('启动服务')
    U.wait(lambda:call('health')['state']=='ready',timeout=95);report['actual_rust']=True
    state=next(c for c in call('code_list') if c['project']==next(s['id'] for s in call('scopes') if s['name']=='代码合成项目 A'));project=next(s for s in call('scopes') if s['id']==state['project'])
    ui.press('代码图谱');U.wait(lambda:any(n.get('key')=='code-worktree-'+state['id'] for n in ui.nodes()));ui.press_key('code-worktree-'+state['id'])
    if not state['enabled']:ui.press_key('code-enable')
    state=U.wait(lambda:(c if (c:=next(x for x in call('code_list') if x['id']==state['id']))['state']=='ready' and c['enabled'] else None),timeout=120)
    ui.press('Agent');U.wait(lambda:any(n.get('key')=='agent-traex' for n in ui.nodes()));ui.press_key('agent-traex')
    path=shutil.which('traex') or shutil.which('traecli');assert path,'known installed traex path required'
    ui.set('agent-path',str(Path(path).resolve()));ui.press_key('probe-agent');U.wait(lambda:'路径已检查' in ui.labels());choose(ui,'agent-project',project['name']);U.wait(lambda:any(n.get('key')=='connect-agent' and 'disabled' not in n.get('states',[]) for n in ui.nodes()));ui.press_key('connect-agent')
    conn=U.wait(lambda:next((c for c in call('connections') if c['agent']=='traex' and c['enabled']),None));installation=U.wait(lambda:(json.loads(p.read_text()) if (p:=f/'client/agent-adapters'/conn['id']/'installation.json').exists() else None))
    request={'action':'run','connection':conn,'options':installation['options'],'run':{'Workspace':str(f/'repo-a'),'UsageChannel':'daily','Prompt':'Use only the Memlane memory_code_search tool. Query mode symbols, query greet, for the current project. Report the returned TypeScript and Python symbol names, relative file, line, file SHA and snapshot. Do not use any other tool, write files, save memories, infer source details or scan repositories yourself.'}}
    report['actual_traex_attempted']=True
    p=subprocess.run([str(OUT/'agent-check')],input=json.dumps(request),capture_output=True,text=True,timeout=200);result=json.loads(p.stdout);report['execution']=result
    assert p.returncode==0 and result['ok'],result.get('error',p.stderr[-500:])
    execution=result['result'];assert execution['agent']=='traex' and execution['version']=='traecli 0.214.1' and execution['channel']=='internal' and execution['exit_code']==0 and execution['process_group_gone']
    calls=[c for c in execution['calls'] if c['tool']=='memory_code_search' and c['response']['ok']];assert calls,'actual model made no successful code tool call'
    payload=calls[-1]['response']['result'];assert payload['state']['id']==state['id'] and payload['state']['generation']==state['generation'] and payload['graph']['receipt']['snapshot']==state['snapshot']
    symbols=payload['graph']['nodes'];assert {'src.greet','app/greet.greet'}.issubset({n['name'] for n in symbols}) and all(n['file_sha'] and n['extractor_id'] for n in symbols)
    assert call('list',list_query={'limit':5})['total']==0
    report['cases'].append('actual_traex_default_model_current_scope_symbols_source_snapshot_and_process_cleanup');report['actual_traex_passed']=True;report['support']='traex 0.214.1 internal actual code tool; resolved default model recorded separately from unreported upstream model'
    ui.press_key('disconnect-agent');U.wait(lambda:not next(c for c in call('connections') if c['id']==conn['id'])['enabled']);U.wait(lambda:not (f/'client/agent-adapters'/conn['id']/'installation.json').exists());U.wait(lambda:'已断开；其他配置与记忆保留。' in ui.labels())
    report['cases'].append('actual_app_disconnect_revokes_model_connection_and_removes_owned_adapter');ui.quit(stop=True);ui=None;report['status']='passed'
except Exception as e:
    report['error']=repr(e)
    if ui:
        try:ui.system_capture('code-agent-failure-system.png');report['failure_labels']=ui.labels()
        except Exception as capture:report['capture_error']=repr(capture)
finally:
    cleanup=[]
    if ui:
        if conn:
            try:
                ui.press('Agent');ui.press_key('connection-'+conn['id']);ui.press_key('disconnect-agent');U.wait(lambda:not (f/'client/agent-adapters'/conn['id']/'installation.json').exists())
            except Exception as e:cleanup.append('disconnect: '+repr(e))
        try:
            G.close_partial_ui(ui,f,app,'code')
        except Exception as e:cleanup.append('close: '+repr(e))
    report['cleanup_errors']=cleanup
    if cleanup:report['status']='failed'
    (OUT/'actual-traex-code-tool.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({k:v for k,v in report.items() if k not in ('execution','failure_labels')},ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
