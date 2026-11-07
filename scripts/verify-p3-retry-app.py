#!/usr/bin/env python3
"""D2b 真实App/traex/Rust与自动恢复；SQL部分失败和service SIGKILL明确故障夹具。"""
import ctypes, datetime, fcntl, hashlib, json, os, signal, socket, sqlite3, subprocess, tempfile, time, uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];OUT=ROOT/'build/p3-recovery-d2b'
f=Path((OUT/'fixture-path.txt').read_text().strip());meta=json.loads((f/'source-info.json').read_text());app=Path(meta['app'])
G=SourceFileLoader('retryguard',str(ROOT/'scripts/p3-c2-common.py')).load_module();G.validate_fixture(f,app,phase='recovery')
U=SourceFileLoader('retryui',str(ROOT/'scripts/p1-ui.py')).load_module();data=f/'client/data'
ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'
def call(op,**kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(8);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline())
    assert r['ok'],r
    return r.get('result')
def visible(key):return any(n.get('key')==key for n in ui.nodes())
def choose(key,label):
    before={n['id'] for n in ui.nodes()};ui.press_key(key)
    def option():
        nodes=ui.nodes();popup={n['id'] for n in nodes if n.get('role')=='popup' and n['id'] not in before}
        return next((n for n in nodes if n.get('label')==label and n.get('parent') in popup),None)
    n=U.wait(option);b=n['bounds'];window=ui.req('windows')['windows'][0]['id']
    ui.req('input',window=window,events=[{'type':'click','x':b['x']+b['width']/2,'y':b['y']+b['height']/2}]);ui.req('idle',timeout=2000)
def next_clock():return (datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(minutes=2)).strftime('%H:%M')
def open_plan():
    ui.press('设置');ui.press_key('schedule-settings');U.wait(lambda:visible('schedule-plan-'+planid));ui.press_key('schedule-plan-'+planid);U.wait(lambda:visible('schedule-edit'))
def reschedule(previous):
    open_plan();ui.press_key('schedule-edit');U.wait(lambda:visible('schedule-local-time'));ui.set('schedule-local-time',next_clock());ui.press_key('schedule-save')
    p=U.wait(lambda:next((p for p in call('schedules') if p['plan']==planid and p['generation']>previous),None));assert p['enabled'] and p['spec']['retry_limit']==1
    return p
def upcoming(p,excluded):
    r=U.wait(lambda:next((r for r in call('schedule_runs',schedule=planid) if r.get('task') and r['task'] not in excluded),None),timeout=155)
    assert r['generation']==p['generation'] and r['due_ms']==p['next_run_ms'];return r
def show_task(tid):
    ui.press('整理记录');ui.press_key('organization-tab-summary');U.wait(lambda:visible('organization-task-'+tid));ui.press_key('organization-task-'+tid);U.wait(lambda:visible('organization-cancel') or tid in ui.labels())
def sql(statement,args=()):
    assert data.resolve().is_relative_to(f.resolve())
    with sqlite3.connect(data/'memlane.sqlite',timeout=5) as db:return db.execute(statement,args).fetchall()
def group_gone(pid):
    try:os.killpg(pid,0)
    except ProcessLookupError:return True
    except PermissionError:return False
    return False
