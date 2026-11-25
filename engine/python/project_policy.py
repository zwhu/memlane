"""Project policy values; no content, credentials or SDK side effects."""
from contextvars import ContextVar
from functools import wraps
from pathlib import Path
import subprocess
import uuid

FIELDS = frozenset({'access', 'capture', 'auto_process'})
ACCESS = {'none': frozenset(), 'read': frozenset({'read'}),
          'maintain': frozenset({'read', 'write', 'delete'})}
DEFAULTS = {'access': 'maintain', 'capture': True, 'auto_process': True}
PROTECTED_PROJECT_NAMES = frozenset({'ml-user-profiles'})
PROTECTED_DEFAULTS = {'access': 'none', 'capture': False, 'auto_process': False}


def is_protected_project(project):
    if not isinstance(project, dict):
        return False
    return (project.get('name') in PROTECTED_PROJECT_NAMES
            or project.get('dataset_name') in PROTECTED_PROJECT_NAMES)


class PolicyError(Exception):
    def __init__(self, code, status=400, *, phase=None):
        super().__init__(code)
        self.code, self.status = code, status
        self.phase = phase


_connection_mutation = ContextVar('connection_mutation', default=None)


def connection_mutated():
    tracker = _connection_mutation.get()
    return tracker is not None and tracker['mutated']


def mark_connection_mutation():
    tracker = _connection_mutation.get()
    if tracker is not None:
        tracker['mutated'] = True


def phased_connection_action(method):
    @wraps(method)
    async def execute(*args, **kwargs):
        tracker = _connection_mutation.get()
        if tracker is None:
            tracker = {'mutated': False}
        token = _connection_mutation.set(tracker)
        try:
            return await method(*args, **kwargs)
        except PolicyError as error:
            if error.phase is None:
                error.phase = 'post_mutation' if tracker['mutated'] else 'pre_mutation'
            raise
        finally:
            _connection_mutation.reset(token)
    return execute


def validate_fields(values, *, allow_reset=False):
    if not isinstance(values, dict) or set(values) - FIELDS:
        raise PolicyError('invalid_policy_fields')
    for key, value in values.items():
        if value is None and allow_reset:
            continue
        if key == 'access':
            if not isinstance(value, str) or value not in ACCESS:
                raise PolicyError('invalid_access')
        elif type(value) is not bool:
            raise PolicyError('invalid_policy_boolean')
    return dict(values)


def project_capture_fields(project):
    return {'auto_capture': project.get('auto_capture', True),
            'auto_capture_version': project.get('auto_capture_version', 1),
            'auto_capture_updated_at': project.get('auto_capture_updated_at', '')}


def effective(defaults, overrides, *, protected=False, project_auto_capture=True,
              auto_capture_version=1, auto_capture_updated_at=''):
    result = dict(PROTECTED_DEFAULTS if protected else defaults)
    result.update(overrides)
    permissions = sorted(ACCESS[result['access']])
    project_enabled = bool(project_auto_capture)
    can_capture = project_enabled and result['capture'] and 'write' in permissions
    scope = 'ok'
    if not project_enabled:
        scope = 'project'
    elif not result['capture']:
        scope = 'agent' if 'capture' in overrides else 'default'
    return {**result, 'permissions': permissions,
            'can_capture': can_capture,
            'capture_policy': {'enabled': can_capture, 'project_enabled': project_enabled,
                               'version': auto_capture_version, 'scope': scope,
                               'updated_at': auto_capture_updated_at},
            'can_auto_process': result['auto_process'] and 'write' in permissions}


def workspace_root(path, *, explicit=False):
    """Host workspace or user selection only; caller must not pass tool cwd."""
    if not isinstance(path, str) or not Path(path).is_absolute():
        raise PolicyError('absolute_workspace_required')
    try:
        root = Path(path).resolve(strict=True)
        if not root.is_dir():
            raise PolicyError('workspace_not_directory')
        result = subprocess.run(['git', '-C', str(root), 'rev-parse', '--show-toplevel'],
                                stdin=subprocess.DEVNULL, capture_output=True, timeout=5, check=False,
                                env={'PATH': '/usr/bin:/bin', 'GIT_CONFIG_NOSYSTEM': '1'})
    except (OSError, subprocess.TimeoutExpired):
        raise PolicyError('workspace_unavailable') from None
    if result.returncode == 0:
        return str(Path(result.stdout.decode().strip()).resolve(strict=True))
    if explicit:
        return str(root)
    raise PolicyError('explicit_workspace_required')


def project_id(namespace, root):
    return str(uuid.uuid5(uuid.UUID(namespace), root))
