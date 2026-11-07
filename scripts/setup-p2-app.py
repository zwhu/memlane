#!/usr/bin/env python3
"""实际 App 明确启动隔离服务并登记合成项目，供真实 Agent 验收。"""
import json
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1]
d=SourceFileLoader('p2common',str(ROOT/'scripts/p2-common.py')).load_module()
ui=None
try:
    ui=d.UI.UI(d.app,d.fixture,ROOT/'build/p2-screenshots')
    ui.press('设置')
    d.UI.wait(lambda:'启动服务' in ui.labels() or '停止服务' in ui.labels(),timeout=90)
    if '启动服务' in ui.labels():ui.press('启动服务')
    d.UI.wait(lambda:d.call('health')['state']=='ready',timeout=90)
    scopes={}
    for letter in ['a','b']:
        name='P2 合成项目 '+letter.upper()
        ui.press('项目');ui.set('project-name',name);ui.set('project-path',str(d.fixture/('repo-'+letter)));ui.press_key('register-project')
        d.UI.wait(lambda:any(s['name']==name for s in d.call('scopes')))
        scopes[letter]=next(s for s in d.call('scopes') if s['name']==name)
    (d.fixture/'projects.json').write_text(json.dumps(scopes,ensure_ascii=False,indent=2))
    ui.quit();ui=None
    (d.OUT/'app-setup.json').write_text(json.dumps({'status':'passed','actual_app_project_registration':True,'service':d.call('health'),'synthetic_only':True},indent=2))
    print(json.dumps({'status':'passed','projects_registered':2,'service_left_running_for_real_agent':True}),flush=True)
finally:
    if ui:ui.quit(stop=True)
