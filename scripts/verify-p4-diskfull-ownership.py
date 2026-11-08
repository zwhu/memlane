#!/usr/bin/env python3
"""Actual APFS ownership rejection, synthetic file and IPC sentinels; no App."""
import argparse
import hashlib
import json
import os
import socket
import subprocess
import threading
import uuid
from importlib.machinery import SourceFileLoader
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--out', type=Path, default=ROOT / 'build/p4-delivery/diskfull-ownership-gate.json')
parser.add_argument('--isolated-image', action='store_true', help='Use a new owned test image while preserving the main acceptance image')
args = parser.parse_args()
f = Path((ROOT / 'build/p4-delivery/diskfull-final-fixture-path.txt').read_text().strip()).resolve()
meta = json.loads((f / 'source-info.json').read_text())
G = SourceFileLoader('diskownershipguard', str(ROOT / 'scripts/p3-c2-common.py')).load_module()
G.validate_fixture(f, Path(meta['app']), 'code')
C = SourceFileLoader('diskownershipcommon', str(ROOT / 'scripts/p4-diskfull-common.py')).load_module()
evidence = C.Evidence(args.out)
data = f / 'client/data'
test_image = f / ('explicit-diskfull-ownership-' + uuid.uuid4().hex + '.dmg') if args.isolated_image else f / 'explicit-diskfull.dmg'
owned = C.OwnedImage(test_image, data)
wire_root = Path(json.loads((f / 'runtime-config.json').read_text())['root']) / 'data'
ipc = Path(f'/tmp/memlane-{os.getuid()}-{hashlib.sha256(str(wire_root).encode()).hexdigest()[:12]}/service.sock')
assert not ipc.exists() and not ipc.is_symlink()
ipc.parent.mkdir(mode=0o700, exist_ok=True)
listener = socket.socket(socket.AF_UNIX)
listener.bind(str(ipc))
listener.listen()
listener.settimeout(5)
requests = []

def observe():
    try:
        connection, _ = listener.accept()
        with connection:
            connection.settimeout(2)
            requests.append(connection.recv(4096))
            connection.sendall(b'{"protocol":1,"id":"sentinel","ok":true}\n')
    except (socket.timeout, OSError):
        pass

thread = threading.Thread(target=observe, daemon=True)
thread.start()
report = {'status': 'failed', 'actual_app': False, 'actual_apfs': True, 'enospc_test': False, 'fault_fixture': 'preexisting files and synthetic IPC listener'}
created = []
try:
    if args.isolated_image:
        assert data.stat().st_dev == data.parent.stat().st_dev and not test_image.exists() and not test_image.is_symlink()
        subprocess.run(['hdiutil', 'create', '-size', '128m', '-fs', 'APFS', '-nospotlight', '-type', 'UDIF', str(test_image)], capture_output=True, check=True, timeout=30)
        report['owned_test_image'] = str(test_image)
        report['main_acceptance_image_preserved'] = True
    owned.attach()
    marker = data / 'memlane.sqlite'
    filler = data / 'explicit-diskfull-filler.bin'
    assert not marker.exists() and not filler.exists()
    for path, content in ((marker, b'explicit ownership sentinel, not a database'), (filler, b'explicit filler ownership sentinel')):
        with path.open('xb') as stream:
            created.append(path)
            stream.write(content)
    before = [marker.read_bytes(), filler.read_bytes()]
    child = ROOT / ('build/p4-delivery/diskfull-ownership-child-' + uuid.uuid4().hex + '-rejected.json')
    assert not child.exists() and not child.is_symlink()
    result = subprocess.run(['python3', str(ROOT / 'scripts/verify-p4-diskfull-app.py'), '--pointer', str(ROOT / 'build/p4-delivery/diskfull-final-fixture-path.txt'), '--out', str(child)], capture_output=True, text=True, timeout=30)
    rejected = json.loads(child.read_text())
    assert result.returncode != 0 and rejected['actual_app'] is False
    assert 'fresh data image required' in rejected['error'], rejected
    assert [marker.read_bytes(), filler.read_bytes()] == before
    assert data.stat().st_dev != data.parent.stat().st_dev
    assert not requests, 'unowned service received stop'
    report.update(status='passed', unowned_files_preserved=True, no_stop_request=True, owned_mount_preserved=True, child_report=str(child))
except Exception as error:
    report['error'] = repr(error)
finally:
    errors = []
    try:
        for path in created:
            path.unlink()
        owned.detach()
    except Exception as error:
        errors.append(repr(error))
    listener.close()
    thread.join(timeout=6)
    ipc.unlink()
    if thread.is_alive():
        errors.append('IPC observer did not stop')
    if requests:
        errors.append('unowned service received a request')
    report['cleanup_errors'] = errors
    if errors:
        report['status'] = 'failed'
    evidence.write(report)
    evidence.close()
print(json.dumps(report))
assert report['status'] == 'passed'
