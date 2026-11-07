#!/usr/bin/env python3
"""Capture final current-data home and detail in the real P1 App."""
import hashlib,json,os,socket
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1];driver=SourceFileLoader('p1ui',str(ROOT/'scripts/p1-ui.py')).load_module()
root=Path((ROOT/'build/p1/fixture-path.txt').read_text().strip());cfg=json.loads((root/'runtime-config.json').read_text());data=Path(cfg['root'])/'data';record=json.loads((root/'management-fixture.json').read_text())
ipc=f"/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock"
def call(op,**fields):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(5);s.connect(ipc);s.sendall((json.dumps({'protocol':1,'id':'capture-'+op,'op':op,**fields})+'\n').encode());r=json.loads(s.makefile('rb',buffering=0).readline())
    assert r['ok'];return r.get('result')
ui=driver.UI(ROOT/'build/p1-app/darwin-arm64/MemlaneP1.app',root,ROOT/'build/p1-screenshots')
try:
    ui.press('设置');driver.wait(lambda:'启动服务' in ui.labels() or '停止服务' in ui.labels(),timeout=30)
    if '启动服务' in ui.labels():ui.press('启动服务')
    driver.wait(lambda:call('health')['state']=='ready',timeout=65)
    total=call('activities',timezone='Asia/Shanghai')['total'];ui.press('总览');driver.wait(lambda:f'过去一年新增 {total} 条记忆' in ui.labels());ui.capture('final-real-home.png');ui.system_capture('system-final-real-home.png')
    project=next(s for s in call('scopes') if s['id']==record['project']);ui.press(project['name']);driver.wait(lambda:record['main_title'] in ui.labels());ui.press(record['main_title']);driver.wait(lambda:record['main_original'] in ui.labels());assert '已提交索引重试，正文和历史保留。' not in ui.labels();assert call('get',memory=record['main_memory'])['revision']==3;ui.capture('final-real-detail.png');ui.system_capture('system-final-real-detail.png')
    ui.press('历史记录');driver.wait(lambda:'恢复 v2' in ui.labels());ui.capture('final-real-history.png')
    result={'status':'passed','actual_home_total':total,'actual_restored_revision':3,'actual_history_visible':True,'selection_message_scoped':True,'system_screenshots':['system-final-real-home.png','system-final-real-detail.png']};(ROOT/'build/p1/final-window-verification.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result))
finally:ui.quit(stop=True)
