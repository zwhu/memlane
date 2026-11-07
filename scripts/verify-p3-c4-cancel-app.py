#!/usr/bin/env python3
"""在已完成合成验收根中通过真实 App 取消活动 v2 模型任务。"""
import hashlib,json,os,socket,uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-c4'
f=Path((OUT/'fixture-path.txt').read_text().strip());app=Path(json.loads((f/'source-info.json').read_text())['app'])
G=SourceFileLoader('c4cancelguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,phase='c4')
evidence=json.loads((OUT/'real-app.json').read_text());meta=json.loads((f/'source-info.json').read_text())
assert evidence['status']=='passed' and evidence.get('fixture')==str(f.resolve()) and evidence.get('identifier')==meta['identifier'],'same-fixture main App flow must pass first'
U=SourceFileLoader('c4cancelui',str(ROOT/'scripts/p1-ui.py')).load_module();root=f/'client/data';ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(root).encode()).hexdigest()[:12]}/service.sock'
def call(op,**k):
 with socket.socket(socket.AF_UNIX) as s:
  s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**k})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline())
 assert r['ok'],r
 return r.get('result')
ui=None;report={'status':'failed','actual_app':True,'real_model_cancel':True,'synthetic_only':True}
try:
 ui=U.UI(app,f,ROOT/'build/p3-c4-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')});ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=95)
 project=next(s['id'] for s in call('scopes') if s['name']=='C4 合成源项目')
 sources=[]
 for i,body in enumerate(['项目使用 pnpm 管理依赖。','依赖变更提交 pnpm-lock.yaml。']):sources.append(call('apply',user_confirmed=True,mutation={'kind':'save','operation_id':str(uuid.uuid4()),'memory':str(uuid.uuid4()),'dataset':project,'text':body,'title':f'C4 活动取消来源 {i}','source':'合成取消来源'}))
 ui.press('C4 合成源项目');ui.press('整理记录');ui.press_key('organization-tab-summary');ui.press('C4 合成源项目');ui.press('立即整理');ui.press_key('organization-review-mode')
 U.wait(lambda:all(any(n.get('key')=='organization-choose-'+v['memory'] for n in ui.nodes()) for v in sources))
 for v in sources:ui.press_key('organization-choose-'+v['memory'])
 old={t['task'] for t in call('organization_list')['items']};ui.press_key('organization-start')
 tid=U.wait(lambda:next((t['task'] for t in call('organization_list')['items'] if t['task'] not in old),None));U.wait(lambda:(call('organization_get',organization_task=tid).get('execution') or {}).get('process_active'),timeout=60)
 ui.press_key('organization-cancel');task=U.wait(lambda:(lambda t:t if t['state']=='cancelled' and (t.get('execution') or {}).get('process_reaped') and (t.get('execution') or {}).get('process_group_gone') else None)(call('organization_get',organization_task=tid)),timeout=45)
 assert task['snapshot']['schema']=='memlane-organization-v2' and not task.get('result') and not task['entries']
 assert all(call('get',memory=v['memory'])['revision']==1 for v in sources);ui.system_capture('review-cancelled-real-system.png');ui.quit(stop=True);ui=None
 report.update(status='passed',task=tid,process_reaped=True,process_group_gone=True,knowledge_unchanged=True,service_stopped=True)
except Exception as e:report['error']=repr(e);raise
finally:
 if ui:ui.quit(stop=True)
 (OUT/'cancel-app.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