def force_stop_fixture():
    G.validate_fixture(f,app,phase='recovery')
    data.mkdir(parents=True,exist_ok=True);path=data/'service-state.json'
    state=json.loads(path.read_text()) if path.exists() else {'restarts':[]}
    if not state.get('stopped'):
        state['stop_generation']=state.get('stop_generation',0)+1
    if not state.get('stop_generation'):state['stop_generation']=1
    state['stopped']=True;state['last_stopped_ms']=int(time.time()*1000)
    # Persist independently of UI/IPC before cancelling anything. A launchd
    # restart must see stopped even when the current helper cannot answer.
    fd,name=tempfile.mkstemp(prefix='.retry-stop-',dir=data)
    try:
        with os.fdopen(fd,'w') as file:
            json.dump(state,file);file.flush();os.fsync(file.fileno())
        os.replace(name,path)
    finally:
        if Path(name).exists():Path(name).unlink()
    if Path(ipc).exists():
        try:call('stop')
        except (OSError,AssertionError):pass
    lockpath=data/'.service.lock'
    with lockpath.open('a+b') as lock:
        try:fcntl.flock(lock.fileno(),fcntl.LOCK_EX|fcntl.LOCK_NB);return
        except BlockingIOError:pass
        owners=subprocess.run(['/usr/sbin/lsof','-t',str(lockpath)],capture_output=True,text=True).stdout.split()
        lib=ctypes.CDLL('/usr/lib/libproc.dylib')
        for value in owners:
            pid=int(value);buf=ctypes.create_string_buffer(4096)
            if lib.proc_pidpath(pid,buf,len(buf))>0 and Path(buf.value.decode()).resolve()==(app/'Contents/Resources/bin/memlaned').resolve():
                try:os.kill(pid,signal.SIGTERM)
                except ProcessLookupError:pass
        deadline=time.monotonic()+35
        while time.monotonic()<deadline:
            try:fcntl.flock(lock.fileno(),fcntl.LOCK_EX|fcntl.LOCK_NB);return
            except BlockingIOError:time.sleep(.1)
        raise AssertionError('isolated helper did not release service ownership after persistent stop')
def close_partial_ui():
    # Allocation precedes __init__, preserving this launch's unique inspector
    # address even if construction fails after Launch Services opened the App.
    path=getattr(ui,'path',None);name=getattr(ui,'name',None)
    if path and name and Path(path).exists():
        with socket.socket(socket.AF_UNIX) as s:
            s.settimeout(3);s.connect(path)
            s.sendall((json.dumps({'id':999999,'method':'menu','params':{'path':[name,'Quit '+name]}})+'\n').encode())
            s.makefile('rb',buffering=0).readline()
