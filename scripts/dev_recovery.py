"""Confirm a dead development owner before archiving its original receipt."""
import fcntl
import json
import os
from pathlib import Path
import time


def recover(config, config_path, *, acquire, process_tree, port_free):
    data = Path(config['official_data']).resolve(strict=True)
    receipt = data / '.memlane-dev-owner.json'
    with acquire(data):
        if not receipt.exists():
            return {'status': 'already_released'}
        if receipt.is_symlink() or not receipt.is_file() or receipt.stat().st_mode & 0o077:
            raise RuntimeError('private owner receipt required for recovery')
        original = receipt.read_bytes()
        state = json.loads(original)
        if (state.get('config') != str(Path(config_path).resolve(strict=True))
                or Path(state.get('official_data', '')).resolve() != data):
            raise RuntimeError('owner recovery configuration mismatch')
        table = process_tree()
        pids = {state['pid'], *state.get('children', []), *state.get('service_children', {}).values(),
                *(int(pid) for pid in state.get('observed_descendants', {}))}
        if any(type(pid) is not int or pid <= 1 for pid in pids):
            raise RuntimeError('owner process identity unavailable')
        # A reused PID is unknown ownership, not permission to kill or ignore it.
        if any(pid in table or any(row[1] == pid for row in table.values()) for pid in pids):
            raise RuntimeError('recorded process or process group remains; recovery refused')
        urls = {config['service_url'], state.get('service_url'), config.get('completion_url'),
                state.get('completion_url'), config.get('project_control', {}).get('url'), state.get('project_control_url')}
        for url in sorted(value for value in urls if value):
            port_free(url)
        locks = []
        try:
            for path in (config.get('generation_file'), config.get('project_control', {}).get('registry_file')):
                if not path:
                    continue
                fd = os.open(path + '.lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
                locks.append(fd)
                fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            if receipt.read_bytes() != original:
                raise RuntimeError('owner receipt changed during recovery')
            archive = receipt.with_name('.memlane-dev-owner-recovered-' + str(time.time_ns()) + '.json')
            # Keep the exact original evidence; no content or configuration is reset.
            os.replace(receipt, archive)
            return {'status': 'released', 'archive': str(archive)}
        finally:
            for fd in locks:
                os.close(fd)
