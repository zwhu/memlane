#!/usr/bin/env python3
"""隔离空根中新包的实际组件下载、同页取消/重试和环境正文复用。"""
import argparse
import base64
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
import os
from pathlib import Path
import socket
import subprocess
import tarfile
import threading
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]


def digest(path):
    value = hashlib.sha256()
    with path.open('rb') as stream:
        while chunk := stream.read(1024 * 1024):
            value.update(chunk)
    return value.hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--reuse-components', type=Path, help='仅复用已通过本轮验收的不可变组件，业务根仍为空')
    args = parser.parse_args()
    out = args.out.resolve()
    out.mkdir(mode=0o700, parents=True, exist_ok=False)
    client = out / 'client'
    client.mkdir(mode=0o700)
    report = {'status': 'failed', 'synthetic_only': True, 'empty_business_root': True,
              'user_data_copied': False, 'agent_configs_copied': False, 'actual_native_app': True,
              'model_inference_verified': False, 'screenshots': [], 'cleanup_errors': []}
    ui = None
    state = {'mode': 'normal', 'delay': 0, 'sent': 0, 'requests': {}}
    lock = threading.Lock()
    archives = {}
    descriptors = []
    paths = [ROOT / 'build/p0-engine/memlane-cognee-rust-0.0.1-darwin-arm64.tar.gz',
             ROOT / 'build/p0-engine/memlane-model-e5-base-int8.tar.gz',
             ROOT / 'build/ui-delivery-20261109-001/fixed-component/cognee-service-1.6.7-memlane.6.tar.gz',
             ROOT / 'build/onboarding-components/cognee-bge-zh-1.5.tar.gz',
             ROOT / 'build/onboarding-components/cognee-codex-plugin-1.7.7.tar.gz']
    for path in paths:
        with tarfile.open(path) as archive:
            raw = archive.extractfile('component.json').read()
            meta = json.loads(raw)
            total = sum(item.size for item in archive.getmembers())
        archives[meta['id']] = path
        descriptors.append({**{key: meta[key] for key in ('id', 'version', 'platform', 'protocol_min', 'protocol_max')},
                            'bytes': path.stat().st_size, 'unpacked_bytes': total,
                            'sha256': digest(path), 'manifest_sha256': hashlib.sha256(raw).hexdigest()})
    signed = None

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_GET(self):
            with lock:
                state['requests'][self.path] = state['requests'].get(self.path, 0) + 1
                mode, delay = state['mode'], state['delay']
            if self.path == '/components.json':
                data = signed
                self.send_response(200)
                self.send_header('Content-Length', str(len(data)))
                self.end_headers()
                self.wfile.write(data)
                return
            path = archives.get(self.path.removeprefix('/').removesuffix('.tar.gz'))
            if path is None:
                self.send_error(404)
                return
            if mode == 'error':
                self.send_error(503)
                return
            offset = 0
            if self.headers.get('Range', '').startswith('bytes='):
                offset = int(self.headers['Range'][6:].split('-')[0])
            size = path.stat().st_size
            if not 0 <= offset < size:
                self.send_error(416)
                return
            self.send_response(206 if offset else 200)
            if offset:
                self.send_header('Content-Range', f'bytes {offset}-{size-1}/{size}')
            self.send_header('Content-Length', str(size-offset))
            self.end_headers()
            sent = 0
            try:
                with path.open('rb') as stream:
                    stream.seek(offset)
                    while chunk := stream.read(65536):
                        self.wfile.write(chunk)
                        self.wfile.flush()
                        sent += len(chunk)
                        if delay:
                            time.sleep(delay)
            except (BrokenPipeError, ConnectionResetError):
                pass
            with lock:
                state['sent'] += sent

    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    server_thread = None
    app = out / 'app/darwin-arm64/MemlaneEnvironment.app'
    try:
        from importlib.machinery import SourceFileLoader
        mac = SourceFileLoader('environment_preflight', str(ROOT / 'scripts/mac-native.py')).load_module().Mac()
        mac.require_unlocked()
        base = f'http://127.0.0.1:{server.server_port}'
        for descriptor in descriptors:
            descriptor['url'] = base + '/' + descriptor['id'] + '.tar.gz'
        payload = json.dumps({'version': 1, 'components': descriptors}, separators=(',', ':')).encode()
        signing = '''import sys,json,base64
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives import serialization
k=Ed25519PrivateKey.generate();print(json.dumps({'signature':base64.b64encode(k.sign(sys.stdin.buffer.read())).decode(),'public_key':base64.b64encode(k.public_key().public_bytes(serialization.Encoding.Raw,serialization.PublicFormat.Raw)).decode()}))'''
        keys = json.loads(subprocess.run([str(ROOT / '.local/cognee-python/bin/python'), '-c', signing],
                                        input=payload, capture_output=True, check=True).stdout)
        signed = b'{"payload":' + payload + b',"signature":' + json.dumps(keys['signature']).encode() + b'}'
        (out / 'component-source.json').write_bytes(signed)
        server_thread = threading.Thread(target=server.serve_forever, daemon=True)
        server_thread.start()
        identifier = 'io.github.zwhu.memlane.environment.' + uuid.uuid4().hex[:12]
        config = {'root': str(client), 'component_source': base + '/components.json',
                  'component_public_key': keys['public_key'], 'worker_id': 'cognee-rust',
                  'model_id': 'multilingual-e5-base-int8', 'service_plist': identifier + '.service.plist',
                  'allow_local_test_source': True,
                  'official': {'source': base + '/components.json', 'public_key': keys['public_key'], 'allow_local_test_source': True}}
        runtime = out / 'runtime-config.json'
        runtime.write_text(json.dumps(config, indent=2) + '\n')
        if args.reuse_components:
            import shutil
            previous = args.reuse_components.resolve()
            prior_report = json.loads((previous / 'report.json').read_text())
            assert prior_report.get('actual_cognee_environment') and prior_report.get('installed_bytes_measured') and prior_report.get('socket_absent') and all(prior_report.get('locks_released', {}).get(name, False) for name in ('service', 'worker')), 'component source must have verified environment and shutdown receipts'
            for relative in ('runtime/components', 'official/runtime/components'):
                target = client / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copytree(previous / 'client' / relative, target, symlinks=True)
            report['reused_verified_components_only'] = True

        with (out / 'build.log').open('w') as log:
            subprocess.run(['python3', str(ROOT / 'scripts/build-client.py'), '--out', str(out / 'app'),
                                '--runtime-config', str(runtime), '--name', 'MemlaneEnvironment', '--identifier', identifier,
                                '--automation', '--skip-dmg'], cwd=ROOT, stdout=log, stderr=subprocess.STDOUT, check=True)
        report['app'] = str(app)
        report['cold_download_bytes'] = sum(d['bytes'] for d in descriptors)
        spec = importlib.util.spec_from_file_location('environment_ui', ROOT / 'scripts/p1-ui.py')
        driver = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(driver)
        ui = driver.UI(app, out, out / 'screenshots', config=runtime)
        # Fresh RPC connections keep a read timeout from poisoning the buffered
        # stream during a long Native component/startup update. Mutations are
        # sent once; only read-only tree observations are retried by wait().
        def request(method, **params):
            ui.seq += 1
            with socket.socket(socket.AF_UNIX) as channel:
                channel.settimeout(45)
                channel.connect(ui.path)
                channel.sendall((json.dumps({'id': ui.seq, 'method': method, 'params': params}) + '\n').encode())
                response = json.loads(channel.makefile('rb', buffering=0).readline())
            assert 'error' not in response, response
            return response['result']
        ui.req = request
        ui.nodes = lambda: ui.req('tree').get('nodes', [])
        (out / 'ui-process.json').write_text(json.dumps({'pid': ui.pid, 'automation': ui.path, 'native': ui.native_path, 'owned': True}) + '\n')

        ipc = Path('/tmp') / ('memlane-' + str(os.getuid()) + '-' + hashlib.sha256(str(client / 'data').encode()).hexdigest()[:12]) / 'service.sock'

        def capture(name):
            from importlib.machinery import SourceFileLoader
            mac = SourceFileLoader('environment_mac', str(ROOT / 'scripts/mac-native.py')).load_module().Mac()
            mac.require_unlocked()
            mac.activate(ui.pid)
            driver.wait(lambda: ui.native()['key_window'], timeout=10)
            mac.capture(ui.pid, out / 'screenshots' / name)
            native = ui.native()
            assert native['key_window'] and native['visible']
            (out / 'screenshots' / (Path(name).stem + '-capture.json')).write_text(json.dumps({'pid': ui.pid, 'window_key': True, 'actual_system_window': True}) + '\n')
            (out / 'screenshots' / (Path(name).stem + '-tree.json')).write_text(json.dumps(ui.nodes(), ensure_ascii=False, indent=2) + '\n')
            report['screenshots'].append(name)

        def resize(width, height):
            with socket.socket(socket.AF_UNIX) as channel:
                channel.settimeout(10)
                channel.connect(ui.native_path)
                channel.sendall((json.dumps({'op': 'resize', 'width': width, 'height': height}) + '\n').encode())
                result = json.loads(channel.makefile('rb', buffering=0).readline())
            assert 'error' not in result, result
            ui.req('idle', timeout=2000)

        def press_running(label):
            node = ui.node(label=label)
            assert 'disabled' not in node.get('states', []), node
            ui.req('act', id=node['id'], action='press')

        def cards():
            nodes = ui.nodes()
            assert all(any(n.get('key') == 'environment-state-' + key for n in nodes) for key in ('database', 'engine', 'model'))
            assert '查看检查详情' not in ui.labels()
            return nodes

        driver.wait(lambda: '安装并继续' in ui.labels(), timeout=90)
        cards()
        capture('01-confirm-system.png')
        if args.reuse_components:
            press_running('安装并继续')
        else:
            with lock:
                state.update(mode='error')
            press_running('安装并继续')
            driver.wait(lambda: '重试准备' in ui.labels() and '503' in ' '.join(ui.labels()))
            cards()
            capture('02-failure-system.png')
            with lock:
                state.update(mode='normal', delay=.008)
            press_running('重试准备')
            driver.wait(lambda: any(n.get('key', '').startswith('environment-progress-') for n in ui.nodes()))
            cards()
            capture('03-progress-system.png')
            press_running('取消准备')
            driver.wait(lambda: '继续准备' in ui.labels(), timeout=90)
            partials = list((client / 'runtime/downloads').glob('*.part'))
            assert partials and sum(p.stat().st_size for p in partials) > 0
            report['retained_partial_bytes'] = sum(p.stat().st_size for p in partials)
            cards()
            capture('04-cancelled-system.png')
            with lock:
                state.update(delay=0)
            press_running('继续准备')
        driver.wait(lambda: '继续连接 Agent' in ui.labels(), timeout=300)
        cards()
        assert any('已安装组件 ' in label for label in ui.labels()), ui.labels()
        capture('05-ready-system.png')
        resize(980, 620)
        capture('06-ready-narrow-system.png')
        resize(1200, 820)
        ui.press('继续连接 Agent')
        ui.press('稍后设置')
        ui.press('设置')
        driver.wait(lambda: any('已安装组件 ' in label for label in ui.labels()), timeout=90)
        cards()
        assert '停止服务' in ui.labels()
        capture('07-settings-system.png')
        resize(980, 620)
        capture('08-settings-narrow-system.png')
        resize(1200, 820)
        ui.press('更多维护选项')
        assert '界面缩放' in ui.labels() and '数据与检索恢复' in ui.labels()
        report['maintenance_reachable'] = True
        ui.press('更多维护选项')
        ui.press('停止服务')
        driver.wait(lambda: '启动服务' in ui.labels(), timeout=90)
        capture('09-settings-stopped-system.png')
        report.update(status='passed', actual_http_503=not bool(args.reuse_components), actual_download_cancel_retry=not bool(args.reuse_components),
                      preparation_cards_stay_on_same_page=True, actual_cognee_environment=True,
                      settings_reuse_same_components=True, installed_bytes_measured=True,
                      normal_and_narrow_system_windows=True)
    except Exception as error:
        report['failure'] = repr(error)
        if ui is not None:
            try:
                ui.system_capture('failure-system.png')
                report['failure_labels'] = ui.labels()
            except Exception as capture_error:
                report['failure_capture_error'] = repr(capture_error)
    finally:
        if ui is not None:
            try:
                ui.quit(stop=True)
            except Exception as error:
                report['cleanup_errors'].append('ui/service: ' + repr(error))
        if server_thread is not None:
            server.shutdown()
            server_thread.join(timeout=5)
        server.server_close()
        report['component_source_closed'] = True
        if args.reuse_components:
            report['reused_without_archive_requests'] = all(not path.endswith('.tar.gz') for path in state['requests'])
            if not report['reused_without_archive_requests']:
                report['cleanup_errors'].append('reusable components unexpectedly downloaded archives')
        with lock:
            report['source'] = dict(state)
        try:
            import fcntl
            ipc = Path('/tmp') / ('memlane-' + str(os.getuid()) + '-' + hashlib.sha256(str(client / 'data').encode()).hexdigest()[:12]) / 'service.sock'
            report['socket_absent'] = not ipc.exists()
            if not report['socket_absent']:
                report['cleanup_errors'].append('service socket retained after shutdown')
            report['locks_released'] = {}
            for name, path in {'service': client / 'data/.service.lock', 'worker': client / 'data/index/.worker.lock'}.items():
                if not path.exists():
                    report['locks_released'][name] = True
                    continue
                with path.open('rb') as handle:
                    fcntl.flock(handle.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                    fcntl.flock(handle.fileno(), fcntl.LOCK_UN)
                report['locks_released'][name] = True
        except Exception as error:
            report['cleanup_errors'].append('runtime shutdown verification: ' + repr(error))
        try:
            import sqlite3
            database = client / 'data/memlane.sqlite'
            if not report.get('socket_absent') or not all(report.get('locks_released', {}).get(name, False) for name in ('service', 'worker')):
                raise RuntimeError('database read requires a stopped service with released locks')
            if database.exists():
                connection = sqlite3.connect('file:' + str(database) + '?mode=ro&immutable=1', uri=True)
                try:
                    report['business_memories'] = connection.execute('SELECT COUNT(*) FROM memories').fetchone()[0]
                    if report['business_memories'] != 0:
                        report['cleanup_errors'].append('initialization created business memories')
                finally:
                    connection.close()
            elif report['status'] == 'passed':
                report['cleanup_errors'].append('expected business database missing')
        except Exception as error:
            report['cleanup_errors'].append('business database verification: ' + repr(error))
        try:
            for root in (client / 'runtime/components', client / 'official/runtime/components'):
                report.setdefault('installed_file_bytes', 0)
                if root.exists():
                    report['installed_file_bytes'] += sum(p.lstat().st_size for p in root.rglob('*') if not p.is_dir())
        except Exception as error:
            report['cleanup_errors'].append('installed file measurement: ' + repr(error))
        if report['cleanup_errors']:
            report['status'] = 'failed'
        (out / 'report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'status': report['status'], 'failure': report.get('failure'), 'report': str(out / 'report.json')}, ensure_ascii=False))
    if report['status'] != 'passed':
        raise SystemExit(1)


if __name__ == '__main__':
    main()
