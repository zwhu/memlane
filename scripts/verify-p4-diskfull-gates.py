#!/usr/bin/env python3
"""Explicit attach/protocol/evidence fault fixtures; no App or actual APFS proof."""
import argparse
import json
import plistlib
import subprocess
import tempfile
from importlib.machinery import SourceFileLoader
from pathlib import Path
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--out', type=Path, default=ROOT / 'build/p4-delivery/diskfull-gate-faults.json')
args = parser.parse_args()
C = SourceFileLoader('diskfullgatescommon', str(ROOT / 'scripts/p4-diskfull-common.py')).load_module()
report = {'status': 'failed', 'actual_app': False, 'actual_apfs': False, 'fault_fixture': True, 'cases': []}
with tempfile.TemporaryDirectory(prefix='memlane-diskfull-gates-') as folder:
    root = Path(folder).resolve()
    image, mountpoint = root / 'fixture.dmg', root / 'mount'
    mountpoint.mkdir()
    for fault in ('nonzero', 'timeout', 'mismatched_mount', 'bad_plist'):
        state = {'attached': False, 'detached': False}

        def run(command, **kwargs):
            if command[1] == 'info':
                if state['attached'] and fault == 'bad_plist':
                    return subprocess.CompletedProcess(command, 0, b'explicit malformed plist')
                images = []
                if state['attached']:
                    images = [{'image-path': str(image), 'system-entities': [{'dev-entry': '/dev/disk987'}, {'dev-entry': '/dev/disk988s1', 'mount-point': str(root / 'wrong-mount')}]}]
                return subprocess.CompletedProcess(command, 0, plistlib.dumps({'images': images}))
            if command[1] == 'attach':
                state['attached'] = True
                if fault == 'timeout':
                    raise subprocess.TimeoutExpired(command, 60)
                return subprocess.CompletedProcess(command, 1 if fault == 'nonzero' else 0, b'', b'explicit attach failure')
            assert command == ['hdiutil', 'detach', '/dev/disk987'], 'unknown device cleanup'
            state.update(attached=False, detached=True)
            return subprocess.CompletedProcess(command, 0, '', '')

        owned = C.OwnedImage(image, mountpoint)
        with patch.object(C.subprocess, 'run', run):
            try:
                owned.attach()
                raise RuntimeError('fault attachment accepted')
            except (AssertionError, subprocess.TimeoutExpired, plistlib.InvalidFileException):
                pass
            if fault == 'bad_plist':
                try:
                    owned.detach()
                    raise RuntimeError('unidentified device cleanup treated as success')
                except plistlib.InvalidFileException:
                    assert not state['detached']
            else:
                owned.detach()
                assert state['detached'] and not state['attached']
        report['cases'].append(fault)

    target = root / 'sentinel'
    target.write_text('unchanged')
    for name, destination in [('existing', None), ('link', target), ('dangling', root / 'absent')]:
        path = root / name
        if destination:
            path.symlink_to(destination)
        else:
            path.write_text('prior failed evidence')
        try:
            C.Evidence(path)
            raise RuntimeError('existing evidence overwritten')
        except FileExistsError:
            pass
        assert target.read_text() == 'unchanged'
        if not destination:
            assert path.read_text() == 'prior failed evidence'
        report['cases'].append('evidence_' + name)
    evidence = C.Evidence(root / 'owned.json')
    evidence.write({'status': 'failed'})
    evidence.write({'status': 'passed'})
    evidence.close()
    assert json.loads((root / 'owned.json').read_text()) == {'status': 'passed'}
    report['cases'].append('owned_evidence_update')
    state = {'owned': True, 'detach_calls': 0}

    def reused_disk(command, **kwargs):
        if command[1] == 'info':
            images = [{'image-path': str(image if state['owned'] else root / 'other-image.dmg'), 'system-entities': [{'dev-entry': '/dev/disk987'}]}]
            return subprocess.CompletedProcess(command, 0, plistlib.dumps({'images': images}))
        assert command == ['hdiutil', 'detach', '/dev/disk987'] and state['owned'], 'unowned reused disk detached'
        state.update(owned=False, detach_calls=state['detach_calls']+1)
        return subprocess.CompletedProcess(command, 16, '', 'explicit busy fault; disk later reused')

    owned = C.OwnedImage(image, mountpoint)
    owned.attempted = True
    with patch.object(C.subprocess, 'run', reused_disk):
        owned.detach()
    assert state['detach_calls'] == 1 and not state['owned']
    report['cases'].append('busy_disk_reused_by_other_image')
report['status'] = 'passed'
out = args.out
evidence = C.Evidence(out)
evidence.write(report)
evidence.close()
print(json.dumps(report))