ui=None;injected=False;planid=''
report={'status':'failed','actual_app':True,'real_agent':'traex','real_rust':True,'model_fixture':False,'clock_changed':False,'synthetic_only':True,'fixture':str(f.resolve()),'faults':['SQL tag INSERT failure','actual service SIGKILL'],'cases':[]}
try:
    ui=U.UI.__new__(U.UI)
    ui.__init__(app,f,ROOT/'build/p3-recovery-d2b-screenshots',extra_env={'PI_CODING_AGENT_DIR':str(f/'pi-agent')});U.wait(lambda:call('health')['state']=='ready',timeout=95)
    assert not call('schedules') and not call('organization_list')['items'] and call('list',list_query={'limit':10})['total']==0,'fresh fixture required'
    ui.press('项目');ui.set('project-name','D2b 合成计划项目');ui.set('project-path',str(f/'repo-a'));ui.press_key('register-project')
    project=U.wait(lambda:next((s['id'] for s in call('scopes') if s['name']=='D2b 合成计划项目'),None));sources=[]
    for i,body in enumerate(['本项目使用 pnpm 管理依赖。','依赖变化应提交 pnpm-lock.yaml，并核对持续集成。']):
        sources.append(call('apply',user_confirmed=True,mutation={'kind':'save','operation_id':str(uuid.uuid4()),'memory':str(uuid.uuid4()),'dataset':project,'title':f'D2b 来源 {i+1}','text':body,'source':'合成故障验收材料','source_detail':{'references':[f'synthetic://retry/{i}']}}))
    ui.press('设置');ui.press_key('schedule-settings');ui.press_key('schedule-new');choose('schedule-project','D2b 合成计划项目');choose('schedule-frequency','每日')
    ui.set('schedule-timezone','UTC');ui.set('schedule-local-time',next_clock());ui.set('schedule-window','60');ui.set('schedule-limit','8')
    assert not visible('schedule-retry-count'),'retry must default off'
    ui.press_key('schedule-retry');U.wait(lambda:visible('schedule-retry-count'));ui.set('schedule-retry-count','1');ui.set('schedule-recovery-window','10');ui.system_capture('retry-explicit-policy-system.png');ui.press_key('schedule-save')
    draft=U.wait(lambda:next(iter(call('schedules')),None));planid=draft['plan'];assert not draft['enabled'] and not call('organization_list')['items'];assert draft['spec']['retry_limit']==1 and draft['spec']['recovery_window_minutes']==10
    source_ids=[str(uuid.UUID(v['memory'])) for v in sources];ids=','.join("'"+i+"'" for i in source_ids)
    sql("CREATE TRIGGER d2b_tag_fault BEFORE INSERT ON memory_tags WHEN NEW.memory IN ("+ids+") BEGIN SELECT RAISE(ABORT,'D2b partial SQL fixture'); END");injected=True
    ui.press_key('schedule-toggle');enabled=U.wait(lambda:next((p for p in call('schedules') if p['plan']==planid and p['enabled']),None))
    run=upcoming(enabled,set());tid=run['task']
    partial=U.wait(lambda:(lambda t:t if t['state']=='partial_failed' and t.get('recovery',{}).get('state')=='waiting' else None)(call('organization_get',organization_task=tid)),timeout=150)
    receipts={e['item_id']:e['receipt'] for e in partial['entries'] if e['state']=='applied'};assert receipts and any(e['state']=='failed' for e in partial['entries'])
    sql('DROP TRIGGER d2b_tag_fault');injected=False
    show_task(tid);U.wait(lambda:any('等待有限重试' in label for label in ui.labels()));ui.system_capture('retry-partial-waiting-system.png')
    done=U.wait(lambda:(lambda t:t if t['state']=='completed' and t['attempt']>partial['attempt'] and t.get('recovery',{}).get('state')=='done' else None)(call('organization_get',organization_task=tid)),timeout=90)
    assert done['attempt_kind']=='apply' and done['recovery']['used']==1 and len(done['attempts'])==2 and done['attempts'][1]['kind']=='apply'
    assert all(e['state']=='applied' for e in done['entries']) and all(next(e for e in done['entries'] if e['item_id']==i)['receipt']==r for i,r in receipts.items())
    summary=next(e for e in done['entries'] if e['item']['action']=='summary');memory=summary['receipt']['memory']
    v=U.wait(lambda:(lambda v:v if v['index_state']=='indexed' else None)(call('get',memory=memory)),timeout=95)
    assert any(h['version']['memory']==memory and h['version']['revision']==1 for h in call('search',datasets=[project],query=v['text'],limit=50))
    assert all(call('get',memory=v['memory'])['revision']==1 for v in sources)
    U.wait(lambda:any('未调用模型' in label for label in ui.labels()));ui.system_capture('retry-apply-completed-system.png')
    report['cases'].append({'case':'real_model_partial_sql_then_auto_apply','task':tid,'generation':run['generation'],'due_ms':run['due_ms'],'first_result_preserved':True,'recovery_used':1,'no_model_on_apply':True,'receipts_preserved':True,'real_rust_memory':memory})
    # Real active cancel on a new source revision and another real clock slot.
    ui.press('D2b 合成计划项目');U.wait(lambda:visible('memory-'+sources[0]['memory']));ui.press_key('memory-'+sources[0]['memory']);ui.press('编辑');ui.set('memory-draft',sources[0]['text']+' 新确认约定：依赖更新前记录持续集成结果。');ui.press('保存');U.wait(lambda:call('get',memory=sources[0]['memory'])['revision']==2)
    p=reschedule(enabled['generation']);cancel_run=upcoming(p,{tid});cancelid=cancel_run['task'];show_task(cancelid)
    U.wait(lambda:(lambda t:t['state']=='running' and (t.get('execution') or {}).get('process_active'))(call('organization_get',organization_task=cancelid)),timeout=60);ui.press_key('organization-cancel')
    cancelled=U.wait(lambda:(lambda t:t if t['state']=='cancelled' and (t.get('execution') or {}).get('process_reaped') and (t.get('execution') or {}).get('process_group_gone') else None)(call('organization_get',organization_task=cancelid)),timeout=45)
    assert not cancelled.get('result') and not cancelled['entries'] and cancelled['recovery']['state']=='cancelled';ui.system_capture('retry-real-cancel-system.png')
    report['cases'].append({'case':'real_active_cancel','task':cancelid,'generation':cancel_run['generation'],'due_ms':cancel_run['due_ms'],'no_result':True,'group_gone':True})
    # Same changed material remains unprocessed: a clock edit alone does not
    # repeat the already processed v1 set, and a cancelled no-result v2 permits
    # another authorized slot. Crash this actual helper, not an Agent mock.
    p2=reschedule(p['generation']);crash_run=upcoming(p2,{tid,cancelid});crashid=crash_run['task'];show_task(crashid)
    U.wait(lambda:(lambda t:t['state']=='running' and (t.get('execution') or {}).get('process_active'))(call('organization_get',organization_task=crashid)),timeout=60)
    guard_pid=sql('SELECT process_pid FROM organization_leases WHERE task=? AND attempt=1',(crashid,))[0][0];assert guard_pid>1
    owners=subprocess.run(['/usr/sbin/lsof','-t',str(data/'.service.lock')],capture_output=True,text=True,check=True).stdout.split();assert len(owners)==1
    service_pid=int(owners[0]);lib=ctypes.CDLL('/usr/lib/libproc.dylib');buf=ctypes.create_string_buffer(4096)
    assert lib.proc_pidpath(service_pid,buf,len(buf))>0 and Path(buf.value.decode()).resolve()==(app/'Contents/Resources/bin/memlaned').resolve(),'only this isolated helper may be killed'
    assert service_pid!=guard_pid;os.kill(service_pid,signal.SIGKILL)
    U.wait(lambda:group_gone(guard_pid),timeout=15);U.wait(lambda:call('health')['state']=='ready',timeout=95)
    restored=U.wait(lambda:(lambda t:t if t['state']=='completed' and t['attempt']>1 and t.get('recovery',{}).get('state')=='done' else None)(call('organization_get',organization_task=crashid)),timeout=150)
    assert restored['recovery']['used']==1 and len(restored['attempts'])==2 and all(a['kind']=='model' for a in restored['attempts'])
    assert restored['result'] and restored['result']['attempt']==restored['snapshot']['attempt'];assert sql('SELECT count(*) FROM organization_results WHERE task=?',(crashid,))[0][0]==1
    fresh=next(e for e in restored['entries'] if e['item']['action']=='summary');current=U.wait(lambda:(lambda v:v if v['index_state']=='indexed' else None)(call('get',memory=fresh['receipt']['memory'])),timeout=95)
    assert any(h['version']['memory']==current['memory'] and h['version']['revision']==current['revision'] for h in call('search',datasets=[project],query=current['text'],limit=50))
    assert sql('SELECT group_gone,released FROM organization_leases WHERE task=? AND attempt=1',(crashid,))[0]==(1,1)
    show_task(crashid);ui.system_capture('retry-crash-restored-system.png')
    report['cases'].append({'case':'actual_service_crash_and_real_model_retry','task':crashid,'generation':crash_run['generation'],'due_ms':crash_run['due_ms'],'old_service_pid':service_pid,'old_guard_pid':guard_pid,'old_group_gone_before_recovery':True,'attempt':restored['attempt'],'used':1,'single_result':True,'real_rust_memory':current['memory']})
    open_plan();ui.press_key('schedule-toggle');U.wait(lambda:not next(p for p in call('schedules') if p['plan']==planid)['enabled']);ui.quit(stop=True);ui=None
    assert json.loads((data/'service-state.json').read_text())['stopped'];report.update(status='passed',explicit_retry_consent=True,default_retry_off=True,service_stopped=True)
except Exception as e:report['error']=repr(e);raise
finally:
    cleanup=[]
    try:force_stop_fixture()
    except Exception as e:cleanup.append({'step':'persistent_stop','error':repr(e)})
    if ui:
        try:ui.quit(stop=True)
        except Exception as e:
            cleanup.append({'step':'ui_quit','error':repr(e)})
            try:close_partial_ui()
            except Exception as e:cleanup.append({'step':'partial_ui_quit','error':repr(e)})
    if injected:
        try:sql('DROP TRIGGER IF EXISTS d2b_tag_fault')
        except Exception as e:cleanup.append({'step':'sql_trigger_cleanup','error':repr(e)})
    try:force_stop_fixture()
    except Exception as e:cleanup.append({'step':'final_persistent_stop','error':repr(e)})
    report['cleanup_errors']=cleanup
    if cleanup and report['status']=='passed':report['status']='failed'
    (OUT/'real-app.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
