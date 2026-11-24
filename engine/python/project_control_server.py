"""Authenticated loopback API for project configuration, recall receipts, and read-only overview aggregation."""
import argparse
import asyncio
import copy
import hashlib
import json
from pathlib import Path
import secrets
import time

from aiohttp import ClientSession, web

from _runtime_common import CachedSecretReader, read_secret
from project_control import Official, Registry, loopback_origin
from project_policy import PolicyError


CODEX_VIEW_CACHE_TTL_SECONDS = 3.0


class _SnapshotRegistry:
    def __init__(self, path, state):
        self.path, self.state = path, state


def _codex_config_lstat(home):
    try:
        st = (home / 'config.toml').lstat()
    except OSError as error:
        return (error.errno,)
    return (st.st_dev, st.st_ino, st.st_mode, st.st_size, st.st_mtime_ns, st.st_ctime_ns)


def fields(body, required, optional=()):
    if not isinstance(body, dict) or set(body) - set(required) - set(optional) or set(required) - set(body):
        raise PolicyError('invalid_request_fields')
    return body


def create_app(registry, manager_token, agent_tokens, *, plugin_source=None, control_url=None, codex_config=None,
               generation=None, generation_validation=None, monitor=None):
    if len(manager_token) < 32 or any(len(token) < 32 for token in agent_tokens.values()):
        raise ValueError('control tokens must contain at least 32 characters')
    tokens = [manager_token, *agent_tokens.values()]
    if len(set(tokens)) != len(tokens):
        raise ValueError('control credentials must be independent')

    if generation is not None and registry is not None and codex_config is not None:
        from agent_bridge_runtime import require_connected_bridge
        generation.connected_bridge_check = lambda profile: require_connected_bridge(registry, codex_config, profile)

    secret_cache = CachedSecretReader()

    @web.middleware
    async def boundary(request, handler):
        try:
            # No browser-origin clients; desktop and hook callers carry independent tokens.
            if request.headers.get('Origin'):
                raise PolicyError('browser_origin_rejected', 403)
            if request.path == '/cognee' or request.path.startswith('/cognee/'):
                return await handler(request)
            token = request.headers.get('Authorization', '')
            request['manager'] = secrets.compare_digest(token, 'Bearer ' + manager_token)
            registered_tokens = dict(agent_tokens)
            for key, value in (registry.state['agents'].items() if registry is not None else []):
                if value.get('control_token_file'):
                    registered_tokens[key] = secret_cache.read(value['control_token_file'])
            registered_tokens = {key: value for key, value in registered_tokens.items()
                                 if key not in (registry.state.get('agent_retirements', {}) if registry else {})}
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

    app = web.Application(client_max_size=320 * 1024, middlewares=[boundary])
    connection = None
    connections = None
    if codex_config:
        from agent_connections import Connections
        from project_codex_connection import CodexConnection
        connections = Connections(registry, codex_config)
        connection = next((adapter for adapter in connections.adapters.values() if isinstance(adapter, CodexConnection)), None)

    codex_view_cache = None
    codex_view_generation = 0
    codex_view_tasks = {}

    def invalidate_codex_view():
        nonlocal codex_view_cache, codex_view_generation
        codex_view_cache = None
        codex_view_generation += 1

    async def read_codex_view(clone, key, generation):
        nonlocal codex_view_cache
        try:
            view = await asyncio.to_thread(clone.view)
        except PolicyError as error:
            return {'status': 'requires_review', 'error_code': error.code}
        if generation == codex_view_generation:
            codex_view_cache = (key, time.monotonic() + CODEX_VIEW_CACHE_TTL_SECONDS, copy.deepcopy(view))
        return view

    def codex_view_finished(key, task):
        if codex_view_tasks.get(key) is task:
            codex_view_tasks.pop(key, None)
        if not task.cancelled():
            task.exception()  # All request waiters may have been cancelled.

    async def directory_result_async(*, force_refresh=False):
        async with registry.mutex:
            result = registry.directory()
            active_connection = connection
            if active_connection is None:
                return result
            identifier = active_connection.id
            state = {key: copy.deepcopy({identifier: registry.state[key][identifier]})
                     if identifier in registry.state.get(key, {}) else {}
                     for key in ('agents', 'codex_connections', 'enrollments')}
            fingerprint = hashlib.sha256(json.dumps(state, sort_keys=True).encode()).digest()
            state['sessions'] = {key: value for key, value in registry.state['sessions'].items()
                                 if json.loads(key)[0] == identifier}
            generation = codex_view_generation
            key = (id(active_connection), identifier, str(active_connection.home), result['revision'],
                   fingerprint, tuple(state['sessions']), generation)
            clone = copy.copy(active_connection)
            clone.registry = _SnapshotRegistry(registry.path, state)

        key += (await asyncio.to_thread(_codex_config_lstat, clone.home),)
        cached = codex_view_cache
        if not force_refresh and cached is not None and cached[0] == key and cached[1] > time.monotonic():
            view = cached[2]
        else:
            task = codex_view_tasks.get(key)
            if task is None:
                task = asyncio.create_task(read_codex_view(clone, key, generation))
                codex_view_tasks[key] = task
                task.add_done_callback(lambda done: codex_view_finished(key, done))
            view = await asyncio.shield(task)
        result['codex_connection'] = copy.deepcopy(view)
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

    if codex_config and codex_config.get('python'):
        from trae_ide_connection import HookDispatch
        trae_ide_dispatch = HookDispatch(registry, codex_config['python'])

        async def trae_ide_hook(request):
            identity = agent(request)
            if request.content_type != 'application/json':
                raise PolicyError('json_required', 415)
            result = await trae_ide_dispatch.run(identity, request.match_info['event'], await request.read())
            return web.json_response(result)

        app.router.add_post('/v1/trae-ide/hooks/{event}', trae_ide_hook)

    async def resolve(request):
        identity = agent(request)
        payload = fields(await body(request), ('workspace', 'host_session_key'), ('explicit_workspace', 'host_environment'))
        if type(payload.get('explicit_workspace', False)) is not bool:
            raise PolicyError('invalid_workspace_flag')
        return web.json_response(await registry.resolve(identity, payload['workspace'], payload['host_session_key'],
                                                       explicit=payload.get('explicit_workspace', False),
                                                       host_environment=payload.get('host_environment')))

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
        return web.json_response(await directory_result_async())

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
        payload = fields(await body(request), ('expected_revision',), ('workspace', 'dataset_id', 'name'))
        return web.json_response(await registry.create_project(payload['expected_revision'], payload.get('workspace'),
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
        payload = fields(await body(request), ('expected_revision',), ('verify_only',))
        return web.json_response(await registry.reconcile(payload['expected_revision'], request.match_info['id'],
                                                        verify_only=payload.get('verify_only', False)))

    async def tool_graph_settings(request):
        manager(request)
        payload = fields(await body(request), ('expected_revision', 'enabled'))
        from tool_graph_policy import configure
        return web.json_response(await configure(registry, payload['expected_revision'], payload['enabled'],
                                                request.match_info.get('id')))

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

    async def retire(request):
        manager(request)
        payload = fields(await body(request), ('expected_revision',))
        identifier = request.match_info['id']
        if connections and identifier in {r.get('instance_id') for r in connections.view()['connections']}:
            raise PolicyError('current_host_cannot_be_retired', 409)
        return web.json_response(await registry.retire_agent(payload['expected_revision'], identifier))

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
        try:
            if request.path.endswith('/activate'):
                if not isinstance(payload.get('proposed_sha256'), str):
                    raise PolicyError('reviewed_preview_required', 409)
                await connection.activate(payload['expected_revision'], payload['proposed_sha256'], include_view=False)
            elif request.path.endswith('/review'):
                await connection.review(payload['expected_revision'], include_view=False)
            else:
                await connection.prepare(payload['expected_revision'], include_view=False)
        finally:
            invalidate_codex_view()
        return web.json_response(await directory_result_async(force_refresh=True))

    async def generic_connections(request):
        manager(request)
        if connections is None:
            raise PolicyError('connection_hosts_not_configured', 503)
        if request.method == 'GET' and 'operation_id' in request.match_info:
            receipt = registry.state.get('connection_operations', {}).get(request.match_info['operation_id'], {})
            if receipt.get('action') == 'allow_capture' and receipt.get('status') != 'complete':
                await connections.checked_view()
            return web.json_response(connections.operation(request.match_info['operation_id']))
        if request.method == 'GET':
            force = request.query.get('force', '').lower() in ('1', 'true', 'yes')
            return web.json_response(await connections.checked_view(force=force))
        preview = request.path == '/v1/connections/preview'
        payload = fields(await body(request), ('expected_revision', 'operation_id', 'instance_id') if preview
                         else ('expected_revision', 'operation_id'), ('proposed_sha256',))
        identifier = payload.get('instance_id') if preview else request.match_info['id']
        action = 'preview' if preview else request.match_info['action']
        try:
            result = await connections.perform(identifier, action, payload['expected_revision'],
                                               payload['operation_id'], payload.get('proposed_sha256'))
        finally:
            if not preview:
                invalidate_codex_view()
        return web.json_response(result)

    async def select_environment(request):
        nonlocal connection
        manager(request)
        payload = fields(await body(request), ('expected_revision', 'kind', 'profile'))
        if connections is None:
            raise PolicyError('connection_hosts_not_configured', 503)
        try:
            result = await connections.select_environment(payload['kind'], payload['profile'], payload['expected_revision'])
        finally:
            invalidate_codex_view()
        from project_codex_connection import CodexConnection
        connection = next((adapter for adapter in connections.adapters.values() if isinstance(adapter, CodexConnection)), None)
        return web.json_response(result)

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
            value = generation.view()
            if generation_validation is not None:
                from agent_bridge_runtime import bridge_choices, bridge_default_states
                value['agent_bridges'] = bridge_choices(generation_validation.runtime)
                value['agent_defaults'] = bridge_default_states(generation_validation.runtime)
            return web.json_response(value)
        payload = await body(request)
        if request.method == 'DELETE':
            fields(payload, ('expected_revision', 'operation_id'))
            if generation_validation is None:
                raise PolicyError('generation_validation_unavailable', 503)
            result = await generation_validation.cancel(request.match_info['operation_id'], payload['expected_revision'], payload['operation_id'])
        elif 'id' in request.match_info and request.path.endswith('/delete'):
            fields(payload, ('expected_revision', 'operation_id', 'profile_revision'))
            result = generation.remove(request.match_info['id'], payload['profile_revision'],
                                       payload['expected_revision'], payload['operation_id'])
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
                    web.post('/v1/generation/profiles/{id}/delete', generation_config),
                    web.post('/v1/generation/validations', generation_config),
                    web.get('/v1/generation/validations/{operation_id}', generation_config),
                    web.delete('/v1/generation/validations/{operation_id}', generation_config),
                    web.get('/v1/generation/operations/{operation_id}', generation_config),
                    web.put('/v1/generation/next', generation_config)])
    # First configuration has no official service or project registry. Keep
    # precisely the same authenticated generation API, without content routes.
    if registry is None:
        if monitor is not None:
            from service_monitor import attach_service_monitor
            attach_service_monitor(app, codex_config or {}, registry=None, connections=connections, monitor=monitor)
        return app
    from knowledge_recall import attach_recall
    recall_store = attach_recall(app, registry)
    app.add_routes([web.post('/v1/project/resolve', resolve), web.get('/v1/project/policy', policy),
                    web.get('/v1/project/binding', binding),
                    web.get('/v1/projects', directory), web.put('/v1/defaults', defaults),
                    web.put('/v1/projects/{id}/overrides', overrides), web.put('/v1/projects/{id}/name', rename), web.post('/v1/projects', create),
                    web.post('/v1/projects/{id}/aliases', alias), web.post('/v1/projects/{id}/reconcile', reconcile),
                    web.put('/v1/projects/{id}/code', code),
                    web.put('/v1/tool-graph/settings', tool_graph_settings),
                    web.put('/v1/projects/{id}/tool-graph/settings', tool_graph_settings),
                    web.post('/v1/agents', provision), web.post('/v1/agents/{id}/reconcile', recover),
                    web.post('/v1/agents/{id}/retire', retire),
                    web.post('/v1/common', common), web.post('/v1/common/reconcile', common),
                    web.get('/v1/linkable-datasets', datasets), web.post('/v1/agents/{id}/installation', installation),
                    web.post('/v1/codex/prepare', codex_connection), web.post('/v1/codex/activate', codex_connection),
                    web.post('/v1/codex/review', codex_connection)])
    app.add_routes([web.get('/v1/connections', generic_connections),
                    web.post('/v1/connections/environment', select_environment),
                    web.get('/v1/connections/operations/{operation_id}', generic_connections),
                    web.post('/v1/connections/preview', generic_connections),
                    web.post('/v1/connections/{id}/{action:prepare|activate|review|allow_capture|defer_capture}', generic_connections)])
    if connections is not None:
        async def capture_cleanup(_):
            pending = list(codex_view_tasks.values())
            if pending:
                try:
                    await asyncio.wait_for(asyncio.gather(*pending, return_exceptions=True), timeout=5.0)
                except asyncio.TimeoutError:
                    pass
            await connections.capture.close()
        app.on_cleanup.append(capture_cleanup)
    if registry is not None:
        from processing_notices import attach_processing_notices
        attach_processing_notices(app, registry, official_data=(codex_config or {}).get('official_data'))
    if registry is not None and connections is not None:
        from user_profiles import attach_user_profiles
        attach_user_profiles(app, registry, connections)
    from service_monitor import attach_service_monitor
    attach_service_monitor(app, codex_config or {}, registry=registry, connections=connections, monitor=monitor, recall_store=recall_store)
    from session_drift import attach_session_drift
    drift_store = attach_session_drift(app, registry)
    from cognee_extension_gateway import attach_cognee_gateway
    attach_cognee_gateway(app, registry, recall_store=recall_store, drift_store=drift_store)
    from raw_records_read import attach_raw_records_read
    attach_raw_records_read(app, registry, codex_config=codex_config)
    from v2_lookups import attach_v2_lookups
    attach_v2_lookups(app, registry, recall_store=recall_store, codex_config=codex_config)
    from v2_api import attach_v2_api
    app['v2_api_handles'] = attach_v2_api(
        app,
        registry,
        manager_token=manager_token,
        recall_store=recall_store,
        codex_config=codex_config,
    )
    return app


