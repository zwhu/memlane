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


def create_app(registry, manager_token, agent_tokens, *, plugin_source=None, control_url=None, codex_config=None,
               generation=None, generation_validation=None):
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
            for key, value in (registry.state['agents'].items() if registry is not None else []):
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
    connection = None
    connections = None
    if codex_config:
        from agent_connections import Connections
        from project_codex_connection import CodexConnection
        connections = Connections(registry, codex_config)
        connection = next((adapter for adapter in connections.adapters.values() if isinstance(adapter, CodexConnection)), None)

    def directory_result():
        result = registry.directory()
        if connection:
            try:
                result['codex_connection'] = connection.view()
            except PolicyError as error:
                result['codex_connection'] = {'status': 'requires_review', 'error_code': error.code}
        return result

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

    async def binding(request):
        identity = agent(request)
        if len(request.query) != 1 or set(request.query) != {'host_session_key'}:
            raise PolicyError('invalid_request_fields')
        return web.json_response(await registry.session_binding(identity, request.query['host_session_key']))

    async def directory(request):
        manager(request)
        async with registry.mutex:
            return web.json_response(directory_result())

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

    async def rename(request):
        manager(request)
        payload = fields(await body(request), ('expected_revision', 'name'))
        return web.json_response(await registry.rename_project(payload['expected_revision'], request.match_info['id'], payload['name']))

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

    async def codex_connection(request):
        manager(request)
        if connection is None:
            raise PolicyError('codex_host_not_configured', 503)
        payload = fields(await body(request), ('expected_revision',),
                         ('proposed_sha256',) if request.path.endswith('/activate') else ())
        if request.path.endswith('/activate'):
            if not isinstance(payload.get('proposed_sha256'), str):
                raise PolicyError('reviewed_preview_required', 409)
            await connection.activate(payload['expected_revision'], payload['proposed_sha256'])
        elif request.path.endswith('/review'):
            await connection.review(payload['expected_revision'])
        else:
            await connection.prepare(payload['expected_revision'])
        return web.json_response(directory_result())

    async def generic_connections(request):
        manager(request)
        if connections is None:
            raise PolicyError('connection_hosts_not_configured', 503)
        if request.method == 'GET' and 'operation_id' in request.match_info:
            return web.json_response(connections.operation(request.match_info['operation_id']))
        if request.method == 'GET':
            return web.json_response(connections.view())
        preview = request.path == '/v1/connections/preview'
        payload = fields(await body(request), ('expected_revision', 'operation_id', 'instance_id') if preview
                         else ('expected_revision', 'operation_id'), ('proposed_sha256',))
        identifier = payload.get('instance_id') if preview else request.match_info['id']
        action = 'preview' if preview else request.match_info['action']
        return web.json_response(await connections.perform(identifier, action, payload['expected_revision'],
                                                           payload['operation_id'], payload.get('proposed_sha256')))

    async def generation_config(request):
        manager(request)
        if generation is None:
            raise PolicyError('generation_not_configured', 503)
        if request.method == 'GET':
            if request.path.startswith('/v1/generation/operations/'):
                receipt = generation.state['operations'].get(request.match_info['operation_id'])
                return web.json_response({'recorded': receipt is not None,
                                          'result': receipt.get('result') if receipt else None})
            if 'operation_id' in request.match_info:
                if generation_validation is None:
                    raise PolicyError('generation_validation_unavailable', 503)
                return web.json_response(generation_validation.get(request.match_info['operation_id']))
            return web.json_response(generation.view())
        payload = await body(request)
        if request.method == 'DELETE':
            fields(payload, ('expected_revision', 'operation_id'))
            if generation_validation is None:
                raise PolicyError('generation_validation_unavailable', 503)
            result = await generation_validation.cancel(request.match_info['operation_id'], payload['expected_revision'], payload['operation_id'])
        elif 'id' in request.match_info:
            fields(payload, ('expected_revision', 'operation_id', 'profile'), ('api_key',))
            result = generation.put(request.match_info['id'], payload['profile'], payload['expected_revision'],
                                    payload['operation_id'], api_key=payload.get('api_key'))
        else:
            fields(payload, ('expected_revision', 'operation_id', 'profile'))
            if request.path.endswith('/next'):
                result = generation.select_next(payload['profile'], payload['expected_revision'], payload['operation_id'])
            else:
                if generation_validation is None:
                    raise PolicyError('generation_validation_unavailable', 503)
                result = generation_validation.start(payload['profile'], payload['expected_revision'], payload['operation_id'])
        return web.json_response({'revision': generation.state['revision'], 'result': result})

    app.add_routes([web.get('/v1/generation', generation_config),
                    web.put('/v1/generation/profiles/{id}', generation_config),
                    web.post('/v1/generation/validations', generation_config),
                    web.get('/v1/generation/validations/{operation_id}', generation_config),
                    web.delete('/v1/generation/validations/{operation_id}', generation_config),
                    web.get('/v1/generation/operations/{operation_id}', generation_config),
                    web.put('/v1/generation/next', generation_config)])
    # First configuration has no official service or project registry. Keep
    # precisely the same authenticated generation API, without content routes.
    if registry is None:
        return app
    app.add_routes([web.post('/v1/project/resolve', resolve), web.get('/v1/project/policy', policy),
                    web.get('/v1/project/binding', binding),
                    web.get('/v1/projects', directory), web.put('/v1/defaults', defaults),
                    web.put('/v1/projects/{id}/overrides', overrides), web.put('/v1/projects/{id}/name', rename), web.post('/v1/projects', create),
                    web.post('/v1/projects/{id}/aliases', alias), web.post('/v1/projects/{id}/reconcile', reconcile),
                    web.put('/v1/projects/{id}/code', code),
                    web.post('/v1/agents', provision), web.post('/v1/agents/{id}/reconcile', recover),
                    web.post('/v1/common', common), web.post('/v1/common/reconcile', common),
                    web.get('/v1/linkable-datasets', datasets), web.post('/v1/agents/{id}/installation', installation),
                    web.post('/v1/codex/prepare', codex_connection), web.post('/v1/codex/activate', codex_connection),
                    web.post('/v1/codex/review', codex_connection)])
    app.add_routes([web.get('/v1/connections', generic_connections),
                    web.get('/v1/connections/operations/{operation_id}', generic_connections),
                    web.post('/v1/connections/preview', generic_connections),
                    web.post('/v1/connections/{id}/{action:prepare|activate|review}', generic_connections)])
    return app


