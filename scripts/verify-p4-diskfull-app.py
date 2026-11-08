#!/usr/bin/env python3
"""Actual App/Rust failures on a bounded APFS data volume and recovery."""
import argparse,contextlib,errno,functools,hashlib,http.server,json,os,plistlib,socket,sqlite3,subprocess,threading,traceback,uuid
from importlib.machinery import SourceFileLoader
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--pointer',type=Path,default=ROOT/'build/p4-delivery/diskfull-fixture-path.txt');parser.add_argument('--out',type=Path,default=ROOT/'build/p4-delivery/diskfull-actual-app.json');parser.add_argument('--resume',type=Path,help='Continue a detached failed run after its filler was released');parser.add_argument('--draft-bytes',type=int,default=50000);parser.add_argument('--finish-short',action='store_true',help='Preserve the accepted large v2 and save a short v3 for the current search closure');args=parser.parse_args();assert 10000<=args.draft_bytes<=750000;assert not args.finish_short or args.resume
assert not args.out.exists() and not args.out.is_symlink(),'each attempt requires an independent report path'
f=Path(args.pointer.read_text().strip()).resolve();meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app']);data=f/'client/data';image=f/'explicit-diskfull.dmg';filler=data/'explicit-diskfull-filler.bin'
G=SourceFileLoader('diskfullguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,'code');U=SourceFileLoader('diskfullui',str(ROOT/'scripts/p1-ui.py')).load_module();ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
# Config may use /var while the guard canonicalizes /private/var. Match the
# actual service root string, as serviceipc.SocketPath does.
runtime=json.loads((f/'runtime-config.json').read_text());wire_root=Path(runtime['root'])/'data';ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(wire_root).encode()).hexdigest()[:12]}/service.sock'
def response(op,**kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(125);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());return json.loads(s.makefile('rb',buffering=0).readline())
def call(op,**kw):
    r=response(op,**kw);assert r['ok'],r;return r.get('result')
def visible(key):return any(n.get('key')==key for n in ui.nodes())
def scroll(key):
    n=U.wait(lambda:next((n for n in ui.nodes() if n.get('key')==key),None))
    if 'scrollIntoView' in n['actions']:ui.req('act',id=n['id'],action='scrollIntoView');ui.req('idle',timeout=1500)
def backups():return [json.loads(p.read_text()) for p in (data/'backups/manual').glob('*/receipt.json')]
class H(http.server.SimpleHTTPRequestHandler):
    def log_message(self,*args):pass
