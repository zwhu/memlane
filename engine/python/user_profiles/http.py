"""Management HTTP routes for user profile sources and operations."""
from aiohttp import web

from project_policy import PolicyError

from .constants import valid_body
from .service import UserProfiles


def attach_user_profiles(app, registry, connections):
    """Attach authenticated management routes for reading and updating user profiles."""
    profiles = UserProfiles(registry, connections)

    async def handler(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        identifier = request.match_info.get('id')
        if request.method == 'GET':
            return web.json_response(await profiles.read(identifier) if identifier else profiles.view())
        payload = await request.json()
        action = request.match_info['action']
        if action == 'reconcile':
            if payload != {}:
                raise PolicyError('invalid_request_fields')
            return web.json_response(await profiles.reconcile(identifier))
        if action == 'cancel':
            if not isinstance(payload, dict) or set(payload) != {'operation_id'}:
                raise PolicyError('invalid_request_fields')
            return web.json_response(await profiles.cancel(identifier, payload['operation_id']))
        expected = {'operation_id', 'body'} if action == 'import' else {'operation_id'}
        if not isinstance(payload, dict) or set(payload) != expected:
            raise PolicyError('invalid_request_fields')
        if action == 'import':
            valid_body(payload['body'])
        return web.json_response(await profiles.start(identifier, payload['operation_id'], body=payload.get('body')))

    app.add_routes([
        web.get('/v1/user-profiles', handler),
        web.get('/v1/user-profiles/{id}/document', handler),
        web.post('/v1/user-profiles/{id}/{action:generate|import|reconcile|cancel}', handler),
    ])

    async def cleanup(_):
        await profiles.close()

    app.on_cleanup.append(cleanup)
    return profiles
