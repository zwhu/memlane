#!/usr/bin/env python3
"""Actual App/Rust rebuild after an explicitly moved synthetic raw index."""
import functools
import argparse
import hashlib
import http.server
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import shutil
import socket
import sqlite3
import subprocess
import threading
import traceback
import uuid

ROOT=Path(__file__).resolve().parents[1]
OUT=ROOT/'build/p4-delivery'
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--pointer',type=Path,default=OUT/'index-recovery-fixture-path.txt')
parser.add_argument('--out',type=Path,default=OUT/'index-recovery-actual-app.json')
parser.add_argument('--screenshots',type=Path,default=ROOT/'build/p4-index-recovery-screenshots')
args=parser.parse_args()
f=Path(args.pointer.read_text().strip())
meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app'])
G=SourceFileLoader('indexrecoveryguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,'code')
U=SourceFileLoader('indexrecoveryui',str(ROOT/'scripts/p1-ui.py')).load_module()
data=f/'client/data';ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def response(op,**kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(125);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());return json.loads(s.makefile('rb',buffering=0).readline())
def call(op,**kw):
    r=response(op,**kw);assert r['ok'],r;return r.get('result')
def visible(key):return any(n.get('key')==key for n in ui.nodes())
def open_ready():
    global ui
    ui=U.UI.__new__(U.UI);U.UI.__init__(ui,app,f,args.screenshots)
    ui.press('设置');U.wait(lambda:'启动服务' in ui.labels() or '停止服务' in ui.labels(),timeout=180)
    if '启动服务' in ui.labels():ui.press('启动服务')
    U.wait(lambda:call('health')['state']=='ready',timeout=180)
def stopped():
    G.validate_fixture(f,app,'code');assert json.loads((data/'service-state.json').read_text())['stopped'];assert not Path(ipc).exists()
def finish_ui():
    global ui
    if ui:ui.quit(stop=True);ui=None
    stopped()
class Handler(http.server.SimpleHTTPRequestHandler):
    def log_message(self,*args):pass
server=http.server.ThreadingHTTPServer(('127.0.0.1',meta['source_port']),functools.partial(Handler,directory=meta['source_root']))
thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
ui=None
report={'status':'failed','synthetic_only':True,'actual_app':False,'actual_rust':False,'model_called':False,'fault_fixture':'move only synthetic project raw index and mark one synthetic memory archived via SQL after actual stop','formal_distribution':False,'fixture':str(f),'replacement_verified':False}
try:
    open_ready();report['actual_app']=True;report['actual_rust']=True
    assert call('list',list_query={'limit':5})['total']==0 and call('code_list')==[],'fresh fixture required'
    ui.press('项目');ui.set('project-name','索引恢复合成项目');ui.set('project-path',str(f/'repo-a'));ui.press_key('register-project')
    project=U.wait(lambda:next((s['id'] for s in call('scopes') if s['name']=='索引恢复合成项目'),None))
    ui.press('索引恢复合成项目')
    originals=[]
    for title,body in [('恢复合成 pnpm 约定','本项目统一使用 pnpm 管理依赖，保留 lockfile。'),('恢复合成来源约定','代码修改需要保留来源和当前版本，不自动覆盖知识。'),('恢复合成归档材料','这个归档材料不应重新变成有效知识。')]:
        ui.press('新增记忆');ui.set('memory-title',title);ui.set('memory-draft',body);ui.press('保存')
        v=U.wait(lambda:next((v for v in call('list',list_query={'datasets':[project],'limit':10})['items'] if v['title']==title),None))
        U.wait(lambda:call('get',memory=v['memory'])['index_state']=='indexed',timeout=120);originals.append(v)
    target=originals[0];ui.press_key('memory-'+target['memory']);U.wait(lambda:visible('memory-draft') or target['text'] in ui.labels());ui.press('编辑')
    new_text='本项目统一使用 pnpm 管理依赖，必须提交 pnpm-lock.yaml，并保留来源。'
    U.wait(lambda:visible('memory-draft'));ui.set('memory-draft',new_text);ui.press('保存')
    current=U.wait(lambda:(v if (v:=call('get',memory=target['memory']))['revision']==2 and v['index_state']=='indexed' else None),timeout=120)
    before=call('search',datasets=[project],query=new_text,limit=10);assert any(h['version']['memory']==current['memory'] and h['version']['revision']==2 for h in before)
    history=call('history',memory=current['memory']);codes=call('code_list');assert call('index_rebuild') is None
    worker=Path(json.loads((f/'client/service.json').read_text())['worker']['DataDir']);G.ensure_inside(worker,f)
    finish_ui()
    # Explicit SQL state fixture and physical raw-index loss, only after both owners exit.
    with sqlite3.connect(data/'memlane.sqlite') as db:
        db.execute("UPDATE memories SET state='archived' WHERE id=?",(originals[2]['memory'],))
        version_rows=db.execute('SELECT * FROM versions ORDER BY memory,revision').fetchall()
        activity_rows=db.execute('SELECT * FROM activities ORDER BY memory').fetchall()
        assert db.execute('PRAGMA integrity_check').fetchone()[0]=='ok'
    lost=worker/project;G.ensure_inside(lost,f);assert lost.is_dir() and not lost.is_symlink();lost.rename(f/'explicit-lost-raw-index')
    open_ready();assert call('get',memory=current['memory'])['text']==new_text and call('history',memory=current['memory'])==history
    missing=response('search',datasets=[project],query=new_text,limit=10)
    if missing['ok']:assert missing['result']==[],missing
    else:assert missing['code']=='service_error' and 'DocumentChunk' in missing['error'] and ('not found' in missing['error'].lower() or 'not exist' in missing['error'].lower()),missing
    assert call('health')['state']=='ready';report['missing_index_query']=missing
    denied=response('rebuild_index',index_rebuild_start={'operation_id':str(uuid.uuid4()),'scope':project});assert not denied['ok'] and denied['error']=='index rebuild requires user confirmation'
    ui.press('设置');U.wait(lambda:visible('index-rebuild-open'));ui.press_key('index-rebuild-open');assert call('index_rebuild') is None
    ui.system_capture('index-rebuild-confirm-system.png');ui.press_key('index-rebuild-dismiss');assert call('index_rebuild') is None
    ui.press_key('index-rebuild-open');ui.press_key('index-rebuild-confirm')
    task=U.wait(lambda:call('index_rebuild'));task_id=task['id'];assert {i['memory'] for i in task['items']}=={v['memory'] for v in originals[:2]}
    ui.press('全部记忆');ui.press('设置');U.wait(lambda:task_id in ' '.join(ui.labels()))
    done=U.wait(lambda:(r if (r:=call('index_rebuild',index_rebuild_id=task_id))['state']=='completed' else None),timeout=120)
    U.wait(lambda:'重建完成' in ui.labels());ui.system_capture('index-rebuild-completed-system.png')
    replay=call('rebuild_index',user_confirmed=True,index_rebuild_start={'operation_id':task_id});assert replay==done
    hits=call('search',datasets=[project],query=new_text,limit=20);assert any(h['version']['memory']==current['memory'] and h['version']['revision']==2 for h in hits)
    assert not any(h['version']['memory']==originals[2]['memory'] or h['version']['memory']==current['memory'] and h['version']['revision']!=2 for h in hits)
    assert call('history',memory=current['memory'])==history and call('code_list')==codes
    finish_ui()
    with sqlite3.connect(data/'memlane.sqlite') as db:
        assert db.execute('SELECT * FROM versions ORDER BY memory,revision').fetchall()==version_rows
        assert db.execute('SELECT * FROM activities ORDER BY memory').fetchall()==activity_rows
        assert db.execute('PRAGMA user_version').fetchone()[0]==13
    legacy=f/'legacy-schema-check';legacy.mkdir();shutil.copy2(data/'memlane.sqlite',legacy/'memlane.sqlite')
    config=json.loads((f/'client/service.json').read_text());config['root']=str(legacy);(f/'legacy-config.json').write_text(json.dumps(config))
    old=ROOT/'build/p4-code-g2-final/darwin-arm64/MemlaneCodeG2Final.app/Contents/Resources/bin/memlaned'
    p=subprocess.run([str(old),'--config',str(f/'legacy-config.json')],capture_output=True,text=True,timeout=15)
    assert p.returncode==1 and p.stderr.strip()=='database schema is newer than this client',(p.returncode,p.stderr)
    open_ready();assert call('index_rebuild',index_rebuild_id=task_id)==done and call('code_list')==codes
    hits=call('search',datasets=[project],query=new_text,limit=20);assert any(h['version']['memory']==current['memory'] and h['version']['revision']==2 for h in hits)
    ui.press('设置');U.wait(lambda:'重建完成' in ui.labels());ui.system_capture('index-rebuild-restart-system.png');finish_ui()
    report.update(status='passed',task=done,current_memory=current['memory'],current_revision=2,history_preserved=True,activities_preserved=True,code_authorization_unchanged=True,replay_no_new_task=True,actual_rust_current_restored=True,old_schema12_helper_rejected=True,service_stopped=True)
except Exception as error:
    report['error']=repr(error);report['traceback']=traceback.format_exc()
    try:ui.system_capture('index-rebuild-failure-system.png');report['failure_labels']=ui.labels()
    except Exception:pass
finally:
    errors=[]
    try:call('stop')
    except (FileNotFoundError,ConnectionRefusedError):pass
    except Exception as error:errors.append('stop: '+repr(error))
    if ui:
        try:G.close_partial_ui(ui,f,app,'code')
        except Exception as error:errors.append('ui: '+repr(error))
    try:server.shutdown();server.server_close();thread.join(timeout=5);assert not thread.is_alive()
    except Exception as error:errors.append('source: '+repr(error))
    report['cleanup_errors']=errors
    if errors:report['status']='failed'
    args.out.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({k:v for k,v in report.items() if k not in ('task','failure_labels','missing_index_query')},ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
