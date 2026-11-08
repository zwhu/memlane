#!/usr/bin/env python3
"""Actual App backup/catalogue/checksum and real Rust; explicit IO damage fixtures."""
import functools,hashlib,http.server,json,os,sqlite3,socket,threading,traceback,uuid
from importlib.machinery import SourceFileLoader
from pathlib import Path

ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p4-delivery'
f=Path((OUT/'backup-fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app']);data=f/'client/data'
G=SourceFileLoader('backupguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,'code')
U=SourceFileLoader('backupui',str(ROOT/'scripts/p1-ui.py')).load_module()
ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def response(op,**kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(125);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());return json.loads(s.makefile('rb',buffering=0).readline())
def call(op,**kw):
    r=response(op,**kw);assert r['ok'],r;return r.get('result')
def visible(key):return any(n.get('key')==key for n in ui.nodes())
def scroll(key):
    node=U.wait(lambda:next((n for n in ui.nodes() if n.get('key')==key),None));assert 'scrollIntoView' in node['actions'],node
    ui.req('act',id=node['id'],action='scrollIntoView');ui.req('idle',timeout=1500)
    node=next(n for n in ui.nodes() if n.get('key')==key);assert node['bounds']['y']>=0,node
def open_ui():
    global ui
    ui=U.UI.__new__(U.UI);U.UI.__init__(ui,app,f,ROOT/'build/p4-backup-screenshots');ui.press('设置')
    U.wait(lambda:'启动服务' in ui.labels() or '停止服务' in ui.labels(),timeout=180)
def ready():
    if '启动服务' in ui.labels():
        node=next(n for n in ui.nodes() if n.get('label')=='启动服务' and 'press' in n.get('actions',[]))
        if 'scrollIntoView' in node['actions']:ui.req('act',id=node['id'],action='scrollIntoView');ui.req('idle',timeout=1500)
        ui.press('启动服务')
    U.wait(lambda:call('health')['state']=='ready',timeout=180)
def records():
    base=data/'backups/manual'
    out=[]
    if base.exists():
        for p in base.glob('*/receipt.json'):
            try:canonical=p.parent.name==str(uuid.UUID(p.parent.name))
            except ValueError:continue
            if canonical:out.append(json.loads(p.read_text()))
    return out
def fingerprints(path,immutable=False):
    uri=path.as_uri()+'?mode=ro'+('&immutable=1' if immutable else '')
    with sqlite3.connect(uri,uri=True) as db:
        db.execute('BEGIN');assert db.execute('PRAGMA integrity_check').fetchone()[0]=='ok'
        out={}
        for name, in db.execute("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name"):
            quoted='"'+name.replace('"','""')+'"';rows=db.execute('SELECT * FROM '+quoted).fetchall();rows.sort(key=repr)
            encoded=json.dumps(rows,ensure_ascii=False,default=lambda v: {'bytes':v.hex()},separators=(',',':')).encode();out[name]={'rows':len(rows),'sha256':hashlib.sha256(encoded).hexdigest()}
        return out
class Handler(http.server.SimpleHTTPRequestHandler):
    def log_message(self,*args):pass
server=http.server.ThreadingHTTPServer(('127.0.0.1',meta['source_port']),functools.partial(Handler,directory=meta['source_root']))
thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
ui=None;held=None;damaged=None;original=None;saved_receipt=None
report={'status':'failed','actual_app':False,'actual_rust':False,'synthetic_only':True,'model_called':False,'formal_distribution':False,'restore_verified':False,'replacement_verified':False,'fixture':str(f),'fault_fixtures':['one-byte checksum damage','temporarily missing receipt']}
try:
    open_ui();ready();report['actual_app']=True;report['actual_rust']=True
    assert call('list',list_query={'limit':5})['total']==0 and records()==[],'new fixture required'
    ui.press('项目');ui.set('project-name','备份合成项目');ui.set('project-path',str(f/'repo-a'));ui.press_key('register-project')
    project=U.wait(lambda:next((s['id'] for s in call('scopes') if s['name']=='备份合成项目'),None));ui.press('备份合成项目')
    ui.press('新增记忆');ui.set('memory-title','备份合成约定');ui.set('memory-draft','本项目统一使用 pnpm，保留来源。');ui.press('保存')
    memory=U.wait(lambda:next((v for v in call('list',list_query={'datasets':[project],'limit':5})['items'] if v['title']=='备份合成约定'),None))['memory']
    U.wait(lambda:call('get',memory=memory)['index_state']=='indexed',timeout=120)
    held=sqlite3.connect((data/'memlane.sqlite').as_uri()+'?mode=ro',uri=True);held.execute('BEGIN');held.execute('SELECT count(*) FROM memories').fetchone()
    ui.press_key('memory-'+memory);U.wait(lambda:'备份合成约定' in ui.labels());ui.press('编辑');U.wait(lambda:visible('memory-draft'))
    text2='本项目统一使用 pnpm，并提交 pnpm-lock.yaml；保留完整来源。';ui.set('memory-draft',text2);ui.press('保存')
    U.wait(lambda:(v:=call('get',memory=memory))['revision']==2 and v['index_state']=='indexed',timeout=120)
    ui.press('设置');U.wait(lambda:visible('backup-create'));scroll('backup-create')
    assert (data/'memlane.sqlite-wal').stat().st_size>0
    golden=fingerprints(data/'memlane.sqlite')
    denied=response('backup_create',backup_id=str(uuid.uuid4()));assert not denied['ok'] and denied['error']=='backup creation requires user confirmation';assert records()==[]
    ui.press_key('backup-create');first=U.wait(lambda:(r[0] if len(r:=records())==1 else None),timeout=90);id1=first['id']
    U.wait(lambda:'一致性备份已完成 · '+id1 in ui.labels(),timeout=90);snapshot=data/'backups/manual'/id1/'snapshot.sqlite'
    assert fingerprints(snapshot,True)==golden and fingerprints(data/'memlane.sqlite')==golden
    assert hashlib.sha256(snapshot.read_bytes()).hexdigest()==first['sha256']
    held.rollback();held.close();held=None
    scroll('backup-verify-'+id1);ui.press_key('backup-verify-'+id1);U.wait(lambda:'备份文件校验通过 · '+id1 in ui.labels());ui.system_capture('backup-created-system.png')
    ui.press('备份合成项目');U.wait(lambda:visible('memory-'+memory));ui.press_key('memory-'+memory);U.wait(lambda:text2 in ui.labels());ui.press('编辑');U.wait(lambda:visible('memory-draft'))
    text3='本项目统一使用 pnpm，并提交 pnpm-lock.yaml；备份之后增加本地缓存约定。';ui.set('memory-draft',text3);ui.press('保存');U.wait(lambda:(v:=call('get',memory=memory))['revision']==3 and v['index_state']=='indexed',timeout=120)
    replay=call('backup_create',user_confirmed=True,backup_id=id1);assert replay['id']==id1 and replay['sha256']==first['sha256'] and fingerprints(snapshot,True)==golden and len(records())==1
    ui.press('设置');scroll('backup-create');ui.press_key('backup-create');second=U.wait(lambda:next((r for r in records() if r['id']!=id1),None),timeout=90);id2=second['id'];assert second['sha256']!=first['sha256'] and second['vault']==first['vault']
    U.wait(lambda:'一致性备份已完成 · '+id2 in ui.labels(),timeout=90)
    G.ensure_inside(snapshot,f);original=snapshot.read_bytes();bad=bytearray(original);bad[-1]^=1;damaged=snapshot;snapshot.write_bytes(bad)
    scroll('backup-verify-'+id1);ui.press_key('backup-verify-'+id1);U.wait(lambda:any('备份文件校验失败' in t for t in ui.labels()));ui.system_capture('backup-checksum-failed-system.png');assert snapshot.read_bytes()==bad
    snapshot.write_bytes(original);damaged=None;original=None
    receipt=snapshot.parent/'receipt.json';saved_receipt=receipt.with_name('receipt.explicit-fault');receipt.rename(saved_receipt)
    U.wait(lambda:'备份记录损坏或不可读，不能用于恢复。' in ui.labels(),timeout=15)
    assert visible('backup-verify-'+id2);scroll('backup-verify-'+id2);ui.press_key('backup-verify-'+id2);U.wait(lambda:'备份文件校验通过 · '+id2 in ui.labels());ui.system_capture('backup-invalid-beside-valid-system.png')
    saved_receipt.rename(receipt);saved_receipt=None
    ui.quit(stop=True);ui=None;assert not Path(ipc).exists() and json.loads((data/'service-state.json').read_text())['stopped']
    open_ui();assert '启动服务' in ui.labels() and not Path(ipc).exists();U.wait(lambda:visible('backup-verify-'+id1) and visible('backup-verify-'+id2));scroll('backup-verify-'+id2);ui.press_key('backup-verify-'+id2);U.wait(lambda:'备份文件校验通过 · '+id2 in ui.labels());assert not Path(ipc).exists();ui.system_capture('backup-stopped-catalogue-system.png')
    scroll('backup-create');ready();hits=call('search',datasets=[project],query=text3,limit=10);assert any(h['version']['memory']==memory and h['version']['revision']==3 for h in hits)
    ui.quit(stop=True);ui=None
    report.update(status='passed',first_backup=id1,second_backup=id2,schema=first['schema'],wal_all_tables=golden,replay_original_snapshot=True,checksum_damage_rejected=True,missing_receipt_not_empty=True,offline_catalogue_and_verify=True,current_revision=3,rust_current_preserved=True,service_stopped=True)
except Exception as error:
    report['error']=repr(error);report['traceback']=traceback.format_exc()
    try:ui.system_capture('backup-failure-system.png');report['failure_labels']=ui.labels()
    except Exception:pass
finally:
    errors=[]
    if held:
        try:held.rollback();held.close()
        except Exception as error:errors.append('reader: '+repr(error))
    try:
        if damaged:damaged.write_bytes(original)
        if saved_receipt:saved_receipt.rename(saved_receipt.with_name('receipt.json'))
    except Exception as error:errors.append('IO fixture cleanup: '+repr(error))
    try:call('stop')
    except (FileNotFoundError,ConnectionRefusedError):pass
    except Exception as error:errors.append('stop: '+repr(error))
    if ui:
        try:G.close_partial_ui(ui,f,app,'code')
        except Exception as error:errors.append('ui: '+repr(error))
    try:server.shutdown();server.server_close();thread.join(timeout=5);assert not thread.is_alive()
    except Exception as error:errors.append('source: '+repr(error))
    report['cleanup_errors']=errors
    if errors:report['status']='failed'
    (OUT/'backup-actual-app.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({k:v for k,v in report.items() if k not in ('wal_all_tables','failure_labels')},ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
