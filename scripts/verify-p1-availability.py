#!/usr/bin/env python3
"""Check existing index vs unavailable worker in the real App, without GUI focus."""
import hashlib,json,os,signal,socket
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];driver=SourceFileLoader('p1ui',str(ROOT/'scripts/p1-ui.py')).load_module()
root=Path((ROOT/'build/p1/fixture-path.txt').read_text().strip());cfg=json.loads((root/'runtime-config.json').read_text());data=Path(cfg['root'])/'data';main=json.loads((root/'management-fixture.json').read_text())
ipc=f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock"
def call(op,**fields):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(5);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':'availability-'+op,'op':op,**fields})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline())
    assert r['ok'],r;return r.get('result')
ui=None;disabled=None;worker=None;report={'status':'failed','system_visual_acceptance':False}
try:
    ui=driver.UI(ROOT/'build/p1-app/darwin-arm64/MemlaneP1.app',root,ROOT/'build/p1-screenshots');ui.press('设置');driver.wait(lambda:'启动服务' in ui.labels() or '停止服务' in ui.labels(),timeout=30)
    if '启动服务' in ui.labels():ui.press('启动服务')
    driver.wait(lambda:call('health')['state']=='ready',timeout=65)
    project=next(s for s in call('scopes') if s['id']==main['project']);ui.press(project['name']);driver.wait(lambda:main['main_title'] in ui.labels());ui.press(main['main_title']);driver.wait(lambda:'可检索' in ui.labels())
    worker=Path(json.loads((Path(cfg['root'])/'service.json').read_text())['worker']['Executable']);assert str(worker).startswith(str(Path(cfg['root'])/'runtime'))
    disabled=worker.with_name(worker.name+'.p1-disabled');assert not disabled.exists();worker.rename(disabled);os.kill(call('health')['worker_pid'],signal.SIGKILL);driver.wait(lambda:call('health')['state']=='failed',timeout=30)
    assert call('get',memory=main['main_memory'])['index_state']=='indexed';driver.wait(lambda:'索引已完成 · 等待服务恢复' in ui.labels());assert '可检索' not in ui.labels();ui.capture('indexed-worker-unavailable-renderer.png')
    disabled.rename(worker);disabled=None;call('retry');driver.wait(lambda:call('health')['state']=='ready',timeout=65);driver.wait(lambda:'可检索' in ui.labels())
    assert call('get',memory=main['main_memory'])['revision']==3;assert call('activities',timezone='Asia/Shanghai')['total']==5
    report.update(status='passed',existing_index_retained=True,worker_unavailability_displayed=True,actual_worker_recovery=True,current_revision_unchanged=3,activity_unchanged=5,system_visual_acceptance=False);print(json.dumps(report))
finally:
    if disabled and disabled.exists():disabled.rename(worker)
    if ui and ui.proc.poll() is None:ui.quit(stop=True)
    (ROOT/'build/p1/availability-verification.json').write_text(json.dumps(report,indent=2)+'\n')
