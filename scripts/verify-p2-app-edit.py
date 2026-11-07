#!/usr/bin/env python3
"""App 查看真实 Codex 保存，手动编辑当前版本；后续真实 pi 读取。"""
import json,shutil
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT=Path(__file__).resolve().parents[1]
d=SourceFileLoader('p2common',str(ROOT/'scripts/p2-common.py')).load_module()
record=json.loads((d.fixture/'real-knowledge.json').read_text())
out=ROOT/'build/p2-screenshots'
env={'PATH':str(Path(shutil.which('codex')).parent)+':/usr/bin:/bin','PI_CODING_AGENT_DIR':str(d.fixture/'pi-agent'),'CODEX_HOME':str(Path.home()/'.codex'),'MEMLANE_PI_AUTH_DIRECTORY':str(Path.home()/'.pi/agent')}
ui=None;report={'status':'failed','actual_app':True,'synthetic_only':True}
try:
    ui=d.UI.UI(d.app,d.fixture,out,extra_env=env)
    d.UI.wait(lambda:d.call('get',memory=record['memory'])['index_state']=='indexed')
    ui.press('全部记忆');d.UI.wait(lambda:any(n.get('key')=='memory-'+record['memory'] for n in ui.nodes()))
    ui.press_key('memory-'+record['memory'])
    d.UI.wait(lambda:record['text'] in ui.labels())
    assert 'codex' in ui.labels()
    assert 'synthetic://p2/project-a/requestId' in ui.labels()
    assert 'Synthetic experience submitted by Codex' in ui.labels()
    ui.capture('cross-agent-app-view.png')
    ui.press('编辑')
    edited='Memlane P2 App edit: API errors must include requestId and owning project. crossAgentP2EditedToken'
    ui.set('memory-draft',edited);ui.press('保存')
    d.UI.wait(lambda:d.call('get',memory=record['memory'])['text']==edited)
    current=d.call('get',memory=record['memory']);assert current['revision']==2
    assert current['original']==record['text'] and current['source_detail']==record['source_detail']
    d.UI.wait(lambda:edited in ui.labels())
    ui.capture('cross-agent-app-edited.png')
    ui.press('历史记录');d.UI.wait(lambda:'Agent 保存' in ' '.join(ui.labels()))
    assert record['text'] in ui.labels()
    ui.capture('cross-agent-app-history.png')
    (d.fixture/'edited-knowledge.json').write_text(json.dumps(current,ensure_ascii=False,indent=2))
    report.update(status='passed',memory=current['memory'],revision=current['revision'],source_preserved=True,original_preserved=True,manual_app_edit=True)
    ui.quit();ui=None
finally:
    if ui:ui.quit()
    (d.OUT/'cross-agent-app-edit.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(report,ensure_ascii=False),flush=True)
