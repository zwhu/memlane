"""Standard-library bridge selection and dependency checks for the launcher."""
import hashlib
import json
import os
from pathlib import Path
import shutil

from project_policy import PolicyError


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
    import tomllib
    choices = {}
    for kind in ('codex', 'pi', 'traex'):
        selected = resolve_bridge_runtime(runtime, {'bridge_kind': kind})
        if not selected['executable'] or not selected['home']:
            continue
        home = Path(selected['home']).expanduser()
        name = {'codex': 'config.toml', 'pi': 'settings.json', 'traex': 'traecli.toml'}[kind]
        path = home / name
        try:
            if path.is_symlink() or path.stat().st_size > 1024 * 1024:
                continue
            settings = json.loads(path.read_text()) if kind == 'pi' else tomllib.loads(path.read_text())
            model = settings.get('defaultModel' if kind == 'pi' else 'model')
            if not isinstance(model, str) or not model.strip() or len(model) > 200:
                continue
            profile = {'mode': 'agent_bridge', 'bridge_kind': kind, 'model': model}
            if kind == 'pi':
                provider = settings.get('defaultProvider')
                if provider not in ('deepseek', 'ollama'):
                    continue
                profile['bridge_provider'] = provider
            choices[kind] = profile
        except (OSError, ValueError, TypeError):
            continue
    return choices
