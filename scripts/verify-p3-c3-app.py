#!/usr/bin/env python3
"""C3 实际 App/traex/Rust 主流程；只用独立合成根，不访问用户记忆或配置。"""
import hashlib,json,os,socket,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-c3';OUT.mkdir(parents=True,exist_ok=True)
f=Path((OUT/'fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app'])
G=SourceFileLoader('c3guard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,phase='c3')
U=SourceFileLoader('c3ui',str(ROOT/'scripts/p1-ui.py')).load_module();data=str(f/'client/data');ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(data.encode()).hexdigest()[:12]}/service.sock'
def call(op,**kw):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline())
 assert r['ok'],r
 return r.get('result')
ui=None;report={'status':'failed','actual_app':True,'real_agent':'traex','real_worker':True,'synthetic_only':True}
try:
 ui=U.UI(app,f,ROOT/'build/p3-c3-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')})
 U.wait(lambda:call('health')['state']=='ready',timeout=95)
 for letter in ['A','B']:
  ui.press('项目');ui.set('project-name','C3 合成项目 '+letter);ui.set('project-path',str(f/('repo-'+letter.lower())));ui.press_key('register-project')
  U.wait(lambda:any(s['name']=='C3 合成项目 '+letter for s in call('scopes')))
 project=next(s['id'] for s in call('scopes') if s['name']=='C3 合成项目 A')
 sources=[]
 for i,(kind,text) in enumerate([('knowledge','项目统一用 pnpm 管理依赖。'),('knowledge','修改依赖必须提交 pnpm-lock.yaml。'),('raw','记录片段：部署失败应保留原文件，用旧版本恢复。')]):
  sources.append(call('apply',user_confirmed=True,mutation={'kind':'save','operation_id':str(uuid.uuid4()),'memory':str(uuid.uuid4()),'dataset':project,'origin':kind,'title':f'C3 来源 {i+1}','text':text+f' c3Source{i}Token','source':'合成验收来源','source_detail':{'fragment':text,'references':[f'synthetic://c3/source/{i}']}}))
 ui.press('C3 合成项目 A');ui.press('整理记录');ui.press_key('organization-tab-summary');ui.press('C3 合成项目 A');ui.press('立即整理')
 U.wait(lambda:all(any(n.get('key')=='organization-choose-'+v['memory'] for n in ui.nodes()) for v in sources[:2]))
 for v in sources[:2]:ui.press_key('organization-choose-'+v['memory'])
 ui.press_key('organization-kind-raw');U.wait(lambda:any(n.get('key')=='organization-choose-'+sources[2]['memory'] for n in ui.nodes()));ui.press_key('organization-choose-'+sources[2]['memory'])
 prior={t['task'] for t in call('organization_list')['items']};ui.press_key('organization-start')
 taskid=U.wait(lambda:next((t['task'] for t in call('organization_list')['items'] if t['task'] not in prior),None))
 task=U.wait(lambda:(lambda t:t if t['state'] not in ['queued','running','applying'] else None)(call('organization_get',organization_task=taskid)),timeout=150)
 assert task['state']=='completed' and task['result'],task
 assert len(task['snapshot']['inputs'])==3
 summaries=[e for e in task['entries'] if e['item']['action']=='summary'];tags=[e for e in task['entries'] if e['item']['action']=='add_tags']
 assert summaries and tags and all(e['state']=='applied' and e.get('receipt') for e in task['entries'])
 assert all(call('get',memory=v['memory'])['revision']==1 and call('get',memory=v['memory'])['state']=='active' for v in sources)
 U.wait(lambda:'整理已完成' in ui.labels());ui.system_capture('organization-real-system.png')
 summary=summaries[0];memory=summary['receipt']['memory'];U.wait(lambda:call('get',memory=memory)['index_state']=='indexed',timeout=90)
 hits=call('search',datasets=[project],query='pnpm 项目开发约定 部署失败',limit=50);assert any(h['version']['memory']==memory and h['version']['revision']==1 for h in hits)
 ui.press_key('organization-open-'+summary['item_id']);U.wait(lambda:'派生摘要 · 自动产生' in ui.labels());ui.system_capture('summary-real-system.png')
 assert any(n.get('key','').startswith('derived-source-') for n in ui.nodes())
 before=call('activities',timezone='Asia/Shanghai')
 # Change a cited source through the App. The automatic summary becomes stale.
 cited=summary['item']['sources'][0]['memory'];ui.press('返回摘要任务');U.wait(lambda:taskid in ui.labels());ui.press_key('organization-source-'+cited)
 U.wait(lambda:any(n.get('label')=='编辑' and 'press' in n.get('actions',[]) for n in ui.nodes()));ui.press('编辑');ui.set('memory-draft',call('get',memory=cited)['text']+' 用户后来明确调整了约定。');ui.press('保存')
 U.wait(lambda:call('get',memory=cited)['revision']==2);U.wait(lambda:call('get',memory=memory).get('derived',{}).get('state')=='stale')
 ui.press('返回摘要任务');U.wait(lambda:'摘要来源已变化，已排除检索。' in ui.labels());ui.press_key('organization-open-'+summary['item_id'])
 U.wait(lambda:'摘要来源已变化 · 不参与检索' in ui.labels());ui.system_capture('summary-stale-real-system.png')
 hits=call('search',datasets=[project],query='pnpm 项目开发约定',limit=50);assert not any(h['version']['memory']==memory for h in hits)
 # Explicit user revision/restore remains authoritative and retains frozen lineage.
 ui.press('编辑');ui.set('memory-draft','用户核对后确认项目仍用 pnpm，依赖变更提交锁文件。 c3UserConfirmedToken');ui.press('保存')
 U.wait(lambda:call('get',memory=memory)['revision']==2);U.wait(lambda:'派生摘要 · 用户确认修订' in ui.labels());U.wait(lambda:call('get',memory=memory)['index_state']=='indexed',timeout=90)
 ui.press('历史记录');U.wait(lambda:any(n.get('label')=='恢复 v1' for n in ui.nodes()));ui.press('恢复 v1');ui.press('确认恢复')
 U.wait(lambda:call('get',memory=memory)['revision']==3);assert call('get',memory=memory)['derived']['state']=='user_confirmed'
 assert len(call('organization_sources',memory=memory,expected_revision=3))==len(summary['item']['sources'])
 after=call('activities',timezone='Asia/Shanghai');assert before['days']==after['days'] and before['imported']==after['imported']
 # REAL active model cancellation through the actual window.
 ui.press('C3 合成项目 A');ui.press('立即整理');U.wait(lambda:all(any(n.get('key')=='organization-choose-'+v['memory'] for n in ui.nodes()) for v in sources[:2]))
 for v in sources[:2]:ui.press_key('organization-choose-'+v['memory'])
 old={t['task'] for t in call('organization_list')['items']};ui.press_key('organization-start')
 cancelledid=U.wait(lambda:next((t['task'] for t in call('organization_list')['items'] if t['task'] not in old),None))
 U.wait(lambda:(call('organization_get',organization_task=cancelledid).get('execution') or {}).get('process_active'),timeout=60)
 ui.press_key('organization-cancel')
 cancelled=U.wait(lambda:(lambda t:t if t['state']=='cancelled' and (t.get('execution') or {}).get('process_reaped') and (t.get('execution') or {}).get('process_group_gone') else None)(call('organization_get',organization_task=cancelledid)),timeout=45)
 assert cancelled.get('result') is None and not cancelled['entries'];ui.capture('organization-cancelled.png')
 ui.quit();ui=None
 ui=U.UI(app,f,ROOT/'build/p3-c3-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')});ui.press('整理记录');ui.press_key('organization-tab-summary')
 U.wait(lambda:any(n.get('key')=='organization-task-'+taskid for n in ui.nodes()));ui.press_key('organization-task-'+taskid);U.wait(lambda:taskid in ui.labels())
 ui.quit(stop=True);ui=None
 report.update(status='passed',task=taskid,material_count=3,summary_count=len(summaries),tag_actions=len(tags),sources_not_rewritten=True,real_rust_current_summary=True,source_app_edit_invalidates=True,stale_summary_excluded=True,user_edit_v2_restore_v3=True,source_history_retained=True,activities_not_fabricated=True,real_app_cancel=cancelledid,persistent_task_after_reopen=True,service_stopped=True)
except Exception as e:
 report['error']=repr(e);raise
finally:
 if ui:ui.quit(stop=True)
 (OUT/'real-app.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
