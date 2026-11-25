"""Prepare the owned first-use runtime, then use the existing dev lifecycle.

The store must already have been initialized by first_install_store. This module
does not create a Cognee database, copy Agent authentication or call a model.
"""
import argparse
import fcntl
import json
import os
from pathlib import Path
import secrets
import stat
import socket
import subprocess
import re
from datetime import datetime
import sys
import urllib.request

sys.path.insert(0, str(Path(__file__).resolve().parent))
ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / 'scripts'))

import agent_discovery
from first_install_store import StoreInitializationError, read_private, write_private, validate_spec
from project_control import loopback_origin
from project_policy import PolicyError


def owned_store(root):
    root = Path(root)
    specification = json.loads(read_private(root / 'first-install-input.json'))
    if Path(specification['root']) != root:
        raise ValueError('first installation store path differs')
    validate_spec(specification)
    receipt = json.loads(read_private(root / 'store-identity.json'))
    if (receipt.get('schema') != 1 or not receipt.get('storage_initialized') or
            receipt.get('store_id') != specification['store_id'] or
            receipt.get('source_commit') != specification['source_commit'] or
            not receipt.get('owner_id') or not receipt.get('key_id')):
        raise ValueError('first installation SDK receipt not confirmed')
    if not read_private(root / 'owner.token').strip():
        raise ValueError('first installation owner credential unavailable')
    return specification, receipt


def secret_file(path):
    path = Path(path)
    if path.exists() or path.is_symlink():
        if len(read_private(path).strip()) < 32:
            raise ValueError('existing runtime secret is incomplete; retained')
    else:
        write_private(path, secrets.token_urlsafe(48))
    return str(path)


def unused_origins(count):
    # Reserve them together so this runtime's three origins are distinct.
    sockets = []
    try:
        for _ in range(count):
            listener = socket.socket()
            listener.bind(('127.0.0.1', 0))
            sockets.append(listener)
        return ['http://127.0.0.1:' + str(listener.getsockname()[1]) for listener in sockets]
    finally:
        for listener in sockets:
            listener.close()


def _managed_runtime_dir_for_graph_assets(path_value):
    if not isinstance(path_value, (str, Path)) or not path_value:
        return None
    path = Path(path_value)
    if not path.is_absolute() or '..' in path.parts or path.parts[-3:] != ('web', 'official-graph', 'dist'):
        return None
    runtime_dir = path.parents[2]
    if re.fullmatch(r'runtime-[0-9a-f]{16}', runtime_dir.name) is None:
        return None
    for component in (path, *path.parents[:3]):
        try:
            if not stat.S_ISDIR(component.lstat().st_mode):
                return None
        except FileNotFoundError:
            continue  # An older managed generation may already have been removed.
        except OSError:
            return None
    return runtime_dir


def _verify_runtime_marker(runtime_dir):
    marker = runtime_dir / '.memlane-runtime.json'
    try:
        mode = marker.lstat().st_mode
        if not stat.S_ISREG(mode) or mode & 0o077:
            return False
        data = json.loads(read_private(marker))
    except (OSError, ValueError, RecursionError, StoreInitializationError):
        return False
    digest = data.get('sha256') if isinstance(data, dict) else None
    return (isinstance(digest, str) and re.fullmatch(r'[0-9a-f]{64}', digest) is not None
            and runtime_dir.name == 'runtime-' + digest[:16])


def _is_managed_runtime_graph_assets(path_value, *install_roots, allow_marked_root=False):
    runtime_dir = _managed_runtime_dir_for_graph_assets(path_value)
    if runtime_dir is None:
        return False
    marker = runtime_dir / '.memlane-runtime.json'
    marked = marker.exists() or marker.is_symlink()
    return ((runtime_dir.parent in install_roots or (allow_marked_root and marked))
            and (not marked or _verify_runtime_marker(runtime_dir)))


