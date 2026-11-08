#!/usr/bin/env python3
"""F1c2 actual App/traex/Rust/Enola authorization recovery in a marked root."""
import argparse
import functools
import hashlib
import http.server
import json
import os
import plistlib
import socket
import subprocess
import threading
import traceback
import uuid
from importlib.machinery import SourceFileLoader
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--pointer',type=Path,default=ROOT/'build/p4-delivery/restore-verified-fixture-path.txt')
parser.add_argument('--out',type=Path,default=ROOT/'build/p4-delivery/restore-authorizations-app.json')
parser.add_argument('--resume-connection',action='store_true',help='resume the first failed pre-initialization run, preserving its owned connector')
args=parser.parse_args();f=Path(args.pointer.read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app']);data=f/'client/data'
G=SourceFileLoader('restoreauthguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,'code')
U=SourceFileLoader('restoreauthui',str(ROOT/'scripts/p1-ui.py')).load_module()
ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'

def response(op,**kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(125);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode())
        return json.loads(s.makefile('rb',buffering=0).readline())
def call(op,**kw):
    r=response(op,**kw);assert r['ok'],r;return r.get('result')
def visible(key):return any(n.get('key')==key for n in ui.nodes())
def scroll(key):
    n=U.wait(lambda:next((n for n in ui.nodes() if n.get('key')==key),None))
    if 'scrollIntoView' in n['actions']:ui.req('act',id=n['id'],action='scrollIntoView');ui.req('idle',timeout=1500)
def choose(key,label):
    before={n['id'] for n in ui.nodes()};ui.press_key(key)
    def option():
        nodes=ui.nodes();popups={n['id'] for n in nodes if n.get('role')=='popup' and n['id'] not in before}
        return next((n for n in nodes if n.get('label')==label and n.get('parent') in popups),None)
    n=U.wait(option);b=n['bounds'];w=ui.req('windows')['windows'][0]['id'];ui.req('input',window=w,events=[{'type':'click','x':b['x']+b['width']/2,'y':b['y']+b['height']/2}]);ui.req('idle',timeout=1500)
def ready():
    ui.press('设置');U.wait(lambda:'启动服务' in ui.labels() or '停止服务' in ui.labels(),timeout=120)
    if '启动服务' in ui.labels():ui.press('启动服务')
    U.wait(lambda:call('health')['state']=='ready',timeout=120)
def reconnect(old_pid):
    exe=app/'Contents/MacOS'/plistlib.loads((app/'Contents/Info.plist').read_bytes())['CFBundleExecutable']
    def changed():
        p=subprocess.run(['pgrep','-f','^'+str(exe)],capture_output=True,text=True);values=[int(v) for v in p.stdout.split()]
        return values[0] if len(values)==1 and values[0]!=old_pid else None
    ui.pid=U.wait(changed,timeout=60)
    ui.f.close();ui.s.close();ui.seq=0
    def connect():
        s=socket.socket(socket.AF_UNIX);s.settimeout(30)
        try:s.connect(ui.path);return s
        except OSError:s.close();return None
    ui.s=U.wait(connect,timeout=60);ui.f=ui.s.makefile('rb',buffering=0);U.wait(lambda:ui.nodes())
def new_backup():
    before={p.parent.name for p in (data/'backups/manual').glob('*/receipt.json')}
    ui.press('设置');scroll('backup-create');ui.press_key('backup-create')
    return U.wait(lambda:next((json.loads(p.read_text()) for p in (data/'backups/manual').glob('*/receipt.json') if p.parent.name not in before),None),timeout=90)
def restore(backup):
    before={p.parent.name for p in (data/'backups/restores').glob('*/receipt.json')}
    scroll('backup-restore-open-'+backup['id']);ui.press_key('backup-restore-open-'+backup['id']);U.wait(lambda:visible('backup-restore-confirm'));scroll('backup-restore-confirm');ui.system_capture('authorization-restore-preview-system.png');old_pid=ui.pid
    try:ui.press_key('backup-restore-confirm')
    except EOFError:pass
    r=U.wait(lambda:next((json.loads(p.read_text()) for p in (data/'backups/restores').glob('*/receipt.json') if p.parent.name not in before),None),timeout=120)
    reconnect(old_pid);assert json.loads((data/'service-state.json').read_text())['stopped'] and not Path(ipc).exists();return r

class Handler(http.server.SimpleHTTPRequestHandler):
    def log_message(self,*args):pass
server=http.server.ThreadingHTTPServer(('127.0.0.1',meta['source_port']),functools.partial(Handler,directory=meta['source_root']));thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
ui=None
report={'status':'failed','synthetic_only':True,'actual_app':False,'actual_traex':False,'actual_rust':False,'actual_enola':False,'model_fixture':False,'formal_distribution':False,'fixture':str(f),'cases':[]}
try:
    assert json.loads((data/'service-state.json').read_text())['stopped'],'stopped primary fixture required'
    assert not (data/'.restore-pending.json').exists()
    project=None
    ui=U.UI.__new__(U.UI);U.UI.__init__(ui,app,f,ROOT/'build/p4-restore-auth-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent'),'TRAE_HOME':str(Path.home()/'.trae'),'MEMLANE_TRAE_AUTH_DIRECTORY':str(Path.home()/'.trae/cli')});ready();report['actual_app']=True
    project=next(s for s in call('scopes') if s['name']=='恢复合成项目')['id']
    originals=call('list',list_query={'datasets':[project],'limit':10})['items'];assert len(originals)==1 and originals[0]['revision']==2
    assert call('schedules')==[] and call('code_list')==[] and call('initialization_list')==[],'pre-initialization fixture required'
    repo=f/'repo-a';config=repo/'.trae/traecli.toml';baseline='# SYNTHETIC_RESTORE_KEEP\nproject_doc_max_bytes = 8192\n'
    if args.resume_connection:
        connections=call('connections');assert len(connections)==1 and connections[0]['enabled'] and baseline.encode() in config.read_bytes()
    else:
        assert call('connections')==[] and not config.exists(),'existing synthetic Agent config must be preserved'
        config.parent.mkdir(exist_ok=True);config.write_text(baseline)
    readme=repo/'README.md'
    if not readme.exists():readme.write_text('本项目使用 pnpm 管理依赖，变更时提交 pnpm-lock.yaml。\n')
    # All repository writes above are explicit synthetic fixture materials,
    # located under the validated root. No user Agent configuration is read.
    ui.press('恢复合成项目');ui.press('Agent')
    if args.resume_connection:
        old=call('connections')[0];U.wait(lambda:visible('connection-'+old['id']));ui.press_key('connection-'+old['id'])
    else:
        U.wait(lambda:visible('agent-traex'));ui.press_key('agent-traex');U.wait(lambda:visible('agent-path'));ui.set('agent-path',str(Path.home()/'.local/bin/traecli'));ui.press_key('probe-agent');U.wait(lambda:'路径已检查' in ui.labels());choose('agent-project','恢复合成项目');scroll('connect-agent');ui.press_key('connect-agent')
        old=U.wait(lambda:next((c for c in call('connections') if c['agent']=='traex' and c['enabled']),None));U.wait(lambda:'配置已安装，请重载 Agent 后验证读写。' in ui.labels())
    assert old['project']==project
    old_config=config.read_bytes();assert old['id'].encode() in old_config and baseline.encode() in old_config
    scroll('verify-agent');ui.press_key('verify-agent')
    U.wait(lambda:'真实 Agent 读写验证通过。' in ui.labels() or any(t.startswith('验证失败') for t in ui.labels()),timeout=190)
    assert '真实 Agent 读写验证通过。' in ui.labels(),ui.labels()
    report['actual_traex']=True
    ui.press_key('agent-initialization');U.wait(lambda:visible('initialization-paths'));ui.set('initialization-paths','README.md');ui.press_key('initialization-prepare');batch=U.wait(lambda:next(iter(call('initialization_list')),None));assert batch['state']=='pending' and batch['saved']==0
    ui.press('代码图谱');ui.press_key('code-new');choose('code-project','恢复合成项目 · '+project[:8]);ui.set('code-path',str(repo));ui.press_key('code-enable')
    code=U.wait(lambda:next((c for c in call('code_list') if c['project']==project and c['state']=='ready'),None),timeout=120);report['actual_enola']=True
    proof=call('code_query',code_query={'id':code['id'],'mode':'symbols','text':'greet'});assert proof['graph']['nodes']
    ui.press('设置');scroll('schedule-settings');ui.press_key('schedule-settings');ui.press_key('schedule-new');choose('schedule-project','恢复合成项目');choose('schedule-frequency','每隔若干小时');ui.set('schedule-interval','24');ui.set('schedule-timezone','UTC');ui.set('schedule-window','60');ui.set('schedule-limit','8');ui.press_key('schedule-save')
    plan=U.wait(lambda:next(iter(call('schedules')),None));assert not plan['enabled'];ui.press_key('schedule-toggle');plan=U.wait(lambda:next((p for p in call('schedules') if p['enabled']),None));assert plan['next_run_ms']>0
    ui.press('恢复合成项目');ui.press('新增记忆');ui.set('memory-title','恢复授权补充来源');ui.set('memory-draft','本项目依赖更新必须提交 pnpm-lock.yaml，并保留来源。');ui.press('保存')
    second=U.wait(lambda:next((v for v in call('list',list_query={'datasets':[project],'limit':10})['items'] if v['title']=='恢复授权补充来源'),None));U.wait(lambda:call('get',memory=second['memory'])['index_state']=='indexed',timeout=120)
    ui.press('恢复合成项目');ui.press('立即整理');U.wait(lambda:visible('merge-choose-'+second['memory']));ui.press_key('merge-choose-'+originals[0]['memory']);ui.press_key('merge-choose-'+second['memory']);ui.press_key('merge-start')
    task=U.wait(lambda:next(iter(call('merge_list',merge_list={})['items']),None));task_id=task['task']
    pending=U.wait(lambda:(t if (t:=call('merge_get',merge_task=task_id)).get('proposal') else None),timeout=180);execution=pending['execution'];assert pending['review_state']=='pending' and execution['process_reaped'] and execution['process_group_gone'] and execution['exit_code']==0;report['actual_traex']=True
    report['merge_execution']={k:execution.get(k) for k in ['version','selected_model','channel','exit_code','process_reaped','process_group_gone']}
    backup=new_backup();receipt=restore(backup);report['restore_receipt']=receipt
    assert receipt['stats']['connections_retired']==1 and receipt['stats']['plans_disabled']==1 and receipt['stats']['code_worktrees_disabled']==1
    ready()
    restored_conn=next(c for c in call('connections') if c['id']==old['id']);assert not restored_conn['enabled'] and restored_conn['generation']==old['generation']+1
    for generation in [old['generation'],restored_conn['generation']]:
        denied=response('agent',agent_request={'protocol_version':1,'connection_id':old['id'],'generation':generation,'workspace':str(repo),'tool':'memory_scopes','arguments':{}});assert not denied['ok'] and denied['code']=='restore_reconnect_required',denied
    off=next(p for p in call('schedules') if p['plan']==plan['plan']);assert not off['enabled'] and off['generation']==plan['generation']+1 and off['next_run_ms']==0
    expired=call('merge_get',merge_task=task_id);assert expired['review_state']=='expired' and expired.get('receipt') is None
    expired_batch=call('initialization_get',initialization_batch=batch['batch']);assert expired_batch['state']=='expired' and expired_batch['saved']==0
    disabled=next(c for c in call('code_list') if c['id']==code['id']);assert not disabled['enabled'] and disabled['generation']==code['generation']+1
    rejected=response('code_query',code_query={'id':code['id'],'mode':'symbols','text':'greet'});assert not rejected['ok'] and rejected['code']=='code_disabled',rejected
    for v in [originals[0],second]:
        U.wait(lambda:call('get',memory=v['memory'])['index_state']=='indexed',timeout=120);hits=call('search',datasets=[project],query=v['text'],limit=10);assert any(h['version']['memory']==v['memory'] and h['version']['revision']==v['revision'] for h in hits)
    report['actual_rust']=True
    ui.press('整理记录');ui.press_key('organization-tab-merge');U.wait(lambda:visible('merge-task-'+task_id));ui.press_key('merge-task-'+task_id);U.wait(lambda:'已过期 · 请重新生成建议' in ui.labels());assert not visible('merge-confirm');ui.system_capture('restored-expired-proposal-system.png')
    ui.press('Agent');U.wait(lambda:visible('connection-'+old['id']));ui.press_key('connection-'+old['id']);scroll('connect-agent');ui.press_key('connect-agent');fresh=U.wait(lambda:next((c for c in call('connections') if c['enabled'] and c['id']!=old['id']),None));U.wait(lambda:'配置已安装，请重载 Agent 后验证读写。' in ui.labels());assert fresh['generation']==1 and fresh['project']==project
    updated=config.read_bytes();assert old['id'].encode() not in updated and fresh['id'].encode() in updated and baseline.encode() in updated
    assert not (f/'client/agent-adapters'/old['id']/'installation.json').exists()
    result=call('agent',agent_request={'protocol_version':1,'connection_id':fresh['id'],'generation':fresh['generation'],'workspace':str(repo),'usage_channel':'verification','tool':'memory_get','arguments':{'memory_id':originals[0]['memory'],'scope':project}});assert result['revision']==2
    ui.system_capture('restored-agent-new-id-system.png')
    ui.press('代码图谱');U.wait(lambda:visible('code-worktree-'+code['id']));ui.press_key('code-worktree-'+code['id']);ui.press_key('code-enable');reenabled=U.wait(lambda:next((c for c in call('code_list') if c['id']==code['id'] and c['enabled'] and c['state']=='ready'),None),timeout=120);assert reenabled['generation']>disabled['generation'];assert call('code_query',code_query={'id':code['id'],'mode':'symbols','text':'greet'})['graph']['nodes'];ui.system_capture('restored-code-explicit-reenable-system.png')
    report['cases'].append({'case':'actual_app_old_authorizations_expired_new_agent_and_code','old_connection':old['id'],'new_connection':fresh['id'],'plan':plan['plan'],'merge_task':task_id,'initialization_batch':batch['batch'],'code':code['id']})
    ui.quit(stop=True);U.wait(lambda:json.loads((data/'service-state.json').read_text())['stopped'] and not Path(ipc).exists(),timeout=30)
    def gone():
        try:os.kill(ui.pid,0);return False
        except ProcessLookupError:return True
    U.wait(gone,timeout=30);ui=None;report.update(status='passed',service_stopped=True)
except Exception as error:
    report['error']=repr(error);report['traceback']=traceback.format_exc()
    try:ui.system_capture('restore-auth-failure-system.png');report['failure_labels']=ui.labels()
    except Exception:pass
finally:
    cleanup=[]
    try:call('stop')
    except (FileNotFoundError,ConnectionRefusedError):pass
    except Exception as error:cleanup.append('stop: '+repr(error))
    if ui:
        try:G.close_partial_ui(ui,f,app,'code')
        except Exception as error:cleanup.append('ui: '+repr(error))
    try:server.shutdown();server.server_close();thread.join(timeout=5);assert not thread.is_alive()
    except Exception as error:cleanup.append('source: '+repr(error))
    report['cleanup_errors']=cleanup
    if cleanup:report['status']='failed'
    args.out.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({k:v for k,v in report.items() if k not in ['failure_labels','traceback']},ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