async def configured_app(config):
    if config.get('project_control'):
        control = config['project_control']
        config = {**config, **control, 'project_control_url': control['url']}
    from first_install_mode import first_install_setup
    first_setup = first_install_setup(config)
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
        await registry.adopt_runtime_projects(config.get('projects', []), config.get('reserved_dataset_ids', []))
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
        backlog = None
        if not first_setup:
            from project_backlog import attach_backlog
            backlog = attach_backlog(app, registry, config)
            from incremental_processing import attach_incremental_processing
            incremental = attach_incremental_processing(app, backlog)
            if generation is not None:
                from generation_handoff import attach_generation_handoff
                attach_generation_handoff(app, generation, registry, config, incremental, backlog)
            from tool_graph_control import attach_tool_graph
            attach_tool_graph(app, registry, config)
        if 'v2_api_handles' in app and backlog is not None:
            app['v2_api_handles'].set_backlog(backlog)
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
    from owner_lifecycle import watch_owner
    watch_owner()
    parser = argparse.ArgumentParser()
    parser.add_argument('--config', required=True)
    args = parser.parse_args()
    config = json.loads(Path(args.config).read_text())
    from urllib.parse import urlparse
    address = urlparse(loopback_origin(config.get('project_control', {}).get('url') or config['project_control_url']))
    web.run_app(configured_app(config), host='127.0.0.1', port=address.port, access_log=None, print=None)
