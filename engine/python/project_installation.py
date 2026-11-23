"""Prepare a private per-installation official plugin artifact and environment.

This never edits an Agent's active settings, starts a host, or handles content.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import re
import shlex

from _runtime_common import match_managed_plugin_manifest, replace_exact
from project_control import private_write, read_secret
from project_policy import PolicyError

ENGINE = Path(__file__).resolve().parents[1]


def prepare(source, root, instance_id, agent, service_url, control_url, host_home=None, *, host_kind='codex', python=None):
    if not re.fullmatch(r'[a-zA-Z0-9_-]{1,64}', instance_id):
        raise PolicyError('invalid_installation_id')
    if host_kind in ('claude_code', 'cursor', 'antigravity'):
        return prepare_official_host(root, instance_id, agent, service_url, control_url, host_home,
                                     host_kind=host_kind, python=python, source=source)
    if host_kind not in ('codex', 'pi', 'traex', 'trae_ide'):
        raise PolicyError('unsupported_installation_host')
    source, root = Path(source), Path(root)
    receipt = json.loads((source / '.memlane-project-access.json').read_text())
    expected, _ = match_managed_plugin_manifest(ENGINE, receipt)
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
            helper = {'traex': 'traex_hook.py', 'trae_ide': 'trae_ide_hook.py'}[host_kind]
            host_files['scripts/' + helper] = (ENGINE / 'python' / helper).read_bytes()
        host_files['host-binding.json'] = json.dumps({'python': python, 'host_kind': host_kind}).encode()
    from project_tool_patch import common_recall_response, forget_response, remember_response
    tool_patch = hashlib.sha256((ENGINE / 'python/project_tool_patch.py').read_bytes()).hexdigest()
    generation_input = {'upstream': expected, 'host_kind': host_kind, 'instance_id': instance_id,
                        'builder_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                        'tool_patch_sha256': tool_patch}
    from project_capture_patch import apply as capture_patch
    capture_sources = {name: (ENGINE / 'python' / name).read_bytes()
                       for name in ('project_capture_patch.py', 'plugin_capture.py')}
    generation_input['capture_patch_sha256'] = hashlib.sha256(b''.join(capture_sources.values())).hexdigest()
    if host_kind == 'codex':
        from project_trace_patch import codex_file_change_output
        generation_input['trace_patch_sha256'] = hashlib.sha256((ENGINE / 'python/project_trace_patch.py').read_bytes()).hexdigest()
    if host_files:
        generation_input.update(host_kind=host_kind, instance_id=instance_id,
                                builder_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                                files={name: hashlib.sha256(data).hexdigest() for name, data in host_files.items()})
    generation = hashlib.sha256(json.dumps(generation_input, sort_keys=True).encode()).hexdigest()[:20]
    marketplace = root / 'managed-marketplaces' / instance_id / generation
    target = marketplace / 'plugins' / 'cognee'
    expected_host_files = {}
    if host_kind == 'codex':
        expected_host_files = {name: (source / name).read_bytes() for name in expected['files']}
        expected_host_files['scripts/_coding_memory.py'] = codex_file_change_output(
            expected_host_files['scripts/_coding_memory.py'].decode()).encode()
    if host_kind != 'codex':
        namespace = host_kind + '-' + instance_id
        for name in expected['files']:
            data = (source / name).read_bytes()
            if name.startswith('scripts/') and Path(name).suffix in ('.py', '.sh'):
                text = data.decode().replace('".cognee-plugin" / "codex"', '".cognee-plugin" / ' + json.dumps(namespace))
                if name == 'scripts/_plugin_common.py':
                    text = replace_exact(text, 'suffix = "_codex"', 'suffix = ' + json.dumps('_' + host_kind),
                                         label='plugin host suffix')
                    text = replace_exact(text, '_normalize("codex-agent")', '_normalize(' + json.dumps(host_kind + '-agent') + ')',
                                         label='plugin host normalize')
                    text = replace_exact(
                        text,
                        'os.environ.get("COGNEE_SESSION_PREFIX", "") or "codex") or "codex"',
                        'os.environ.get("COGNEE_SESSION_PREFIX", "") or ' + json.dumps(host_kind) + ') or ' + json.dumps(host_kind),
                        label='plugin session prefix',
                    )
                data = text.replace('.cognee-plugin/codex', '.cognee-plugin/' + namespace).encode()
            expected_host_files[name] = data
        expected_host_files.update(host_files)
        if host_kind == 'traex':
            # TraeX's direct installer normalizes the manifest name. Publish
            # that name before hashing so the native cache remains verifiable.
            manifest = json.loads(expected_host_files['.codex-plugin/plugin.json'])
            manifest['name'] = 'memlane-cognee'
            # Native cache paths include plugin version, not our source path.
            # A distinct semantic build version keeps a running host's old
            # cache intact when another generation is explicitly activated.
            manifest['version'] += '+memlane.' + generation
            expected_host_files['.codex-plugin/plugin.json'] = (json.dumps(manifest, indent=2) + '\n').encode()
            events = ('SessionStart', 'UserPromptSubmit', 'PostToolUse', 'PostToolUseFailure', 'Stop', 'SessionEnd')
            command = shlex.quote(python) + ' "${PLUGIN_ROOT}/scripts/traex_hook.py" '
            expected_host_files['hooks.json'] = json.dumps({'hooks': {
                event: [{'hooks': [{'type': 'command', 'command': command + event, 'timeout': 25}]}]
                for event in events}}).encode()
    expected_host_files['scripts/_memlane_policy.py'] = common_recall_response(expected_host_files['scripts/_memlane_policy.py'].decode()).encode()
    expected_host_files['scripts/cognee-forget.sh'] = forget_response(expected_host_files['scripts/cognee-forget.sh'].decode()).encode()
    expected_host_files['scripts/_remember_http.py'] = remember_response(expected_host_files['scripts/_remember_http.py'].decode()).encode()
    expected_host_files['scripts/_plugin_common.py'] = capture_patch(expected_host_files['scripts/_plugin_common.py'].decode()).encode()
    expected_host_files['scripts/_memlane_capture.py'] = capture_sources['plugin_capture.py']
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
        if expected_host_files:
            for name, data in expected_host_files.items():
                path = target / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(data)
            host_receipt = {**expected, 'host_kind': host_kind, 'host_generation': generation,
                            'tool_patch_sha256': tool_patch,
                            'files': {str(path.relative_to(target)): hashlib.sha256(path.read_bytes()).hexdigest()
                                      for path in target.rglob('*') if path.is_file() and path.name != '.memlane-project-access.json'}}
            if host_kind == 'codex':
                host_receipt['trace_patch_sha256'] = generation_input['trace_patch_sha256']
            private_write(target / '.memlane-project-access.json', json.dumps(host_receipt, sort_keys=True).encode())
    installed_receipt = json.loads((target / '.memlane-project-access.json').read_text())
    host_hashes = {name: hashlib.sha256(data).hexdigest() for name, data in expected_host_files.items()}
    if (installed_receipt.get('host_generation') != generation or installed_receipt.get('host_kind') != host_kind
            or installed_receipt.get('trace_patch_sha256') != generation_input.get('trace_patch_sha256')
            or installed_receipt.get('tool_patch_sha256') != tool_patch or installed_receipt.get('files') != host_hashes):
        raise PolicyError('existing_installation_changed', 409)
    for name, digest in installed_receipt['files'].items():
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


def prepare_official_host(root, instance_id, agent, service_url, control_url, host_home, *, host_kind, python, source=None):
    from project_host_plugin import files_for
    if not python or not Path(python).is_absolute() or not Path(python).is_file():
        raise PolicyError('host_helper_python_required')
    home = Path(host_home) if host_home else Path.home()
    if not home.is_absolute() or not home.is_dir():
        raise PolicyError('absolute_host_home_required')
    root = Path(root)
    files, upstream = files_for(host_kind, source=source)
    from project_tool_patch import forget_response, remember_response
    files['scripts/cognee-forget.sh'] = forget_response(files['scripts/cognee-forget.sh'].decode()).encode()
    files['scripts/_remember_http.py'] = remember_response(files['scripts/_remember_http.py'].decode()).encode()
    namespace = host_kind + '-' + instance_id
    old = {'claude_code': 'claude-code', 'cursor': 'cursor', 'antigravity': 'antigravity'}[host_kind]
    for name, raw in list(files.items()):
        if Path(name).suffix in ('.py', '.sh', '.ts') or Path(name).name.startswith('run-'):
            text = raw.decode().replace('".cognee-plugin" / "' + old + '"', '".cognee-plugin" / ' + json.dumps(namespace))
            text = text.replace('.cognee-plugin/' + old, '.cognee-plugin/' + namespace)
            files[name] = text.encode()
    files['host-binding.json'] = json.dumps({'python': python, 'host_kind': host_kind}).encode()
    if host_kind == 'antigravity':
        files['scripts/run-agy-hook'] = ('#!/bin/sh\n'
            'SELF_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)\n'
            'exec ' + shlex.quote(python) + ' "$SELF_DIR/agy_hook.py" "$@"\n').encode()
    hashes = {name: hashlib.sha256(raw).hexdigest() for name, raw in files.items()}
    receipt = {**upstream, 'files': hashes,
               'tool_patch_sha256': hashlib.sha256((ENGINE / 'python/project_tool_patch.py').read_bytes()).hexdigest(),
               'builder_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    generation = hashlib.sha256(json.dumps(receipt, sort_keys=True).encode()).hexdigest()[:20]
    marketplace_name = 'memlane-' + hashlib.sha256(instance_id.encode()).hexdigest()[:12] + '-' + generation
    marketplace = root / 'managed-marketplaces' / instance_id / generation
    target = marketplace / 'plugins/cognee'
    if not target.exists():
        for name, raw in files.items():
            private_write(target / name, raw)
        # Official wrappers are shell executables (Cursor invokes them directly).
        for name in files:
            if name.startswith('scripts/') and (Path(name).suffix == '.sh' or Path(name).name.startswith('run-')):
                (target / name).chmod(0o700)
        private_write(target / '.memlane-project-access.json', json.dumps(receipt, sort_keys=True).encode())
    if json.loads((target / '.memlane-project-access.json').read_text()) != receipt:
        raise PolicyError('existing_installation_changed', 409)
    for name, expected in hashes.items():
        path = target / name
        if path.is_symlink() or hashlib.sha256(path.read_bytes()).hexdigest() != expected:
            raise PolicyError('existing_installation_changed', 409)
    environment = root / 'agent-credentials' / (instance_id + '.env')
    values = {'COGNEE_BASE_URL': service_url, 'COGNEE_API_KEY': read_secret(agent['key_file']),
              'MEMLANE_PROJECT_CONTROL_URL': control_url,
              'MEMLANE_PROJECT_CONTROL_TOKEN_FILE': agent['control_token_file'],
              'COGNEE_PLUGIN_IDENTITY': 'false', 'COGNEE_SHARED_AGENT_MEMORY': 'false',
              'COGNEE_MANAGED_ENDPOINT': 'true', 'COGNEE_CODING_MEMORY': 'true',
              'COGNEE_DISABLE_AUTO_MEMORY': 'false', 'COGNEE_UPDATE_CHECK': 'off', 'COGNEE_IDLE_DISABLED': 'true'}
    if host_kind == 'antigravity':
        values.update(COGNEE_ANTIGRAVITY_BACKEND='cloud', COGNEE_PLUGIN_IN_VENV='1')
    if any('\r' in value or '\n' in value for value in values.values()):
        raise PolicyError('installation_environment_invalid')
    private_write(environment, ''.join(key + '="' + value + '"\n' for key, value in values.items()).encode())
    private_write(target / '.memlane-installation.json', json.dumps({
        'instance_id': instance_id, 'host_kind': host_kind, 'env_file': str(environment)}).encode())
    if host_kind == 'claude_code':
        private_write(marketplace / '.claude-plugin/marketplace.json', json.dumps({
            'name': marketplace_name, 'owner': {'name': 'Memlane'},
            'plugins': [{'name': 'cognee', 'source': './plugins/cognee'}]}).encode())
    return {'plugin_dir': str(target), 'env_file': str(environment), 'home': str(home),
            'marketplace_root': str(marketplace), 'marketplace_name': marketplace_name,
            'helper_sha256': hashes['scripts/_memlane_policy.py'],
            'manifest_sha256': hashlib.sha256((target / '.memlane-project-access.json').read_bytes()).hexdigest(),
            'host_kind': host_kind, 'instance_id': instance_id, 'status': 'prepared'}
