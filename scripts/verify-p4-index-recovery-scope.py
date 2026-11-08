#!/usr/bin/env python3
"""Actual scope Select and budget error UI; 10001 raw rows are explicit SQL fixtures."""
import hashlib,json,os,socket,sqlite3,threading,time,traceback,uuid
from importlib.machinery import SourceFileLoader
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p4-delivery'
f=Path((OUT/'index-recovery-final-fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app']);data=f/'client/data'
G=SourceFileLoader('indexscopeguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,'code')
assert json.loads((OUT/'index-recovery-final-app.json').read_text())['status']=='passed','actual normal recovery must pass first'
assert json.loads((data/'service-state.json').read_text())['stopped']
ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock';assert not Path(ipc).exists()
U=SourceFileLoader('indexscopeui',str(ROOT/'scripts/p1-ui.py')).load_module()
def call(op,**kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(125);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline());assert r['ok'],r;return r.get('result')
def choose(key,label):
    before={n['id'] for n in ui.nodes()};ui.press_key(key)
    def option():
        nodes=ui.nodes();popup={n['id'] for n in nodes if n.get('role')=='popup' and n['id'] not in before};return next((n for n in nodes if n.get('label')==label and n.get('parent') in popup),None)
    n=U.wait(option);b=n['bounds'];window=ui.req('windows')['windows'][0]['id'];ui.req('input',window=window,events=[{'type':'click','x':b['x']+b['width']/2,'y':b['y']+b['height']/2}]);ui.req('idle',timeout=1500)
budget=uuid.uuid4().hex;budget=str(uuid.UUID(budget));ids=[str(uuid.uuid4()) for _ in range(10001)]
ui=None;inserted=False
report={'status':'failed','actual_app':False,'actual_rust':False,'synthetic_only':True,'model_called':False,'sql_fixture':'10001 synthetic raw rows with completed index jobs solely to exercise actual maintenance size gate','formal_distribution':False,'fixture':str(f)}
try:
    with sqlite3.connect(data/'memlane.sqlite') as db:
        db.execute("INSERT INTO scopes(id,kind,name) VALUES(?,'project','明确SQL上限夹具')",(budget,))
        db.executemany("INSERT INTO memories(id,dataset,current_revision,indexed_revision,generation,state) VALUES(?,?,1,0,'','active')",[(i,budget) for i in ids])
        db.executemany("INSERT INTO versions(memory,revision,text,operation) VALUES(?,1,'explicit SQL raw budget fixture','save')",[(i,) for i in ids])
        db.executemany("INSERT INTO memory_metadata(memory,source,origin,created_at) VALUES(?,'explicit SQL budget fixture','raw',CURRENT_TIMESTAMP)",[(i,) for i in ids])
        db.executemany("INSERT INTO jobs(memory,revision,state) VALUES(?,1,'completed')",[(i,) for i in ids])
    inserted=True
    ui=U.UI.__new__(U.UI);U.UI.__init__(ui,app,f,ROOT/'build/p4-index-recovery-final-screenshots');report['actual_app']=True
    ui.press('设置');U.wait(lambda:'启动服务' in ui.labels());ui.press('启动服务');U.wait(lambda:call('health')['state']=='ready',timeout=120);report['actual_rust']=True
    original=call('index_rebuild');ui.press_key('index-rebuild-open');ui.press_key('index-rebuild-confirm')
    U.wait(lambda:any('超过 10000 条' in t for t in ui.labels()));assert call('index_rebuild')==original,'budget rejection recorded a partial task'
    ui.system_capture('index-rebuild-budget-system.png')
    project=next(s for s in call('scopes') if s['name']=='索引恢复合成项目')
    choose('index-rebuild-scope',project['name']+' · '+project['id'][:8]);ui.press_key('index-rebuild-confirm')
    scoped=U.wait(lambda:(r if (r:=call('index_rebuild'))['id']!=original['id'] else None));assert scoped['scope']==project['id'] and len(scoped['items'])==2 and not any(i['memory'] in ids for i in scoped['items'])
    done=U.wait(lambda:(r if (r:=call('index_rebuild',index_rebuild_id=scoped['id']))['state']=='completed' else None),timeout=120)
    U.wait(lambda:'重建完成' in ui.labels());ui.system_capture('index-rebuild-scope-system.png')
    assert call('index_rebuild',index_rebuild_id=original['id'])==original
    ui.quit(stop=True);ui=None
    report.update(status='passed',limit_rejected_without_receipt=True,actual_scope_selected=project['id'],scoped_task=done,prior_task_preserved=True)
except Exception as error:report['error']=repr(error);report['traceback']=traceback.format_exc()
finally:
    errors=[]
    if ui:
        try:G.close_partial_ui(ui,f,app,'code')
        except Exception as error:errors.append('ui: '+repr(error))
    try:
        if Path(ipc).exists():call('stop')
        U.wait(lambda:not Path(ipc).exists(),timeout=20)
        assert json.loads((data/'service-state.json').read_text())['stopped']
        if inserted:
            with sqlite3.connect(data/'memlane.sqlite') as db:
                assert db.execute('SELECT count(*) FROM index_rebuild_items WHERE memory IN (SELECT id FROM memories WHERE dataset=?)',(budget,)).fetchone()[0]==0
                for table,column in [('jobs','memory'),('memory_metadata','memory'),('versions','memory')]:db.execute(f'DELETE FROM {table} WHERE {column} IN (SELECT id FROM memories WHERE dataset=?)',(budget,))
                db.execute('DELETE FROM memories WHERE dataset=?',(budget,));db.execute('DELETE FROM scopes WHERE id=?',(budget,))
            report['sql_fixture_reclaimed']=True
    except Exception as error:errors.append('fixture cleanup: '+repr(error))
    report['cleanup_errors']=errors
    if errors:report['status']='failed'
    (OUT/'index-recovery-scope-app.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({k:v for k,v in report.items() if k!='scoped_task'},ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
