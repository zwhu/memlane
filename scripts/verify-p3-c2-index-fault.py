#!/usr/bin/env python3
"""真实 App/traex/Rust 索引失败与恢复；专用 dataset 路径阻挡文件是故障夹具。"""
import hashlib,json,os,socket,subprocess,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-c2'
f=Path((OUT/'fixture-path.txt').read_text().strip());assert f.name.startswith('memlane-p3-c2-app-')
app=Path(json.loads((f/'source-info.json').read_text())['app']);config=f/'client/service.json'
guard=SourceFileLoader('c2faultguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();guard.validate_fixture(f,app)
normal=json.loads(config.read_text());root=Path(normal['root']);assert root==f/'client/data'
ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(root).encode()).hexdigest()[:12]}/service.sock'
U=SourceFileLoader('c2faultui',str(ROOT/'scripts/p1-ui.py')).load_module()
def call(op,**k):
 if op=='merge_list':k.setdefault('merge_list',{})
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**k})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline())
 assert r['ok'],r
 return r.get('result')
assert json.loads((root/'service-state.json').read_text())['stopped']
failed=json.loads(subprocess.check_output([str(OUT/'ui-failure-fixture'),'--root',str(root)],text=True))
(OUT/'ui-failure-fixture.json').write_text(json.dumps(failed,indent=2))
ui=None;blocked=None
report={'status':'failed','actual_app':True,'real_agent':'traex','real_worker':True,'fault':'new synthetic Rust dataset path blocked by a file','synthetic_only':True}
try:
 ui=U.UI(app,f,ROOT/'build/p3-c2-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')})
 ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=95)
 name='C2 索引故障项目 '+uuid.uuid4().hex[:6];repo=f/('repo-index-'+uuid.uuid4().hex[:6]);subprocess.run(['git','init',str(repo)],check=True,capture_output=True)
 ui.press('项目');ui.set('project-name',name);ui.set('project-path',str(repo));ui.press_key('register-project')
 project=U.wait(lambda:next((s['id'] for s in call('scopes') if s['name']==name),None))
 ui.press('整理记录');U.wait(lambda:any(n.get('key')=='merge-task-'+failed['task'] for n in ui.nodes()));ui.press_key('merge-task-'+failed['task'])
 U.wait(lambda:'整理失败' in ui.labels() and 'agent_timeout' in ui.labels());ui.capture('explicit-ui-failure-fixture.png')
 report['failed_state_ui_fixture_verified']=True
 ui.quit(stop=True);ui=None
 normal=json.loads(config.read_text());worker_data=Path(normal['worker']['DataDir']).resolve();assert worker_data.is_relative_to(f.resolve())
 blocked=worker_data/project;assert not blocked.exists();blocked.write_text('Memlane C2 synthetic dataset path fault\n')
 ui=U.UI(app,f,ROOT/'build/p3-c2-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')})
 ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=95)
 sources=[]
 for i,text in enumerate(['项目用 pnpm 管理依赖。','项目必须提交 pnpm-lock.yaml。']):
  sources.append(call('apply',user_confirmed=True,mutation={'kind':'save','operation_id':str(uuid.uuid4()),'memory':str(uuid.uuid4()),'dataset':project,'title':'C2 索引故障来源 '+str(i+1),'text':text+' indexFaultC2Source'+str(i),'source':'合成故障来源'}))
 ui.press(name);ui.press('立即整理')
 U.wait(lambda:all(any(n.get('key')=='merge-choose-'+v['memory'] for n in ui.nodes()) for v in sources))
 for v in sources:ui.press_key('merge-choose-'+v['memory'])
 prior={t['task'] for t in call('merge_list')['items']};ui.press_key('merge-start')
 tid=U.wait(lambda:next((t['task'] for t in call('merge_list')['items'] if t['task'] not in prior),None))
 t=U.wait(lambda:(lambda t:t if t['state'] in ['completed','failed'] else None)(call('merge_get',merge_task=tid)),timeout=150)
 assert t['state']=='completed' and t['review_state']=='pending',t
 assert all(call('get',memory=v['memory'])['revision']==1 for v in sources)
 U.wait(lambda:any(n.get('key')=='merge-confirm' for n in ui.nodes()));ui.press_key('merge-confirm')
 applied=U.wait(lambda:(lambda t:t if t.get('receipt') else None)(call('merge_get',merge_task=tid)))
 merged=applied['receipt']['memory'];U.wait(lambda:call('get',memory=merged)['index_state']=='failed')
 U.wait(lambda:'v1 · 索引失败 · 正文已保留' in ui.labels())
 ui.press_key('merge-open-result');U.wait(lambda:'索引失败 · 正文已保留' in ui.labels())
 ui.system_capture('index-failed-real-system.png')
 assert call('get',memory=merged)['text']==t['proposal']['text']
 ui.quit(stop=True);ui=None
 assert blocked.read_text()=='Memlane C2 synthetic dataset path fault\n';blocked.unlink();blocked=None
 ui=U.UI(app,f,ROOT/'build/p3-c2-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')})
 ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=95)
 U.wait(lambda:call('get',memory=merged)['index_state']=='indexed',timeout=95)
 ui.press('整理记录');U.wait(lambda:any(n.get('key')=='merge-task-'+tid for n in ui.nodes()));ui.press_key('merge-task-'+tid)
 U.wait(lambda:'v1 · 可检索' in ui.labels());ui.press_key('merge-open-result')
 hits=call('search',datasets=[project],query='pnpm 锁文件',limit=50)
 assert any(h['version']['memory']==merged and h['version']['revision']==1 for h in hits)
 assert call('merge_get',merge_task=tid)['receipt']==applied['receipt']
 ui.system_capture('index-recovered-real-system.png');ui.quit(stop=True);ui=None
 report.update(status='passed',task=tid,receipt=applied['receipt'],real_app_confirmed=True,failed_body_preserved=True,real_rust_recovered=True,service_stopped_at_end=True)
except Exception as e:
 report['error']=repr(e);raise
finally:
 if ui:ui.quit(stop=True)
 if blocked and blocked.exists():
  assert blocked.read_text()=='Memlane C2 synthetic dataset path fault\n';blocked.unlink()
 (OUT/'index-fault-app.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