async def configured_app(config):
    if config.get('project_control'):
        control = config['project_control']
        config = {**config, **control, 'project_control_url': control['url']}
    if config.get('generation_setup_only'):
        from generation_config import GenerationStore
        from generation_validation import GenerationValidation
        generation = GenerationStore(config['generation_file'], config['service_commit'])
        validation = None
        try:
            if generation.state['active'] is not None:
                raise PolicyError('generation_setup_requires_unconfigured_store', 409)
            validation = GenerationValidation(generation, config)
            app = create_app(None, read_secret(config['management_token_file']), {},
                             generation=generation, generation_validation=validation)
        except BaseException:
            generation.close()
            raise
        async def setup_cleanup(_):
            try:
                await validation.close()
            finally:
                generation.close()
        app.on_cleanup.append(setup_cleanup)
        return app
    session = ClientSession()
    registry = None
    generation = validation = None
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
        if config.get('generation_file'):
            from generation_config import GenerationStore
            from generation_validation import GenerationValidation
            generation = GenerationStore(config['generation_file'], config['service_commit'], legacy_model=config.get('model'))
            validation = GenerationValidation(generation, config)
        app = create_app(registry, read_secret(config['management_token_file']),
                         {key: read_secret(value) for key, value in config.get('agent_control_token_files', {}).items()},
                         plugin_source=config.get('plugin_dir'), control_url=config.get('project_control_url'), codex_config=config,
                         generation=generation, generation_validation=validation)
    except BaseException:
        if registry is not None:
            registry.close()
        if generation is not None:
            generation.close()
        await session.close()
        raise

    async def cleanup(_):
        if validation is not None:
            await validation.close()
        if generation is not None:
            generation.close()
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
