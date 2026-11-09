#!/usr/bin/env python3
"""新隔离包验证真实知识版本、Native历史弹框与独立原文阅读。"""
import argparse
import base64
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import threading
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--components', type=Path, required=True)
    args = parser.parse_args()
    out, previous = args.out.resolve(), args.components.resolve()
    out.mkdir(mode=0o700, parents=True, exist_ok=False)
    prior = json.loads((previous / 'report.json').read_text())
    assert prior['status'] == 'passed' and prior['socket_absent'] and all(prior['locks_released'].values())
    client = out / 'client'
    client.mkdir(mode=0o700)
    shutil.copytree(previous / 'client/runtime/components', client / 'runtime/components', symlinks=True)
    source = json.loads((previous / 'component-source.json').read_text())['payload']
    descriptors = [d for d in source['components'] if d['id'] in ('cognee-rust', 'multilingual-e5-base-int8')]
    assert len(descriptors) == 2
    report = dict(status='failed', synthetic_only=True, actual_native_app=True,
                  actual_packaged_daemon=True, user_data_copied=False, agent_configs_copied=False,
                  initial_configuration_fixture=True, model_inference_verified=False,
                  native_generated_knowledge_lineage_verified=False, screenshots=[], cleanup_errors=[])
    ui = None
    signed = b''
    requests = []

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_GET(self):
            requests.append(self.path)
            if self.path != '/components.json':
                self.send_error(404)
                return
            self.send_response(200)
            self.send_header('Content-Length', str(len(signed)))
            self.end_headers()
            self.wfile.write(signed)

    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    app = out / 'app/darwin-arm64/MemlaneReading.app'
    runtime = out / 'runtime-config.json'
    ipc = Path('/tmp') / ('memlane-' + str(os.getuid()) + '-' + hashlib.sha256(str(client/'data').encode()).hexdigest()[:12]) / 'service.sock'
    try:
        base = f'http://127.0.0.1:{server.server_port}'
        for d in descriptors:
            d['url'] = base + '/' + d['id'] + '.tar.gz'
        payload = json.dumps(dict(version=1, components=descriptors), separators=(',', ':')).encode()
        signing = '''import sys,json,base64
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives import serialization
k=Ed25519PrivateKey.generate();print(json.dumps({'signature':base64.b64encode(k.sign(sys.stdin.buffer.read())).decode(),'public_key':base64.b64encode(k.public_key().public_bytes(serialization.Encoding.Raw,serialization.PublicFormat.Raw)).decode()}))'''
        keys = json.loads(subprocess.run([str(ROOT / '.local/cognee-python/bin/python'), '-c', signing],
                                        input=payload, capture_output=True, check=True).stdout)
        signed = b'{"payload":' + payload + b',"signature":' + json.dumps(keys['signature']).encode() + b'}'
        (out / 'component-source.json').write_bytes(signed)
        identifier = 'io.github.zwhu.memlane.reading.' + uuid.uuid4().hex[:12]
        config = dict(root=str(client), component_source=base+'/components.json', component_public_key=keys['public_key'],
                      worker_id='cognee-rust', model_id='multilingual-e5-base-int8',
                      service_plist=identifier+'.service.plist', allow_local_test_source=True)
        runtime.write_text(json.dumps(config, indent=2)+'\n')
        # This bypasses setup presentation only. No old library, connection,
        # Agent configuration, model result or service state enters this root.
        (client / 'first-launch.json').write_text(json.dumps(dict(stage='ready', step='processing', completed=True))+'\n')
        thread.start()
        with (out / 'build.log').open('w') as log:
            subprocess.run(['python3', str(ROOT/'scripts/build-client.py'), '--out', str(out/'app'),
                            '--runtime-config', str(runtime), '--name', 'MemlaneReading', '--identifier', identifier,
                            '--automation', '--skip-dmg'], cwd=ROOT, stdout=log, stderr=subprocess.STDOUT, check=True)
        spec = importlib.util.spec_from_file_location('reading_ui', ROOT/'scripts/p1-ui.py')
        driver = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(driver)
        ui = driver.UI(app, out, out/'screenshots', config=runtime)
        (out/'ui-process.json').write_text(json.dumps(dict(pid=ui.pid, automation=ui.path, native=ui.native_path, owned=True))+'\n')

        def call(op, **fields):
            with socket.socket(socket.AF_UNIX) as channel:
                channel.settimeout(15)
                channel.connect(str(ipc))
                channel.sendall((json.dumps(dict(protocol=1, id=str(uuid.uuid4()), op=op, **fields))+'\n').encode())
                response = json.loads(channel.makefile('rb', buffering=0).readline())
            assert response['ok'], response
            return response.get('result')

        def capture(name):
            from importlib.machinery import SourceFileLoader
            mac = SourceFileLoader('reading_mac', str(ROOT/'scripts/mac-native.py')).load_module().Mac()
            mac.require_unlocked()
            was_dialog = any(n.get('key') == 'knowledge-history-dialog' for n in ui.nodes())
            mac.activate(ui.pid)
            driver.wait(lambda: ui.native()['key_window'], timeout=10)
            ui.req('capture')
            mac.capture(ui.pid, out/'screenshots'/name)
            assert ui.native()['visible'] and ui.native()['key_window']
            assert was_dialog == any(n.get('key') == 'knowledge-history-dialog' for n in ui.nodes())
            (out/'screenshots'/(Path(name).stem+'-capture.json')).write_text(json.dumps(dict(pid=ui.pid, actual_system_window=True, dialog_kept_open=was_dialog))+'\n')
            (out/'screenshots'/ (Path(name).stem+'-tree.json')).write_text(json.dumps(ui.nodes(), ensure_ascii=False, indent=2)+'\n')
            report['screenshots'].append(name)

        def resize(width, height):
            with socket.socket(socket.AF_UNIX) as channel:
                channel.settimeout(10)
                channel.connect(ui.native_path)
                channel.sendall((json.dumps(dict(op='resize', width=width, height=height))+'\n').encode())
                result = json.loads(channel.makefile('rb', buffering=0).readline())
            assert 'error' not in result, result

        driver.wait(lambda: '服务运行中' in ui.labels(), timeout=90)
        repo = out/'synthetic-repository'
        subprocess.run(['/usr/bin/git', 'init', str(repo)], capture_output=True, check=True)
        project = call('register_project', user_confirmed=True, project=dict(name='本轮阅读验收', path=str(repo)))
        identifier = str(uuid.uuid4())
        first = '最初保存的知识正文，不能冒充完整会话原文。\n' + '长历史保留原始版本内容。\n'*80
        current = '同一代码仓库对应一个项目，多个编码 Agent 共享已授权的记忆。\n\n共享范围\n默认使用当前项目知识、全局个人偏好与通用经验。其他项目需要明确授权。'
        call('apply', user_confirmed=True, mutation=dict(operation_id=str(uuid.uuid4()), kind='save',
             dataset=project['id'], memory=identifier, title='项目记忆与版本', text=first, origin='knowledge', source='本轮合成输入'))
        call('apply', user_confirmed=True, mutation=dict(operation_id=str(uuid.uuid4()), kind='edit', dataset=project['id'],
             memory=identifier, expected_revision=1, title='项目记忆与版本', text=current))
        raw_id = str(uuid.uuid4())
        raw_text = '用户：跨仓库如何共享？\nAgent：逐项目明确授权。\n' + '独立原始材料，保留当时上下文。\n'*60
        call('apply', user_confirmed=True, mutation=dict(operation_id=str(uuid.uuid4()), kind='save', dataset=project['id'],
             memory=raw_id, title='独立原始材料', text=raw_text, origin='raw', source='本轮合成输入'))
        driver.wait(lambda: call('get', memory=identifier)['index_state'] == 'indexed', timeout=90)
        report['actual_rust_index'] = True
        driver.wait(lambda: project['name'] in ui.labels())
        ui.press(project['name'])
        driver.wait(lambda: '知识 1' in ui.labels() and '原始记录 1' in ui.labels())
        ui.press('项目记忆与版本')
        driver.wait(lambda: '这条知识未附完整会话原文。' in ui.labels())
        assert first not in ui.labels()
        capture('01-reading-system.png')
        ui.set('reading-search', '本轮不匹配的关键词')
        ui.req('act', id=ui.node(key='reading-search')['id'], action='focus')
        ui.req('input', events=[dict(type='key', key='Enter')])
        driver.wait(lambda: '没有匹配的内容' in ui.labels())
        assert '知识 1' in ui.labels() and '原始记录 1' in ui.labels()
        assert '整理项目' not in ui.labels()
        capture('01b-filter-zero-system.png')
        ui.press('清除内容筛选')
        driver.wait(lambda: any(n.get('key') == 'memory-'+identifier for n in ui.nodes()))
        report['filter_zero_keeps_scope_counts_and_reading'] = True
        ui.press('历史')
        driver.wait(lambda: 'v1' in ui.labels() and 'v2' in ui.labels())
        capture('02-history-current-system.png')
        ui.press('v1')
        assert first in ui.labels()
        capture('03-history-old-system.png')
        preview = ui.node(key='knowledge-history-preview')['bounds']
        before_text_y = ui.node(label=first)['bounds']['y']
        ui.req('input', events=[dict(type='wheel', x=preview['x']+preview['width']/2,
                                   y=preview['y']+preview['height']/2, dx=0, dy=400, precise=True)])
        assert ui.node(label=first)['bounds']['y'] < before_text_y
        assert any(n.get('key') == 'knowledge-history-dismiss' for n in ui.nodes())
        capture('03b-history-scroll-system.png')
        report['long_history_preview_scrolls_with_fixed_close'] = True
        after = call('get', memory=identifier)
        assert after['revision'] == 2 and after['text'] == current
        report['history_reads_real_immutable_versions'] = True
        report['history_does_not_replace_current_knowledge'] = True
        resize(980, 620)
        capture('04-history-narrow-system.png')
        for _ in range(10):
            ui.req('input', events=[dict(type='key', key='Tab')])
            focused = ui.req('tree').get('focus')
            nodes = ui.nodes()
            by_id = {n['id']: n for n in nodes}
            modal_id = next(n['id'] for n in nodes if n.get('key') == 'knowledge-history-dialog')
            ancestor = focused
            while ancestor in by_id and ancestor != modal_id:
                ancestor = by_id[ancestor].get('parent')
            assert ancestor == modal_id, by_id.get(focused)
        ui.req('input', events=[dict(type='key', key='Escape')])
        driver.wait(lambda: not any(n.get('key') == 'knowledge-history-dialog' for n in ui.nodes()))
        report['history_escape_and_tab_verified'] = True
        resize(1200, 820)
        ui.press('历史')
        driver.wait(lambda: 'v1' in ui.labels())
        ui.press('关闭知识历史')
        driver.wait(lambda: not any(n.get('key') == 'knowledge-history-dialog' for n in ui.nodes()))
        ui.press('历史')
        driver.wait(lambda: 'v1' in ui.labels())
        ui.req('input', events=[dict(type='click', x=10, y=10)])
        driver.wait(lambda: not any(n.get('key') == 'knowledge-history-dialog' for n in ui.nodes()))
        report['history_close_button_and_backdrop_verified'] = True
        ui.press('历史')
        driver.wait(lambda: 'v1' in ui.labels())
        ui.press('关闭')
        ui.press_key('reading-tab-raw')
        driver.wait(lambda: '独立原始材料' in ui.labels())
        ui.press('独立原始材料')
        ui.press('阅读完整原始记录')
        driver.wait(lambda: '逐项目明确授权' in ' '.join(ui.labels()))
        capture('05-original-reader-system.png')
        assert call('get', memory=raw_id)['text'] == raw_text
        ui.press('返回所选记忆')
        ui.press_key('reading-tab-knowledge')
        driver.wait(lambda: any(n.get('key') == 'knowledge-history-open' for n in ui.nodes()))
        assert call('get', memory=identifier)['revision'] == 2
        report['knowledge_selection_survives_original_tab_return'] = True
        report['actual_independent_original_read'] = True
        report['scope_counts_verified'] = True
        # Stop only this isolated daemon through its real lifecycle contract.
        # A later filter read must retain the previously loaded rows.
        call('stop')
        driver.wait(lambda: not ipc.exists(), timeout=30)
        ui.set('reading-search', '停服后的筛选请求')
        ui.req('act', id=ui.node(key='reading-search')['id'], action='focus')
        ui.req('input', events=[dict(type='key', key='Enter')])
        driver.wait(lambda: any(label.startswith('读取失败，保留同范围') for label in ui.labels()))
        assert any(n.get('key') == 'memory-'+identifier for n in ui.nodes())
        assert '没有匹配的内容' not in ui.labels()
        capture('06-stopped-filter-failure-system.png')
        report['actual_stopped_filter_retains_previous_rows'] = True
        report['status'] = 'passed'
    except Exception as error:
        report['failure'] = repr(error)
        if ui is not None:
            try:
                ui.system_capture('failure-system.png')
                report['failure_labels'] = ui.labels()
            except Exception as capture_error:
                report['failure_capture_error'] = repr(capture_error)
    finally:
        stopped = False
        if ui is not None:
            try:
                ui.quit(stop=True)
                stopped = True
            except Exception as error:
                report['cleanup_errors'].append(repr(error))
        if not stopped and app.exists():
            try:
                log = out/'fallback-stop.log'
                result = subprocess.run(['/usr/bin/open', '-n', '-W', '--stdout', str(log), '--stderr', str(log),
                    '--env', 'HOME='+str(out/'ui-home'), '--env', 'PATH=/usr/bin:/bin', str(app),
                    '--args', '--p0-config', str(runtime), '--p0-action', 'stop'], capture_output=True, text=True, timeout=45)
                assert result.returncode == 0
                receipts = []
                for line in log.read_text().splitlines():
                    try: value = json.loads(line)
                    except ValueError: continue
                    if isinstance(value, dict) and value.get('action') == 'stop': receipts.append(value)
                assert receipts and not receipts[-1].get('error'), receipts
                import plistlib, re, signal
                meta = plistlib.loads((app/'Contents/Info.plist').read_bytes())
                executable = str(app/'Contents/MacOS'/meta['CFBundleExecutable'])
                partial = subprocess.run(['pgrep', '-f', '^'+re.escape(executable)], capture_output=True, text=True)
                for pid in partial.stdout.split(): os.kill(int(pid), signal.SIGTERM)
                report['fallback_stop'] = receipts[-1]
            except Exception as error:
                report['cleanup_errors'].append('fallback stop: '+repr(error))
        if thread.is_alive():
            server.shutdown()
            thread.join(timeout=5)
        server.server_close()
        report['component_requests'] = requests
        report['no_repeated_download'] = all(path == '/components.json' for path in requests)
        report['component_download_requirement_bytes'] = sum(d['bytes'] for d in descriptors)
        if app.exists():
            report['app_file_bytes'] = sum(p.lstat().st_size for p in app.rglob('*') if not p.is_dir())
            allocated = subprocess.run(['/usr/bin/du', '-sk', str(app), str(client)], capture_output=True, text=True, check=True)
            report['allocated_disk_bytes'] = sum(int(line.split()[0]) for line in allocated.stdout.splitlines())*1024
        report['socket_absent'] = not ipc.exists()
        try:
            import fcntl
            report['locks_released'] = {}
            for name, path in dict(service=client/'data/.service.lock', worker=client/'data/index/.worker.lock').items():
                if path.exists():
                    with path.open('rb') as handle:
                        fcntl.flock(handle.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                        fcntl.flock(handle.fileno(), fcntl.LOCK_UN)
                report['locks_released'][name] = True
        except Exception as error:
            report['cleanup_errors'].append('lock verification: '+repr(error))
        if report['cleanup_errors'] or not report.get('socket_absent', False):
            report['status'] = 'failed'
        (out/'report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n')
        print(json.dumps(dict(status=report['status'], failure=report.get('failure'), report=str(out/'report.json')), ensure_ascii=False))
        if report['status'] != 'passed': raise SystemExit(1)


if __name__ == '__main__':
    main()
