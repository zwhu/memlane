#!/usr/bin/env python3
"""Actual G1 App, Enola and Rust; static mainflow, no Agent/model call in this driver."""
import functools
import hashlib
import http.server
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import socket
import threading
import uuid

ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p4-code'
f=Path((OUT/'app-fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app'])
G=SourceFileLoader('codeguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,phase='code')
U=SourceFileLoader('codeui',str(ROOT/'scripts/p1-ui.py')).load_module()
data=f/'client/data';ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def raw(op,**kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(125);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());return json.loads(s.makefile('rb',buffering=0).readline())
def call(op,**kw):
    r=raw(op,**kw);assert r['ok'],r;return r.get('result')
def choose(ui,key,label):
    before={n['id'] for n in ui.nodes()};ui.press_key(key)
    def option():
        nodes=ui.nodes();popup={n['id'] for n in nodes if n.get('role')=='popup' and n['id'] not in before};return next((n for n in nodes if n.get('label')==label and n.get('parent') in popup),None)
    n=U.wait(option);b=n['bounds'];w=ui.req('windows')['windows'][0]['id'];ui.req('input',window=w,events=[{'type':'click','x':b['x']+b['width']/2,'y':b['y']+b['height']/2}]);ui.req('idle',timeout=1500)
class Handler(http.server.SimpleHTTPRequestHandler):
    def log_message(self,*args):pass
    def do_GET(self):
        requests.append(self.path);super().do_GET()
requests=[];server=http.server.ThreadingHTTPServer(('127.0.0.1',meta['source_port']),functools.partial(Handler,directory=meta['source_root']));thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
ui=None;report={'status':'failed','actual_app':False,'actual_enola':False,'actual_rust':False,'actual_daily_agent':False,'model_called':False,'synthetic_only':True,'formal_distribution':False,'cases':[]}
try:
    ui=U.UI(app,f,ROOT/'build/p4-code-screenshots');report['actual_app']=True;ui.press('设置');U.wait(lambda:'准备 / 重试' in ui.labels(),timeout=95)
    if any(n.get('label')=='准备 / 重试' and 'press' in n.get('actions',[]) and 'disabled' not in n.get('states',[]) for n in ui.nodes()):ui.press('准备 / 重试')
    U.wait(lambda:call('health')['state']=='ready',timeout=180);report['actual_rust']=True
    assert call('list',list_query={'limit':5})['total']==0
    config=json.loads((f/'client/service.json').read_text());assert config['worker']['ExpectedVersion']=='0.0.2' and Path(config['code_extractor']).is_file()
    assert all('/'+d['id']+'.tar.gz' in requests for d in meta['component_descriptors'])
    report['cases'].append('actual_app_cold_signed_test_source_components_and_helper_ready')
    for name,repo in [('代码合成项目 A','repo-a'),('代码合成项目 B','repo-b')]:
        ui.press('项目');ui.set('project-name',name);ui.set('project-path',str(f/repo));ui.press_key('register-project');U.wait(lambda:any(s['name']==name for s in call('scopes')))
    project=next(s for s in call('scopes') if s['name']=='代码合成项目 A');other=next(s for s in call('scopes') if s['name']=='代码合成项目 B')
    assert call('code_list')==[];report['cases'].append('register_project_does_not_enable_code_scan')
    ui.press('代码图谱');ui.press_key('code-new');choose(ui,'code-project',project['name']+' · '+project['id'][:8]);ui.set('code-path',str(f/'repo-a'));ui.press_key('code-enable')
    state=U.wait(lambda:next((c for c in call('code_list') if c['project']==project['id']),None));identifier=state['id'];assert state['dataset']!=project['id'] and state['enabled']
    state=U.wait(lambda:(c if (c:=next(x for x in call('code_list') if x['id']==identifier))['state']=='ready' else None),timeout=120)
    report['actual_enola']=True
    assert len(call('code_list'))==1 and not any(x['project']==other['id'] for x in call('code_list'))
    U.wait(lambda:any('就绪' in x for x in ui.labels()));ui.set('code-query','greet');ui.press_key('code-search')
    proof=call('code_query',code_query={'id':identifier,'mode':'symbols','text':'greet'});assert len(proof['graph']['nodes'])==2 and proof['graph']['manifest']['digest']==state['manifest']['digest']
    expected_keys={'code-node-'+n['id'] for n in proof['graph']['nodes']};U.wait(lambda:expected_keys.issubset({n.get('key') for n in ui.nodes()}),timeout=45)
    assert all(n['file_sha'] and n['extractor_id'] for n in proof['graph']['nodes'])
    report['initial_state']=state;report['initial_query']=proof;report['cases'].append('actual_app_enable_enola_rust_symbols_and_source_versions')
    ui.system_capture('code-ready-system.png')
    ui.set('code-query','run');ui.press_key('code-search');run=next(n for n in call('code_query',code_query={'id':identifier,'mode':'symbols','text':'run'})['graph']['nodes'] if n['language']=='typescript');U.wait(lambda:any(n.get('key')=='code-node-'+run['id'] for n in ui.nodes()));ui.press_key('code-node-'+run['id']);ui.press_key('code-calls');U.wait(lambda:'src.run → calls → src.greet' in ui.labels())
    report['cases'].append('actual_app_specific_call_navigation');ui.system_capture('code-calls-system.png')
    ui.press_key('code-manage');ui.press_key('code-disable');off=U.wait(lambda:(c if not (c:=next(x for x in call('code_list') if x['id']==identifier))['enabled'] else None));assert off['snapshot']==state['snapshot']
    denied=raw('code_query',code_query={'id':identifier,'mode':'symbols','text':'greet'});assert not denied['ok'] and denied['code']=='code_disabled'
    assert call('list',list_query={'limit':5})['total']==0;report['cases'].append('actual_app_disable_excludes_graph_and_preserves_snapshot_and_knowledge')
    ui.system_capture('code-disabled-system.png');ui.quit(stop=True);ui=None
    report['status']='passed';report['support_boundary']='Static App mainflow only; actual daily Agent, auto updates/recovery and formal distribution remain separate.'
except Exception as e:
    report['error']=repr(e)
    if ui:
        try:ui.system_capture('code-failure-system.png');report['failure_labels']=ui.labels()
        except Exception as capture:report['capture_error']=repr(capture)
finally:
    cleanup=[]
    if ui:
        try:G.close_partial_ui(ui,f,app,'code')
        except Exception as e:cleanup.append(repr(e))
    try:server.shutdown();server.server_close();thread.join(timeout=5);assert not thread.is_alive()
    except Exception as e:cleanup.append(repr(e))
    report['cleanup_errors']=cleanup;report['source_requests']=requests
    if cleanup:report['status']='failed'
    (OUT/'app-static-mainflow.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({k:v for k,v in report.items() if k not in ('initial_state','initial_query','failure_labels')},ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
