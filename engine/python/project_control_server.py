"""Narrow authenticated loopback API for project configuration, never content."""
import argparse
import asyncio
import json
from pathlib import Path
import secrets

from aiohttp import ClientSession, web

from project_control import Official, Registry, loopback_origin, read_secret
from project_policy import PolicyError


def fields(body, required, optional=()):
    if not isinstance(body, dict) or set(body) - set(required) - set(optional) or set(required) - set(body):
        raise PolicyError('invalid_request_fields')
    return body


def create_app(registry, manager_token, agent_tokens, *, plugin_source=None, control_url=None):
    if len(manager_token) < 32 or any(len(token) < 32 for token in agent_tokens.values()):
        raise ValueError('control tokens must contain at least 32 characters')
    tokens = [manager_token, *agent_tokens.values()]
    if len(set(tokens)) != len(tokens):
        raise ValueError('control credentials must be independent')

    @web.middleware
    async def boundary(request, handler):
        try:
            # No browser-origin clients; desktop and hook callers carry independent tokens.
            if request.headers.get('Origin'):
                raise PolicyError('browser_origin_rejected', 403)
            token = request.headers.get('Authorization', '')
            request['manager'] = secrets.compare_digest(token, 'Bearer ' + manager_token)
            registered_tokens = dict(agent_tokens)
            for key, value in registry.state['agents'].items():
                if value.get('control_token_file'):
                    registered_tokens[key] = read_secret(value['control_token_file'])
            request['agent'] = next((key for key, value in registered_tokens.items()
                                     if secrets.compare_digest(token, 'Bearer ' + value)), None)
            if not request['manager'] and request['agent'] is None:
                raise PolicyError('control_authentication_required', 401)
            return await asyncio.wait_for(handler(request), timeout=120)
        except PolicyError as error:
            return web.json_response({'error': error.code}, status=error.status)
        except (ValueError, KeyError, TypeError):
            return web.json_response({'error': 'invalid_request'}, status=400)
        except asyncio.TimeoutError:
            return web.json_response({'error': 'configuration_result_unknown'}, status=503)

    app = web.Application(client_max_size=16 * 1024, middlewares=[boundary])

    def manager(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)

    def agent(request):
        if request['agent'] is None:
            raise PolicyError('agent_identity_required', 403)
        return request['agent']

    async def body(request):
        if request.content_type != 'application/json':
            raise PolicyError('json_required', 415)
        return await request.json()

    async def resolve(request):
        identity = agent(request)
        payload = fields(await body(request), ('workspace', 'host_session_key'), ('explicit_workspace',))
        if type(payload.get('explicit_workspace', False)) is not bool:
            raise PolicyError('invalid_workspace_flag')
        return web.json_response(await registry.resolve(identity, payload['workspace'], payload['host_session_key'],
                                                       explicit=payload.get('explicit_workspace', False)))

    async def policy(request):
        identity = agent(request)
        async with registry.mutex:
            project = registry.state['projects'].get(request.query.get('project_id'))
            if project is None:
                raise PolicyError('project_not_found', 404)
            return web.json_response(await registry.checked_policy(project, identity))

    async def directory(request):
        manager(request)
        async with registry.mutex:
            return web.json_response(registry.directory())

    async def datasets(request):
        manager(request)
        async with registry.mutex:
            owner = await registry.official.request('GET', '/api/v1/users/me')
            rows = await registry.official.request('GET', '/api/v1/datasets')
            excluded = {group['dataset_id'] for group in registry.managed_groups()}
            excluded.update(group.get('code_dataset_id') for group in registry.state['projects'].values())
            excluded.update(registry.state.get('reserved_datasets', []))
            return web.json_response([{'id': row['id'], 'name': row['name']} for row in rows
                                      if row.get('ownerId') == owner['id'] and row['id'] not in excluded])

    async def defaults(request):
        manager(request)
        payload = fields(await body(request), ('expected_revision', 'fields'))
        return web.json_response(await registry.set_policy(payload['expected_revision'], payload['fields']))

    async def overrides(request):
        manager(request)
        payload = fields(await body(request), ('expected_revision', 'agent_id', 'fields'))
        return web.json_response(await registry.set_policy(payload['expected_revision'], payload['fields'],
                                                         project_id=request.match_info['id'], agent_id=payload['agent_id']))

    async def create(request):
        manager(request)
        payload = fields(await body(request), ('expected_revision', 'workspace'), ('dataset_id', 'name'))
        return web.json_response(await registry.create_project(payload['expected_revision'], payload['workspace'],
                                                             dataset_id=payload.get('dataset_id'), name=payload.get('name')))

    async def alias(request):
        manager(request)
        payload = fields(await body(request), ('expected_revision', 'workspace'))
        return web.json_response(await registry.add_alias(payload['expected_revision'], request.match_info['id'], payload['workspace']))

    async def reconcile(request):
        manager(request)
        payload = fields(await body(request), ('expected_revision',))
        return web.json_response(await registry.reconcile(payload['expected_revision'], request.match_info['id']))

    async def code(request):
        manager(request)
        payload = fields(await body(request), ('expected_revision', 'enabled'), ('dataset_id',))
        return web.json_response(await registry.configure_code(payload['expected_revision'], request.match_info['id'],
                                                                payload.get('dataset_id'), payload['enabled']))

    async def provision(request):
        manager(request)
        payload = fields(await body(request), ('expected_revision', 'instance_id', 'name'))
        return web.json_response(await registry.provision_agent(payload['expected_revision'], payload['instance_id'], payload['name']))

    async def recover(request):
        manager(request)
        payload = fields(await body(request), ('expected_revision',))
        return web.json_response(await registry.recover_agent(payload['expected_revision'], request.match_info['id']))

    async def installation(request):
        manager(request)
        if not plugin_source or not control_url:
            raise PolicyError('managed_plugin_not_configured', 503)
        payload = fields(await body(request), ('expected_revision',), ('host_home',))
        return web.json_response(await registry.prepare_installation(payload['expected_revision'], request.match_info['id'],
                                                                      plugin_source, control_url, payload.get('host_home')))

    async def common(request):
        manager(request)
        payload = fields(await body(request), ('expected_revision',))
        return web.json_response(await registry.ensure_common(payload['expected_revision'], reconcile=request.path.endswith('/reconcile')))

    app.add_routes([web.post('/v1/project/resolve', resolve), web.get('/v1/project/policy', policy),
                    web.get('/v1/projects', directory), web.put('/v1/defaults', defaults),
                    web.put('/v1/projects/{id}/overrides', overrides), web.post('/v1/projects', create),
                    web.post('/v1/projects/{id}/aliases', alias), web.post('/v1/projects/{id}/reconcile', reconcile),
                    web.put('/v1/projects/{id}/code', code),
                    web.post('/v1/agents', provision), web.post('/v1/agents/{id}/reconcile', recover),
                    web.post('/v1/common', common), web.post('/v1/common/reconcile', common),
                    web.get('/v1/linkable-datasets', datasets), web.post('/v1/agents/{id}/installation', installation)])
    return app


