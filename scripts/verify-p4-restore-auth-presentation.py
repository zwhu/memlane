#!/usr/bin/env python3
"""Continue the recorded restored F1c2 App, verify new Agent and re-enable code."""
import functools
import argparse
import hashlib
import http.server
import json
import os
import socket
import threading
import traceback
from importlib.machinery import SourceFileLoader
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1]
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--out',type=Path,default=ROOT/'build/p4-delivery/restore-authorizations-presentation.json')
args=parser.parse_args()
prior_path=ROOT/'build/p4-delivery/restore-authorizations-app-v2.json'
prior=json.loads(prior_path.read_text());assert prior['status']=='failed' and 'unknown field' in prior['error'] and prior['actual_traex'] and prior['actual_enola'] and prior['restore_receipt']
f=Path(prior['fixture']);meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app']);data=f/'client/data'
G=SourceFileLoader('restorepresentationguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,'code')
U=SourceFileLoader('restorepresentationui',str(ROOT/'scripts/p1-ui.py')).load_module()
ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def response(op,**kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(125);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':'auth-presentation','op':op,**kw})+'\n').encode());return json.loads(s.makefile('rb',buffering=0).readline())
def call(op,**kw):
    r=response(op,**kw);assert r['ok'],r;return r.get('result')
def visible(key):return any(n.get('key')==key for n in ui.nodes())
def scroll(key):
    n=U.wait(lambda:next((n for n in ui.nodes() if n.get('key')==key),None))
    if 'scrollIntoView' in n['actions']:ui.req('act',id=n['id'],action='scrollIntoView');ui.req('idle',timeout=1500)
class Handler(http.server.SimpleHTTPRequestHandler):
    def log_message(self,*args):pass
server=http.server.ThreadingHTTPServer(('127.0.0.1',meta['source_port']),functools.partial(Handler,directory=meta['source_root']));thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
ui=None
report={'status':'failed','synthetic_only':True,'actual_app':False,'actual_new_agent_verify':False,'actual_rust':False,'actual_enola':False,'model_fixture':False,'formal_distribution':False,'previous_failed_report':str(prior_path),'fixture':str(f)}
try:
    assert json.loads((data/'service-state.json').read_text())['stopped'] and not (data/'.restore-pending.json').exists()
    ui=U.UI.__new__(U.UI);U.UI.__init__(ui,app,f,ROOT/'build/p4-restore-auth-final-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent'),'TRAE_HOME':str(Path.home()/'.trae'),'MEMLANE_TRAE_AUTH_DIRECTORY':str(Path.home()/'.trae/cli')})
    ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=120);report['actual_app']=True
    project=next(s['id'] for s in call('scopes') if s['name']=='恢复合成项目');repo=f/'repo-a'
    connections=call('connections');assert len(connections)==2
    old=next(c for c in connections if not c['enabled']);fresh=next(c for c in connections if c['enabled']);assert old['generation']==2 and fresh['generation']==1
    original=next(v for v in call('list',list_query={'datasets':[project],'limit':20})['items'] if v['title']=='恢复合成约定');assert original['revision']==2
    for generation in [1,2]:
        denied=response('agent',agent_request={'protocol_version':1,'connection_id':old['id'],'generation':generation,'workspace':str(repo),'tool':'memory_scopes','arguments':{}});assert not denied['ok'] and denied['code']=='restore_reconnect_required',denied
    current=call('agent',agent_request={'protocol_version':1,'connection_id':fresh['id'],'generation':1,'workspace':str(repo),'usage_channel':'verification','tool':'memory_get','arguments':{'memory_id':original['memory'],'scope':project}});assert current['revision']==2
    plan=call('schedules')[0];assert not plan['enabled'] and plan['generation']==3 and plan['next_run_ms']==0
    batch=call('initialization_list')[0];assert batch['state']=='expired' and batch['saved']==0
    task=call('merge_get',merge_task=call('merge_list',merge_list={})['items'][0]['task']);assert task['review_state']=='expired' and task.get('receipt') is None and task['execution']['exit_code']==0
    code=call('code_list')[0];assert not code['enabled'] and code['generation']==2
    rejected=response('code_query',code_query={'id':code['id'],'mode':'symbols','text':'greet'});assert not rejected['ok'] and rejected['code']=='code_disabled',rejected
    config=(repo/'.trae/traecli.toml').read_bytes();assert old['id'].encode() not in config and fresh['id'].encode() in config and b'SYNTHETIC_RESTORE_KEEP' in config
    ui.press('整理记录');ui.press_key('organization-tab-merge');U.wait(lambda:visible('merge-task-'+task['task']));ui.press_key('merge-task-'+task['task']);U.wait(lambda:'已过期 · 请重新生成建议' in ui.labels());assert not visible('merge-confirm');ui.system_capture('restored-proposal-expired-system.png')
    ui.press('设置');scroll('schedule-settings');ui.press_key('schedule-settings');U.wait(lambda:visible('schedule-plan-'+plan['plan']));ui.press_key('schedule-plan-'+plan['plan']);U.wait(lambda:visible('schedule-toggle'));assert '确认启用计划' in ui.labels();ui.system_capture('restored-plan-disabled-system.png')
    ui.press('设置');scroll('initialization-settings');ui.press_key('initialization-settings');U.wait(lambda:visible('initialization-batch-'+batch['batch']));ui.press_key('initialization-batch-'+batch['batch']);U.wait(lambda:any('过期' in label for label in ui.labels()));ui.system_capture('restored-initialization-expired-system.png')
    ui.press('Agent');U.wait(lambda:visible('connection-'+fresh['id']));ui.press_key('connection-'+fresh['id']);scroll('verify-agent');ui.press_key('verify-agent');U.wait(lambda:'真实 Agent 读写验证通过。' in ui.labels() or any(t.startswith('验证失败') for t in ui.labels()),timeout=190);assert '真实 Agent 读写验证通过。' in ui.labels(),ui.labels()
    install=json.loads((f/'client/agent-adapters'/fresh['id']/'installation.json').read_text());assert install['verified_generation']==fresh['generation'];report['actual_new_agent_verify']=True;report['agent_version']=install['agent_version'];report['agent_channel']=install['agent_channel'];report['model_selection']='current traex model; model name is not persisted in installation receipt'
    ui.system_capture('restored-new-agent-real-verify-system.png')
    hits=call('search',datasets=[project],query=original['text'],limit=10);assert any(h['version']['memory']==original['memory'] and h['version']['revision']==2 for h in hits);report['actual_rust']=True
    ui.press('代码图谱');U.wait(lambda:visible('code-worktree-'+code['id']));ui.press_key('code-worktree-'+code['id']);U.wait(lambda:visible('code-enable'));ui.press_key('code-enable')
    enabled=U.wait(lambda:next((c for c in call('code_list') if c['id']==code['id'] and c['enabled'] and c['state']=='ready'),None),timeout=120);assert enabled['generation']>code['generation'];proof=call('code_query',code_query={'id':code['id'],'mode':'symbols','text':'greet'});assert proof['graph']['nodes'];report['actual_enola']=True;ui.system_capture('restored-code-reenabled-system.png')
    report.update(old_connection=old['id'],new_connection=fresh['id'],retired_ids_rejected=True,proposal_expired=True,plan_disabled=True,initialization_expired=True,old_graph_query_rejected=True,new_code_generation=enabled['generation'],owned_config_preserved=True,restored_memory=original['memory'],restored_revision=2,restore_receipt=prior['restore_receipt'])
    ui.quit(stop=True)
    U.wait(lambda:json.loads((data/'service-state.json').read_text())['stopped'] and not Path(ipc).exists(),timeout=30)
    def gone():
        try:os.kill(ui.pid,0);return False
        except ProcessLookupError:return True
    U.wait(gone,timeout=30);ui=None;report.update(status='passed',service_stopped=True)
except Exception as error:
    report['error']=repr(error);report['traceback']=traceback.format_exc()
    try:ui.system_capture('restored-auth-presentation-failure-system.png');report['failure_labels']=ui.labels()
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
