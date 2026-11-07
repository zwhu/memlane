#!/usr/bin/env python3
"""实际 App 栏内确认恢复跨 Agent 记忆，保留 v1/v2 并创建 v3。"""
import json, shutil
from pathlib import Path
from importlib.machinery import SourceFileLoader

ROOT = Path(__file__).resolve().parents[1]
d = SourceFileLoader('p2common', str(ROOT/'scripts/p2-common.py')).load_module()
original = json.loads((d.fixture/'real-knowledge.json').read_text())
edited = json.loads((d.fixture/'edited-knowledge.json').read_text())
env = {'PATH': str(Path(shutil.which('codex')).parent)+':/usr/bin:/bin',
       'PI_CODING_AGENT_DIR': str(d.fixture/'pi-agent'),
       'CODEX_HOME': str(Path.home()/'.codex'),
       'MEMLANE_PI_AUTH_DIRECTORY': str(Path.home()/'.pi/agent')}
report = {'status': 'failed', 'actual_app': True, 'synthetic_only': True}
ui = None
try:
    ui = d.UI.UI(d.app, d.fixture, ROOT/'build/p2-screenshots', extra_env=env)
    assert d.call('get', memory=original['memory'])['revision'] == 2
    ui.press('全部记忆')
    d.UI.wait(lambda: any(n.get('key') == 'memory-'+original['memory'] for n in ui.nodes()))
    ui.press_key('memory-'+original['memory'])
    d.UI.wait(lambda: edited['text'] in ui.labels())
    assert original['source_detail']['fragment'] in ui.labels()
    ui.system_capture('system-cross-agent-v2.png')
    ui.press('历史记录')
    d.UI.wait(lambda: '恢复 v1' in ui.labels())
    ui.press('恢复 v1')
    assert d.call('get', memory=original['memory'])['revision'] == 2
    ui.system_capture('system-cross-agent-restore-confirm.png')
    ui.press('确认恢复')
    d.UI.wait(lambda: d.call('get', memory=original['memory'])['revision'] == 3)
    d.UI.wait(lambda: d.call('get', memory=original['memory'])['index_state'] == 'indexed')
    current = d.call('get', memory=original['memory'])
    assert current['text'] == original['text'] and current['source_detail'] == original['source_detail']
    d.UI.wait(lambda: original['text'] in ui.labels())
    assert 'v3 · 恢复版本' in ui.labels()
    ui.system_capture('system-cross-agent-restored.png')
    history = d.call('history', memory=original['memory'])['items']
    assert sorted(h['revision'] for h in history) == [1, 2, 3]
    assert next(h for h in history if h['revision'] == 2)['text'] == edited['text']
    ui.press('总览')
    ui.system_capture('system-p2-home.png')
    (d.fixture/'restored-knowledge.json').write_text(json.dumps(current, ensure_ascii=False, indent=2)+'\n')
    report.update(status='passed', memory=current['memory'], revision=3,
                  confirmation_preserves_v2=True, history_revisions=[1,2,3], source_preserved=True,
                  actual_system_windows=True, home_wide=True)
finally:
    if ui: ui.quit()
    (d.OUT/'cross-agent-app-restore.json').write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n')
print(json.dumps(report, ensure_ascii=False), flush=True)
