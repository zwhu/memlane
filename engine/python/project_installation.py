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


def prepare(source, root, instance_id, agent, service_url, control_url, host_home=None, *, host_kind='codex', python=None):
    if not re.fullmatch(r'[a-zA-Z0-9_-]{1,64}', instance_id):
        raise PolicyError('invalid_installation_id')
    if host_kind not in ('codex', 'pi', 'traex'):
        raise PolicyError('unsupported_installation_host')
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
    host_files = {}
    if host_kind != 'codex':
        if not python or not Path(python).is_absolute() or not Path(python).is_file():
            raise PolicyError('host_helper_python_required')
        host_files['scripts/agent_memory_adapter.py'] = (ENGINE / 'python/agent_memory_adapter.py').read_bytes()
        if host_kind == 'pi':
            host_files.update({'extension/' + path.name: path.read_bytes() for path in (ENGINE / 'agents/pi').iterdir() if path.is_file()})
        else:
            host_files['scripts/traex_hook.py'] = (ENGINE / 'python/traex_hook.py').read_bytes()
        host_files['host-binding.json'] = json.dumps({'python': python, 'host_kind': host_kind}).encode()
    generation_input = {'upstream': expected}
    if host_files:
        generation_input.update(host_kind=host_kind, instance_id=instance_id,
                                builder_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                                files={name: hashlib.sha256(data).hexdigest() for name, data in host_files.items()})
    generation = hashlib.sha256(json.dumps(expected if host_kind == 'codex' else generation_input, sort_keys=True).encode()).hexdigest()[:20]
    marketplace = root / 'managed-marketplaces' / instance_id / generation
    target = marketplace / 'plugins' / 'cognee'
    expected_host_files = {}
    if host_kind != 'codex':
        namespace = host_kind + '-' + instance_id
        for name in expected['files']:
            data = (source / name).read_bytes()
            if name.startswith('scripts/') and Path(name).suffix in ('.py', '.sh'):
                text = data.decode().replace('".cognee-plugin" / "codex"', '".cognee-plugin" / ' + json.dumps(namespace))
                if name == 'scripts/_plugin_common.py':
                    text = text.replace('suffix = "_codex"', 'suffix = ' + json.dumps('_' + host_kind))
                    text = text.replace('_normalize("codex-agent")', '_normalize(' + json.dumps(host_kind + '-agent') + ')')
                data = text.replace('.cognee-plugin/codex', '.cognee-plugin/' + namespace).encode()
            expected_host_files[name] = data
        expected_host_files.update(host_files)
        if host_kind == 'traex':
            events = ('SessionStart', 'UserPromptSubmit', 'PostToolUse', 'PostToolUseFailure', 'Stop', 'SessionEnd')
            command = 'python3 "${PLUGIN_ROOT}/scripts/traex_hook.py" '
            expected_host_files['hooks.json'] = json.dumps({'hooks': {
                event: [{'hooks': [{'type': 'command', 'command': command + event, 'timeout': 25}]}]
                for event in events}}).encode()
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
        if host_kind != 'codex':
            for name, data in expected_host_files.items():
                path = target / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(data)
            host_receipt = {**expected, 'host_kind': host_kind, 'host_generation': generation,
                            'files': {str(path.relative_to(target)): hashlib.sha256(path.read_bytes()).hexdigest()
                                      for path in target.rglob('*') if path.is_file() and path.name != '.memlane-project-access.json'}}
            private_write(target / '.memlane-project-access.json', json.dumps(host_receipt, sort_keys=True).encode())
    installed_receipt = json.loads((target / '.memlane-project-access.json').read_text())
    host_hashes = {name: hashlib.sha256(data).hexdigest() for name, data in expected_host_files.items()}
    if host_kind != 'codex' and (installed_receipt.get('host_generation') != generation or installed_receipt.get('host_kind') != host_kind
                               or installed_receipt.get('files') != host_hashes):
        raise PolicyError('existing_installation_changed', 409)
    for name, digest in (expected['files'] if host_kind == 'codex' else installed_receipt['files']).items():
        path = target / name
        if path.is_symlink() or hashlib.sha256(path.read_bytes()).hexdigest() != digest:
            raise PolicyError('existing_installation_changed', 409)
    # Official dotenv parsing strips outer quotes and does not decode JSON
    # escapes. Keep literal Unicode/quotes; this file is never sourced by a shell.
    private_write(environment, ''.join(key + '="' + value + '"\n' for key, value in values.items()).encode())
    binding = {'instance_id': instance_id, 'env_file': str(environment)}
    if host_kind != 'codex':
        binding['host_kind'] = host_kind
    private_write(target / '.memlane-installation.json', json.dumps(binding).encode())
    # A catalog is installable through the host's own plugin manager. Each
    # generation has a distinct marketplace/cache namespace; preparing a new
    # generation cannot replace the files of a running host's old install.
    marketplace_name = 'memlane-' + hashlib.sha256(instance_id.encode()).hexdigest()[:12] + '-' + generation
    catalog = {'name': marketplace_name, 'plugins': [
        {'name': 'cognee', 'source': {'source': 'local', 'path': './plugins/cognee'},
         'policy': {'installation': 'AVAILABLE', 'authentication': 'ON_INSTALL'},
         'category': 'Productivity'}]}
    private_write(marketplace / '.agents' / 'plugins' / 'marketplace.json',
                  json.dumps(catalog, indent=2).encode())
    return {'plugin_dir': str(target), 'env_file': str(environment), 'home': str(home),
            'marketplace_root': str(marketplace), 'marketplace_name': marketplace_name,
            'helper_sha256': expected['extension_sha256'],
            'manifest_sha256': hashlib.sha256((target / '.memlane-project-access.json').read_bytes()).hexdigest(),
            'status': 'prepared'}