async def configured_app(config):
    if config.get('project_control'):
        control = config['project_control']
        config = {**config, **control, 'project_control_url': control['url']}
    session = ClientSession()
    registry = None
    try:
        registry = Registry(config['registry_file'], Official(session, config['service_url'], config['token_file']))
        reserved = {project['code_dataset_id'] for project in config.get('projects', []) if project.get('code_dataset_id')}
        reserved.update(config.get('reserved_dataset_ids', []))
        registry.state['reserved_datasets'] = sorted(reserved)
        registry.save()
        # Explicit pre-existing runtime associations are configuration, not a
        # content migration. Reuse their UUIDs before any new host resolves.
        for project in config.get('projects', []):
            linked = await registry.create_project(registry.state['revision'], project['path'],
                                                   dataset_id=project['dataset_id'], name=project['name'])
            if project.get('code_dataset_id') and not linked.get('code_configured'):
                await registry.configure_code(registry.state['revision'], linked['id'], project['code_dataset_id'],
                                              project.get('code_enabled', False), apply=linked['status'] == 'verified')
        await registry.ensure_common(registry.state['revision'])
        app = create_app(registry, read_secret(config['management_token_file']),
                         {key: read_secret(value) for key, value in config.get('agent_control_token_files', {}).items()},
                         plugin_source=config.get('plugin_dir'), control_url=config.get('project_control_url'))
    except BaseException:
        if registry is not None:
            registry.close()
        await session.close()
        raise

    async def cleanup(_):
        await session.close()
        registry.close()
    app.on_cleanup.append(cleanup)
    return app


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--config', required=True)
    args = parser.parse_args()
    config = json.loads(Path(args.config).read_text())
    from urllib.parse import urlparse
    address = urlparse(loopback_origin(config.get('project_control', {}).get('url') or config['project_control_url']))
    web.run_app(configured_app(config), host='127.0.0.1', port=address.port, access_log=None, print=None)
