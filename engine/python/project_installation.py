"""Prepare a private per-installation official plugin artifact and environment.

This never edits an Agent's active settings, starts a host, or handles content.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import re

from project_control import private_write, read_secret
from project_policy import PolicyError

ENGINE = Path(__file__).resolve().parents[1]


def prepare(source, root, instance_id, agent, service_url, control_url, host_home=None):
    if not re.fullmatch(r'[a-zA-Z0-9_-]{1,64}', instance_id):
        raise PolicyError('invalid_installation_id')
    source, root = Path(source), Path(root)
    expected = json.loads((ENGINE / 'managed-project-plugin.json').read_text())
    receipt = json.loads((source / '.memlane-project-access.json').read_text())
    if receipt != expected:
        raise PolicyError('managed_plugin_input_mismatch', 503)
    for name, digest in expected['files'].items():
        path = source / name
        if path.is_symlink() or hashlib.sha256(path.read_bytes()).hexdigest() != digest:
            raise PolicyError('managed_plugin_input_mismatch', 503)
    home = Path(host_home) if host_home else Path.home()
    if not home.is_absolute() or not home.is_dir():
        raise PolicyError('absolute_host_home_required')
    # A code generation change gets a new artifact. Existing hosts keep their
    # exact old path until explicitly reconfigured; never replace it in place.
    generation = hashlib.sha256(json.dumps(expected, sort_keys=True).encode()).hexdigest()[:20]
    target = root / 'managed-plugins' / instance_id / generation
    environment = root / 'agent-credentials' / (instance_id + '.env')
    values = {'COGNEE_BASE_URL': service_url, 'COGNEE_API_KEY': read_secret(agent['key_file']),
              'MEMLANE_PROJECT_CONTROL_URL': control_url,
              'MEMLANE_PROJECT_CONTROL_TOKEN_FILE': agent['control_token_file'],
              'COGNEE_CODING_MEMORY': 'true', 'COGNEE_PLUGIN_IDENTITY': 'false',
              'COGNEE_SHARED_AGENT_MEMORY': 'false', 'COGNEE_MANAGED_ENDPOINT': 'true'}
    if any('\n' in value or '\r' in value for value in values.values()):
        raise PolicyError('installation_environment_invalid')
    if not target.exists():
        target.parent.mkdir(parents=True, mode=0o700, exist_ok=True)
        shutil.copytree(source, target, ignore=shutil.ignore_patterns('__pycache__', '*.pyc', '.memlane-installation.json'))
    for name, digest in expected['files'].items():
        path = target / name
        if path.is_symlink() or hashlib.sha256(path.read_bytes()).hexdigest() != digest:
            raise PolicyError('existing_installation_changed', 409)
    # Official dotenv parsing strips outer quotes and does not decode JSON
    # escapes. Keep literal Unicode/quotes; this file is never sourced by a shell.
    private_write(environment, ''.join(key + '="' + value + '"\n' for key, value in values.items()).encode())
    private_write(target / '.memlane-installation.json', json.dumps({'instance_id': instance_id, 'env_file': str(environment)}).encode())
    return {'plugin_dir': str(target), 'env_file': str(environment), 'home': str(home),
            'helper_sha256': expected['extension_sha256'],
            'manifest_sha256': hashlib.sha256((target / '.memlane-project-access.json').read_bytes()).hexdigest(),
            'status': 'prepared'}