def _validate_runtime_store(config, root, specification):
    first_install = config.get('first_install')
    if (not isinstance(first_install, dict) or first_install.get('store_id') != specification['store_id']
            or config.get('official_data') != str(root)
            or config.get('service_commit') != specification['source_commit']
            or config.get('token_file') != str(root / 'owner.token')
            or config.get('generation_file') != str(root / 'generation.json')):
        raise ValueError('existing configuration belongs to another installation; retained')


def _refresh_existing_runtime(config_path, root, specification, candidate, installation_root, *, refresh=True):
    # Go's graph-assets refresh locks the same directory inode across renames.
    parent = os.open(config_path.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        fcntl.flock(parent, fcntl.LOCK_EX)
        for _ in range(2):
            original = read_private(config_path)
            config = json.loads(original)
            _validate_runtime_store(config, root, specification)
            if not refresh:
                return
            old_assets = config.get('graph_assets', '')
            roots = (config_path.parent, config_path.parent / 'components' / 'installation')
            if not _is_managed_runtime_graph_assets(candidate, *roots, installation_root, allow_marked_root=True):
                return
            manifest = candidate / 'assets.json'
            if not (candidate.is_dir() and manifest.is_file()) or manifest.is_symlink():
                return
            if str(candidate) == old_assets or (old_assets != '' and not _is_managed_runtime_graph_assets(
                    old_assets, *roots, candidate.parents[2].parent)):
                return
            if read_private(config_path) == original:
                config['graph_assets'] = str(candidate)
                write_private(config_path, json.dumps(config, indent=2))
                return
    finally:
        os.close(parent)


def prepare_runtime(root, config_path, plugin, *, python=None, host_home=None, graph_assets=None,
                    installation_root=None, skip_graph_refresh=False, existing_only=False):
    root, config_path = Path(root), Path(config_path)
    if not config_path.is_absolute() or config_path.parent != root.parent:
        raise ValueError('first runtime config must be beside the owned store')
    config_exists = config_path.exists() or config_path.is_symlink()
    if existing_only and not config_exists:
        raise ValueError('existing first runtime configuration required')
    specification, _ = owned_store(root)
    if config_exists:
        candidate = Path(graph_assets) if graph_assets and str(graph_assets).strip() else None
        default_assets = ROOT / 'web/official-graph/dist'
        if candidate is None and (default_assets / 'assets.json').is_file():
            candidate = default_assets
        _refresh_existing_runtime(config_path, root, specification, candidate,
                                  Path(installation_root) if installation_root is not None else None, refresh=not skip_graph_refresh)
        from dev import load_config
        return load_config(config_path)
    source_manifest = json.loads((ROOT / 'engine/coding-memory-components.json').read_text())
    if source_manifest['service']['commit'] != specification['source_commit']:
        raise ValueError('runtime adapters differ from initialized SDK')
    environment_file = root / 'service-environment.json'
    environment = {'EMBEDDING_PROVIDER': 'fastembed', 'EMBEDDING_MODEL': 'BAAI/bge-small-zh-v1.5',
                   'EMBEDDING_DIMENSIONS': '512', 'EMBEDDING_MAX_TOKENS': '512',
                   'HF_HOME': specification['model_cache'], 'HF_HUB_OFFLINE': '1',
                   'DEFAULT_USER_EMAIL': 'memlane-local@example.com'}
    if environment_file.exists() or environment_file.is_symlink():
        previous = json.loads(read_private(environment_file))
        if any(previous.get(key) != value for key, value in environment.items()):
            raise ValueError('first runtime embedding settings changed; retained')
        if set(previous) != set(environment) | {'FASTAPI_USERS_JWT_SECRET', 'FASTAPI_USERS_RESET_PASSWORD_TOKEN_SECRET',
                                                'FASTAPI_USERS_VERIFICATION_TOKEN_SECRET'}:
            raise ValueError('first runtime environment contains unexpected fields')
        environment = previous
    else:
        for key in ('FASTAPI_USERS_JWT_SECRET', 'FASTAPI_USERS_RESET_PASSWORD_TOKEN_SECRET',
                    'FASTAPI_USERS_VERIFICATION_TOKEN_SECRET'):
            environment[key] = secrets.token_urlsafe(48)
        write_private(environment_file, json.dumps(environment))
    resolved_graph_assets = ''
    if graph_assets is not None:
        resolved_graph_assets = str(Path(graph_assets)) if str(graph_assets).strip() else ''
    elif (ROOT / 'web/official-graph/dist/assets.json').is_file():
        resolved_graph_assets = str(ROOT / 'web/official-graph/dist')
    service, control, completion = unused_origins(3)
    config = {
        'python': str(python or sys.executable), 'official_data': str(root),
        'official_source': specification['source'], 'official_archive': specification['archive'],
        'service_commit': specification['source_commit'], 'token_file': str(root / 'owner.token'),
        'service_environment_file': str(environment_file), 'service_url': service,
        'completion_url': completion, 'completion_token_file': secret_file(root / 'completion.token'),
        'generation_file': str(root / 'generation.json'), 'generation_mode': 'unconfigured', 'model': '',
        'client_state_file': str(root / 'client-state.json'), 'graph_assets': resolved_graph_assets,
        'projects': [], 'common_datasets': [],
        'first_install': {'schema': 1, 'store_id': specification['store_id'], 'setup': True},
        'project_control': {'url': control, 'registry_file': str(root / 'projects.json'),
                            'management_token_file': secret_file(root / 'management.token'),
                            'plugin_dir': str(Path(plugin).absolute()), 'agent_control_token_files': {}}
    }
    host = Path(host_home) if host_home is not None else Path.home()
    from agent_connections import detect_executable
    executable = detect_executable('codex', {})
    if not executable and host_home is not None:
        candidate = agent_discovery.detect_executable('codex', home=host, which=lambda _: None)
        if candidate:
            executable = detect_executable('codex', {'executable': candidate})
    if executable:
        config['codex_executable'] = executable
        config['auth_home'] = agent_discovery.default_profile('codex', home=host)
    from dev import validate_project_control
    validate_project_control(config['project_control'], config)
    from first_install_mode import first_install_setup
    if not first_install_setup(config):
        raise ValueError('first installation mode not confirmed')
    write_private(config_path, json.dumps(config, indent=2))
    return config


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def confirm_service_owner(config):
    _, expected = owned_store(Path(config['official_data']))
    request = urllib.request.Request(loopback_origin(config['service_url']) + '/api/v1/users/me',
                                    headers={'X-Api-Key': read_private(config['token_file']).strip()})
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), _NoRedirect())
    with opener.open(request, timeout=10) as response:
        if response.status != 200 or json.load(response).get('id') != expected['owner_id']:
            raise ValueError('authenticated first service owner differs')


