#!/usr/bin/env python3
"""E 主流程之后真实 pi Extension 全局读取，配置和数据均隔离。"""
import hashlib, json, os, socket, subprocess, time, uuid
from pathlib import Path
from importlib.machinery import SourceFileLoader

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'build/p3-global'
f = Path((OUT / 'fixture-path.txt').read_text().strip())
meta = json.loads((f / 'source-info.json').read_text())
app = Path(meta['app'])
G = SourceFileLoader('globalagentguard', str(ROOT / 'scripts/p3-c2-common.py')).load_module()
cfg = G.validate_fixture(f, app, phase='global')
main = json.loads((OUT / 'real-app.json').read_text())
assert main['status'] == 'passed' and main['fixture'] == str(f.resolve()) and main['identifier'] == meta['identifier']
target = json.loads((f / 'global-read-target.json').read_text())
U = SourceFileLoader('globalagentui', str(ROOT / 'scripts/p1-ui.py')).load_module()
data = f / 'client/data'
ipc = f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(data).encode()).hexdigest()[:12]}/service.sock'

def call(op, **kw):
    with socket.socket(socket.AF_UNIX) as s:
        s.settimeout(8)
        s.connect(ipc)
        s.sendall((json.dumps({'protocol': 1, 'id': str(uuid.uuid4()), 'op': op, **kw}) + '\n').encode())
        r = json.loads(s.makefile('rb', buffering=0).readline())
    assert r['ok'], r
    return r.get('result')

def adapter(action, **kw):
    proc = subprocess.run([str(OUT / 'agent-check')], input=json.dumps({'action': action, **kw}), text=True, capture_output=True, timeout=180)
    r = json.loads(proc.stdout)
    assert r['ok'], r
    return r['result']

ui = None
report = {'status': 'failed', 'actual_installed_agent': True, 'agent': 'pi', 'synthetic_only': True, 'isolated_config': True, 'authentication_owned_by_agent': True, 'fixture': str(f.resolve()), 'identifier': meta['identifier']}
try:
    ui = U.UI(app, f, ROOT / 'build/p3-global-screenshots', extra_env={'PI_CODING_AGENT_DIR': str(f / 'pi-agent')})
    ui.press('设置')
    U.wait(lambda: '启动服务' in ui.labels())
    ui.press('启动服务')
    U.wait(lambda: call('health')['state'] == 'ready', timeout=95)
    found = next(x for x in adapter('discover', home=str(Path.home())) if x['agent'] == 'pi')
    assert found.get('path') and not found.get('error'), found
    connection = next(x for x in call('connections') if x['id'] == target['connection']['id'])
    assert connection['enabled'] and connection['generation'] == target['connection']['generation']
    options = {'ClientRoot': cfg['root'], 'ServiceRoot': str(data), 'Bridge': str(app / 'Contents/Resources/bin/memlane'), 'AgentPath': found['path'], 'Workspace': str(f / 'repo-b'), 'PiDirectory': str(f / 'pi-agent')}
    settings = f / 'pi-agent/settings.json'
    if not settings.exists():
        settings.write_text(json.dumps({'synthetic_user_option': 'preserve-this-field'}))
    assert json.loads(settings.read_text())['synthetic_user_option'] == 'preserve-this-field'
    installed = adapter('install', connection=connection, options=options)
    assert Path(installed['config_path']).resolve().is_relative_to(f.resolve())
    assert json.loads(settings.read_text())['synthetic_user_option'] == 'preserve-this-field'
    memory = call('get', memory=target['memory']['memory'])
    prompt = ('这是Memlane隔离合成验收。只调用Memlane工具，不读取文件、不执行命令、不修改记忆。先memory_search，query=' + json.dumps(memory['text'], ensure_ascii=False) + '，scopes=[' + json.dumps(target['global']) + ']；再memory_get，memory_id=' + json.dumps(memory['memory']) + '，scope=' + json.dumps(target['global']) + '。报告当前正文、版本、适用条件、来源标识及项目明确约定优先。最后memory_get读取memory_id=' + json.dumps(target['source']['memory']) + '，scope=' + json.dumps(target['global']) + '，该来源属于未授权项目，应得到scope_denied并保留错误回执。不得绕过授权。')
    began = time.monotonic()
    execution = adapter('run', connection=connection, options=options, run={'Prompt': prompt, 'Workspace': str(f / 'repo-b'), 'PiAuthDirectory': str(Path.home() / '.pi/agent')})

    report['execution'] = execution
    calls = execution['calls']
    get = next(c['response']['result'] for c in calls if c['tool'] == 'memory_get' and c['response']['ok'])
    search = next(c['response']['result'] for c in calls if c['tool'] == 'memory_search' and c['response']['ok'])
    assert get['memory'] == memory['memory'] and get['revision'] == 3 and get['text'] == memory['text']
    assert get['experience']['conditions'] == memory['experience']['conditions']
    assert any(h['version']['memory'] == memory['memory'] and h['version']['revision'] == 3 for h in search['hits'])
    denied = next(c for c in calls if c['tool'] == 'memory_get' and not c['response']['ok'] and c.get('arguments', {}).get('memory_id') == target['source']['memory'] and c.get('arguments', {}).get('scope') == target['global'])
    assert denied['response']['error']['code'] == 'scope_denied', denied
    assert 'GLOBAL_PRIVATE_ORIGIN_MARKER' not in json.dumps(execution)
    report.update(status='passed', version=found['version'], seconds=round(time.monotonic() - began, 3), execution=execution, real_global_read=True, real_scope_denied=True, model_fixture=False)
    adapter('uninstall', connection=connection, options=options)
    assert json.loads(settings.read_text())['synthetic_user_option'] == 'preserve-this-field'
    report['isolated_disconnect_preserved_config'] = True
    ui.quit(stop=True)
    ui = None
    report['service_stopped'] = True
except Exception as e:
    report['error'] = repr(e)
    raise
finally:
    if ui:
        ui.quit(stop=True)
    (OUT / 'pi-real-read.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
print(json.dumps({k: v for k, v in report.items() if k != 'execution'}, ensure_ascii=False))
