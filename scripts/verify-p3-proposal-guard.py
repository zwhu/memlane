#!/usr/bin/env python3
"""实际 helper 管道/进程崩溃夹具；不调用模型，不读取业务库。"""
import json, os, signal, subprocess, sys, tempfile, time
from pathlib import Path
ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'build/p3-recovery'
helper = OUT / 'memlaned'
assert helper.is_file()
root = Path(tempfile.mkdtemp(prefix='memlane-p3-recovery-guard-'))
report = {'status':'failed', 'actual_helper':True, 'process_fault_fixture':True, 'model_called':False, 'database_opened':False}
def wait(fn, seconds=8):
    deadline=time.monotonic()+seconds
    while time.monotonic()<deadline:
        if fn(): return
        time.sleep(.02)
    raise AssertionError('guard fixture deadline')
def gone(pgid):
    try: os.killpg(pgid,0)
    except ProcessLookupError: return True
    except PermissionError: return False
    return False
processes=[]
try:
    # The helper waits for a persisted-owner permit before running any command.
    r,w=os.pipe(); marker=root/'unpermitted'
    with (root/'unpermitted.log').open('w') as log:
        p=subprocess.Popen([str(helper),'--proposal-guard','/usr/bin/touch',str(marker)],stdin=subprocess.DEVNULL,stdout=log,stderr=log,pass_fds=(r,),preexec_fn=lambda: os.dup2(r,3),start_new_session=True)
    processes.append(p); os.close(r)
    time.sleep(.2); assert p.poll() is None and not marker.exists()
    os.close(w); assert p.wait(timeout=5)==2 and not marker.exists()
    # The Agent must not inherit the private control FD. Normal exit also
    # verifies that guard cleanup does not kill its own successful process.
    fdcheck=root/'fdcheck.py'
    fdcheck.write_text('import os\ntry: os.fstat(3)\nexcept OSError: pass\nelse: raise AssertionError("control FD inherited")\n')
    r,w=os.pipe()
    p=subprocess.Popen([str(helper),'--proposal-guard',sys.executable,str(fdcheck)],stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE,pass_fds=(r,),preexec_fn=lambda:os.dup2(r,3),start_new_session=True)
    processes.append(p);os.close(r);os.write(w,b'S')
    assert p.wait(timeout=5)==0,p.stderr.read().decode()
    os.close(w)
    # Child owns the sole control writer. Killing that owner closes the pipe;
    # the helper terminates its own group including the live synthetic command.
    runner=root/'owner.py'
    runner.write_text('''import os,subprocess,sys,time,json
r,w=os.pipe()
p=subprocess.Popen([sys.argv[1],'--proposal-guard','/bin/sh','-c','touch "$1"; sleep 30 & wait','guard-fixture',sys.argv[2]],stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,pass_fds=(r,),preexec_fn=lambda: os.dup2(r,3),start_new_session=True)
os.close(r);os.write(w,b'S')
print(json.dumps({'guard':p.pid}),flush=True)
time.sleep(30)
''')
    marker=root/'active'
    owner=subprocess.Popen([sys.executable,str(runner),str(helper),str(marker)],stdout=subprocess.PIPE,text=True)
    processes.append(owner); guard=json.loads(owner.stdout.readline())['guard']
    report.update(root=str(root),guard_pid=guard)
    wait(marker.exists); assert not gone(guard)
    owner.kill(); owner.wait(timeout=5); wait(lambda:gone(guard))
    report.update(status='passed', unpermitted_command_not_started=True, control_fd_not_inherited=True, normal_guard_exit=True, actual_owner_sigkill=True, active_group_gone_on_eof=True, root=str(root))
except Exception as e:
    report['error']=repr(e); raise
finally:
    for p in processes:
        if p.poll() is None: p.kill(); p.wait(timeout=5)
    (OUT/'guard-crash.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False))
