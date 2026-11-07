#!/usr/bin/env python3
"""C4 实际 App/traex/Rust 审核；合成独立根，正常模型不使用协议夹具。"""
import argparse,hashlib,json,os,socket,sqlite3,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-c4';OUT.mkdir(exist_ok=True,parents=True)
f=Path((OUT/'fixture-path.txt').read_text().strip());app=Path(json.loads((f/'source-info.json').read_text())['app'])
G=SourceFileLoader('c4guard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,phase='c4')
parser=argparse.ArgumentParser();parser.add_argument('--resume',action='store_true');args=parser.parse_args()
U=SourceFileLoader('c4ui',str(ROOT/'scripts/p1-ui.py')).load_module();data=f/'client/data';ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def call(op,**k):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**k})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline())
 assert r['ok'],r
 return r.get('result')
ui=None;project=None;destination=None;mode_set=False
report={'status':'failed','actual_app':True,'real_agent':'traex','real_worker':True,'synthetic_only':True,'fixture':str(f.resolve()),'identifier':json.loads((f/'source-info.json').read_text())['identifier'],'cases':[]}
if args.resume:
 prior=json.loads((OUT/'real-app.json').read_text());assert [c['action'] for c in prior['cases']]==['merge','overwrite','delete','move','duplicate','conflict']
 assert json.loads((data/'service-state.json').read_text())['stopped'] and not (data/'memlane.sqlite-wal').exists(),'resume evidence check requires stopped checkpointed fixture'
 with sqlite3.connect('file:'+str(data/'memlane.sqlite')+'?mode=ro&immutable=1',uri=True) as db:
  for case in prior['cases']:
   stored=db.execute("SELECT receipt FROM organization_reviews WHERE task=? AND id=? AND state='applied'",(case['task'],case['item'])).fetchone();assert stored and json.loads(stored[0])==case['receipt'],'partial evidence must belong to current fixture'
  if prior.get('real_app_reject'):
   assert db.execute("SELECT count(*) FROM organization_reviews WHERE task=? AND state='rejected'",(prior['real_app_reject'],)).fetchone()[0]>0
   report['real_app_reject']=prior['real_app_reject']
  expired=db.execute("SELECT r.task FROM organization_reviews r JOIN memories m ON m.id=json_extract(r.payload,'$.target') JOIN versions v ON v.memory=m.id AND v.revision=m.current_revision JOIN organization_tasks t ON t.id=r.task WHERE r.action='move' AND r.state='expired' AND instr(v.text,'用户后来修订了条件')>0 ORDER BY t.created_at DESC LIMIT 1").fetchone()
  if expired:report['pending_expiry_task']=expired[0]
 report['cases']=prior['cases'];report['continued_from']='six actions already verified in this fixture; prior driver stopped before refusal button appeared'
def seed_and_start(action,extra=''):
 global mode_set
 bodies={
 'merge':[('knowledge','项目依赖统一使用 pnpm。'),('knowledge','项目依赖使用 pnpm，依赖变更提交 pnpm-lock.yaml；两条重复约定可合并保留。')],
 'overwrite':[('knowledge','项目依赖使用 npm（已有旧约定）。'),('raw','已确认维护记录：此项目已迁移 pnpm，旧 npm 约定应该覆盖为 pnpm，锁文件使用 pnpm-lock.yaml。')],
 'delete':[('knowledge','临时调试分支曾使用 debug-temp 前缀。'),('raw','维护核对记录：上述临时调试分支前缀规则已正式作废，不再适用；应删除这条旧规则，保留历史。')],
 'move':[('knowledge','仅 C4 合成目标项目适用：发布分支必须以 release/ 开头。当前这条规则误存到 C4 合成源项目。'),('raw','范围核对：上述发布分支规则仅属于 C4 合成目标项目，应从误存的 C4 合成源项目移动到授权目标，正文保留。')],
 'duplicate':[('knowledge','项目接口必须保留 request_id 以定位失败。'),('knowledge','项目接口必须保留 request_id 以定位失败。')],
 'conflict':[('knowledge','项目版本号必须使用日期 YYYYMMDD。'),('knowledge','项目版本号必须使用语义化版本 major.minor.patch；没有谁优先的记录。')],
 }[action]
 sources=[]
 for i,(kind,body) in enumerate(bodies):
  sources.append(call('apply',user_confirmed=True,mutation={'kind':'save','operation_id':str(uuid.uuid4()),'memory':str(uuid.uuid4()),'dataset':project,'origin':kind,'title':f'C4 {action}{extra} 来源 {i+1}','text':body,'source':'合成审核来源','source_detail':{'fragment':body,'references':[f'synthetic://c4/{action}/{extra}/{i}']}}))
 ui.press('C4 合成源项目');ui.press('整理记录');ui.press_key('organization-tab-summary');ui.press('C4 合成源项目');ui.press('立即整理')
 if not mode_set:ui.press_key('organization-review-mode');mode_set=True
 if action=='move':ui.press_key('organization-destination-'+destination)
 for kind in ['knowledge','raw']:
  selected=[v for v,(origin,_) in zip(sources,bodies) if origin==kind]
  if not selected:continue
  ui.press_key('organization-kind-'+kind)
  U.wait(lambda:all(any(n.get('key')=='organization-choose-'+v['memory'] for n in ui.nodes()) for v in selected))
  for v in selected:ui.press_key('organization-choose-'+v['memory'])
 old={t['task'] for t in call('organization_list')['items']};ui.press_key('organization-start')
 tid=U.wait(lambda:next((t['task'] for t in call('organization_list')['items'] if t['task'] not in old),None))
 task=U.wait(lambda:(lambda t:t if t['state'] not in ['queued','running','applying'] else None)(call('organization_get',organization_task=tid)),timeout=150)
 assert task.get('result'),task
 entry=next((e for e in task['entries'] if e['item']['action']==action),None);assert entry is not None,('actual model did not propose required action',action,task)
 assert entry['state']=='review_pending',entry
 assert all(call('get',memory=v['memory'])['revision']==1 and call('get',memory=v['memory'])['dataset']==project for v in sources)
 return sources,task,entry
