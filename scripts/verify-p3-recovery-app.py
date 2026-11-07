#!/usr/bin/env python3
"""D2a 真实 App/traex/Rust 正常与活动取消，停服后核对持久租约；无模型协议夹具。"""
import fcntl, hashlib, json, os, socket, sqlite3, uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-recovery'
f=Path((OUT/'fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app'])
G=SourceFileLoader('recoveryguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,phase='recovery')
U=SourceFileLoader('recoveryui',str(ROOT/'scripts/p1-ui.py')).load_module();data=f/'client/data'
ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def call(op,**kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline())
    assert r['ok'],r
    return r.get('result')
def visible(key):return any(n.get('key')==key for n in ui.nodes())
def start():
    ui.press('D2a 合成项目');ui.press('立即整理')
    U.wait(lambda:all(visible('organization-choose-'+v['memory']) for v in sources))
    for v in sources:ui.press_key('organization-choose-'+v['memory'])
    old={t['task'] for t in call('organization_list')['items']};ui.press_key('organization-start')
    return U.wait(lambda:next((t['task'] for t in call('organization_list')['items'] if t['task'] not in old),None))
ui=None;report={'status':'failed','actual_app':True,'real_agent':'traex','real_rust':True,'model_fixture':False,'synthetic_only':True,'fixture':str(f.resolve())}
try:
    ui=U.UI(app,f,ROOT/'build/p3-recovery-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')})
    U.wait(lambda:call('health')['state']=='ready',timeout=95)
    assert not call('organization_list')['items'] and call('list',list_query={'limit':10})['total']==0,'fresh fixture required'
    ui.press('项目');ui.set('project-name','D2a 合成项目');ui.set('project-path',str(f/'repo-a'));ui.press_key('register-project')
    project=U.wait(lambda:next((s['id'] for s in call('scopes') if s['name']=='D2a 合成项目'),None))
    sources=[]
    for i,text in enumerate(['本项目使用 pnpm 管理依赖。','修改依赖必须提交 pnpm-lock.yaml 并核对持续集成。']):
        sources.append(call('apply',user_confirmed=True,mutation={'kind':'save','operation_id':str(uuid.uuid4()),'memory':str(uuid.uuid4()),'dataset':project,'title':f'D2a 来源 {i+1}','text':text,'source':'合成验收来源','source_detail':{'references':[f'synthetic://recovery/{i}']}}))
    ui.press('整理记录');ui.press_key('organization-tab-summary')
    normal=start()
    task=U.wait(lambda:(lambda t:t if t['state'] not in ['queued','running','applying'] else None)(call('organization_get',organization_task=normal)),timeout=150)
    assert task['state']=='completed' and task['result'] and task['execution']['process_reaped'] and task['execution']['process_group_gone'],task
    summaries=[e for e in task['entries'] if e['item']['action']=='summary' and e['state']=='applied'];assert summaries
    v=U.wait(lambda:(lambda v:v if v['index_state']=='indexed' else None)(call('get',memory=summaries[0]['receipt']['memory'])),timeout=95)
    hits=call('search',datasets=[project],query=v['text'],limit=50)
    assert any(h['version']['memory']==v['memory'] and h['version']['revision']==1 for h in hits)
    U.wait(lambda:'整理已完成' in ui.labels());ui.system_capture('lease-normal-system.png')
    assert all(call('get',memory=s['memory'])['revision']==1 for s in sources)
    cancelled=start();U.wait(lambda:(call('organization_get',organization_task=cancelled).get('execution') or {}).get('process_active'),timeout=60)
    U.wait(lambda:visible('organization-cancel'));ui.press_key('organization-cancel')
    t=U.wait(lambda:(lambda t:t if t['state']=='cancelled' and (t.get('execution') or {}).get('process_reaped') and (t.get('execution') or {}).get('process_group_gone') else None)(call('organization_get',organization_task=cancelled)),timeout=45)
    assert not t.get('result') and not t['entries'];ui.system_capture('lease-cancel-system.png')
    ui.quit(stop=True);ui=None
    assert json.loads((data/'service-state.json').read_text())['stopped']
    with (data/'memlane.sqlite.owner.lock').open('r+b') as lock:
        fcntl.flock(lock.fileno(),fcntl.LOCK_EX|fcntl.LOCK_NB)
        assert not (data/'memlane.sqlite-wal').exists()
        with sqlite3.connect('file:'+str(data/'memlane.sqlite')+'?mode=ro&immutable=1',uri=True) as db:
            assert db.execute('PRAGMA user_version').fetchone()[0]==8
            leases=[]
            for tid in [normal,cancelled]:
                row=db.execute('SELECT attempt,expires_ms,process_pid,released,process_reaped,group_gone FROM organization_leases WHERE task=?',(tid,)).fetchone()
                assert row and row[0]==1 and row[1]==0 and row[2]>1 and row[3:]==(1,1,1),row
                leases.append({'task':tid,'attempt':row[0],'pid':row[2],'released':True,'reaped':True,'group_gone':True})
            assert db.execute('SELECT count(*) FROM organization_tasks').fetchone()[0]==2
    report.update(status='passed',normal_task=normal,cancel_task=cancelled,real_rust_memory=v['memory'],sources_preserved=True,leases=leases,service_stopped=True,D2b_passed=False)
except Exception as e:report['error']=repr(e);raise
finally:
    if ui:ui.quit(stop=True)
    (OUT/'real-app.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