server=http.server.ThreadingHTTPServer(('127.0.0.1',meta['source_port']),functools.partial(H,directory=meta['source_root']));thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start();ui=None;device=None;filler_owned=False
C=SourceFileLoader('diskfullcommon',str(ROOT/'scripts/p4-diskfull-common.py')).load_module();evidence=C.Evidence(args.out);manifest_file=C.Evidence(f/(args.out.stem+'-manifest.json'));owned=C.OwnedImage(image,data)
report={'status':'failed','actual_app':False,'actual_rust':False,'synthetic_only':True,'model_called':False,'formal_distribution':False,'image':str(image),'mountpoint':str(data),'fixture':str(f),'fault':'actual ENOSPC on 128MiB APFS image, components outside data volume'}
try:
    assert image.is_file() and not image.is_symlink() and not (data/'memlane.sqlite').exists(),'fresh data image required'
    if (f/'diskfull-manifest.json').exists():
        manifest=json.loads((f/'diskfull-manifest.json').read_text());assert manifest['image']==str(image) and manifest['mountpoint']==str(data) and manifest['size_limit_bytes']==128*1024*1024
    previous=None
    if args.resume:
        previous=json.loads(args.resume.read_text());assert previous['fixture']==str(f) and previous['actual_enospc'] and previous['detached'] and previous['cleanup_errors']==[] and previous['free_after_release']>previous['free_full'],'completed cleanup required for continuation'
        report['continued_from']=str(args.resume.resolve());report['prior_fault']= {k:previous[k] for k in ('actual_enospc','filler_bytes','free_full','free_after_release','backup_under_full_count')}
    device=owned.attach();report['device']=device;os.chmod(data,0o700);manifest_file.write(report)
    ui=U.UI.__new__(U.UI);U.UI.__init__(ui,app,f,ROOT/'build'/('p4-'+args.out.stem+'-screenshots'));report['actual_app']=True;report['screenshots']=str(ui.out)
    ui.press('设置');U.wait(lambda:'启动服务' in ui.labels() or '停止服务' in ui.labels(),timeout=180)
    if '启动服务' in ui.labels():ui.press('启动服务')
    U.wait(lambda:call('health')['state']=='ready',timeout=180)
    if previous:
        project=next(s['id'] for s in call('scopes') if s['name']=='空间不足合成项目');v=next(v for v in call('list',list_query={'datasets':[project],'limit':10})['items'] if v['title']=='空间故障原文');memory=v['memory'];assert v['revision']==2 or (args.finish_short and v['revision']==3);first=next(r for r in backups() if r['id']==previous['baseline_backup']);report['baseline_backup']=first['id'];report['actual_rust']=True
    else:
        ui.press('项目');ui.set('project-name','空间不足合成项目');ui.set('project-path',str(f/'repo-a'));ui.press_key('register-project');project=U.wait(lambda:next((s['id'] for s in call('scopes') if s['name']=='空间不足合成项目'),None));ui.press('空间不足合成项目')
    if not previous:
        ui.press('新增记忆');ui.set('memory-title','空间故障原文');ui.set('memory-draft','本项目使用 pnpm；空间故障不能破坏当前原文与来源。');ui.press('保存');v=U.wait(lambda:next((v for v in call('list',list_query={'datasets':[project],'limit':10})['items'] if v['title']=='空间故障原文'),None));memory=v['memory'];U.wait(lambda:call('get',memory=memory)['index_state']=='indexed',timeout=120);assert call('search',datasets=[project],query=v['text'],limit=10);report['actual_rust']=True
        ui.press('设置');scroll('backup-create');ui.press_key('backup-create');first=U.wait(lambda:next(iter(backups()),None),timeout=90);report['baseline_backup']=first['id']
        ui.press('空间不足合成项目');ui.press_key('memory-'+memory);ui.press('编辑');text='本项目确认保留来源与当前版本。 '+'pnpm '*(args.draft_bytes//5);ui.set('memory-draft',text);report['draft_bytes']=len(text.encode());report['draft_sha256']=hashlib.sha256(text.encode()).hexdigest()
        before=os.statvfs(data);report['free_before']=before.f_bavail*before.f_frsize;errors=[]
        with filler.open('xb',buffering=0) as out:
            filler_owned=True
            for size in [1024*1024,4096]:
                while True:
                    assert out.tell()<=128*1024*1024,'data volume limit not enforced'
                    try:out.write(b'\x00'*size);os.fsync(out.fileno())
                    except OSError as e:assert e.errno==errno.ENOSPC,(e.errno,str(e));errors.append({'errno':e.errno,'size':size,'message':str(e)});break
        report['actual_enospc']=errors;report['filler_bytes']=filler.stat().st_size;s=os.statvfs(data);report['free_full']=s.f_bavail*s.f_frsize
        ui.press('保存');U.wait(lambda:any('保存失败' in t for t in ui.labels()) or not visible('memory-draft'),timeout=30)
        save_state=response('get',memory=memory);report['save_ui_labels']=[t for t in ui.labels() if '保存' in t];report['save_under_full']={**save_state}
        if save_state['ok']:report['save_under_full']['result']={k:value for k,value in save_state['result'].items() if k not in ('text','original')}
        if save_state['ok']:
            current=save_state['result'];assert current['revision'] in [1,2]
            if current['revision']==1:
                assert current['text']==v['text'],'failed write changed original'
                assert any('保存失败' in t for t in ui.labels()) and visible('memory-draft'),'failed save lost draft or showed success'
                assert ui.node(key='memory-draft')['value']==text,'failed save changed original draft'
                report['failed_draft_preserved']=True
        ui.system_capture('actual-full-save-system.png')
        ui.press('设置');scroll('backup-create');ui.press_key('backup-create');U.wait(lambda:any('备份提交失败' in t for t in ui.labels()) or len(backups())>1,timeout=90);report['backup_under_full_count']=len(backups());report['backup_ui_labels']=[t for t in ui.labels() if '备份' in t];ui.system_capture('actual-full-backup-system.png')
        assert len(backups())==1,'disk-full backup unexpectedly published; exact failure needs separate proof'
        # A separately identified real private IPC request exposes the OS error;
        # the App operation above remains the actual UI failure acceptance.
        probe=response('backup_create',user_confirmed=True,backup_id=str(uuid.uuid4()));report['backup_full_backend_probe']=probe;assert not probe['ok'] and 'no space left on device' in probe['error'].lower(),probe
        filler.unlink();filler_owned=False;U.wait(lambda:os.statvfs(data).f_bavail*os.statvfs(data).f_frsize>report['free_full'],timeout=15);s=os.statvfs(data);report['free_after_release']=s.f_bavail*s.f_frsize
    text=v['text'] if previous else text
    assert call('health')['state']=='ready';current=call('get',memory=memory)
    ui.press('空间不足合成项目');U.wait(lambda:visible('memory-'+memory));ui.press_key('memory-'+memory)
    if current['revision']==1:
        if not visible('memory-draft'):ui.press('编辑');U.wait(lambda:visible('memory-draft'))
        assert ui.node(key='memory-draft')['value']==text,'retained draft lost after navigation'
        ui.press('保存')
    current=U.wait(lambda:(x if (x:=call('get',memory=memory))['revision'] in ([2,3] if args.finish_short else [2]) else None),timeout=30);final_revision=current['revision']
    if args.finish_short:
        if current['revision']==2:
            large_text=current['text'];long_key='read-current-'+memory+'2'
        else:
            large_text=next(h['text'] for h in call('history',memory=memory)['items'] if h['revision']==2);ui.press('历史记录');long_key='read-history-'+memory+'-2'
        assert hashlib.sha256(large_text.encode()).hexdigest()==previous['draft_sha256'];report['large_v2_preserved_sha256']=previous['draft_sha256'];report['large_index_acceptance']=False
        U.wait(lambda:visible(long_key+'-next'));scroll(long_key+'-next');ui.press_key(long_key+'-next');U.wait(lambda:any('第 2 /' in t for t in ui.labels()));ui.press_key(long_key+'-copy');copied=subprocess.run(['/usr/bin/pbpaste'],capture_output=True,check=True).stdout;assert hashlib.sha256(copied).hexdigest()==previous['draft_sha256'],'actual clipboard lost full large text';report['actual_long_text_paging']=True;report['actual_complete_clipboard']=True;ui.system_capture('long-v2-readable-system.png')
        if current['revision']==2:
            ui.press('编辑');U.wait(lambda:visible('memory-draft'));text='本项目确认保留来源与当前版本。使用 pnpm，空间释放后保留完整来源与失败编辑记录。';ui.set('memory-draft',text);ui.press('保存');final_revision=3;current=U.wait(lambda:(x if (x:=call('get',memory=memory))['revision']==3 else None),timeout=30)
        else:ui.press('返回详情')
    if current['index_state']=='failed':U.wait(lambda:'重试索引' in ui.labels());ui.press('重试索引')
    U.wait(lambda:call('get',memory=memory)['index_state']=='indexed',timeout=180 if args.finish_short else 120);hits=call('search',datasets=[project],query='本项目确认保留来源与当前版本 pnpm',limit=10);report['recovered_search']=[{'memory':h['version']['memory'],'revision':h['version']['revision']} for h in hits];assert any(h['version']['memory']==memory and h['version']['revision']==final_revision for h in hits),'current version missing from Rust search';assert all(h['version']['revision']==final_revision for h in hits if h['version']['memory']==memory),'old version returned';history=call('history',memory=memory)['items'];report['history_revisions']=[h['revision'] for h in history];assert [h['revision'] for h in history]==list(range(final_revision,0,-1)),history
    ui.press('设置');scroll('backup-create');before_backup_ids={r['id'] for r in backups()};ui.press_key('backup-create');second=U.wait(lambda:next((r for r in backups() if r['id'] not in before_backup_ids),None),timeout=90);U.wait(lambda:'一致性备份已完成 · '+second['id'] in ui.labels(),timeout=90);report['recovered_backup']=second['id'];report['new_backup_ids_before']=sorted(before_backup_ids);ui.system_capture('disk-released-recovered-system.png')
    assert current['text']==text and current['original']=='本项目使用 pnpm；空间故障不能破坏当前原文与来源。' and history[-1]['text']==current['original'];assert [h['operation'] for h in history]==['edit']*(final_revision-1)+['save'];report['source_and_history_preserved']=True
    if args.finish_short:assert hashlib.sha256(history[1]['text'].encode()).hexdigest()==previous['draft_sha256']
    assert hashlib.sha256((data/'backups/manual'/first['id']/'snapshot.sqlite').read_bytes()).hexdigest()==first['sha256'],'old backup changed'
    ui.quit(stop=True);ui=None;assert json.loads((data/'service-state.json').read_text())['stopped'] and not Path(ipc).exists()
    with contextlib.closing(sqlite3.connect((data/'memlane.sqlite').as_uri()+'?mode=ro&immutable=1',uri=True)) as db:
        assert db.execute('PRAGMA integrity_check').fetchone()[0]=='ok' and db.execute('PRAGMA foreign_key_check').fetchall()==[];report['sqlite_integrity']='ok'
    report.update(status='passed',current_revision=final_revision,current_indexed=True,history_versions=len(history),service_stopped=True)
except Exception as e:report['error']=repr(e);report['traceback']=traceback.format_exc()
finally:
    cleanup=[]
    try:
        if filler_owned and filler.exists():filler.unlink()
    except Exception as e:cleanup.append('filler:'+repr(e))
    try:
        if owned.attempted and ui is not None:call('stop')
    except (FileNotFoundError,ConnectionRefusedError):pass
    except Exception as e:cleanup.append('stop:'+repr(e))
    if ui:
        try:G.close_partial_ui(ui,f,app,'code')
        except Exception as e:cleanup.append('ui:'+repr(e))
    try:owned.detach();report['detached']=True
    except Exception as e:cleanup.append('detach:'+repr(e))
    try:server.shutdown();server.server_close();thread.join(timeout=5);assert not thread.is_alive()
    except Exception as e:cleanup.append('source:'+repr(e))
    report['detach_retries']=owned.detach_retries
    report['cleanup_errors']=cleanup
    if cleanup:report['status']='failed'
    evidence.write(report);evidence.close();manifest_file.close()
print(json.dumps({k:v for k,v in report.items() if k not in ['traceback','save_under_full']},ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
