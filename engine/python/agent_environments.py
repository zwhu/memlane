"""Discover explicit host targets without scanning or changing user profiles."""
import os
from pathlib import Path

from project_policy import PolicyError

DEFAULTS = {'codex': '.codex', 'pi': '.pi/agent', 'traex': '.trae',
            'claude_code': '.claude', 'cursor': '.cursor', 'opencode': '.config/opencode',
            'antigravity': '.gemini/config'}
VARIABLES = {'codex': ('CODEX_HOME',), 'pi': ('PI_CODING_AGENT_DIR',),
             'traex': ('TRAE_HOME', 'TRAECLI_HOME'), 'claude_code': ('CLAUDE_CONFIG_DIR',),
             'cursor': (), 'opencode': ('OPENCODE_CONFIG_DIR',), 'antigravity': ()}
ROOT = Path(__file__).resolve().parents[2]


def profile_path(value):
    if not isinstance(value, (str, Path)) or not str(value) or '\x00' in str(value):
        raise PolicyError('invalid_host_environment')
    path = Path(value).expanduser()
    if not path.is_absolute():
        raise PolicyError('absolute_host_environment_required')
    return str(path.absolute())


def verification_profile(value):
    path = Path(value).resolve()
    try:
        parts = path.relative_to(ROOT / '.local').parts
    except ValueError:
        return False
    # Only our known verification roots; a user's folder named test is valid.
    return (any(p in ('native-setup', 'pa-verification') for p in parts)
            or (path / '.memlane-verification-profile.json').is_file())


def environment(config, state, kind, *, home=None, environ=None):
    home = Path(home or Path.home())
    environ = os.environ if environ is None else environ
    configured = config.get('agent_hosts', {}).get(kind, {}).get('profile')
    if kind == 'codex':
        configured = configured or config.get('auth_home')
    default = home / DEFAULTS[kind]
    if kind == 'opencode' and environ.get('XDG_CONFIG_HOME'):
        default = Path(environ['XDG_CONFIG_HOME']) / 'opencode'
    hints = [profile_path(environ[v]) for v in VARIABLES[kind] if environ.get(v)]
    selected = state.get('host_targets', {}).get(kind)
    target = profile_path(selected or configured or (hints[0] if hints else default))
    candidates = []
    seen = set()
    for label, value in [('default', default), *[('environment', h) for h in hints],
                         *([('configured', configured)] if configured else []),
                         *([('selected', selected)] if selected else [])]:
        path = profile_path(value)
        if path not in seen:
            candidates.append({'profile': path, 'source': label, 'exists': Path(path).is_dir(),
                               'verification': verification_profile(path)})
            seen.add(path)
    issue = ''
    if verification_profile(target) and not config.get('allow_verification_agent_profiles', False):
        issue = 'verification_environment'
    elif not selected and len(set([target, *hints])) > 1:
        issue = 'environment_ambiguous'
    elif Path(target).exists() and not Path(target).is_dir():
        issue = 'environment_unavailable'
    elif configured and not Path(target).exists():
        issue = 'environment_missing'
    return {'profile': target, 'environment_issue': issue, 'environments': candidates,
            'environment_source': 'selected' if selected else 'configured' if configured else 'environment' if hints else 'default'}


def validate_daily_environments(config):
    for kind in DEFAULTS:
        row = environment(config, {}, kind)
        if row['environment_issue'] == 'verification_environment':
            raise ValueError(kind + ': verification environment cannot be used for daily Agent connection: ' + row['profile'])
