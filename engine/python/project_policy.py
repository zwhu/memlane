"""Project policy values; no content, credentials or SDK side effects."""
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
    def __init__(self, code, status=400):
        super().__init__(code)
        self.code, self.status = code, status


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


def effective(defaults, overrides, *, protected=False):
    result = dict(PROTECTED_DEFAULTS if protected else defaults)
    result.update(overrides)
    permissions = sorted(ACCESS[result['access']])
    return {**result, 'permissions': permissions,
            'can_capture': result['capture'] and 'write' in permissions,
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
