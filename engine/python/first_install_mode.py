"""Allow account/Agent setup on an explicitly initialized, unconfigured store.

This is never a fallback for a broken daily configuration. Content processing
stays unavailable until the normal validated generation startup succeeds.
"""
import json
import re
from pathlib import Path


def first_install_setup(config):
    specification = config.get('first_install')
    if specification is None:
        return False
    if (not isinstance(specification, dict) or specification.get('schema') != 1 or
            type(specification.get('setup')) is not bool):
        raise ValueError('invalid first installation mode')
    if not specification['setup']:
        return False
    from first_install_store import read_private, validate_spec
    root = Path(config['official_data'])
    marker = json.loads(read_private(root / '.memlane-first-store.json'))
    validate_spec({**marker, 'root': str(root)})
    receipt = json.loads(read_private(root / 'store-identity.json'))
    if (specification.get('store_id') != marker['store_id'] or
            receipt.get('schema') != 1 or not receipt.get('storage_initialized') or
            receipt.get('store_id') != marker['store_id'] or
            receipt.get('source_commit') != marker['source_commit'] or
            config.get('service_commit') != marker['source_commit'] or
            not receipt.get('owner_id') or not receipt.get('key_id')):
        raise ValueError('first installation store identity not confirmed')
    if (Path(config['token_file']) != root / 'owner.token' or
            not read_private(root / 'owner.token').strip()):
        raise ValueError('first installation owner credential not confirmed')
    if (config.get('generation_mode') != 'unconfigured' or config.get('model') or
            config.get('generation_reference') or config.get('generation_setup_only')):
        raise ValueError('first installation mode cannot replace a generation configuration')
    generation = Path(config['generation_file'])
    if generation.parent != root or generation.is_symlink():
        raise ValueError('first installation generation file must belong to the store')
    if generation.exists():
        state = json.loads(read_private(generation))
        if state.get('schema_version') != 2 or state.get('active') is not None:
            raise ValueError('first installation requires unconfigured generation')
    return True


def setup_request_allowed(method, path):
    if '//' in path or any(part in ('.', '..') for part in path.split('/')):
        return False
    if method == 'GET' and path.rstrip('/') in ('/health', '/api/v1/users/me'):
        return True
    dataset_path = path.rstrip('/')
    if dataset_path == '/api/v1/datasets':
        return method in ('GET', 'POST')
    if method == 'GET' and dataset_path.startswith('/api/v1/datasets/'):
        return (dataset_path in ('/api/v1/datasets/status', '/api/v1/datasets/status/progress')
                or re.fullmatch(r'/api/v1/datasets/[^/]+/(?:data(?:/count)?|processing-status)', dataset_path) is not None)
    if path.startswith('/api/v1/permissions/'):
        return method in ('GET', 'POST', 'PUT', 'DELETE')
    if path.rstrip('/') == '/api/v1/agents/create':
        return method == 'POST'
    if path.rstrip('/') == '/api/v1/agents':
        return method == 'GET'
    if path.startswith('/api/v1/capture/entries/'):
        return method == 'GET'
    return False


class SetupOnlyMiddleware:
    def __init__(self, app):
        self.app = app

    async def __call__(self, scope, receive, send):
        kind = scope['type']
        if kind == 'websocket':
            await send({'type': 'websocket.close', 'code': 1008})
            return
        if kind == 'http' and scope['method'] == 'GET' and scope['path'] in ('/health', '/health/'):
            # Uvicorn serves requests only after the SDK lifespan/migrations
            # complete. Setup readiness must not initialize vector/graph engines
            # through the normal comprehensive health route. Owner identity is
            # checked separately through the authenticated users/me endpoint.
            body = b'{"status":"setup_ready","generation":"unconfigured","checks":"liveness_only"}'
            await send({'type': 'http.response.start', 'status': 200,
                        'headers': [(b'content-type', b'application/json'),
                                    (b'cache-control', b'no-store')]})
            await send({'type': 'http.response.body', 'body': body})
            return
        if kind == 'http' and not setup_request_allowed(scope['method'], scope['path']):
            body = b'{"detail":"generation_not_configured"}'
            await send({'type': 'http.response.start', 'status': 503,
                        'headers': [(b'content-type', b'application/json'),
                                    (b'cache-control', b'no-store')]})
            await send({'type': 'http.response.body', 'body': body})
            return
        await self.app(scope, receive, send)
