#!/usr/bin/env python3
"""当前完整包：官方Codex采集、真实pi提炼、Native知识到原文与撤权。"""
import argparse
import fcntl
import hashlib
from importlib.machinery import SourceFileLoader
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import traceback
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--pointer', type=Path, required=True)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--continue-from', type=Path, help='同包继续既有真实材料，不重跑编码任务')
    args = parser.parse_args()
    out = args.out.resolve()
    out.mkdir(mode=0o700, parents=True, exist_ok=False)
    fixture = Path(args.pointer.read_text().strip())
    assert json.loads((fixture/'synthetic-only.json').read_text())['synthetic_only']
    meta = json.loads((fixture/'source-info.json').read_text())
    app, runtime = Path(meta['app']), fixture/'runtime-config.json'
    client = fixture/'client'
    ipc = Path('/tmp')/('memlane-'+str(os.getuid())+'-'+hashlib.sha256(str(client/'data').encode()).hexdigest()[:12])/'service.sock'
    U = SourceFileLoader('generated_read_ui', str(ROOT/'scripts/p1-ui.py')).load_module()
    mac = SourceFileLoader('generated_read_mac', str(ROOT/'scripts/mac-native.py')).load_module().Mac()
    report = dict(status='failed', synthetic_only=True, actual_native_app=True, actual_packaged_daemon=True,
                  business_data_copied=False, historical_chats_copied=False, actual_official_capture=False,
                  actual_pi_distillation=False, actual_generated_knowledge_lineage=False,
                  current_processing_dispatch_verified=False, new_c_repository_task_verified=False,
                  legacy_daily_task_preparation_used=True, screenshots=[], steps=[], cleanup_errors=[])
    ui = None
    source_process = None
    clipboard = subprocess.check_output(['/usr/bin/pbpaste'])
    auth_paths = [Path('/Users/bytedance/.pi/agent')/name for name in ('auth.json', 'models.json', 'settings.json')]
    auth_paths += [Path('/Users/bytedance/.codex')/name for name in ('auth.json', 'config.toml')]
    hashes = {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in auth_paths if p.is_file()}

    def step(name):
        report['steps'].append(name)
        print(json.dumps(dict(step=name), ensure_ascii=False), flush=True)

    def call(op, **fields):
        with socket.socket(socket.AF_UNIX) as channel:
            channel.settimeout(30)
            channel.connect(str(ipc))
            channel.sendall((json.dumps(dict(protocol=1, id=str(uuid.uuid4()), op=op, **fields))+'\n').encode())
            response = json.loads(channel.makefile('rb', buffering=0).readline())
        assert response['ok'], response
        return response.get('result')

    def denied(op, **fields):
        try:
            call(op, **fields)
        except AssertionError as error:
            response = error.args[0]
            assert isinstance(response, dict) and response.get('ok') is False, response
            return response['code']
        raise AssertionError('未授权或过期请求被接受')

    def press(key):
        def ready():
            node = ui.node(key=key)
            return node if 'disabled' not in node.get('states', []) else False
        node = U.wait(ready)
        if 'scrollIntoView' in node.get('actions', []):
            ui.req('act', id=node['id'], action='scrollIntoView')
        ui.req('act', id=node['id'], action='press')
        ui.req('capture')

    def set_value(key, value):
        attempts = 0
        def set_when_available():
            nonlocal attempts
            node = ui.node(key=key)
            if node.get('value') == value:
                return True
            if 'disabled' in node.get('states', []):
                return False
            ui.req('act', id=node['id'], action='setValue', text=value)
            attempts += 1
            return ui.node(key=key).get('value') == value
        U.wait(set_when_available)
        report.setdefault('input_receipts', []).append(dict(key=key, value_bytes=len(value.encode()), attempts=attempts))

    def capture(name):
        mac.require_unlocked()
        mac.activate(ui.pid)
        U.wait(lambda: ui.native()['key_window'], timeout=10)
        ui.req('capture')
        mac.capture(ui.pid, out/'screenshots'/name)
        assert ui.native()['key_window'] and ui.native()['visible']
        (out/'screenshots'/(Path(name).stem+'-tree.json')).write_text(json.dumps(ui.nodes(), ensure_ascii=False, indent=2)+'\n')
        report['screenshots'].append(name)

    def model_receipts():
        path = client/'official/model-bridge/events.jsonl'
        return [json.loads(line) for line in path.read_text().splitlines() if line] if path.exists() else []

    def copied(expected):
        sentinel = ('memlane-copy-probe-'+uuid.uuid4().hex).encode()
        subprocess.run(['/usr/bin/pbcopy'], input=sentinel, check=True)
        started = time.monotonic()
        observation = dict(expected_bytes=len(expected.encode()), expected_sha256=hashlib.sha256(expected.encode()).hexdigest())
        observation['disabled_frames_before_press'] = 0
        def copy_when_available():
            node = next(n for n in ui.nodes() if n.get('key') == 'source-reader-copy')
            if 'disabled' in node.get('states', []):
                observation['disabled_frames_before_press'] += 1
                return False
            ui.req('act', id=node['id'], action='press')
            return True
        def received():
            actual = subprocess.check_output(['/usr/bin/pbpaste'])
            observation.update(actual_bytes=len(actual), actual_sha256=hashlib.sha256(actual).hexdigest(),
                               still_sentinel=actual == sentinel, elapsed_seconds=time.monotonic()-started)
            return actual == expected.encode()
        try:
            U.wait(copy_when_available, timeout=45)
            observation['immediate_match'] = received()
            U.wait(received, timeout=10)
        finally:
            report.setdefault('clipboard_observations', []).append(observation)
            (out/'clipboard-observations.json').write_text(json.dumps(report['clipboard_observations'], indent=2)+'\n')

    def wait_reader():
        return U.wait(lambda: any(n.get('key') == 'source-reader-copy' and 'disabled' not in n.get('states', []) for n in ui.nodes()), timeout=45)

    def open_settings():
        if not any(n.get('key') == 'settings-agents' for n in ui.nodes()):
            ui.press('设置')

    try:
        if args.continue_from:
            previous = args.continue_from.resolve()
            prior = json.loads((previous/'report.json').read_text())
            assert prior['actual_official_capture'] and prior['actual_pi_distillation']
            assert prior['socket_absent'] and not prior['cleanup_errors']
            assert (previous.parent/'fixture-path.txt').resolve() == args.pointer.resolve()
            assert not ipc.exists()
            source_root = fixture/('source-continuation-'+uuid.uuid4().hex[:8])
            with (out/'component-source.log').open('w') as log:
                source_process = subprocess.Popen([str(ROOT/'.local/cognee-python/bin/python'),
                    str(ROOT/'scripts/onboarding-component-source.py'), '--root', str(source_root),
                    '--archives', str(fixture/'archives.json')], stdout=log, stderr=log, start_new_session=True)
            U.wait(lambda: (source_root/'source-info.json').exists(), timeout=30)
            source = json.loads((source_root/'source-info.json').read_text())
            config = json.loads(runtime.read_text())
            config.update(component_source=source['source'], component_public_key=source['public_key'])
            config['official'].update(source=source['source'], public_key=source['public_key'])
            runtime = out/'runtime-continuation.json'
            runtime.write_text(json.dumps(config, indent=2)+'\n')
            report.update(continuation_from=str(previous), same_package=True, same_business_root=True,
                          coding_task_rerun=False, resumed_from_stopped_service=True)
        ui = U.UI(app, fixture, out/'screenshots', config=runtime)
        (out/'ui-process.json').write_text(json.dumps(dict(pid=ui.pid, automation=ui.path, owned=True))+'\n')
        if args.continue_from:
            U.wait(lambda: '服务已停止' in ui.labels(), timeout=180)
            open_settings()
            press('settings-environment')
            U.wait(lambda: '启动服务' in ui.labels())
            ui.press('启动服务')
        if args.continue_from:
            U.wait(lambda: ipc.exists() and call('list', list_query=dict(kind='knowledge'))['total'] > 0, timeout=180)
        else:
            U.wait(lambda: '服务运行中' in ui.labels(), timeout=180)
        if args.continue_from:
            initial = json.loads((previous/'initial-generated-materials.json').read_text())
            project_id = initial[0]['knowledge']['dataset']
            project = dict(id=project_id)
            conn = next(v for v in call('connections') if v['project'] == project_id and v['enabled'])
            open_settings()
            press('settings-agents')
            U.wait(lambda: any(n.get('key') == 'connection-'+conn['id'] for n in ui.nodes()))
            press('connection-'+conn['id'])
            press('agent-first-task')
            U.wait(lambda: any(n.get('key') in ('cognee-prepare', 'official-agent-run') for n in ui.nodes()))
            if any(n.get('key') == 'cognee-prepare' for n in ui.nodes()):
                press('cognee-prepare')
                U.wait(lambda: any(n.get('key') in ('official-agent-auth', 'official-agent-run') for n in ui.nodes()), timeout=300)
            knowledge = call('list', list_query=dict(kind='knowledge', datasets=[project_id]))['items']
            raw = call('list', list_query=dict(kind='raw', datasets=[project_id]))['items']
            native = next(v for v in raw if v.get('source_detail', {}).get('native'))
            receipts = [r for r in model_receipts() if r.get('http_status') == 200]
            final_materials = []
            for item in knowledge:
                refs = call('organization_sources', memory=item['memory'], expected_revision=item['revision'])
                sources = [call('source_read', source_read=dict(project=project_id, owner=item['memory'], owner_revision=item['revision'], memory=r['source_memory'], revision=r['source_revision'])) for r in refs]
                assert all(s['state'] == 'available' and s.get('session_original') for s in sources)
                final_materials.append(dict(knowledge=call('get', memory=item['memory']), references=refs, actual_sources=sources))
            (out/'resumed-generated-materials.json').write_text(json.dumps(final_materials, ensure_ascii=False, indent=2)+'\n')
            report.update(actual_official_capture=True, actual_pi_distillation=True,
                          final_knowledge_count=len(knowledge), final_raw_count=len(raw),
                          initial_quality_verdict='requires_itemwise_review')
            step('同包重开既有真实材料与官方服务，未重跑编码任务')
        else:
            assert call('list', list_query=dict(kind='knowledge'))['total'] == 0
            assert call('list', list_query=dict(kind='raw'))['total'] == 0
            project = call('register_project', user_confirmed=True, project=dict(name='本轮真实出处', path=str(fixture/'repo-a')))
            conn = call('configure_connection', user_confirmed=True, connection=dict(agent='codex', name='本轮官方采集', project=project['id'], allowed_scopes=[project['id']]))
            # This is an explicitly scoped synthetic coding task. The official
            # plugin supplies all materials; no knowledge or source rows are seeded.
            (fixture/'repo-a/ticket.py').write_text('def normalize_ticket(text):\n    return text.strip()\n')
            (fixture/'repo-a/test_ticket.py').write_text('''import unittest
from ticket import normalize_ticket
class TicketTests(unittest.TestCase):
    def test_normalize(self):
        self.assertEqual(normalize_ticket("  Source  Note  "), "source-note")
    def test_type(self):
        with self.assertRaises(TypeError): normalize_ticket(42)
''')
            open_settings()
            press('settings-agents')
            U.wait(lambda: any(n.get('key') == 'connection-'+conn['id'] for n in ui.nodes()))
            press('connection-'+conn['id'])
            press('agent-first-task')
            U.wait(lambda: any(n.get('key') == 'cognee-prepare' for n in ui.nodes()))
            press('cognee-prepare')

            def prepared():
                state_path = client/'official-onboarding.json'
                if state_path.exists():
                    state = json.loads(state_path.read_text())
                    phase = state.get('Phase', state.get('phase'))
                    assert phase not in ('failed', 'cancelled'), state
                return any(n.get('key') == 'official-agent-auth' for n in ui.nodes())
            U.wait(prepared, timeout=300)
            ui.set('official-agent-executable', '/Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex')
            ui.set('official-agent-auth', '/Users/bytedance/.codex')
            press('official-agent-prepare')
            U.wait(lambda: any(n.get('key') == 'official-agent-task' for n in ui.nodes()), timeout=100)
            before_model = sum(r.get('http_status') == 200 for r in model_receipts())
            step('空业务根与真实官方环境准备完成')
            prompt = '请修复本轮合成仓库ticket.py的normalize_ticket。先实际运行python3 -m unittest，复现已有测试失败，再读源码和测试并修复：只接受str，其他类型抛TypeError；去掉两端空白，连续空白变成短横线，英文字母转小写。重跑已有两项测试，不削弱断言。项目明确约定：发布前执行python3 -m unittest。最后中文报告实际失败与修复结果。仅授权官方Cognee插件采集这次正常修复会话与工具轨迹，不手工调用接口注入知识，不访问其他项目、历史会话或验收目录。'
            ui.set('official-agent-task', prompt)
            press('official-agent-run')

            def completed():
                messages = ui.labels()
                failures = [label for label in messages if label.startswith(('任务未完成', '官方同步或授权未完成', '同步未完成'))]
                assert not failures, failures
                if not any(n.get('key') == 'first-task-memory-counts' for n in ui.nodes()):
                    return False
                knowledge_page = call('list', list_query=dict(kind='knowledge', datasets=[project['id']]))
                raw_page = call('list', list_query=dict(kind='raw', datasets=[project['id']]))
                return knowledge_page['total'] > 0 and any(v.get('source_detail', {}).get('native') for v in raw_page['items'])
            U.wait(completed, timeout=900)
            knowledge = call('list', list_query=dict(kind='knowledge', datasets=[project['id']]))['items']
            raw = call('list', list_query=dict(kind='raw', datasets=[project['id']]))['items']
            assert knowledge and raw
            native = next(v for v in raw if v.get('source_detail', {}).get('native'))
            receipts = [r for r in model_receipts() if r.get('http_status') == 200]
            assert len(receipts) > before_model
            assert all(r.get('native_schema_requested') and r.get('native_schema_response_status') == 200 and r.get('terminal') and r.get('reaped') and r.get('group_gone') and r.get('rpc_resolved_provider') == 'deepseek' and r.get('rpc_resolved_model') == 'deepseek-flash' for r in receipts)
            initial = []
            for item in knowledge:
                refs = call('organization_sources', memory=item['memory'], expected_revision=item['revision'])
                assert refs
                sources = [call('source_read', source_read=dict(project=project['id'], owner=item['memory'], owner_revision=item['revision'], memory=r['source_memory'], revision=r['source_revision'])) for r in refs]
                assert all(s['state'] == 'available' and s['record']['text'] for s in sources)
                initial.append(dict(knowledge=call('get', memory=item['memory']), references=refs, actual_sources=sources))
            (out/'initial-generated-materials.json').write_text(json.dumps(initial, ensure_ascii=False, indent=2)+'\n')
            (out/'model-receipts.json').write_text(json.dumps(receipts, ensure_ascii=False, indent=2)+'\n')
            report.update(actual_official_capture=True, actual_pi_distillation=True, initial_quality_verdict='requires_itemwise_review')
            step('官方Codex采集与真实pi提炼已产生知识及独立原文')
        target = initial[0]['knowledge']
        ref = next(r for r in initial[0]['references'] if r['snapshot']['origin'] == 'raw')
        U.wait(lambda: all(call('get', memory=v['memory'])['index_state'] == 'indexed' for v in knowledge), timeout=120)
        ui.press_key('project-open-'+project['id'])
        U.wait(lambda: any(n.get('key') == 'memory-'+target['memory'] for n in ui.nodes()))
        press('memory-'+target['memory'])
        source_key = 'knowledge-source-'+ref['source_memory']
        U.wait(lambda: any(n.get('key') == source_key for n in ui.nodes()))
        capture('01-generated-knowledge-system.png')
        press(source_key)
        wait_reader()
        assert any(n.get('key') == 'memory-'+target['memory'] for n in ui.nodes())
        copied(native['text'])
        capture('02-generated-original-right-pane-system.png')
        report['actual_generated_knowledge_lineage'] = True
        report['full_original_copy_sha256'] = hashlib.sha256(native['text'].encode()).hexdigest()
        set_value('source-reader-search', 'python3 -m unittest')
        press('source-reader-find')
        U.wait(lambda: any('处匹配' in label and not label.startswith('0 ') for label in ui.labels()))
        press('source-reader-match')
        capture('03-original-search-system.png')
        press('source-reader-close')
        U.wait(lambda: ui.req('tree').get('focus') == ui.node(key=source_key)['id'])
        ui.press('编辑')
        amended = '用户修订：本项目发布前执行python3 -m unittest；原始会话仅为历史依据。'
        ui.set('memory-draft', amended)
        ui.press('保存')
        U.wait(lambda: call('get', memory=target['memory'])['revision'] == 2 and call('get', memory=target['memory'])['index_state'] == 'indexed', timeout=120)
        U.wait(lambda: any(n.get('key') == source_key for n in ui.nodes()))
        press(source_key)
        wait_reader()
        copied(native['text'])
        capture('04-edited-knowledge-keeps-original-system.png')
        report['native_user_edit_keeps_actual_original'] = True
        newer = '本轮受控并发修订，验证旧阅读回执不可继续使用。'
        call('apply', user_confirmed=True, mutation=dict(operation_id=str(uuid.uuid4()), kind='edit', dataset=project['id'], memory=target['memory'], expected_revision=2, title=target['title'], text=newer))
        U.wait(lambda: any('所选知识已更新' in label for label in ui.labels()), timeout=45)
        assert 'disabled' in ui.node(key='source-reader-copy').get('states', [])
        assert not any(n.get('key') == 'source-reader-original' for n in ui.nodes())
        capture('05-owner-changed-reader-stops-system.png')
        press('source-reader-close')
        U.wait(lambda: newer in ui.labels())
        press(source_key)
        wait_reader()
        query = dict(project=project['id'], owner=target['memory'], owner_revision=3, memory=ref['source_memory'], revision=ref['source_revision'])
        other = call('register_project', user_confirmed=True, project=dict(name='本轮隔离项目', path=str(fixture/'repo-b')))
        report['cross_project_denied'] = denied('source_read', source_read={**query, 'project': other['id']})
        call('apply', user_confirmed=True, mutation=dict(operation_id=str(uuid.uuid4()), kind='edit', dataset=project['id'], memory=ref['source_memory'], expected_revision=ref['source_revision'], title=ref['snapshot']['title'], text='本轮受控来源变更，引用的旧版本必须停止读取。'))
        U.wait(lambda: any('来源身份保留' in label for label in ui.labels()), timeout=45)
        assert 'disabled' in ui.node(key='source-reader-copy').get('states', [])
        capture('06-source-changed-reader-stops-system.png')
        call('disconnect', user_confirmed=True, connection_id=conn['id'])
        report['revoked_source_denied'] = denied('source_read', source_read=query)
        U.wait(lambda: any('原文读取未完成' in label for label in ui.labels()), timeout=45)
        assert not any(n.get('key') == 'source-reader-original' for n in ui.nodes())
        capture('07-revoked-source-right-pane-system.png')
        report['source_change_and_revocation_stop_disclosure'] = True
        report['status'] = 'passed'
        step('Native原文、编辑、版本变化与撤权主线通过')
    except Exception as error:
        report['failure'], report['failure_traceback'] = repr(error), traceback.format_exc()
        if ui is not None:
            try:
                (out/'failure-tree.json').write_text(json.dumps(ui.req('tree'), ensure_ascii=False, indent=2)+'\n')
                capture('failure-system.png')
            except Exception as capture_error:
                report['failure_capture_error'] = repr(capture_error)
    finally:
        if ui is not None:
            try:
                ui.quit(stop=True)
            except Exception as error:
                report['cleanup_errors'].append(repr(error))
        if ipc.exists() or ui is None:
            try:
                log = out/'fallback-stop.log'
                result = subprocess.run(['/usr/bin/open', '-n', '-W', '--stdout', str(log), '--stderr', str(log), str(app), '--args', '--p0-config', str(runtime), '--p0-action', 'stop'], capture_output=True, timeout=45)
                assert result.returncode == 0
            except Exception as error:
                report['cleanup_errors'].append('fallback stop: '+repr(error))
        if source_process is not None:
            try:
                os.killpg(source_process.pid, signal.SIGTERM)
                source_process.wait(timeout=15)
            except Exception as error:
                report['cleanup_errors'].append('continuation source stop: '+repr(error))
        try:
            pid = meta['component_source_pid']
            command = subprocess.run(['ps', '-p', str(pid), '-o', 'command='], capture_output=True, text=True).stdout
            if command.strip():
                assert str(fixture/'source') in command and os.getpgid(pid) == pid, command
                os.killpg(pid, signal.SIGTERM)
        except Exception as error:
            report['cleanup_errors'].append('source stop: '+repr(error))
        subprocess.run(['/usr/bin/pbcopy'], input=clipboard, check=True)
        report['original_agent_config_hashes_unchanged'] = all(p.is_file() and hashlib.sha256(p.read_bytes()).hexdigest() == digest for path, digest in hashes.items() if (p := Path(path)))
        report['socket_absent'] = not ipc.exists()
        try:
            report['locks_released'] = {}
            for name, path in dict(service=client/'data/.service.lock', worker=client/'data/index/.worker.lock').items():
                if path.exists():
                    with path.open('rb') as handle:
                        fcntl.flock(handle.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                        fcntl.flock(handle.fileno(), fcntl.LOCK_UN)
                report['locks_released'][name] = True
        except Exception as error:
            report['cleanup_errors'].append('lock verification: '+repr(error))
        report['remaining_fixture_processes'] = [line for line in subprocess.check_output(['ps', '-axo', 'pid,ppid,pgid,command'], text=True).splitlines() if str(fixture) in line]
        if report['remaining_fixture_processes']:
            report['cleanup_errors'].append('隔离根仍有进程')
        report['app_file_bytes'] = sum(p.lstat().st_size for p in app.rglob('*') if not p.is_dir())
        report['component_download_requirement_bytes'] = sum(c['bytes'] for c in meta['source']['actual_components'])
        allocated = subprocess.run(['/usr/bin/du', '-sk', str(app), str(client)], capture_output=True, text=True)
        report['allocated_disk_bytes'] = sum(int(line.split()[0]) for line in allocated.stdout.splitlines())*1024
        if report['cleanup_errors'] or not report['socket_absent'] or not report['original_agent_config_hashes_unchanged']:
            report['status'] = 'failed'
        (out/'report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n')
        print(json.dumps(dict(status=report['status'], failure=report.get('failure'), report=str(out/'report.json')), ensure_ascii=False), flush=True)
        if report['status'] != 'passed':
            raise SystemExit(1)


if __name__ == '__main__':
    main()
