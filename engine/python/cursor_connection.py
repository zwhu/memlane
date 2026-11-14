"""Cursor's documented workspaceOpen loader for immutable official plugins.

This only selects an artifact. Collection and recall stay in the official
plugin; each open workspace receives an absolute generation path.
"""
import json
import shlex

from project_policy import PolicyError


def loader(installation, python):
    result = json.dumps({'pluginPaths': [installation['plugin_dir']]})
    return {'command': shlex.join([python, '-c', 'print(' + repr(result) + ')'])}


def proposal(raw, previous, replacement):
    try:
        config = json.loads(raw) if raw else {'version': 1, 'hooks': {}}
        if (not isinstance(config, dict) or type(config.get('version')) is not int
                or config['version'] != 1 or not isinstance(config.get('hooks'), dict)):
            raise ValueError()
        hooks = config['hooks']
        if any(not isinstance(items, list) or any(not isinstance(item, dict) for item in items)
               for items in hooks.values()):
            raise ValueError()
        entries = hooks.get('workspaceOpen', [])
        if previous is not None and entries.count(previous) != 1:
            raise PolicyError('cursor_loader_changed_requires_review', 409)
        for event, items in hooks.items():
            if any('cognee' in str(item).lower() and not (event == 'workspaceOpen' and item == previous)
                   for item in items):
                raise PolicyError('duplicate_cursor_memory_hook_requires_review', 409)
        # Replace only our exact recorded entry. Preserve other hooks and
        # settings, including their ordering and fields unknown to Memlane.
        if previous is None:
            if replacement is not None:
                hooks.setdefault('workspaceOpen', []).append(replacement)
        elif replacement is not None:
            entries[entries.index(previous)] = replacement
        return (json.dumps(config, ensure_ascii=False, indent=2) + '\n').encode()
    except (ValueError, TypeError):
        raise PolicyError('cursor_configuration_requires_review', 409) from None
