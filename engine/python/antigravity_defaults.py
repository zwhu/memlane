"""Read agy's explicit CLI model setting; no auth files or conversation guesses."""
import json
from pathlib import Path

from project_policy import PolicyError


def settings_path(home):
    return Path(home).parent / 'antigravity-cli/settings.json'


def read_defaults(home):
    path = settings_path(home)
    try:
        if path.is_symlink() or not path.is_file() or path.stat().st_size > 1024 * 1024:
            raise ValueError()
        value = json.loads(path.read_bytes())
        model = value.get('model') if isinstance(value, dict) else None
        if not isinstance(model, str) or not model.strip() or len(model) > 200 or any(ord(c) < 32 for c in model):
            raise ValueError()
        # A native alias may encode the chosen reasoning variant. Do not
        # infer a separate --effort or service tier from its text.
        return {'model': model, 'provider': 'google', 'source': 'agent_config',
                'configuration_source': 'agy_cli_settings'}
    except (OSError, ValueError, TypeError):
        raise PolicyError('agent_default_model_unavailable', 409) from None
