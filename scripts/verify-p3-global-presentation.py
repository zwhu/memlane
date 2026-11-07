#!/usr/bin/env python3
"""E 修订包真实窗口导航、重启持久化和来源变化补验，不运行模型。"""
import hashlib, json, os, socket, uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader
ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'build/p3-global'
f = Path((OUT / 'fixture-path.txt').read_text().strip())
meta = json.loads((f / 'source-info.json').read_text())
app = Path(meta['app'])
G = SourceFileLoader('globalpresentguard', str(ROOT / 'scripts/p3-c2-common.py')).load_module()
G.validate_fixture(f, app, phase='global')
main = json.loads((OUT / 'real-app.json').read_text())
pi = json.loads((OUT / 'pi-real-read.json').read_text())
assert main['status'] == pi['status'] == 'passed' and main['fixture'] == pi['fixture'] == str(f.resolve())
root = f / 'client/data'
assert json.loads((root / 'service-state.json').read_text())['stopped']
U = SourceFileLoader('globalpresentui', str(ROOT / 'scripts/p1-ui.py')).load_module()
ipc = f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(root).encode()).hexdigest()[:12]}/service.sock'
def call(op, **kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(8); s.connect(ipc)
        s.sendall((json.dumps({'protocol': 1, 'id': str(uuid.uuid4()), 'op': op, **kw}) + '\n').encode())
        r = json.loads(s.makefile('rb', buffering=0).readline())
    assert r['ok'], r
    return r.get('result')
ui = None
report = {'status': 'failed', 'actual_app': True, 'actual_system_window': True, 'model_called': False, 'fixture': str(f.resolve()), 'identifier': meta['identifier']}
try:
    ui = U.UI(app, f, ROOT / 'build/p3-global-screenshots', extra_env={'PI_CODING_AGENT_DIR': str(f / 'pi-agent')})
    ui.press('设置'); U.wait(lambda: '启动服务' in ui.labels()); ui.press('启动服务')
    U.wait(lambda: call('health')['state'] == 'ready', timeout=95)
    case = next(c for c in main['cases'] if c['action'] == 'distill')
    ui.press('整理记录'); ui.press_key('organization-tab-summary')
    U.wait(lambda: any(n.get('key') == 'organization-task-' + case['task'] for n in ui.nodes()))
    ui.press_key('organization-task-' + case['task'])
    U.wait(lambda: any(n.get('key') == 'organization-open-' + case['item'] for n in ui.nodes()))
    ui.press_key('organization-open-' + case['item'])
    U.wait(lambda: any('v3 · 恢复版本' in label for label in ui.labels()))
    assert sum(label == '全局记忆' for label in ui.labels()) >= 3, 'global output must navigate to global page'
    v = call('get', memory=case['receipt']['memory'])
    sources = call('organization_sources', memory=v['memory'], expected_revision=v['revision'])
    assert len(sources) == 1 and sources[0]['source_changed'] and sources[0]['current_revision'] == 2 and sources[0]['source_revision'] == 1
    assert any('来源已变化 · 当前 v2' in label for label in ui.labels())
    assert v['experience']['conditions'] in ui.labels()
    ui.system_capture('final-global-page-system.png')
    hits = call('search', datasets=[v['dataset']], query=v['text'], limit=50)
    assert any(h['version']['memory'] == v['memory'] and h['version']['revision'] == 3 for h in hits)
    assert call('organization_get', organization_task=case['task'])['entries'][0]['receipt'] == case['receipt']
    ui.quit(stop=True); ui = None
    report.update(status='passed', global_page_route=True, restored_conditions_persisted=True, source_change_visible=True, frozen_source_revision=1, current_source_revision=2, rust_current_revision=3, original_receipt_preserved=True, service_stopped=True)
except Exception as e:
    report['error'] = repr(e); raise
finally:
    if ui: ui.quit(stop=True)
    (OUT / 'presentation.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
print(json.dumps(report, ensure_ascii=False))