try:
 ui=U.UI(app,f,ROOT/'build/p3-c4-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')})
 if args.resume:
  ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务')
 U.wait(lambda:call('health')['state']=='ready',timeout=95)
 for name,repo in ([] if args.resume else [('C4 合成源项目','repo-a'),('C4 合成目标项目','repo-b')]):
  ui.press('项目');ui.set('project-name',name);ui.set('project-path',str(f/repo));ui.press_key('register-project');U.wait(lambda:any(s['name']==name for s in call('scopes')))
 project=next(s['id'] for s in call('scopes') if s['name']=='C4 合成源项目');destination=next(s['id'] for s in call('scopes') if s['name']=='C4 合成目标项目')
 for action in ([] if args.resume else ['merge','overwrite','delete','move','duplicate','conflict']):
  sources,task,entry=seed_and_start(action)
  U.wait(lambda:any(n.get('key')=='organization-confirm-'+entry['item_id'] for n in ui.nodes()))
  ui.system_capture(action+'-pending-real-system.png');ui.press_key('organization-confirm-'+entry['item_id'])
  done=U.wait(lambda:(lambda t:t if next(e for e in t['entries'] if e['item_id']==entry['item_id'])['state']=='applied' else None)(call('organization_get',organization_task=task['task'])))
  applied=next(e for e in done['entries'] if e['item_id']==entry['item_id']);receipt=applied['receipt'];memory=receipt['memory']
  if memory:
   current=call('get',memory=memory)
   if action=='merge':assert all(call('get',memory=v['memory'])['state']=='archived' for v in sources)
   if action=='overwrite':assert current['revision']==2 and current['text']==entry['item']['text']
   if action=='delete':assert current['revision']==2 and current['state']=='deleted'
   if action=='move':assert current['revision']==2 and current['dataset']==destination
   if action!='delete':
    U.wait(lambda:call('get',memory=memory)['index_state']=='indexed',timeout=95)
    scope=destination if action=='move' else project
    hits=call('search',datasets=[scope],query=current['text'],limit=50);assert any(h['version']['memory']==memory and h['version']['revision']==receipt['revision'] for h in hits)
    if action=='move':assert not any(h['version']['memory']==memory for h in call('search',datasets=[project],query=current['text'],limit=50))
   ui.press_key('organization-open-'+entry['item_id']);U.wait(lambda:current['text'] in ui.labels());ui.system_capture(action+'-applied-real-system.png')
  else:assert action in ['duplicate','conflict'] and all(call('get',memory=v['memory'])['revision']==1 for v in sources)
  assert next(e for e in call('organization_get',organization_task=task['task'])['entries'] if e['item_id']==entry['item_id'])['receipt']==receipt
  report['cases'].append({'action':action,'task':task['task'],'item':entry['item_id'],'receipt':receipt,'current_rust_verified':bool(memory and action!='delete')})
 # Refusal leaves all knowledge unchanged.
 if not report.get('real_app_reject'):
  sources,task,entry=seed_and_start('merge','拒绝');U.wait(lambda:any(n.get('key')=='organization-reject-'+entry['item_id'] for n in ui.nodes()));ui.press_key('organization-reject-'+entry['item_id'])
  U.wait(lambda:next(e for e in call('organization_get',organization_task=task['task'])['entries'] if e['item_id']==entry['item_id'])['state']=='rejected')
  assert all(call('get',memory=v['memory'])['revision']==1 for v in sources)
  report['real_app_reject']=task['task']
 # A source update expires the untouched review. Resume can finish its already
 # verified database state through the actual window, without another model run.
 if report.get('pending_expiry_task'):
  tid=report['pending_expiry_task'];ui.press('整理记录');ui.press_key('organization-tab-summary');U.wait(lambda:any(n.get('key')=='organization-task-'+tid for n in ui.nodes()));ui.press_key('organization-task-'+tid)
 else:
  sources,task,entry=seed_and_start('move','过期');tid=task['task'];v=call('get',memory=entry['item']['target']);call('apply',user_confirmed=True,mutation={'kind':'edit','operation_id':str(uuid.uuid4()),'memory':v['memory'],'dataset':project,'expected_revision':v['revision'],'text':v['text']+' 用户后来修订了条件。'})
  U.wait(lambda:next(e for e in call('organization_get',organization_task=tid)['entries'] if e['item_id']==entry['item_id'])['state']=='expired')
 U.wait(lambda:any('来源已变化' in label for label in ui.labels()));ui.system_capture('review-expired-real-system.png')
 report['real_source_expiry']=tid;report.pop('pending_expiry_task',None)
 ui.quit(stop=True);ui=None;report.update(status='passed',service_stopped=True)
except Exception as e:report['error']=repr(e);raise
finally:
 if ui:ui.quit(stop=True)
 (OUT/'real-app.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
