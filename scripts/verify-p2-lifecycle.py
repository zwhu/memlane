#!/usr/bin/env python3
"""真实 Codex 幂等重放、两个 Agent 停服错误、App 显式恢复与持久性。"""
import argparse, json, shutil, subprocess
from pathlib import Path
from importlib.machinery import SourceFileLoader

ROOT = Path(__file__).resolve().parents[1]
d = SourceFileLoader('p2common', str(ROOT/'scripts/p2-common.py')).load_module()
report = {'status': 'failed', 'synthetic_only': True, 'actual_agents': True, 'checks': []}
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--resume-after-restart', action='store_true', help='保留前三组真实回执，从已明确启动的服务继续')
flags = parser.parse_args()
if flags.resume_after_restart:
    previous = json.loads((d.OUT/'real-lifecycle-verification.json').read_text())
    assert [c['name'] for c in previous['checks']] == ['real-Codex-replay','codex-real-stopped-error','pi-real-stopped-error']
    assert all(c['status'] == 'passed' for c in previous['checks'])
    shutil.copy2(d.OUT/'real-lifecycle-verification.json', d.OUT/'lifecycle-last-seen-assertion-failure.json')
    report['checks'] = previous['checks']
ui = None
pairs = {}
env = {'PATH': str(Path(shutil.which('codex')).parent)+':/usr/bin:/bin',
       'PI_CODING_AGENT_DIR': str(d.fixture/'pi-agent'), 'CODEX_HOME': str(Path.home()/'.codex'),
       'MEMLANE_PI_AUTH_DIRECTORY': str(Path.home()/'.pi/agent')}

def adapter(action, **fields):
    result = subprocess.run([str(d.OUT/'agent-check')], input=json.dumps({'action': action, **fields}),
                            text=True, capture_output=True)
    return json.loads(result.stdout)

def check(name, **evidence):
    report['checks'].append({'name': name, 'status': 'passed', 'evidence': evidence})
    (d.OUT/'real-lifecycle-verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n')
    print(json.dumps({'name': name, 'status': 'passed',
                      'evidence': {k:v for k,v in evidence.items() if k != 'execution'}}, ensure_ascii=False), flush=True)

def invoke(agent, tool, arguments):
    connection, options = pairs[agent]
    prompt = ('这是 Memlane 合成验收，只调用一次 '+tool+'，参数严格使用下列 JSON。'
              '不要读取文件或执行命令。服务不可用时显示实际工具错误，不将失败描述为成功或空结果：'+
              json.dumps(arguments, ensure_ascii=False))
    run = {'Prompt': prompt, 'Workspace': str(d.fixture/'repo-a'),
           'CodexAuthHome': str(Path.home()/'.codex'), 'PiAuthDirectory': str(Path.home()/'.pi/agent')}
    result = adapter('run', connection=connection, options=options, run=run)
    assert result['ok'], result
    calls = [c for c in result['result']['calls'] if c['tool'] == tool]
    assert calls, result
    return calls[0]['response'], result['result']

