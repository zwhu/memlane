#!/usr/bin/env python3
"""Explicit SIGKILL service-owner fault with real guarded Enola/Rust, no App claim."""
import hashlib
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import tempfile
import time
import traceback
import uuid

ROOT=Path(__file__).resolve().parents[1]
OUT=ROOT/'build/p4-code'
cache=Path((OUT/'root-app-fixture-path.txt').read_text().strip())
cached=json.loads((cache/'client/service.json').read_text())
f=Path(tempfile.mkdtemp(prefix='memlane-p4-code-owner-'))
repo=f/'repo'
subprocess.run(['/usr/bin/git','init',str(repo)],check=True,capture_output=True)
(repo/'sample.ts').write_text('export function ownerRecovery(){return 3;}\n')
cfg={**cached,'root':str(f/'data'),'worker':{**cached['worker'],'DataDir':str(f/'data/index')}}
(f/'service.json').write_text(json.dumps(cfg))
helper=ROOT/'build/p4-code/helper-latest'
ipc=f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(cfg["root"].encode()).hexdigest()[:12]}/service.sock'
def call(op,**kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(125);s.connect(ipc)
        s.sendall((json.dumps({'protocol':1,'id':str(uuid.uuid4()),'op':op,**kw})+'\n').encode())
        r=json.loads(s.makefile('rb',buffering=0).readline())
    assert r['ok'],r
    return r.get('result')
def wait(fn,timeout=120):
    end=time.monotonic()+timeout
    while time.monotonic()<end:
        try:
            r=fn()
            if r:return r
        except (OSError,AssertionError,StopIteration):pass
        time.sleep(.1)
    raise AssertionError('owner fault deadline')
def group_gone(pid):
    try:os.killpg(pid,0);return False
    except ProcessLookupError:return True
processes=[]
captured_groups=set()
def capture_group(pid):
    # Bind a group while its leader is still a direct child of this fixture owner.
    row=subprocess.check_output(['/bin/ps','-p',str(pid),'-o','ppid=,pgid='],text=True).split()
    assert len(row)==2 and all(v.isdigit() for v in row), 'missing owned process identity'
    assert int(row[0])==service.pid and int(row[1])==pid and pid>1 and pid!=os.getpgrp(), 'unowned process group'
    captured_groups.add(pid)
def cleanup_groups(groups):
    errors=[]
    for pgid in groups:
        try:
            if pgid<=1 or pgid==os.getpgrp():
                errors.append('refused unsafe captured process group');continue
            if group_gone(pgid):continue
            errors.append(f'captured group {pgid} needed forced cleanup')
            os.killpg(pgid,signal.SIGKILL)
            wait(lambda:group_gone(pgid),timeout=5)
        except ProcessLookupError:pass
        except Exception as error:errors.append(f'captured group {pgid}: {error!r}')
    return errors
def start():
    p=subprocess.Popen([str(helper),'--config',str(f/'service.json')],env={'PATH':'/usr/bin:/bin','HOME':str(f/'home')},stdout=(f/'service.log').open('ab'),stderr=subprocess.STDOUT,start_new_session=True)
    processes.append(p);return p
def state():return next(c for c in call('code_list') if c['id']==identifier)
report={'status':'failed','synthetic_only':True,'fault':'SIGKILL actual service owner while Enola generates','actual_app':False,'actual_enola':False,'actual_rust':False,'model_called':False,'root':str(f)}
try:
    service=start();health=wait(lambda:(h if (h:=call('health'))['state']=='ready' else None));report['actual_rust']=True
    project=call('register_project',project={'name':'synthetic code owner fault','path':str(repo)},user_confirmed=True)
    c=call('code_action',code_action={'operation_id':str(uuid.uuid4()),'kind':'enable','project':project['id'],'path':str(repo)},user_confirmed=True)
    identifier=c['id'];baseline=wait(lambda:(c if (c:=state())['state']=='ready' else None))
    report['actual_rust']=True;report['actual_enola']=True
    load=repo/'owner-load.ts';load.write_text(''.join(f'export function ownerLoad{n}(){{return {n};}}\n' for n in range(3500)))
    refreshed=call('code_action',code_action={'operation_id':str(uuid.uuid4()),'kind':'refresh','id':identifier,'expected_generation':baseline['generation']},user_confirmed=True)
    def generating():
        job=next((j for j in call('code_jobs',code_id=identifier) if j['state']=='running'),None)
        if not job:return None
        lines=subprocess.check_output(['/bin/ps','-axo','pid=,ppid=,args='],text=True).splitlines()
        for line in lines:
            parts=line.strip().split(None,2)
            if len(parts)==3 and parts[0].isdigit() and parts[1].isdigit() and int(parts[1])==service.pid and '--proposal-guard' in parts[2] and '--generate' in parts[2] and job['id'] in parts[2]:return job,int(parts[0])
        return None
    job,guard=wait(generating)
    worker=call('health')['worker_pid']
    capture_group(guard);capture_group(worker)
    service.kill();service.wait(timeout=10)
    wait(lambda:group_gone(guard),timeout=20);wait(lambda:group_gone(worker),timeout=20)
    private=f/'data/code-extraction'/job['id'];assert private.is_dir(),'expected real crash residual missing'
    load.unlink()
    service=start();wait(lambda:call('health')['state']=='ready')
    current=wait(lambda:(c if (c:=state())['state']=='ready' else None))
    jobs=call('code_jobs',code_id=identifier)
    ended=next(j for j in jobs if j['id']==job['id']);assert ended['state']=='interrupted'
    assert current['generation']==refreshed['generation'] and current['dataset']==baseline['dataset'] and current['snapshot']==baseline['snapshot']
    assert len([j for j in jobs if j['generation']==refreshed['generation'] and j['state']=='completed'])==1
    proof=call('code_query',code_query={'id':identifier,'mode':'symbols','text':'ownerRecovery'});assert len(proof['graph']['nodes'])==1
    wait(lambda:not private.exists());assert call('health')['code_cleanup_error']==''
    report.update(status='passed',old_guard=guard,old_worker=worker,old_groups_gone=True,old_job=job['id'],interrupted=ended,new_state=current,jobs=jobs,real_crash_residual_reclaimed=True,query=proof)
except Exception as error:report['error']=repr(error);report['traceback']=traceback.format_exc()
finally:
    errors=[]
    try:call('stop')
    except (FileNotFoundError,ConnectionRefusedError):pass
    except Exception as error:errors.append(repr(error))
    for p in processes:
        if p.poll() is None:
            try:p.wait(timeout=15)
            except subprocess.TimeoutExpired:
                errors.append('owner needed forced cleanup')
                try:p.terminate();p.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    try:p.kill();p.wait(timeout=5)
                    except Exception as error:errors.append(f'owner final kill: {error!r}')
                except Exception as error:errors.append(f'owner terminate: {error!r}')
            except Exception as error:errors.append(f'owner wait: {error!r}')
    errors.extend(cleanup_groups(captured_groups))
    report['captured_groups']=sorted(captured_groups)
    report['cleanup_errors']=errors
    if errors:report['status']='failed'
    (OUT/'g2-code-owner-crash.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({k:v for k,v in report.items() if k not in ('jobs','query','new_state','interrupted')},ensure_ascii=False))
if report['status']!='passed':raise SystemExit(1)
