#!/usr/bin/env python3
"""真实 C2 App/traex/Rust 主流程；仅新建的隔离合成根。"""
import hashlib,json,os,socket,time,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader

ROOT=Path(__file__).resolve().parents[1]
OUT=ROOT/'build/p3-c2';OUT.mkdir(parents=True,exist_ok=True)
fixture=Path((OUT/'fixture-path.txt').read_text().strip())
assert fixture.name.startswith('memlane-p3-c2-app-')
meta=json.loads((fixture/'source-info.json').read_text());app=Path(meta['app'])
guard=SourceFileLoader('c2guard',str(ROOT/'scripts/p3-c2-common.py')).load_module()
guard.validate_fixture(fixture,app)
U=SourceFileLoader('c2ui',str(ROOT/'scripts/p1-ui.py')).load_module()
cfg=json.loads((fixture/'runtime-config.json').read_text());data=str(Path(cfg['root'])/'data')
ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(data.encode()).hexdigest()[:12]}/service.sock'
def call(op,**fields):
 if op=='merge_list':fields.setdefault('merge_list',{})
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**fields})+'\n').encode())
  r=json.loads(s.makefile('rb',buffering=0).readline())
 assert r['ok'],r
 return r.get('result')
report={'status':'failed','actual_app':True,'real_agent':'traex 0.214.1 internal','synthetic_only':True,'scenarios':[]}
ui=None
try:
 ui=U.UI(app,fixture,ROOT/'build/p3-c2-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(fixture/'pi-agent')})
 ui.press('设置');U.wait(lambda:'启动服务' in ui.labels() or '停止服务' in ui.labels(),timeout=95)
 if '启动服务' in ui.labels():ui.press('启动服务')
 U.wait(lambda:call('health')['state']=='ready',timeout=95)
 for letter in ['A','B']:
  if any(s['name']=='C2 合成项目 '+letter for s in call('scopes')):continue
  ui.press('项目');ui.set('project-name','C2 合成项目 '+letter);ui.set('project-path',str(fixture/('repo-'+letter.lower())));ui.press_key('register-project')
  U.wait(lambda:any(s['name']=='C2 合成项目 '+letter for s in call('scopes')))
 project=next(s for s in call('scopes') if s['name']=='C2 合成项目 A')['id']
 projects=call('scopes');(fixture/'projects.json').write_text(json.dumps(projects,ensure_ascii=False))
 def seed(name):
  values=[]
  for i,text in enumerate(['本项目统一使用 pnpm 管理依赖，禁止混用其他包管理器。','本项目依赖变更必须提交 pnpm-lock.yaml，与依赖版本保持一致。']):
   m=str(uuid.uuid4());text+=f' {name}Source{i}Token'
   v=call('apply',user_confirmed=True,mutation={'kind':'save','operation_id':str(uuid.uuid4()),'dataset':project,'memory':m,'title':f'C2 {name} 来源 {i+1}','text':text,'source':'合成验收来源','source_detail':{'fragment':text,'references':[f'synthetic://c2/{name}/{i}']}})
   values.append(v)
  return values
 def start(name):
  sources=seed(name)
  U.wait(lambda:all(call('get',memory=v['memory'])['index_state']=='indexed' for v in sources),timeout=60)
  ui.press('C2 合成项目 A');ui.press('立即整理')
  U.wait(lambda:all(any(n.get('key')=='merge-choose-'+v['memory'] for n in ui.nodes()) for v in sources))
  for v in sources:ui.press_key('merge-choose-'+v['memory'])
  prior={t['task'] for t in call('merge_list')['items']}
  began=time.monotonic();ui.press_key('merge-start')
  task=U.wait(lambda:next((t for t in call('merge_list')['items'] if t['task'] not in prior),None))
  tid=task['task'];U.wait(lambda:tid in ui.labels())
  assert call('merge_get',merge_task=tid)['task']==tid
  report['scenarios'].append({'name':name,'task':tid,'start_seconds':round(time.monotonic()-began,3),'stable_task_id':True})
  return sources,tid
 def pending(tid):
  t=U.wait(lambda:(lambda t:t if t['state'] in ['completed','failed','cancelled','interrupted'] else None)(call('merge_get',merge_task=tid)),timeout=150)
  assert t['state']=='completed' and t['review_state']=='pending',t
  U.wait(lambda:any(n.get('key')=='merge-confirm' for n in ui.nodes()))
  e=t['execution'];assert e['terminal_completed'] and e['process_reaped'] and e['process_group_gone'] and e['exit_code']==0 and not e.get('failure') and not e.get('item_errors'),e
  return t
 sources,tid=start('confirm');t=pending(tid)
 for v in sources:
  current=call('get',memory=v['memory']);assert current['revision']==1 and current['state']=='active' and current['text']==v['text']
 ui.system_capture('review-real-system.png')
 ui.press('总览');U.wait(lambda:any(n.get('key')=='merge-home-'+tid for n in ui.nodes()));ui.press_key('merge-home-'+tid)
 U.wait(lambda:tid in ui.labels());ui.press_key('merge-confirm')
 applied=U.wait(lambda:(lambda t:t if t.get('receipt') else None)(call('merge_get',merge_task=tid)))
 receipt=applied['receipt'];merged=receipt['memory']
 assert applied['review_state']=='applied'
 U.wait(lambda:call('get',memory=merged)['index_state']=='indexed',timeout=70)
 hits=call('search',datasets=[project],query='本项目 pnpm 锁文件 依赖管理',limit=20)
 assert any(h['version']['memory']==merged and h['version']['revision']==1 for h in hits),hits
 assert not any(h['version']['memory'] in [v['memory'] for v in sources] for h in hits)
 ui.press_key('merge-open-result');U.wait(lambda:call('get',memory=merged)['text'] in ui.labels())
 ui.system_capture('merged-real-system.png')
 ui.press_key('memory-merge-'+tid);U.wait(lambda:tid in ui.labels())
 ui.press_key('merge-open-source-'+sources[0]['memory']);U.wait(lambda:'已归档 · 保留来源与历史' in ui.labels())
 ui.press('历史记录');U.wait(lambda:'v2 · 整理归档' in ui.labels())
 ui.press('恢复 v1');ui.press('确认恢复');U.wait(lambda:call('get',memory=sources[0]['memory'])['revision']==3)
 assert call('get',memory=merged)['revision']==1
 report['scenarios'][-1].update(confirmation_before_knowledge_unchanged=True,app_confirmed=True,real_rust_current_merge=True,archived_history_and_restore_v3=True,receipt=receipt)
 sources,tid=start('reject');pending(tid);ui.press_key('merge-reject');U.wait(lambda:call('merge_get',merge_task=tid)['review_state']=='rejected')
 assert all(call('get',memory=v['memory'])['revision']==1 for v in sources)
 U.wait(lambda:'已拒绝' in ui.labels());ui.capture('rejected.png');report['scenarios'][-1]['app_rejected_sources_unchanged']=True
 sources,tid=start('cancel');U.wait(lambda:(call('merge_get',merge_task=tid).get('execution') or {}).get('process_active'),timeout=60)
 ui.press_key('merge-cancel');U.wait(lambda:call('merge_get',merge_task=tid)['state']=='cancelled')
 def closed():
  t=call('merge_get',merge_task=tid);e=t.get('execution') or {}
  return t if e.get('process_reaped') and e.get('process_group_gone') else None
 cancelled=U.wait(closed,timeout=45)
 assert cancelled.get('proposal') is None and all(call('get',memory=v['memory'])['revision']==1 for v in sources)
 U.wait(lambda:'已取消' in ui.labels());ui.capture('cancelled.png');report['scenarios'][-1]['real_active_turn_app_cancel']=True
 sources,tid=start('expired');pending(tid)
 # Source change is an explicit fault setup using the real service, not a model fixture.
 v=sources[1];call('apply',user_confirmed=True,mutation={'kind':'edit','operation_id':str(uuid.uuid4()),'dataset':project,'memory':v['memory'],'expected_revision':1,'text':v['text']+' 用户后来补充新约定。'})
 U.wait(lambda:call('merge_get',merge_task=tid)['review_state']=='expired')
 U.wait(lambda:'已过期 · 请重新生成建议' in ui.labels());assert not any(n.get('key')=='merge-confirm' for n in ui.nodes())
 ui.capture('expired.png');report['scenarios'][-1]['source_edit_fault_setup_app_expired']=True
 # App exit and reopen must reconstruct records from persistent service state.
 ui.quit();ui=None
 ui=U.UI(app,fixture,ROOT/'build/p3-c2-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(fixture/'pi-agent')})
 ui.press('整理记录');U.wait(lambda:any(n.get('key')=='merge-task-'+tid for n in ui.nodes()));ui.press_key('merge-task-'+tid)
 U.wait(lambda:'已过期 · 请重新生成建议' in ui.labels());report['app_reopen_persistent_list']=True
 sources,tid=start('interrupted');U.wait(lambda:(call('merge_get',merge_task=tid).get('execution') or {}).get('process_active'),timeout=60)
 ui.quit(stop=True);ui=None
 ui=U.UI(app,fixture,ROOT/'build/p3-c2-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(fixture/'pi-agent')})
 ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=95)
 ui.press('整理记录');U.wait(lambda:any(n.get('key')=='merge-task-'+tid for n in ui.nodes()));ui.press_key('merge-task-'+tid)
 U.wait(lambda:'执行中断 · 需重新整理' in ui.labels());interrupted=call('merge_get',merge_task=tid)
 assert interrupted['state']=='interrupted' and interrupted['attempt']==2 and interrupted.get('proposal') is None
 assert all(call('get',memory=v['memory'])['revision']==1 for v in sources)
 ui.capture('interrupted.png');report['scenarios'][-1]['real_app_stop_restart_interrupted']=True
 ui.quit(stop=True);ui=None
 report['status']='passed';report['service_stopped_at_end']=True
except Exception as e:
 report['error']=repr(e)
 raise
finally:
 if ui:ui.quit(stop=True)
 (OUT/'real-app.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
