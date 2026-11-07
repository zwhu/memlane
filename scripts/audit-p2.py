#!/usr/bin/env python3
"""以真实回执、系统截图、已停止合成库和当前完整包逐项审计 P2。"""
import fcntl, hashlib, json, os, plistlib, sqlite3, subprocess
from pathlib import Path
from importlib.machinery import SourceFileLoader

ROOT = Path(__file__).resolve().parents[1]
d = SourceFileLoader('p2common', str(ROOT/'scripts/p2-common.py')).load_module()
report = {'status': 'failed', 'artifacts': [], 'requirements': []}

def artifact(path):
    p = ROOT/path
    assert p.is_file(), path
    report['artifacts'].append({'path': path, 'sha256': hashlib.sha256(p.read_bytes()).hexdigest()})
    return p

def evidence(name):
    p = 'build/p2/'+name+'.json'
    r = json.loads(artifact(p).read_text())
    assert r['status'] == 'passed', (p, r)
    return r

try:
    contract = evidence('contract-verification')
    assert len(contract['checks']) == 10 and not contract['real_agent_verified']
    adapters = evidence('adapter-verification')
    assert len(adapters['checks']) == 6
    regression = json.loads(artifact('build/p2/p1-regression/service-verification.json').read_text())
    assert regression['status'] == 'passed' and len(regression['checks']) == 6
    saved = evidence('codex-real-save')
    call = next(c for c in saved['execution']['calls'] if c['tool'] == 'memory_save')
    assert call['response']['ok'] and call['response']['result']['persisted']
    memory = call['response']['result']['record']['memory']
    assert call['response']['result']['saved_revision'] == 1
    edit = evidence('cross-agent-app-edit')
    assert edit['manual_app_edit'] and edit['revision'] == 2 and edit['source_preserved']
    pi = evidence('pi-real-read')
    get = next(c for c in pi['execution']['calls'] if c['tool'] == 'memory_get')['response']['result']
    assert get['memory'] == memory and get['revision'] == 2 and get['source'] == 'codex'
    assert get['source_detail']['references'] == ['synthetic://p2/project-a/requestId']
    scope = evidence('real-scope-verification')
    assert scope['actual_agent_processes'] and len(scope['checks']) == 7
    ui = evidence('agent-ui-verification')
    assert ui['visual_complete'] and len(ui['checks']) == 9
    assert not next(c for c in ui['checks'] if c['name'] == 'actual-agent-verification-state')['evidence']['failure']
    restored = evidence('cross-agent-app-restore')
    assert restored['revision'] == 3 and restored['history_revisions'] == [1,2,3]
    pi3 = evidence('pi-real-restored-read')
    assert next(c for c in pi3['execution']['calls'] if c['tool'] == 'memory_get')['response']['result']['revision'] == 3
    lifecycle = evidence('real-lifecycle-verification')
    assert len(lifecycle['checks']) == 5
    visual = evidence('final-window-verification')
    assert visual['design_images_actually_viewed'] and visual['actual_system_windows']
    for filename in visual['system_screenshots']:
        artifact('build/p2-screenshots/'+filename)
        meta = json.loads(artifact('build/p2-screenshots/'+Path(filename).stem+'-capture.json').read_text())
        assert meta['actual_system_window'] and meta['window_key'] and meta['visible']
    checks = json.loads(artifact('build/p2/final-checks.json').read_text())
    assert len(checks) == 2 and all(c['exit_code'] == 0 for c in checks)
    for c in checks: artifact(c['log'])
    assert json.loads((d.data/'service-state.json').read_text())['stopped']
    assert not Path(d.ipc).exists()
    with (d.data/'.service.lock').open('r+b') as owner:
        fcntl.flock(owner, fcntl.LOCK_EX|fcntl.LOCK_NB)
        wal = d.data/'memlane.sqlite-wal'
        assert not wal.exists() or wal.stat().st_size == 0
        with sqlite3.connect((d.data/'memlane.sqlite').as_uri()+'?mode=ro&immutable=1', uri=True) as db:
            assert db.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
            assert db.execute('PRAGMA user_version').fetchone()[0] == 2
            assert db.execute('SELECT current_revision,indexed_revision,state FROM memories WHERE id=?',(memory,)).fetchone() == (3,3,'active')
            versions = db.execute('SELECT revision,text,operation FROM versions WHERE memory=? ORDER BY revision',(memory,)).fetchall()
            assert [v[0] for v in versions] == [1,2,3]
            assert versions[0][1] == versions[2][1] and versions[1][1] != versions[2][1]
            assert versions[2][2] == 'restore'
            source, detail = db.execute('SELECT source,source_detail FROM memory_metadata WHERE memory=?',(memory,)).fetchone()
            assert source == 'codex' and json.loads(detail)['references'] == ['synthetic://p2/project-a/requestId']
            assert db.execute('SELECT count(*) FROM activities WHERE memory=?',(memory,)).fetchone()[0] == 1
            assert db.execute('SELECT count(*) FROM connections WHERE enabled=1').fetchone()[0] == 0
            report['database'] = {'schema':2, 'integrity':'ok', 'memory':memory, 'current_revision':3,
                                  'indexed_revision':3, 'history':[1,2,3], 'source':'codex',
                                  'one_creation_event':True, 'validation_connections_disconnected':True, 'owner_stopped':True}
    sizes = json.loads(artifact('build/p2/final-sizes.json').read_text())
    dmg = artifact('build/p0-final/darwin-arm64/memlane-0.0.1-arm64.dmg')
    assert dmg.stat().st_size == sizes['dmg_bytes'] and hashlib.sha256(dmg.read_bytes()).hexdigest() == sizes['dmg_sha256']
    app = ROOT/'build/p0-final/darwin-arm64/Memlane.app'
    plist = plistlib.loads((app/'Contents/Library/LaunchAgents/io.github.zwhu.memlane.service.plist').read_bytes())
    assert (app/plist['BundleProgram']).is_file()
    assert (app/'Contents/Resources/bin/memlane').is_file()
    assert subprocess.run(['codesign','--verify','--deep','--strict',str(app)],capture_output=True).returncode == 0
    assert sum(p.stat().st_size for p in app.rglob('*') if p.is_file() and not p.is_symlink()) == sizes['app_file_bytes']
    for path in app.rglob('*'):
        assert path.name not in ['model.onnx','tokenizer.json','memlane-cognee-worker','runtime-config.json']
    assert sizes['first_preparation_download_bytes'] == 285752286 and sizes['prepared_install_disk_bytes'] > sizes['app_file_bytes']
    for path in ['docs/images/home.jpg','docs/images/memory-detail.jpg','docs/images/p2-codex.png',
                 'docs/images/p2-pi.png','docs/images/p2-home.png','docs/images/p2-detail.png']:
        artifact(path)
    requirements = [
        ('统一 MCP/CLI、持久化与版本契约','contract-verification'),
        ('配置发现、增量保留和撤销','adapter-verification'),
        ('真实 Codex 保存 v1','codex-real-save'), ('真实 App 查看编辑 v2','cross-agent-app-edit'),
        ('真实 pi 检索 v2 来源与版本','pi-real-read'), ('真实 A/B、全局授权与 worktree','real-scope-verification'),
        ('App Codex/pi 连接验证断开与重开','agent-ui-verification'), ('App 确认恢复新增 v3','cross-agent-app-restore'),
        ('真实 pi 恢复后检索 v3','pi-real-restored-read'), ('真实幂等停服错误与重启持久性','real-lifecycle-verification'),
        ('实际系统窗口对照设计','final-window-verification'), ('完整主包与三类体积','final-sizes')]
    report['requirements'] = [{'name': name, 'evidence': 'build/p2/'+path+'.json', 'status':'proven'} for name,path in requirements]
    report['sizes'] = sizes
    report['commit'] = subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip()
    report['status'] = 'passed'
    print(json.dumps({'status':'passed','requirements':len(requirements),'current_revision':3,
                      'dmg_bytes':sizes['dmg_bytes'],'synthetic_service_stopped':True}, ensure_ascii=False))
except Exception as error:
    report['error'] = repr(error)
    raise
finally:
    (d.OUT/'final-audit.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
