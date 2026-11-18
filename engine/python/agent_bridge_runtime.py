"""Standard-library bridge selection and dependency checks for the launcher."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import copy
import sqlite3

from project_policy import PolicyError


def current_session_model(kind, home):
    """Read the calling Agent's official session metadata, never chat bodies.

    An explicit session identity must exist in the selected home. Do not pick
    an arbitrary recent thread when several Agent windows are open.
    """
    if kind not in ('codex', 'traex'):
        return None
    names = ('CODEX_THREAD_ID', 'CODEX_SESSION_ID') if kind == 'codex' else ('TRAE_THREAD_ID', 'TRAE_SESSION_ID', 'CODEX_THREAD_ID')
    identity = next((os.environ[name] for name in names if os.environ.get(name)), None)
    if not identity or len(identity) > 128:
        return None
    root = Path(home) / 'cli' if kind == 'traex' else Path(home)
    candidates = sorted(root.glob('state_*.sqlite'), key=lambda path:path.name, reverse=True)
    for path in candidates:
        try:
            with sqlite3.connect(path.absolute().as_uri() + '?mode=ro', uri=True, timeout=1) as connection:
                connection.execute('PRAGMA query_only=ON')
                columns = {r[1] for r in connection.execute('PRAGMA table_info(threads)')}
                if 'id' not in columns:
                    continue
                fields = [name for name in ('model', 'model_provider', 'reasoning_effort', 'service_tier') if name in columns]
                if not fields:
                    continue
                row = connection.execute('SELECT ' + ','.join(fields) + ' FROM threads WHERE id=?', (identity,)).fetchone()
                if row is None:
                    continue
                values = dict(zip(fields, row))
                if not values.get('model'):
                    raise PolicyError('agent_session_model_unavailable', 409)
                return {**{key:value for key,value in values.items() if value is not None}, 'session_id':identity}
        except (OSError, sqlite3.Error):
            raise PolicyError('agent_session_state_unavailable', 409) from None
    return None


def read_agent_defaults(kind, home):
    """Read model scalars only; never use a saved Memlane model as a fallback."""
    import tomllib
    home = Path(home)
    name = {'codex': 'config.toml', 'traex': 'traecli.toml', 'pi': 'settings.json'}.get(kind)
    if name is None:
        raise PolicyError('unsupported_generation_bridge', 409)
    try:
        path = home / name
        if not path.is_file() or path.stat().st_size > 1024 * 1024:
            raise ValueError()
        raw = path.read_bytes()
        if len(raw) > 1024 * 1024:
            raise ValueError()
        value = json.loads(raw) if kind == 'pi' else tomllib.loads(raw.decode())
        if not isinstance(value, dict):
            raise ValueError()
        if kind != 'pi' and value.get('profile'):
            selected = value.get('profiles', {}).get(value['profile'])
            if not isinstance(selected, dict):
                raise ValueError()
            value = {**value, **selected}
        session = current_session_model(kind, home)
        if session:
            value = {**value, **{name:session[name] for name in ('model','model_provider','service_tier') if name in session}}
            if 'reasoning_effort' in session:
                value['model_reasoning_effort'] = session['reasoning_effort']
        model = value.get('defaultModel' if kind == 'pi' else 'model')
        if not isinstance(model, str) or not model.strip() or len(model) > 200 or any(ord(c) < 32 for c in model):
            raise ValueError()
        result = {'model': model, 'source':'agent_session' if session else 'agent_config'}
        if session:
            result['session_id'] = session['session_id']
        if kind == 'pi':
            provider = value.get('defaultProvider')
            if provider not in ('deepseek', 'ollama'):
                raise PolicyError('pi_provider_schema_unsupported', 409)
            result['provider'] = provider
            option, field = value.get('defaultThinkingLevel'), 'thinking_level'
        else:
            provider = 'trae' if kind == 'traex' else 'openai'
            if value.get('model_provider') not in (None, provider):
                raise PolicyError('agent_default_provider_unsupported', 409)
            result['provider'] = provider
            option, field = value.get('model_reasoning_effort'), 'reasoning_effort'
            if value.get('service_tier') is not None:
                tier = value['service_tier']
                if not isinstance(tier, str) or not tier or len(tier) > 32 or any(ord(c) < 32 for c in tier):
                    raise ValueError()
                result['service_tier'] = tier
        if option is not None:
            if not isinstance(option, str) or not option or len(option) > 32 or any(ord(c) < 32 for c in option):
                raise ValueError()
            result[field] = option
        return result
    except (OSError, ValueError, TypeError, KeyError, AttributeError):
        raise PolicyError('agent_default_model_unavailable', 409) from None


def snapshot_agent_defaults(args):
    """Freeze defaults at request admission, sharing only lifecycle ownership."""
    if getattr(args, 'agent_defaults_snapshot', None) is not None:
        return args
    selection = read_agent_defaults(getattr(args, 'bridge_kind', 'codex'), args.auth_home)
    frozen = copy.copy(args)
    frozen.model = selection['model']
    if getattr(args, 'bridge_kind', 'codex') == 'pi':
        frozen.bridge_provider = selection['provider']
    frozen.agent_defaults_snapshot = selection
    return frozen


def resolve_bridge_runtime(runtime, profile=None):
    kind = (profile or {}).get('bridge_kind', 'codex')
    if kind not in ('codex', 'pi', 'traex'):
        raise PolicyError('unsupported_generation_bridge', 409)
    settings = runtime.get('agent_hosts', {}).get(kind, {})
    command = {'codex': 'codex', 'pi': 'pi', 'traex': 'traecli'}[kind]
    explicit = settings.get('executable') or runtime.get(kind + '_executable')
    executable = explicit or shutil.which(command)
    if not executable:
        executable = next((str(base / command) for base in (Path.home()/'.local/bin', Path('/opt/homebrew/bin'), Path('/usr/local/bin'))
                           if (base / command).is_file()), None)
    directory = settings.get('profile') or runtime.get('auth_home' if kind == 'codex' else kind + '_auth_home')
    if not directory and kind != 'codex':
        directory = str(Path.home() / ('.pi/agent' if kind == 'pi' else '.trae'))
    return {'kind': kind, 'executable': executable, 'home': directory}


def check_bridge_runtime(runtime, profile=None):
    selected = resolve_bridge_runtime(runtime, profile)
    try:
        executable = Path(selected['executable']).expanduser()
        home = Path(selected['home']).expanduser()
        auth = home / ('cli/auth.json' if selected['kind'] == 'traex' else 'auth.json')
        if not executable.is_absolute() or not executable.is_file() or not os.access(executable, os.X_OK) or not home.is_absolute() or not home.is_dir():
            raise ValueError()
        if auth.is_symlink() or not auth.is_file() or auth.stat().st_mode & 0o077:
            raise ValueError()
    except (KeyError, OSError, ValueError, TypeError):
        raise PolicyError('generation_bridge_authentication_required', 409) from None
    return {**selected, 'executable': executable, 'home': home}


def bridge_runtime_fingerprint(runtime, profile):
    selected = check_bridge_runtime(runtime, profile)
    executable = selected['executable'].resolve(strict=True)
    metadata = executable.stat()
    value = {'kind': selected['kind'], 'executable': str(executable), 'home': str(selected['home'].resolve(strict=True)),
             'file': [metadata.st_dev, metadata.st_ino, metadata.st_size, metadata.st_mtime_ns]}
    if selected['kind'] == 'pi':
        extension = Path(__file__).resolve().parents[1] / 'prompts/pi-schema-output.ts'
        path = selected['home'] / 'models.json'
        if path.is_symlink() or (path.exists() and path.stat().st_size > 1024 * 1024):
            raise PolicyError('pi_provider_configuration_invalid', 409)
        models = path.read_bytes() if path.exists() else None
        value['models_sha256'] = hashlib.sha256(models or b'').hexdigest()
        value['extension_sha256'] = hashlib.sha256(extension.read_bytes()).hexdigest()
    return hashlib.sha256(json.dumps(value, sort_keys=True).encode()).hexdigest()


def require_bridge_validation(store, runtime, row):
    profile = row['profile']
    if profile['mode'] != 'agent_bridge':
        return
    records = [r for r in store.state['validations'].values() if r.get('digest') == row['digest'] and r.get('status') == 'passed']
    latest = max(records, key=lambda r: r.get('registered_revision', 0), default={})
    expected = latest.get('bridge_runtime_fingerprint')
    # Preserve existing Codex validations. New bridges require current evidence.
    if expected is None and profile['bridge_kind'] == 'codex':
        return
    if expected is None or expected != bridge_runtime_fingerprint(runtime, profile):
        raise PolicyError('generation_bridge_changed_revalidate', 409)


def bridge_choices(runtime):
    """Read only each Agent's selected model scalars, never authentication."""
    choices = {}
    for kind in ('codex', 'pi', 'traex'):
        selected = resolve_bridge_runtime(runtime, {'bridge_kind': kind})
        if not selected['executable'] or not selected['home']:
            continue
        try:
            defaults = read_agent_defaults(kind, Path(selected['home']).expanduser())
            profile = {'mode': 'agent_bridge', 'bridge_kind': kind, 'model': defaults['model']}
            if kind == 'pi':
                profile['bridge_provider'] = defaults['provider']
            choices[kind] = profile
        except PolicyError:
            continue
    return choices


def bridge_default_states(runtime):
    result = {}
    for kind in ('codex', 'pi', 'traex'):
        selected = resolve_bridge_runtime(runtime, {'bridge_kind':kind})
        if selected['executable'] and selected['home']:
            try:
                result[kind] = read_agent_defaults(kind, Path(selected['home']).expanduser())
            except PolicyError:
                pass
    return result


def require_connected_bridge(registry, runtime, profile):
    from agent_connections import Connections
    kind = profile['bridge_kind']
    rows = Connections(registry, runtime).view()['connections']
    if not any(row['kind'] == kind and row['status'] == 'configured_pending_new_session' for row in rows):
        raise PolicyError('generation_agent_not_connected', 409)
    check_bridge_runtime(runtime, profile)
