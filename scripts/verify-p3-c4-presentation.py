#!/usr/bin/env python3
"""最终 C4 包实际窗口显示与删除/恢复检索补验；不调用模型。"""
import hashlib,json,os,socket,sqlite3,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-c4'
f=Path((OUT/'fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app'])
G=SourceFileLoader('c4presentguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,phase='c4')
main=json.loads((OUT/'real-app.json').read_text());assert main['status']=='passed' and main['fixture']==str(f.resolve()) and main['identifier']==meta['identifier']
root=f/'client/data';assert json.loads((root/'service-state.json').read_text())['stopped'] and not (root/'memlane.sqlite-wal').exists()
with sqlite3.connect('file:'+str(root/'memlane.sqlite')+'?mode=ro&immutable=1',uri=True) as db:
 pending=db.execute("SELECT task FROM organization_reviews WHERE state='review_pending' ORDER BY task LIMIT 1").fetchone()
 deleted=next(c for c in main['cases'] if c['action']=='delete');moved=next(c for c in main['cases'] if c['action']=='move')
 bindings=db.execute('SELECT count(*) FROM index_bindings WHERE memory=?',(deleted['receipt']['memory'],)).fetchone()[0]
 assert bindings>0,'delete exclusion requires a previously indexed version'
U=SourceFileLoader('c4presentui',str(ROOT/'scripts/p1-ui.py')).load_module();ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(root).encode()).hexdigest()[:12]}/service.sock'
def call(op,**k):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**k})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline())
 assert r['ok'],r
 return r.get('result')
def select_task(tid):
 ui.press('整理记录');ui.press_key('organization-tab-summary');U.wait(lambda:any(n.get('key')=='organization-task-'+tid for n in ui.nodes()));ui.press_key('organization-task-'+tid);U.wait(lambda:tid in ui.labels())
ui=None;report={'status':'failed','actual_app':True,'synthetic_only':True,'model_called':False,'fixture':str(f.resolve()),'identifier':meta['identifier']}
try:
 ui=U.UI(app,f,ROOT/'build/p3-c4-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')});ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=95)
 if pending:
  select_task(pending[0]);U.wait(lambda:any('等待审核' in label for label in ui.labels()));ui.system_capture('final-review-pending-system.png');report['pending_heading_verified']=True
 select_task(main['real_source_expiry']);U.wait(lambda:'执行已完成 · 有过期建议' in ui.labels());ui.system_capture('final-review-expired-system.png')
 select_task(moved['task']);ui.press_key('organization-open-'+moved['item']);U.wait(lambda:any('v2 · 项目移动' in label for label in ui.labels()));ui.system_capture('final-move-system.png')
 memory=deleted['receipt']['memory'];v=call('get',memory=memory);assert v['state']=='deleted' and v['revision']==2
 hits=call('search',datasets=[v['dataset']],query=v['text'],limit=50);assert not any(h['version']['memory']==memory for h in hits)
 select_task(deleted['task']);ui.press_key('organization-open-'+deleted['item']);U.wait(lambda:'历史记录' in ui.labels());ui.press('历史记录');U.wait(lambda:'恢复 v1' in ui.labels());ui.press('恢复 v1');ui.press('确认恢复')
 restored=U.wait(lambda:(lambda r:r if r['revision']==3 and r['state']=='active' else None)(call('get',memory=memory)))
 U.wait(lambda:call('get',memory=memory)['index_state']=='indexed',timeout=95)
 hits=call('search',datasets=[restored['dataset']],query=restored['text'],limit=50);assert any(h['version']['memory']==memory and h['version']['revision']==3 for h in hits)
 assert next(e for e in call('organization_get',organization_task=deleted['task'])['entries'] if e['item_id']==deleted['item'])['receipt']==deleted['receipt']
 ui.system_capture('final-delete-restored-system.png');ui.quit(stop=True);ui=None
 report.update(status='passed',expired_heading_verified=True,move_chinese_label=True,deleted_previously_indexed=True,real_rust_deleted_excluded=True,app_restore_new_revision=3,restore_receipt_preserved=True,real_rust_restore=True,service_stopped=True)
except Exception as e:report['error']=repr(e);raise
finally:
 if ui:ui.quit(stop=True)
 (OUT/'presentation.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
