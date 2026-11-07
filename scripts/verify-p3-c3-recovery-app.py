#!/usr/bin/env python3
"""真实 C3 App 部分失败/连续恢复/索引恢复；SQL 与 dataset 阻挡是故障夹具。"""
import hashlib,json,os,socket,sqlite3,subprocess,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-c3'
f=Path((OUT/'fixture-path.txt').read_text().strip());app=Path(json.loads((f/'source-info.json').read_text())['app'])
G=SourceFileLoader('c3recoverguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,phase='c3')
U=SourceFileLoader('c3recoverui',str(ROOT/'scripts/p1-ui.py')).load_module()
config=f/'client/service.json';normal=json.loads(config.read_text());root=Path(normal['root']);assert root.resolve()==(f/'client/data').resolve()
ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(root).encode()).hexdigest()[:12]}/service.sock'
def call(op,**k):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**k})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline())
 assert r['ok'],r
 return r.get('result')
def sql(text):
 with sqlite3.connect(root/'memlane.sqlite',timeout=5) as db:db.execute(text)
def open_ready():
 ui=U.UI(app,f,ROOT/'build/p3-c3-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')})
 ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=95)
 return ui
ui=None;blocked=None;injected=False
report={'status':'failed','actual_app':True,'real_agent':'traex','real_worker':True,'faults':['SQL tag INSERT failure','new synthetic Rust dataset blocked by file'],'synthetic_only':True}
try:
 ui=open_ready()
 name='C3 恢复故障项目 '+uuid.uuid4().hex[:6];repo=f/('repo-fault-'+uuid.uuid4().hex[:6]);subprocess.run(['git','init',str(repo)],check=True,capture_output=True)
 ui.press('项目');ui.set('project-name',name);ui.set('project-path',str(repo));ui.press_key('register-project')
 project=U.wait(lambda:next((s['id'] for s in call('scopes') if s['name']==name),None))
 ui.quit(stop=True);ui=None
 worker=Path(json.loads(config.read_text())['worker']['DataDir']).resolve();assert worker.is_relative_to(f.resolve())
 blocked=worker/project;assert not blocked.exists();blocked.write_text('C3 synthetic dataset failure\n')
 ui=open_ready();sources=[]
 for i,text in enumerate(['项目使用 pnpm 管理依赖。','修改依赖必须提交 pnpm-lock.yaml。']):
  sources.append(call('apply',user_confirmed=True,mutation={'kind':'save','operation_id':str(uuid.uuid4()),'memory':str(uuid.uuid4()),'dataset':project,'title':'C3 故障来源 '+str(i+1),'text':text,'source':'合成故障来源'}))
 ids=','.join("'"+str(uuid.UUID(v['memory']))+"'" for v in sources)
 sql("CREATE TRIGGER c3_tag_failure BEFORE INSERT ON memory_tags WHEN NEW.memory IN ("+ids+") BEGIN SELECT RAISE(ABORT,'C3 SQL tag fault fixture'); END");injected=True
 ui.press(name);ui.press('整理记录');ui.press_key('organization-tab-summary');ui.press(name);ui.press('立即整理')
 U.wait(lambda:all(any(n.get('key')=='organization-choose-'+v['memory'] for n in ui.nodes()) for v in sources))
 for v in sources:ui.press_key('organization-choose-'+v['memory'])
 old={t['task'] for t in call('organization_list')['items']};ui.press_key('organization-start')
 tid=U.wait(lambda:next((t['task'] for t in call('organization_list')['items'] if t['task'] not in old),None))
 partial=U.wait(lambda:(lambda t:t if t['state']=='partial_failed' else None)(call('organization_get',organization_task=tid)),timeout=150)
 receipts={e['item_id']:e['receipt'] for e in partial['entries'] if e['state']=='applied'}
 assert receipts and any(e['state']=='failed' for e in partial['entries'])
 applied=len(receipts);failed=len(partial['entries'])-applied
 U.wait(lambda:f'已应用 {applied} 项 · 未应用 {failed} 项' in ui.labels());ui.system_capture('organization-partial-sql-fixture-system.png')
 for _ in range(2):
  previous=partial['attempt'];ui.press_key('organization-resume')
  partial=U.wait(lambda:(lambda t:t if t['attempt']>previous and t['state']=='partial_failed' else None)(call('organization_get',organization_task=tid)))
  U.wait(lambda:'部分未应用' in ui.labels())
  assert {e['item_id']:e['receipt'] for e in partial['entries'] if e['state']=='applied'}==receipts
 sql('DROP TRIGGER c3_tag_failure');injected=False
 previous=partial['attempt'];ui.press_key('organization-resume')
 done=U.wait(lambda:(lambda t:t if t['attempt']>previous and t['state']=='completed' else None)(call('organization_get',organization_task=tid)))
 assert all(e['state']=='applied' for e in done['entries'])
 assert all(next(e for e in done['entries'] if e['item_id']==item)['receipt']==receipt for item,receipt in receipts.items())
 summary=next(e for e in done['entries'] if e['item']['action']=='summary');memory=summary['receipt']['memory']
 U.wait(lambda:call('get',memory=memory)['index_state']=='failed')
 U.wait(lambda:'v1 · 索引失败 · 正文已保留' in ui.labels());ui.press_key('organization-open-'+summary['item_id']);U.wait(lambda:'索引失败 · 正文已保留' in ui.labels());ui.system_capture('organization-index-failed-system.png')
 ui.quit(stop=True);ui=None
 assert blocked.read_text()=='C3 synthetic dataset failure\n';blocked.unlink();blocked=None
 ui=open_ready();U.wait(lambda:call('get',memory=memory)['index_state']=='indexed',timeout=95)
 hits=call('search',datasets=[project],query='pnpm 锁文件',limit=50);assert any(h['version']['memory']==memory and h['version']['revision']==1 for h in hits)
 assert call('organization_get',organization_task=tid)['entries']==done['entries']
 ui.press('整理记录');ui.press_key('organization-tab-summary');U.wait(lambda:any(n.get('key')=='organization-task-'+tid for n in ui.nodes()));ui.press_key('organization-task-'+tid);U.wait(lambda:'v1 · 可检索' in ui.labels());ui.system_capture('organization-index-recovered-system.png')
 ui.quit(stop=True);ui=None
 report.update(status='passed',task=tid,attempt=done['attempt'],real_normal_model=True,partial_count=failed,repeated_resume_without_model=True,receipts_preserved=True,real_rust_index_recovered=True,service_stopped=True)
except Exception as e:report['error']=repr(e);raise
finally:
 if ui:ui.quit(stop=True)
 if injected:sql('DROP TRIGGER IF EXISTS c3_tag_failure')
 if blocked and blocked.exists():
  assert blocked.read_text()=='C3 synthetic dataset failure\n';blocked.unlink()
 (OUT/'recovery-app.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