def activation_scope(config, request, *, allow_applied=False):
    from generation_config import validation_passed
    specification, _ = owned_store(config['official_data'])
    if (request.get('schema') != 1 or request.get('store_id') != specification['store_id'] or
            not re.fullmatch(r'[A-Za-z0-9_-]{8,80}', request.get('operation_id', ''))):
        raise ValueError('invalid first activation identity')
    state = json.loads(read_private(config['generation_file']))
    reference = request.get('reference')
    if not isinstance(reference, dict) or set(reference) != {'id', 'revision'}:
        raise ValueError('explicit generation reference required')
    rows = state.get('profiles', {}).get(reference['id'], [])
    row = next((row for row in rows if row['revision'] == reference['revision']), None)
    if row is None or row['digest'] != request.get('digest'):
        raise ValueError('first activation requires the same validated generation')
    applied = state.get('active') == reference
    legacy_applied = (allow_applied and applied and row['profile']['mode'] == 'agent_bridge'
                      and state.get('connected_agent_selections', {}).get(row['digest']))
    if not legacy_applied and not validation_passed(state.get('validations', {}), row['digest']):
        raise PolicyError('generation_validation_required', 409)
    if any(row.get('status') in ('queued', 'running') for row in state.get('validations', {}).values()):
        raise ValueError('generation validation still active')
    if not (allow_applied and applied):
        if state.get('active') is not None or state.get('next') != reference or state.get('revision') != request.get('revision'):
            raise ValueError('first activation selection changed; review required')
    return applied


