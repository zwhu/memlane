"""Local project registry and ACL reconciliation. Never accepts knowledge bodies."""
import asyncio
import copy
import fcntl
import json
import os
from pathlib import Path
import secrets
import tempfile
import uuid

from project_policy import ACCESS, DEFAULTS, PolicyError, effective, project_id, validate_fields, workspace_root


def private_write(path, data):
    path = Path(path)
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd, name = tempfile.mkstemp(prefix='.' + path.name + '-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(name, path)
        directory = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def read_secret(path):
    path = Path(path)
    if path.is_symlink() or path.stat().st_mode & 0o077:
        raise PolicyError('private_credential_required', 503)
    value = path.read_text().strip()
    if not value:
        raise PolicyError('credential_unavailable', 503)
    return value


class Official:
    """Fixed official API only; management key is never returned to a caller."""
    def __init__(self, session, url, token_file):
        self.session, self.url, self.token_file = session, url.rstrip('/'), token_file

    async def request(self, method, path, body=None, token_file=None):
        import aiohttp
        try:
            async with self.session.request(
                method, self.url + path, json=body,
                headers={'X-Api-Key': read_secret(token_file or self.token_file)},
                timeout=aiohttp.ClientTimeout(total=15), allow_redirects=False,
            ) as response:
                if response.status < 200 or response.status >= 300:
                    raise PolicyError('official_http_' + str(response.status), 503)
                raw = await response.content.read(2 * 1024 * 1024 + 1)
                if len(raw) > 2 * 1024 * 1024:
                    raise PolicyError('official_response_limit', 503)
                return json.loads(raw)
        except (aiohttp.ClientError, asyncio.TimeoutError, ValueError):
            raise PolicyError('official_result_unknown', 503) from None

    async def owned_dataset(self, name, dataset_id=None):
        me = await self.request('GET', '/api/v1/users/me')
        rows = await self.request('GET', '/api/v1/datasets')
        matches = [row for row in rows if (row['id'] == dataset_id if dataset_id else row['name'] == name)
                   and row.get('ownerId') == me['id']]
        if len(matches) > 1 or (dataset_id and not matches):
            raise PolicyError('dataset_ownership_unconfirmed', 409)
        if matches:
            return matches[0]
        # Caller persists operation_id before creation. A timeout leaves it unconfirmed;
        # reconcile does the same GET first, never repeats content or model requests.
        row = await self.request('POST', '/api/v1/datasets', {'name': name})
        if row.get('ownerId') != me['id'] or row.get('name') != name:
            raise PolicyError('dataset_ownership_unconfirmed', 503)
        uuid.UUID(row['id'])
        return row

    async def sync_acl(self, agent, dataset, permissions):
        principal = str(uuid.UUID(agent['principal_id']))
        # Revoke excess direct grants before granting new rights. Role/tenant union
        # is checked below; an unexpected inherited grant blocks publication.
        for permission in ('share', 'delete', 'write', 'read'):
            if permission not in permissions:
                await self.request('DELETE', f'/api/v1/permissions/datasets/{principal}?permission_name={permission}', [dataset])
        for permission in ('read', 'write', 'delete'):
            if permission in permissions:
                await self.request('POST', f'/api/v1/permissions/datasets/{principal}?permission_name={permission}', [dataset])
        actual = await self.effective_permissions(agent, dataset)
        if actual != set(permissions):
            raise PolicyError('effective_acl_mismatch', 503)

    async def effective_permissions(self, agent, dataset):
        me = await self.request('GET', '/api/v1/users/me', token_file=agent['key_file'])
        if me['id'] != agent['principal_id'] or not me.get('parent_user_id'):
            raise PolicyError('independent_agent_required', 503)
        tenants = await self.request('GET', '/api/v1/permissions/tenants/me', token_file=agent['key_file'])
        principals = {me['id']}
        # Effective official ACL = direct + tenant + roles. Children cannot create
        # child Agents, so the official get_user_agents union is empty for these users.
        for tenant in tenants:
            principals.add(tenant['id'])
            roles = await self.request('GET', f"/api/v1/permissions/tenants/{tenant['id']}/roles/users/{me['id']}")
            principals.update(role['id'] for role in roles)
        result = set()
        for permission in ('read', 'write', 'delete', 'share'):
            for principal in principals:
                rows = await self.request('GET', f'/api/v1/permissions/principals/{principal}/datasets?permission_name={permission}')
                if any(row['id'] == dataset for row in rows):
                    result.add(permission)
        return result


class Registry:
    """Single writer with durable desired state before any official mutation."""
    def __init__(self, path, official):
        self.path, self.official = Path(path), official
        self.path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        self._lock_file = os.open(str(self.path) + '.lock', os.O_CREAT | os.O_RDWR, 0o600)
        try:
            fcntl.flock(self._lock_file, fcntl.LOCK_EX | fcntl.LOCK_NB)
            if self.path.exists():
                if self.path.is_symlink() or self.path.stat().st_mode & 0o077:
                    raise PolicyError('private_registry_required', 503)
                self.state = json.loads(self.path.read_text())
                if self.state['schema_version'] != 1:
                    raise PolicyError('unsupported_registry', 503)
            else:
                self.state = {'schema_version': 1, 'service_namespace': str(uuid.uuid4()),
                              'revision': 0, 'defaults': dict(DEFAULTS), 'agents': {},
                              'projects': {}, 'sessions': {}}
                self.save()
        except BaseException:
            os.close(self._lock_file)
            raise
        self.mutex = asyncio.Lock()

    def close(self):
        os.close(self._lock_file)

    def save(self):
        private_write(self.path, json.dumps(self.state, ensure_ascii=False, sort_keys=True).encode())

    def check_revision(self, expected):
        if type(expected) is not int or expected != self.state['revision']:
            raise PolicyError('revision_conflict', 409)

    def policy(self, project, agent_id):
        if agent_id not in self.state['agents']:
            raise PolicyError('agent_not_registered', 403)
        values = effective(self.state['defaults'], project['overrides'].get(agent_id, {}))
        applied = project['applied'].get(agent_id, {})
        ready = applied.get('revision') == project['desired_revision'] and applied.get('status') == 'verified'
        return {'project_id': project['id'], 'dataset_id': project['dataset_id'],
                'dataset_name': project['dataset_name'], 'revision': self.state['revision'],
                'desired_revision': project['desired_revision'], 'ready': ready,
                'applied': copy.deepcopy(applied), 'policy': values, 'code_enabled': False}

    def directory(self):
        return {'revision': self.state['revision'], 'defaults': copy.deepcopy(self.state['defaults']),
                'agents': [{'id': key, 'name': value['name']} for key, value in self.state['agents'].items()],
                'projects': [copy.deepcopy(project) for project in self.state['projects'].values()]}

    async def add_agent(self, instance_id, name, principal_id, key_file, expected):
        """Only management can enroll a provisioned official sub-user, never an owner."""
        async with self.mutex:
            self.check_revision(expected)
            if instance_id in self.state['agents']:
                raise PolicyError('agent_already_registered', 409)
            agent = {'name': name, 'principal_id': str(uuid.UUID(principal_id)),
                     'key_file': str(Path(key_file).absolute())}
            me = await self.official.request('GET', '/api/v1/users/me', token_file=agent['key_file'])
            owner = await self.official.request('GET', '/api/v1/users/me')
            if me['id'] != agent['principal_id'] or me.get('parent_user_id') != owner['id']:
                raise PolicyError('independent_agent_required', 403)
            if any(a['principal_id'] == agent['principal_id'] for a in self.state['agents'].values()):
                raise PolicyError('principal_already_registered', 409)
            self.state['agents'][instance_id] = agent
            self.state['revision'] += 1
            for project in self.state['projects'].values():
                project['desired_revision'] = self.state['revision']
            self.save()
            for project in self.state['projects'].values():
                await self.reconcile_locked(project)
            return self.directory()

    async def resolve(self, agent_id, workspace, host_session_key, *, explicit=False):
        if not isinstance(host_session_key, str) or not 1 <= len(host_session_key) <= 256:
            raise PolicyError('host_session_key_required')
        async with self.mutex:
            if agent_id not in self.state['agents']:
                raise PolicyError('agent_not_registered', 403)
            key = json.dumps([agent_id, host_session_key])
            existing = self.state['sessions'].get(key)
            if existing:
                # Host binding wins over temporary cd, and unconfirmed operations are
                # never silently retried by repeated hook events.
                return self.policy(self.state['projects'][existing], agent_id)
            root = workspace_root(workspace, explicit=explicit)
            project = await self.ensure_locked(root)
            self.state['sessions'][key] = project['id']
            self.save()
            return self.policy(project, agent_id)

    async def ensure_locked(self, root, dataset_id=None, name=None):
        for project in self.state['projects'].values():
            if root in project['aliases']:
                if dataset_id and project['dataset_id'] != dataset_id:
                    raise PolicyError('project_binding_conflict', 409)
                return project
        if dataset_id and any(p['dataset_id'] == dataset_id for p in self.state['projects'].values()):
            raise PolicyError('dataset_already_linked', 409)
        identifier = project_id(self.state['service_namespace'], root)
        self.state['revision'] += 1
        project = {'id': identifier, 'name': name or Path(root).name, 'aliases': [root],
                   'dataset_id': dataset_id, 'dataset_name': 'ml-project-' + identifier,
                   'overrides': {}, 'applied': {}, 'desired_revision': self.state['revision'],
                   'operation_id': secrets.token_hex(16), 'status': 'unconfirmed',
                   'code_enabled': False, 'source': 'associated' if dataset_id else 'discovered'}
        self.state['projects'][identifier] = project
        self.save()
        await self.reconcile_locked(project)
        return project

    async def reconcile_locked(self, project):
        project['status'] = 'transition'
        for agent_id in self.state['agents']:
            project['applied'][agent_id] = {'status': 'transition'}
        self.save()
        try:
            row = await self.official.owned_dataset(project['dataset_name'], project['dataset_id'])
            project['dataset_id'], project['dataset_name'] = row['id'], row['name']
            self.save()
            for agent_id, agent in self.state['agents'].items():
                policy = effective(self.state['defaults'], project['overrides'].get(agent_id, {}))
                await self.official.sync_acl(agent, row['id'], policy['permissions'])
                project['applied'][agent_id] = {'status': 'verified', 'revision': project['desired_revision']}
                self.save()
            project['status'] = 'verified'
            project.pop('error_code', None)
        except PolicyError as error:
            project['status'], project['error_code'] = 'unconfirmed', error.code
            # Partial permission mutation is not rolled back to broad owner access.
            for agent_id in self.state['agents']:
                project['applied'][agent_id] = {'status': 'unconfirmed', 'error_code': error.code}
        self.save()

    async def set_policy(self, expected, fields, *, project_id=None, agent_id=None):
        values = validate_fields(fields, allow_reset=project_id is not None)
        async with self.mutex:
            self.check_revision(expected)
            if project_id:
                project = self.state['projects'].get(project_id)
                if not project or agent_id not in self.state['agents']:
                    raise PolicyError('project_or_agent_not_found', 404)
                overrides = project['overrides'].setdefault(agent_id, {})
                for key, value in values.items():
                    if value is None:
                        overrides.pop(key, None)
                    else:
                        overrides[key] = value
                affected = [project]
            else:
                self.state['defaults'].update(values)
                affected = list(self.state['projects'].values())
            self.state['revision'] += 1
            for project in affected:
                project['desired_revision'] = self.state['revision']
                project['status'] = 'transition'
            self.save()
            for project in affected:
                await self.reconcile_locked(project)
            return self.directory()

    async def create_project(self, expected, workspace, *, dataset_id=None, name=None):
        root = workspace_root(workspace, explicit=True)
        if dataset_id:
            dataset_id = str(uuid.UUID(dataset_id))
        async with self.mutex:
            self.check_revision(expected)
            project = await self.ensure_locked(root, dataset_id, name)
            return copy.deepcopy(project)

    async def add_alias(self, expected, identifier, workspace):
        root = workspace_root(workspace, explicit=True)
        async with self.mutex:
            self.check_revision(expected)
            project = self.state['projects'].get(identifier)
            if project is None:
                raise PolicyError('project_not_found', 404)
            if any(root in p['aliases'] and p['id'] != identifier for p in self.state['projects'].values()):
                raise PolicyError('project_binding_conflict', 409)
            if root not in project['aliases']:
                project['aliases'].append(root)
                self.state['revision'] += 1
                self.save()
            return copy.deepcopy(project)
