"""Client configuration only; extraction and watermarks belong to Cognee."""
from datetime import datetime, timezone

from project_policy import PolicyError


def settings(state, project_id=None):
    defaults = state.get('tool_graph', {})
    enabled = defaults.get('enabled', False)
    if type(enabled) is not bool:
        raise PolicyError('tool_graph_config_unconfirmed', 503)
    override = None
    project = None
    if project_id is not None:
        project = state['projects'].get(project_id)
        if project is None:
            raise PolicyError('project_not_found', 404)
        override = project.get('tool_graph', {}).get('enabled')
        if override is not None and type(override) is not bool:
            raise PolicyError('tool_graph_config_unconfirmed', 503)
    changed = project.get('tool_graph', {}).get('changed_at') if project is not None else None
    if override is None:
        changed = max(filter(None, [changed, defaults.get('changed_at')]), default=None)
    return {'global_enabled': enabled, 'override': override,
            'effective': enabled if override is None else override,
            'source': 'global' if override is None else 'project',
            'changed_at': changed}


async def configure(registry, expected, enabled, project_id=None):
    if type(enabled) is not bool and not (project_id is not None and enabled is None):
        raise PolicyError('invalid_tool_graph_enabled')
    async with registry.mutex:
        registry.check_revision(expected)
        settings(registry.state, project_id)
        target = registry.state if project_id is None else registry.state['projects'][project_id]
        old = target.get('tool_graph', {}).get('enabled', False if project_id is None else None)
        if old is not enabled:
            target['tool_graph'] = {'enabled': enabled, 'changed_at': datetime.now(timezone.utc).isoformat()}
            registry.state['revision'] += 1
            registry.save()
        return registry.directory()