try:
    original = json.loads((d.fixture/'real-knowledge.json').read_text())
    current = d.call('get', memory=original['memory'])
    assert current['revision'] == 3
    history = d.call('history', memory=original['memory'])['items']
    initial_connections = json.loads((d.fixture/'agent-connections.json').read_text())
    discovered = {a['agent']: a for a in adapter('discover', home=str(Path.home()))['result']}
    global_id = next(s['id'] for s in d.call('scopes') if s['kind'] == 'experience')
    for agent in ['codex', 'pi']:
        # Reuse the original save connection ID, so the same stable operation
        # resolves to the original memory rather than a newly scoped operation.
        selected = initial_connections[agent]
        if flags.resume_after_restart:
            i = json.loads((Path(d.cfg['root'])/'agent-adapters'/selected['id']/'installation.json').read_text())
            pairs[agent] = (i['connection'], i['options'])
            continue
        c = d.call('configure_connection', user_confirmed=True, connection={
            'id': selected['id'], 'agent': agent, 'name': 'P2 重启与错误 '+agent,
            'project': original['dataset'] if agent == 'codex' else '',
            'allowed_scopes': [original['dataset'], global_id]})
        options = {'ClientRoot': d.cfg['root'], 'ServiceRoot': str(d.data),
                   'Bridge': str(d.app/'Contents/Resources/bin/memlane'),
                   'AgentPath': discovered[agent]['path'], 'Workspace': str(d.fixture/'repo-a'),
                   'PiDirectory': str(d.fixture/'pi-agent')}
        installed = adapter('install', connection=c, options=options)
        assert installed['ok'], installed
        pairs[agent] = (c, options)
    if not flags.resume_after_restart:
        saved_event = json.loads((d.OUT/'codex-real-save.json').read_text())['execution']['calls'][0]
        response, execution = invoke('codex', 'memory_save', saved_event['arguments'])
        receipt = response['result']
        assert response['ok'] and receipt['persisted'] and receipt['saved_revision'] == 1
        assert receipt['record']['memory'] == original['memory'] and receipt['record']['revision'] == 3
        assert d.call('history', memory=original['memory'])['items'] == history
        check('real-Codex-replay', same_memory=original['memory'], saved_revision=1, current_revision=3,
              history_unchanged=True, execution=execution)
    ui = d.UI.UI(d.app, d.fixture, ROOT/'build/p2-screenshots', extra_env=env)
    ui.press('设置')
    d.UI.wait(lambda: '停止服务' in ui.labels())
    if not flags.resume_after_restart:
        ui.press('停止服务')
        d.UI.wait(lambda: '启动服务' in ui.labels())
        d.UI.wait(lambda: not Path(d.ipc).exists())
        assert json.loads((d.data/'service-state.json').read_text())['stopped']
        ui.system_capture('system-p2-service-stopped.png')
        for agent in ['codex', 'pi']:
            response, execution = invoke(agent, 'memory_search', {'query': 'crossAgentP2OriginalToken'})
            assert not response['ok'] and response['error']['code'] == 'service_unavailable', response
            assert 'result' not in response
            assert not Path(d.ipc).exists()
            check(agent+'-real-stopped-error', error=response['error'], no_empty_success=True,
                  no_automatic_service_start=True, execution=execution)
        ui.press('启动服务')
    d.UI.wait(lambda: d.call('health')['state'] == 'ready', timeout=90)
    actual = d.call('get', memory=original['memory'])
    assert actual['revision'] == 3 and actual['text'] == current['text']
    assert d.call('history', memory=original['memory'])['items'] == history
    persisted = d.call('connections')
    for c, _ in pairs.values():
        after = next(p for p in persisted if p['id'] == c['id'])
        # Tool calls intentionally update last_seen. Connection identity,
        # generation, binding and grants are the persistent configuration.
        assert {k:v for k,v in after.items() if k != 'last_seen'} == {k:v for k,v in c.items() if k != 'last_seen'}
    check('App-explicit-service-restart', memory_revision=3, history_unchanged=True,
          connections_and_generations_unchanged=True)
    response, execution = invoke('pi', 'memory_get', {'memory_id': original['memory']})
    assert response['ok'] and response['result']['revision'] == 3
    assert response['result']['text'] == current['text'] and response['result']['source_detail'] == current['source_detail']
    check('real-pi-after-service-restart', current_revision=3, source_preserved=True, execution=execution)
    ui.press('全部记忆')
    d.UI.wait(lambda: any(n.get('key') == 'memory-'+original['memory'] for n in ui.nodes()))
    ui.press_key('memory-'+original['memory'])
    d.UI.wait(lambda: current['text'] in ui.labels())
    ui.system_capture('system-p2-restarted-current.png')
    report['status'] = 'passed'
finally:
    if ui: ui.quit()
    # Retain the explicit stop on failure; never revive a stopped service merely
    # to perform cleanup. On success disconnect only these validation adapters.
    if report['status'] == 'passed':
        for connection, options in pairs.values():
            d.call('disconnect', connection_id=connection['id'], user_confirmed=True)
            result = adapter('uninstall', connection=connection, options=options)
            if not result['ok']:
                report['status'] = 'failed'
                report['cleanup_error'] = result.get('error')
                (d.OUT/'real-lifecycle-verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n')
                raise AssertionError(result)
    (d.OUT/'real-lifecycle-verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n')
print(json.dumps({'status': report['status'], 'checks': len(report['checks'])}), flush=True)
