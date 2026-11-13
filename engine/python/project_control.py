"""Local project registry and ACL reconciliation. Never accepts knowledge bodies."""
import asyncio
import copy
import fcntl
import json
import os
from pathlib import Path
import secrets
import re
import tempfile
import uuid
from urllib.parse import urlparse
from urllib.parse import quote

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


def loopback_origin(value):
    url = urlparse(value)
    if (url.scheme != 'http' or url.hostname != '127.0.0.1' or not url.port
            or url.path not in ('', '/') or url.query or url.fragment or url.username):
        raise PolicyError('explicit_loopback_origin_required')
    return value.rstrip('/')


class Official:
    """Fixed official API only; management key is never returned to a caller."""
    def __init__(self, session, url, token_file):
        self.session, self.url, self.token_file = session, loopback_origin(url), token_file

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
        owner = await self.request('GET', '/api/v1/users/me')
        if me['id'] != agent['principal_id'] or me.get('parent_user_id') != owner['id']:
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
                # This endpoint authorizes/scopes against the requester's
                # current tenant. Use the child to inspect its own principals;
                # an owner without a selected tenant cannot inspect a child.
                rows = await self.request('GET', f'/api/v1/permissions/principals/{principal}/datasets?permission_name={permission}',
                                          token_file=agent['key_file'])
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
                if self.state['schema_version'] not in (1, 2):
                    raise PolicyError('unsupported_registry', 503)
                if self.state['schema_version'] == 1:
                    private_write(str(self.path) + '.schema1-backup', self.path.read_bytes())
                    self.state['connections'] = copy.deepcopy(self.state.get('codex_connections', {}))
                    for identifier, agent in self.state['agents'].items():
                        # Older manually enrolled identities have no proven host.
                        agent.setdefault('kind', 'codex' if identifier.startswith('codex-') else 'unknown')
                        agent.setdefault('capabilities', {})
                        agent.setdefault('entrypoints', [])
                    self.state['schema_version'] = 2
                    self.save()
            else:
                self.state = {'schema_version': 2, 'service_namespace': str(uuid.uuid4()),
                              'revision': 0, 'defaults': dict(DEFAULTS), 'agents': {},
                              'projects': {}, 'sessions': {}, 'connections': {}}
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
        ready = (project['status'] == 'verified' and
                 applied.get('revision') == project['desired_revision'] and applied.get('status') == 'verified')
        return {'project_id': project['id'], 'dataset_id': project['dataset_id'],
                'dataset_name': project['dataset_name'], 'revision': self.state['revision'],
                'desired_revision': project['desired_revision'], 'ready': ready,
                'applied': copy.deepcopy(applied), 'policy': values, 'code_enabled': project.get('code_enabled', False),
                'code': {'enabled': project.get('code_enabled', False),
                         'dataset_id': project.get('code_dataset_id'),
                         'ready': ready and project.get('code_configured', False),
                         'roots': project['aliases']},
                'common': self.common_policy(agent_id)}

    def common_policy(self, agent_id):
        common = self.state.get('common')
        if not common:
            return {'ready': False, 'readable': False}
        applied = common['applied'].get(agent_id, {})
        ready = (common['status'] == 'verified' and applied.get('status') == 'verified'
                 and applied.get('revision') == common['desired_revision'])
        return {'dataset_id': common['dataset_id'], 'revision': common['desired_revision'],
                'ready': ready, 'readable': ready}

    async def checked_policy(self, project, agent_id):
        """Read-only recheck before local caches can be used by a hook.

        Out-of-band ACL/role changes must withdraw cached delivery too. This
        never repairs grants or promotes an unconfirmed configuration.
        """
        agent = self.state['agents'].get(agent_id)
        if not agent:
            raise PolicyError('agent_not_registered', 403)
        changed = False
        for group in [project, *([self.state['common']] if self.state.get('common') else [])]:
            if group['status'] != 'verified':
                continue
            expected = ({'read'} if group.get('kind') == 'common' else
                        effective(self.state['defaults'], group['overrides'].get(agent_id, {}))['permissions'])
            try:
                actual = await self.official.effective_permissions(agent, group['dataset_id'])
                if actual != set(expected):
                    raise PolicyError('effective_acl_changed', 503)
                if group.get('code_dataset_id'):
                    actual = await self.official.effective_permissions(agent, group['code_dataset_id'])
                    if actual != (set(expected) if group.get('code_enabled') else set()):
                        raise PolicyError('effective_code_acl_changed', 503)
            except PolicyError as error:
                group['status'], group['error_code'] = 'unconfirmed', error.code
                group['applied'][agent_id] = {'status': 'unconfirmed', 'error_code': error.code}
                changed = True
        if changed:
            self.state['revision'] += 1
            self.save()
        return self.policy(project, agent_id)

    def directory(self):
        return {'revision': self.state['revision'], 'defaults': copy.deepcopy(self.state['defaults']),
                'agents': [{'id': key, 'name': value['name'], 'kind': value.get('kind', 'unknown'),
                            'capabilities': copy.deepcopy(value.get('capabilities', {})),
                            'entrypoints': copy.deepcopy(value.get('entrypoints', [])),
                            'installation': copy.deepcopy(value.get('installation'))}
                           for key, value in self.state['agents'].items()],
                'enrollments': {key: {field: value[field] for field in ('status', 'error_code') if field in value}
                                for key, value in self.state.get('enrollments', {}).items()},
                'projects': [copy.deepcopy(project) for project in self.state['projects'].values()],
                'common': copy.deepcopy(self.state.get('common')),
                'bindings': [{'agent_id': json.loads(key)[0], 'host_session_key': json.loads(key)[1], 'project_id': value}
                             for key, value in self.state['sessions'].items()]}

    async def prepare_installation(self, expected, instance_id, plugin_source, control_url, host_home=None):
        async with self.mutex:
            self.check_revision(expected)
            agent = self.state['agents'].get(instance_id)
            if not agent or not agent.get('control_token_file'):
                raise PolicyError('managed_agent_required', 409)
            from project_installation import prepare
            agent['installation'] = prepare(plugin_source, self.path.parent, instance_id, agent,
                                             self.official.url, control_url, host_home)
            self.state['revision'] += 1
            self.save()
            return self.directory()

    def managed_groups(self):
        return [*self.state['projects'].values(), *([self.state['common']] if self.state.get('common') else [])]

    async def ensure_common(self, expected, *, reconcile=False):
        async with self.mutex:
            self.check_revision(expected)
            common = self.state.get('common')
            if common is None:
                self.state['revision'] += 1
                identifier = str(uuid.uuid5(uuid.UUID(self.state['service_namespace']), 'common-knowledge'))
                common = {'id': identifier, 'kind': 'common', 'name': '通用知识',
                          'dataset_id': None, 'dataset_name': 'ml-common-' + identifier,
                          'overrides': {}, 'applied': {}, 'desired_revision': self.state['revision'],
                          'operation_id': secrets.token_hex(16), 'status': 'unconfirmed', 'code_enabled': False}
                self.state['common'] = common
                self.save()
                await self.reconcile_locked(common)
            elif reconcile:
                await self.reconcile_locked(common)
            return self.directory()

    async def reconcile(self, expected, identifier):
        async with self.mutex:
            self.check_revision(expected)
            project = self.state['projects'].get(identifier)
            if project is None:
                raise PolicyError('project_not_found', 404)
            await self.reconcile_locked(project)
            return self.directory()

    async def configure_code(self, expected, identifier, dataset_id, enabled, *, apply=True):
        if type(enabled) is not bool:
            raise PolicyError('invalid_code_enabled')
        if dataset_id:
            dataset_id = str(uuid.UUID(dataset_id))
        async with self.mutex:
            self.check_revision(expected)
            project = self.state['projects'].get(identifier)
            if project is None:
                raise PolicyError('project_not_found', 404)
            dataset_id = dataset_id or project.get('code_dataset_id')
            if enabled and not dataset_id:
                raise PolicyError('code_dataset_required', 409)
            if dataset_id:
                # Only an explicitly manager-owned existing code group can be
                # associated. Never create an Agent-owned fallback here.
                if any(group['dataset_id'] == dataset_id or
                       (group['id'] != identifier and group.get('code_dataset_id') == dataset_id)
                       for group in self.managed_groups()):
                    raise PolicyError('code_dataset_already_linked', 409)
                await self.official.owned_dataset('', dataset_id)
            if (project.get('code_configured') and project.get('code_dataset_id') == dataset_id
                    and project.get('code_enabled') == enabled):
                return self.directory()
            if project.get('code_dataset_id') and project['code_dataset_id'] != dataset_id:
                raise PolicyError('code_dataset_replacement_requires_review', 409)
            self.state['revision'] += 1
            project.update(code_configured=True, code_dataset_id=dataset_id, code_enabled=enabled,
                           desired_revision=self.state['revision'], status='transition')
            self.save()
            if apply:
                await self.reconcile_locked(project)
            else:
                project['status'] = 'unconfirmed'
                self.save()
            return self.directory()

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
            for project in self.managed_groups():
                project['desired_revision'] = self.state['revision']
            self.save()
            for project in self.managed_groups():
                await self.reconcile_locked(project)
            return self.directory()

    async def provision_agent(self, expected, instance_id, name):
        """Persist a single creation attempt; unknown creates require explicit recovery."""
        if not isinstance(instance_id, str) or not re.fullmatch(r'[a-zA-Z0-9_-]{1,64}', instance_id):
            raise PolicyError('invalid_installation_id')
        if not isinstance(name, str) or not 1 <= len(name.strip()) <= 100:
            raise PolicyError('invalid_agent_name')
        async with self.mutex:
            self.check_revision(expected)
            enrollments = self.state.setdefault('enrollments', {})
            if instance_id in self.state['agents'] or instance_id in enrollments:
                raise PolicyError('agent_creation_requires_review', 409)
            unique_name = 'ml-' + self.state['service_namespace'] + '-' + instance_id
            enrollments[instance_id] = {'name': name.strip(), 'official_name': unique_name,
                                        'operation_id': secrets.token_hex(16), 'status': 'unconfirmed'}
            self.state['revision'] += 1
            self.save()
            try:
                result = await self.official.request('POST', '/api/v1/agents/create?name=' + quote(unique_name, safe=''))
                principal = str(uuid.UUID(result['agentId']))
                key = result['agentApiKey']
                if not isinstance(key, str) or not key.strip():
                    raise PolicyError('agent_creation_result_unknown', 503)
                credential_root = self.path.parent / 'agent-credentials'
                key_file = credential_root / (instance_id + '.official')
                control_file = credential_root / (instance_id + '.control')
                private_write(key_file, key.strip().encode())
                private_write(control_file, secrets.token_urlsafe(32).encode())
                enrollments[instance_id].update({'principal_id': principal, 'key_file': str(key_file),
                                                 'control_token_file': str(control_file)})
                self.save()
                # Do not publish a credential until it proves this exact child identity.
                me = await self.official.request('GET', '/api/v1/users/me', token_file=key_file)
                owner = await self.official.request('GET', '/api/v1/users/me')
                if me['id'] != principal or me.get('parent_user_id') != owner['id']:
                    raise PolicyError('independent_agent_required', 503)
                self.state['agents'][instance_id] = {'name': name.strip(), 'principal_id': principal,
                                                      'key_file': str(key_file), 'control_token_file': str(control_file)}
                enrollments[instance_id]['status'] = 'verified'
                for project in self.managed_groups():
                    project['desired_revision'] = self.state['revision']
                self.save()
                for project in self.managed_groups():
                    await self.reconcile_locked(project)
            except (PolicyError, KeyError, ValueError) as error:
                enrollments[instance_id]['error_code'] = error.code if isinstance(error, PolicyError) else 'agent_creation_result_unknown'
                self.save()
            return self.directory()

    async def recover_agent(self, expected, instance_id):
        """Verify the original saved identity only; never create a replacement."""
        async with self.mutex:
            self.check_revision(expected)
            enrollment = self.state.get('enrollments', {}).get(instance_id)
            if not enrollment or not enrollment.get('key_file') or instance_id in self.state['agents']:
                raise PolicyError('agent_recovery_requires_original_credentials', 409)
            me = await self.official.request('GET', '/api/v1/users/me', token_file=enrollment['key_file'])
            owner = await self.official.request('GET', '/api/v1/users/me')
            if me['id'] != enrollment['principal_id'] or me.get('parent_user_id') != owner['id']:
                raise PolicyError('independent_agent_required', 503)
            self.state['agents'][instance_id] = {field: enrollment[field] for field in
                                                  ('name', 'principal_id', 'key_file', 'control_token_file')}
            enrollment['status'] = 'verified'
            enrollment.pop('error_code', None)
            self.state['revision'] += 1
            for project in self.managed_groups():
                project['desired_revision'] = self.state['revision']
            self.save()
            for project in self.managed_groups():
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

    async def ensure_locked(self, root, dataset_id=None, name=None, source=None):
        for project in self.state['projects'].values():
            if root in project['aliases']:
                if dataset_id and project['dataset_id'] != dataset_id:
                    raise PolicyError('project_binding_conflict', 409)
                return project
        if dataset_id and any(p['dataset_id'] == dataset_id for p in self.state['projects'].values()):
            raise PolicyError('dataset_already_linked', 409)
        if dataset_id and ((self.state.get('common') or {}).get('dataset_id') == dataset_id
                           or any(group.get('code_dataset_id') == dataset_id for group in self.state['projects'].values())
                           or dataset_id in self.state.get('reserved_datasets', [])):
            raise PolicyError('non_project_dataset_cannot_be_linked', 409)
        identifier = project_id(self.state['service_namespace'], root)
        self.state['revision'] += 1
        project = {'id': identifier, 'name': name or Path(root).name, 'aliases': [root],
                   'dataset_id': dataset_id, 'dataset_name': 'ml-project-' + identifier,
                   'overrides': {}, 'applied': {}, 'desired_revision': self.state['revision'],
                   'operation_id': secrets.token_hex(16), 'status': 'unconfirmed',
                   'import_acl': bool(dataset_id),
                   'code_enabled': False, 'source': source or ('associated' if dataset_id else 'discovered')}
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
            if project.get('import_acl'):
                # Association imports facts, never applies the new-project template.
                imported = {}
                for agent_id, agent in self.state['agents'].items():
                    actual = await self.official.effective_permissions(agent, row['id'])
                    level = next((key for key, value in ACCESS.items() if value == actual), None)
                    if level is None:
                        raise PolicyError('existing_acl_requires_review', 409)
                    imported[agent_id] = {'access': level}
                project['overrides'] = imported
                project['import_acl'] = False
                self.save()
            for agent_id, agent in self.state['agents'].items():
                policy = (effective({'access': 'read', 'capture': False, 'auto_process': False}, {})
                          if project.get('kind') == 'common' else
                          effective(self.state['defaults'], project['overrides'].get(agent_id, {})))
                await self.official.sync_acl(agent, row['id'], policy['permissions'])
                if project.get('code_dataset_id'):
                    # The optional group follows project access only while
                    # explicitly enabled, including effective-role verification.
                    await self.official.owned_dataset('', project['code_dataset_id'])
                    await self.official.sync_acl(agent, project['code_dataset_id'],
                                                policy['permissions'] if project.get('code_enabled') else set())
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
            project = await self.ensure_locked(root, dataset_id, name, source='associated' if dataset_id else 'manual')
            return copy.deepcopy(project)

    async def rename_project(self, expected, identifier, name):
        if not isinstance(name, str) or not 1 <= len(name.strip()) <= 120 or any(ord(c) < 32 for c in name):
            raise PolicyError('invalid_project_name')
        async with self.mutex:
            self.check_revision(expected)
            project = self.state['projects'].get(identifier)
            if project is None:
                raise PolicyError('project_not_found', 404)
            if project['name'] != name.strip():
                project['name'] = name.strip()
                self.state['revision'] += 1
                self.save()
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
                if project['aliases']:
                    raise PolicyError('project_binding_locked', 409)
                project['aliases'].append(root)
                self.state['revision'] += 1
                self.save()
            return copy.deepcopy(project)