def prepare_activation(config, config_path, request_path, *, host_home=None):
    # Keep simultaneous App instances from issuing overlapping shutdowns. The
    # service-owner lock remains the authority for the service itself; it cannot
    # be held here while asking the existing launcher to shut down.
    root = Path(config['official_data'])
    owned_store(root)
    descriptor = os.open(root / 'first-install-activation.lock',
                         os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
    with os.fdopen(descriptor, 'a+') as lock:
        identity = os.fstat(lock.fileno())
        if not stat.S_ISREG(identity.st_mode) or identity.st_mode & 0o077:
            raise ValueError('activation lock must be a private regular file')
        fcntl.flock(lock.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        return _prepare_activation(config, config_path, request_path, host_home=host_home)


def _check_activation_scope(config, request, request_path):
    try:
        activation_scope(config, request, allow_applied=request['status'] == 'starting')
    except (ValueError, PolicyError, OSError, StoreInitializationError) as error:
        if request.get('status') in ('approved', 'stopping'):
            request.update(status='rejected', phase='pre_mutation', error_code=(
                error.code if isinstance(error, PolicyError) else 'first_activation_precondition_failed'))
            write_private(request_path, json.dumps(request))
        raise


def _prepare_activation(config, config_path, request_path, *, host_home=None):
    root, config_path, request_path = Path(config['official_data']), Path(config_path), Path(request_path)
    if request_path != root / 'first-install-activation.json':
        raise ValueError('activation receipt must belong to the same first store')
    request = json.loads(read_private(request_path))
    if request.get('status') == 'completed':
        specification, _ = owned_store(root)
        if request.get('store_id') != specification['store_id'] or not (root / 'client-state.json').is_file():
            raise ValueError('completed activation identity differs')
        return False
    if request.get('status') not in ('approved', 'stopping', 'starting'):
        raise ValueError('activation outcome needs read-only review before another attempt')
    _check_activation_scope(config, request, request_path)
    owner_path = root / '.memlane-dev-owner.json'
    if owner_path.exists():
        owner = json.loads(read_private(owner_path))
        if owner.get('phase') != 'first_install_setup':
            raise ValueError('another startup is already in progress; retain its owner')
        if request['status'] != 'starting':
            request['status'] = 'stopping'
            write_private(request_path, json.dumps(request))
        try:
            stop_kwargs = {'stdin': subprocess.DEVNULL, 'stdout': subprocess.PIPE, 'stderr': subprocess.STDOUT, 'check': True}
            if host_home is not None:
                stop_kwargs['env'] = {**os.environ, 'HOME': str(host_home)}
            subprocess.run([sys.executable, str(ROOT / 'scripts/dev.py'), 'stop', '--config', str(config_path)],
                           **stop_kwargs)
        except subprocess.SubprocessError as error:
            request.update(status='result_unknown', error_code='owned_stop_unconfirmed')
            if isinstance(error, subprocess.CalledProcessError):
                request['exit_code'] = error.returncode
            write_private(request_path, json.dumps(request))
            raise ValueError('owned setup shutdown is unconfirmed; original state retained') from None
    from dev import acquire
    with acquire(root):
        if owner_path.exists():
            raise ValueError('previous service owner still requires recovery')
        # The same store lock prevents another service writer between the
        # confirmed shutdown, selection check and persistent activation intent.
        parent = os.open(config_path.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
        try:
            fcntl.flock(parent, fcntl.LOCK_EX)
            latest = json.loads(read_private(config_path))
            specification, _ = owned_store(root)
            _validate_runtime_store(latest, root, specification)
            _check_activation_scope(latest, request, request_path)
            updated = {**latest, 'first_install': {**latest['first_install'], 'setup': False}}
            request['status'] = 'starting'
            write_private(request_path, json.dumps(request))
            write_private(config_path, json.dumps(updated, indent=2))
        finally:
            os.close(parent)
    return True


def finish_activation(config, config_path):
    if config.get('first_install') is None:
        return
    root = Path(config['official_data'])
    request_path = root / 'first-install-activation.json'
    if not request_path.exists():
        return
    request = json.loads(read_private(request_path))
    if request.get('status') == 'completed':
        return
    if request.get('status') != 'starting':
        raise ValueError('first activation receipt does not permit completion')
    if config.get('generation_reference') != request.get('reference'):
        raise ValueError('applied first generation differs from approved reference')
    activation_scope(config, request, allow_applied=True)
    # Called only after the existing normal startup has verified applied PID,
    # frozen digest, authenticated content readiness and initialization state.
    client = json.loads(read_private(config['client_state_file']))
    if client.get('schema_version') != 1 or not client.get('initialization_completed_at'):
        raise ValueError('client initialization completion not confirmed')
    if not client.get('first_used_timezone') or datetime.fromisoformat(client['initialization_completed_at'].replace('Z', '+00:00')).tzinfo is None:
        raise ValueError('initialization timezone not confirmed')
    datetime.strptime(client['first_used_date'], '%Y-%m-%d')
    base = {key: value for key, value in config.items() if key not in ('generation_reference', 'generation_applied_file')}
    write_private(config_path, json.dumps(base, indent=2))
    request.update(status='completed', applied_reference=config['generation_reference'])
    write_private(request_path, json.dumps(request))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--store-root', required=True, type=Path)
    parser.add_argument('--runtime-config', required=True, type=Path)
    parser.add_argument('--plugin', required=True, type=Path)
    parser.add_argument('--host-home', type=Path)
    parser.add_argument('--prepare-only', action='store_true')
    parser.add_argument('--python-executable', type=Path)
    parser.add_argument('--graph-assets', type=Path)
    parser.add_argument('--skip-graph-refresh', action='store_true')
    parser.add_argument('--existing-only', action='store_true')
    parser.add_argument('--installation-root', type=Path)
    parser.add_argument('--activate-file', type=Path)
    args = parser.parse_args()
    if args.host_home is not None:
        if not args.host_home.is_absolute() or not args.host_home.is_dir():
            raise ValueError('existing Agent host home required')
    if args.python_executable is not None:
        if not args.python_executable.is_absolute() or not os.access(args.python_executable, os.X_OK):
            raise ValueError('existing runtime Python executable required')
    config = prepare_runtime(args.store_root, args.runtime_config, args.plugin,
                             python=args.python_executable, host_home=args.host_home,
                             graph_assets=args.graph_assets, installation_root=args.installation_root,
                             skip_graph_refresh=args.skip_graph_refresh, existing_only=args.existing_only)
    if args.activate_file is not None:
        if args.prepare_only:
            raise ValueError('activation cannot be requested during read-only preparation')
        if not prepare_activation(config, args.runtime_config, args.activate_file, host_home=args.host_home):
            return
    if args.prepare_only:
        return
    environment = {key: os.environ[key] for key in ('HOME', 'PATH', 'LANG', 'TMPDIR') if key in os.environ}
    if args.host_home is not None:
        environment['HOME'] = str(args.host_home)
    environment['PYTHONDONTWRITEBYTECODE'] = '1'
    os.execve(config['python'], [config['python'], str(ROOT / 'scripts/dev.py'), 'start',
                               '--config', str(args.runtime_config), '--no-ui'], environment)


if __name__ == '__main__':
    main()
