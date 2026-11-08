#!/usr/bin/env python3
"""Verify the latest complete recovery App against preserved synthetic state."""
import argparse,functools,hashlib,http.server,json,os,socket,threading,traceback
from importlib.machinery import SourceFileLoader
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--pointer',type=Path,default=ROOT/'build/p4-delivery/restore-verified-fixture-path.txt');parser.add_argument('--out',type=Path,default=ROOT/'build/p4-delivery/restore-final-package-app.json');args=parser.parse_args()
f=Path(args.pointer.read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app']);data=f/'client/data'
G=SourceFileLoader('finalrecoveryguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,'code');U=SourceFileLoader('finalrecoveryui',str(ROOT/'scripts/p1-ui.py')).load_module();ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def call(op,**kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(125);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':'final-ui','op':op,**kw})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline());assert r['ok'],r;return r.get('result')
def visible(key):return any(n.get('key')==key for n in ui.nodes())
class H(http.server.SimpleHTTPRequestHandler):
    def log_message(self,*args):pass
server=http.server.ThreadingHTTPServer(('127.0.0.1',meta['source_port']),functools.partial(H,directory=meta['source_root']));thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start();ui=None
report={'status':'failed','actual_app':False,'actual_rust':False,'actual_enola':False,'model_called':False,'formal_distribution':False,'bundle':str(app),'fixture':str(f)}
try:
    ui=U.UI.__new__(U.UI);U.UI.__init__(ui,app,f,ROOT/'build/p4-recovery-final-screenshots');report['actual_app']=True;ui.press('设置');U.wait(lambda:'启动服务' in ui.labels(),timeout=120);ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=120)
    batch=call('initialization_list')[0];ui.press('设置');ui.press_key('initialization-settings');U.wait(lambda:visible('initialization-batch-'+batch['batch']));ui.press_key('initialization-batch-'+batch['batch']);U.wait(lambda:'备份已恢复，批次过期' in ui.labels());assert '材料已变化，批次过期' not in ui.labels() and 'backup_restored' not in ui.labels();ui.system_capture('initialization-restore-reason-system.png')
    task=call('merge_list',merge_list={})['items'][0]['task'];ui.press('整理记录');ui.press_key('organization-tab-merge');U.wait(lambda:visible('merge-task-'+task));ui.press_key('merge-task-'+task);U.wait(lambda:'已过期 · 请重新生成建议' in ui.labels());assert '该建议已失效，整项不能应用。请核对当前知识后重新生成建议。' in ui.labels() and not visible('merge-confirm');ui.system_capture('merge-expired-reason-system.png')
    connection=next(c for c in call('connections') if c['enabled']);ui.press('Agent');U.wait(lambda:visible('connection-'+connection['id']));ui.press_key('connection-'+connection['id']);U.wait(lambda:'真实 Agent 已通过保存、状态与检索；范围由 Memlane 校验。' in ui.labels());install=json.loads((f/'client/agent-adapters'/connection['id']/'installation.json').read_text());assert install['verified_generation']==connection['generation'];ui.system_capture('new-agent-still-verified-system.png')
    scope=connection['project'];v=next(v for v in call('list',list_query={'datasets':[scope],'limit':20})['items'] if v['title']=='恢复合成约定');assert v['revision']==2;hits=call('search',datasets=[scope],query=v['text'],limit=10);assert any(h['version']['memory']==v['memory'] and h['version']['revision']==2 for h in hits);report['actual_rust']=True
    code=call('code_list')[0];assert code['enabled'] and code['generation']==3;assert call('code_query',code_query={'id':code['id'],'mode':'symbols','text':'greet'})['graph']['nodes'];report['actual_enola']=True
    ui.quit(stop=True);U.wait(lambda:json.loads((data/'service-state.json').read_text())['stopped'] and not Path(ipc).exists());ui=None;report.update(status='passed',service_stopped=True,accurate_restore_expiry_copy=True,current_version=2,code_generation=3)
except Exception as e:
    report['error']=repr(e);report['traceback']=traceback.format_exc()
    try:ui.system_capture('final-ui-failure-system.png');report['failure_labels']=ui.labels()
    except Exception:pass
finally:
    errors=[]
    try:call('stop')
    except (FileNotFoundError,ConnectionRefusedError):pass
    except Exception as e:errors.append('stop:'+repr(e))
    if ui:
        try:G.close_partial_ui(ui,f,app,'code')
        except Exception as e:errors.append('ui:'+repr(e))
    try:server.shutdown();server.server_close();thread.join(timeout=5);assert not thread.is_alive()
    except Exception as e:errors.append('source:'+repr(e))
    report['cleanup_errors']=errors
    if errors:report['status']='failed'
    args.out.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({k:v for k,v in report.items() if k not in ['traceback','failure_labels']},ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
