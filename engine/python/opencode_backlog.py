"""Manager-owned protocol adapter for an immutable OpenCode outbox worker.

Only counts, IDs and process ownership enter this adapter. Bodies and the send
journal remain in the fixed official outbox; Node rechecks the full installation
and authenticates the manager before accessing it.
"""
import asyncio
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
from urllib.parse import urlparse

from project_control import private_write
from project_policy import PolicyError

LIMIT = 16 * 1024 * 1024


def private_file(path, limit=65536):
    path = Path(path)
    if (not path.is_absolute() or path.resolve() != path or path.is_symlink()
            or not path.is_file() or path.stat().st_mode & 0o077 or path.stat().st_size > limit):
        raise ValueError('private file required')
    return path.read_bytes()


def verify(installation, runtime, agent_id):
    from opencode_installation import verify as verify_artifact
    try:
        if installation.get('host_kind') != 'opencode' or installation.get('instance_id') != agent_id:
            raise ValueError('wrong host')
        root = Path(installation['plugin_dir'])
        if not root.is_absolute() or root.resolve() != root:
            raise ValueError('canonical artifact required')
        manifest = verify_artifact(root, installation['manifest_sha256'])
        if 'dist/src/managed-backlog.js' not in manifest['files']:
            raise PolicyError('backlog_host_unsupported', 409)
        helper = root / 'dist/src/managed-pending-cli.js'
        if manifest['files'][str(helper.relative_to(root))] != installation['helper_sha256']:
            raise ValueError('worker changed')
        raw = private_file(installation['binding_file'])
        if hashlib.sha256(raw).hexdigest() != installation['binding_sha256']:
            raise ValueError('binding changed')
        binding = json.loads(raw)
        control = runtime['project_control']
        for value in (control['url'], runtime['service_url']):
            parsed = urlparse(value)
            if (parsed.scheme != 'http' or parsed.hostname != '127.0.0.1' or not parsed.port
                    or parsed.path not in ('', '/') or parsed.username or parsed.query or parsed.fragment):
                raise ValueError('explicit original loopback required')
        if (binding.get('version') != 1 or binding.get('instance_id') != agent_id
                or binding.get('plugin_root') != str(root)
                or binding.get('profile') != installation['profile']
                or binding['memlane']['origin'] != control['url'].rstrip('/')
                or binding['cognee']['baseUrl'] != runtime['service_url'].rstrip('/')):
            raise ValueError('original target changed')
        if not isinstance(binding.get('secrets'), list) or len(binding['secrets']) != 2:
            raise ValueError('binding secrets changed')
        for secret in binding['secrets']:
            if hashlib.sha256(private_file(secret['path'])).hexdigest() != secret['sha256']:
                raise ValueError('bound credential changed')
        private_file(control['management_token_file'])
        node = Path(installation['node_executable'])
        if (not node.is_absolute() or node.resolve() != node or not node.is_file()
                or node.stat().st_size > 512 * 1024 * 1024 or not os.access(node, os.X_OK)
                or hashlib.sha256(node.read_bytes()).hexdigest() != installation['node_sha256']):
            raise ValueError('node changed')
        profile = Path(installation['profile'])
        if not profile.is_absolute() or profile.resolve() != profile or not profile.is_dir():
            raise ValueError('profile changed')
        return node, helper, profile
    except PolicyError:
        raise
    except (OSError, ValueError, TypeError, KeyError):
        raise PolicyError('backlog_installation_changed', 409) from None


async def execute(runtime, installation, project_id, target, action, cancel_file, update=None):
    if action not in ('review', 'save', 'process'):
        raise PolicyError('backlog_worker_unconfirmed', 503)
    node, helper, profile = await asyncio.to_thread(verify, installation, runtime, target['agent_id'])
    command = {'action': 'backlog_' + action, 'project_id': project_id,
               'dataset_id': target['dataset'], 'host_session_key': target['host']}
    if action != 'review':
        command.update(session_id=target['session_id'], native_session=target['native'], cancel_file=str(cancel_file))
    if action == 'save':
        command['tokens'] = target['tokens']
    allowed = ('plugin_dir', 'manifest_sha256', 'binding_file', 'binding_sha256', 'node_executable', 'node_sha256')
    payload = {'installation': {key: installation[key] for key in allowed}, 'agent_id': target['agent_id'],
               'operator': {'token_file': runtime['project_control']['management_token_file'],
                            'control_url': runtime['project_control']['url'], 'service_url': runtime['service_url']},
               'command': command}
    raw = json.dumps(payload).encode()
    if len(raw) > LIMIT:
        raise PolicyError('backlog_snapshot_unconfirmed', 503)
    env = {k: v for k, v in os.environ.items() if not k.upper().startswith(
        ('COGNEE_', 'MEMLANE_', 'LLM_', 'EMBEDDING_', 'NODE_', 'OPENAI_', 'ANTHROPIC_', 'GEMINI_', 'GOOGLE_'))}
    env['HOME'] = str(profile)
    process = await asyncio.create_subprocess_exec(str(node), str(helper), cwd=profile, env=env,
        stdin=asyncio.subprocess.PIPE, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL, limit=LIMIT)
    owner_path = Path(str(cancel_file) + '.opencode-worker.json')
    owner = {'pid': process.pid, 'phase': 'starting', 'action': action,
             'agent_id': target['agent_id'], 'host': target['host']}
    try:
        private_write(owner_path, json.dumps(owner).encode())
        started = await asyncio.to_thread(subprocess.check_output,
            ['ps', '-p', str(process.pid), '-o', 'lstart='], text=True)
        owner.update(started=started.strip(), phase='running')
        private_write(owner_path, json.dumps(owner).encode())
        process.stdin.write(raw)
        await process.stdin.drain()
        process.stdin.close()
        last = None
        while True:
            line = await asyncio.wait_for(process.stdout.readline(), 45)
            if not line:
                break
            value = json.loads(line)
            if not isinstance(value, dict) or value.get('error'):
                code = value.get('error', '') if isinstance(value, dict) else ''
                if not re.fullmatch(r'(pending|original|project)_[a-z_]{1,80}', code):
                    code = 'backlog_worker_unconfirmed'
                raise PolicyError(code, 503)
            if update:
                update(value)
            last = value
        await asyncio.wait_for(process.wait(), 10)
        if process.returncode != 0 or last is None:
            raise PolicyError('backlog_worker_unconfirmed', 503)
        return last
    except BaseException:
        private_write(cancel_file, b'stop')
        try:
            process.terminate()
        except ProcessLookupError:
            pass
        try:
            await asyncio.wait_for(process.wait(), 35)
        except asyncio.TimeoutError:
            owner['phase'] = 'exit_unconfirmed'
            raise PolicyError('backlog_sender_exit_unconfirmed', 503) from None
        raise
    finally:
        if process.returncode is not None:
            owner.update(phase='confirmed_exited', exit_code=process.returncode)
        private_write(owner_path, json.dumps(owner).encode())
