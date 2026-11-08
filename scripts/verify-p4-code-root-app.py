#!/usr/bin/env python3
"""Actual fresh App/worker 0.0.3 root TS/Python scope and source verification."""
import functools
import argparse
import hashlib
import http.server
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import socket
import threading
import traceback
import uuid

ROOT=Path(__file__).resolve().parents[1]
OUT=ROOT/'build/p4-code'
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--pointer',type=Path,default=OUT/'root-app-fixture-path.txt')
parser.add_argument('--out',type=Path,default=OUT/'g2-root-actual-app.json')
parser.add_argument('--screenshots',type=Path,default=ROOT/'build/p4-code-root-screenshots')
args=parser.parse_args()
f=Path(args.pointer.read_text().strip())
meta=json.loads((f/'source-info.json').read_text())
app=Path(meta['app'])
G=SourceFileLoader('rootappguard',str(ROOT/'scripts/p3-c2-common.py')).load_module()
G.validate_fixture(f,app,'code')
U=SourceFileLoader('rootappui',str(ROOT/'scripts/p1-ui.py')).load_module()
data=f/'client/data'
ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def call(op,**kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(125);s.connect(ipc)
        s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode())
        r=json.loads(s.makefile('rb',buffering=0).readline())
    assert r['ok'],r
    return r.get('result')
def choose(key,label):
    before={n['id'] for n in ui.nodes()};ui.press_key(key)
    def option():
        nodes=ui.nodes();popup={n['id'] for n in nodes if n.get('role')=='popup' and n['id'] not in before}
        return next((n for n in nodes if n.get('label')==label and n.get('parent') in popup),None)
    n=U.wait(option);b=n['bounds'];window=ui.req('windows')['windows'][0]['id']
    ui.req('input',window=window,events=[{'type':'click','x':b['x']+b['width']/2,'y':b['y']+b['height']/2}]);ui.req('idle',timeout=1500)
class Handler(http.server.SimpleHTTPRequestHandler):
    def log_message(self,*args):pass
server=http.server.ThreadingHTTPServer(('127.0.0.1',meta['source_port']),functools.partial(Handler,directory=meta['source_root']))
thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
ui=U.UI.__new__(U.UI)
report={'status':'failed','synthetic_only':True,'actual_app':False,'actual_rust':False,'actual_enola':False,'model_called':False,'formal_distribution':False,'replacement_verified':False,'fixture':str(f),'bundle':str(app)}
try:
    U.UI.__init__(ui,app,f,args.screenshots);report['actual_app']=True
    ui.press('设置');U.wait(lambda:'启动服务' in ui.labels() or '停止服务' in ui.labels(),timeout=180)
    if '启动服务' in ui.labels():ui.press('启动服务')
    U.wait(lambda:call('health')['state']=='ready',timeout=180);report['actual_rust']=True
    config=json.loads((f/'client/service.json').read_text());assert config['worker']['ExpectedVersion']=='0.0.3'
    assert call('code_list')==[] and not any(s['kind']=='project' for s in call('scopes'))
    ui.press('项目');ui.set('project-name','根目录混合语言合成');ui.set('project-path',str(f/'repo-a'));ui.press_key('register-project')
    project=U.wait(lambda:next((s for s in call('scopes') if s['name']=='根目录混合语言合成'),None))
    assert call('code_list')==[]
    ui.press('代码图谱');ui.press_key('code-new');choose('code-project',project['name']+' · '+project['id'][:8]);ui.set('code-path',str(f/'repo-a'));ui.press_key('code-enable')
    state=U.wait(lambda:next((c for c in call('code_list') if c['project']==project['id'] and c['state']=='ready'),None),timeout=120)
    report['actual_enola']=True
    proof=call('code_query',code_query={'id':state['id'],'mode':'symbols','text':'root'})
    symbols=[n for n in proof['graph']['nodes'] if n['file'] in ('root.ts','root.py')]
    assert len(symbols)==2 and {n['language'] for n in symbols}=={'typescript','python'}
    assert {n['name'] for n in symbols}=={'..rootExperience','root.root_experience'}
    for node in symbols:
        assert node['file_sha']==hashlib.sha256((f/'repo-a'/node['file']).read_bytes()).hexdigest() and node['extractor_id'] and node['line']==1
    assert proof['graph']['receipt']['snapshot']==state['snapshot'] and proof['graph']['manifest']['digest']==state['manifest']['digest']
    ui.set('code-query','root');ui.press_key('code-search')
    expected={'code-node-'+n['id'] for n in symbols};U.wait(lambda:expected.issubset({n.get('key') for n in ui.nodes()}))
    ui.system_capture('root-mixed-symbols-system.png')
    assert call('list',list_query={'limit':5})['total']==0
    assert len(call('code_list'))==1
    report['state']=state;report['query']=proof
    report['status']='passed'
except Exception as error:
    report['error']=repr(error)
    report['traceback']=traceback.format_exc()
    try:ui.system_capture('root-app-failure-system.png');report['failure_labels']=ui.labels()
    except Exception as capture:report['capture_error']=repr(capture)
finally:
    errors=[]
    try:call('stop')
    except (FileNotFoundError,ConnectionRefusedError):pass
    except Exception as error:errors.append('stop: '+repr(error))
    try:G.close_partial_ui(ui,f,app,'code')
    except Exception as error:errors.append('close: '+repr(error))
    try:server.shutdown();server.server_close();thread.join(timeout=5);assert not thread.is_alive()
    except Exception as error:errors.append('source: '+repr(error))
    report['cleanup_errors']=errors
    if errors:report['status']='failed'
    args.out.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({k:v for k,v in report.items() if k not in ('state','query','failure_labels')},ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
