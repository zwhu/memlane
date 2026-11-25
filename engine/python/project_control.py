"""Local project registry and ACL reconciliation. Never accepts knowledge bodies."""
import asyncio
import copy
from datetime import datetime, timezone
import fcntl
import hashlib
import json
import os
import time
from pathlib import Path
import secrets
import re
import sqlite3
import tempfile
import uuid
from urllib.parse import urlparse
from urllib.parse import quote

from _runtime_common import private_write, read_secret
from project_policy import (
    ACCESS, DEFAULTS, PolicyError, effective, is_protected_project, project_capture_fields,
    project_id, validate_fields, workspace_root,
)
from registry_sessions_store import RegistrySessionsStore

_DYNAMIC_STATE_KEYS = ('sessions', 'connection_operations', 'connection_receipts')
_SYNC_META_KEY = '_sessions_store_sync'


def _payload_timestamp(payload) -> float:
    if not isinstance(payload, dict):
        return 0.0
    for field in ('received_at', 'completed_at', 'updated_at', 'at'):
        val = payload.get(field)
        if type(val) in (int, float) and val > 0:
            return float(val)
    return 0.0


def _merge_dynamic_by_freshness(
    json_map: dict,
    db_meta_map: dict,
    *,
    json_rev: int,
    db_rev: int,
    json_mtime: float,
    use_payload_ts: bool = False,
) -> dict:
    """Merge legacy/rollback JSON entries with SQLite rows by picking the newer version per key."""
    merged = {k: v for k, (v, _) in db_meta_map.items()}
    for key, json_val in json_map.items():
        if key not in db_meta_map:
            merged[key] = json_val
            continue
        db_val, db_row_ts = db_meta_map[key]
        if json_val == db_val:
            continue
        if db_rev < 0:
            merged[key] = json_val
            continue
        if use_payload_ts:
            j_ts = _payload_timestamp(json_val)
            d_ts = _payload_timestamp(db_val)
            if j_ts != d_ts:
                if j_ts > d_ts:
                    merged[key] = json_val
                continue
        if json_rev > db_rev or (json_rev == db_rev and json_mtime >= db_row_ts):
            merged[key] = json_val
    return merged


def _installation_view(installation):
    if installation is None:
        return None
    view = copy.deepcopy(installation)
    if (isinstance(view, dict) and not view.get('helper_sha256')
            and view.get('host_kind') != 'opencode'
            and view.get('plugin_dir') and view.get('manifest_sha256')):
        try:
            manifest = Path(view['plugin_dir']) / '.memlane-project-access.json'
            if manifest.is_absolute() and not manifest.is_symlink() and manifest.is_file():
                raw = manifest.read_bytes()
                if hashlib.sha256(raw).hexdigest() == view['manifest_sha256']:
                    files = json.loads(raw).get('files')
                    if isinstance(files, dict):
                        helper = files.get('scripts/_memlane_policy.py')
                        if isinstance(helper, str) and helper:
                            view['helper_sha256'] = helper
        except (OSError, ValueError, TypeError):
            pass
    return view


def _is_transient_policy_probe_error(error):
    return error.code == 'official_result_unknown' or error.code.startswith('official_http_5')


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

    async def request(self, method, path, body=None, token_file=None, *, adapter_token=None):
        import aiohttp
        try:
            headers = {'X-Api-Key': read_secret(token_file or self.token_file)}
            if adapter_token is not None:
                headers['X-Memlane-Management'] = adapter_token
            async with self.session.request(
                method, self.url + path, json=body,
                headers=headers,
                timeout=aiohttp.ClientTimeout(total=15), allow_redirects=False,
            ) as response:
                if response.status < 200 or response.status >= 300:
                    raise PolicyError('official_http_' + str(response.status), 503)
                raw = bytearray()
                async for chunk in response.content.iter_chunked(64 * 1024):
                    raw.extend(chunk)
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

    async def retirement_datasets(self, agent):
        rows = await self.request('GET', '/api/v1/datasets', token_file=agent['key_file'])
        identifiers = {row['id'] for row in rows}
        principal = str(uuid.UUID(agent['principal_id']))
        for permission in ('read', 'write', 'delete', 'share'):
            rows = await self.request('GET', f'/api/v1/permissions/principals/{principal}/datasets?permission_name={permission}',
                                      token_file=agent['key_file'])
            identifiers.update(row['id'] for row in rows)
        return identifiers

    async def effective_permissions_batch(self, agent, datasets):
        """Return effective official permissions for each non-empty dataset id in one pass."""
        targets = [ds for ds in dict.fromkeys(datasets) if ds]
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
        results = {ds: set() for ds in targets}
        if not targets:
            return results
        target_set = set(targets)
        for permission in ('read', 'write', 'delete', 'share'):
            for principal in principals:
                # This endpoint authorizes/scopes against the requester's
                # current tenant. Use the child to inspect its own principals;
                # an owner without a selected tenant cannot inspect a child.
                rows = await self.request('GET', f'/api/v1/permissions/principals/{principal}/datasets?permission_name={permission}',
                                          token_file=agent['key_file'])
                for row in rows:
                    ds_id = row.get('id')
                    if ds_id in target_set:
                        results[ds_id].add(permission)
        return results

    async def effective_permissions(self, agent, dataset):
        return (await self.effective_permissions_batch(agent, [dataset])).get(dataset, set())


class Registry:
    """Single writer with durable desired state before any official mutation."""
    def __init__(self, path, official):
        self.path, self.official = Path(path), official
        self.path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        self._lock_file = os.open(str(self.path) + '.lock', os.O_CREAT | os.O_RDWR, 0o600)
        self.sessions_store = None
        self.backlog = None
        self._sync_version = 0
        try:
            fcntl.flock(self._lock_file, fcntl.LOCK_EX | fcntl.LOCK_NB)
            self.sessions_store = RegistrySessionsStore(self.path.parent / 'registry-sessions.sqlite3')
            db_sess_meta, db_ops_meta, db_rec_meta, db_meta = self.sessions_store.load_all_with_meta()
            db_sessions = {k: v for k, (v, _) in db_sess_meta.items()}
            db_ops = {k: v for k, (v, _) in db_ops_meta.items()}
            db_receipts = {k: v for k, (v, _) in db_rec_meta.items()}
            self._sync_version = int(db_meta.get('sync_version') or 0)
            if self.path.exists():
                file_stat = self.path.stat()
                if self.path.is_symlink() or file_stat.st_mode & 0o077:
                    raise PolicyError('private_registry_required', 503)
                self.state = json.loads(self.path.read_text())
                if self.state['schema_version'] not in (1, 2):
                    raise PolicyError('unsupported_registry', 503)
                json_sync_meta = self.state.pop(_SYNC_META_KEY, None)
                if isinstance(json_sync_meta, dict):
                    try:
                        self._sync_version = max(self._sync_version, int(json_sync_meta.get('version') or 0))
                    except (TypeError, ValueError):
                        pass
                migrated_dynamic = False
                json_sessions = self.state.get('sessions') if isinstance(self.state.get('sessions'), dict) else {}
                json_ops = self.state.get('connection_operations') if isinstance(self.state.get('connection_operations'), dict) else {}
                json_receipts = self.state.get('connection_receipts') if isinstance(self.state.get('connection_receipts'), dict) else {}
                if json_sessions or json_ops or json_receipts:
                    json_rev = int(self.state.get('revision') or 0)
                    db_rev = int(db_meta.get('revision', -1))
                    json_mtime = float(file_stat.st_mtime)
                    merged_sessions = _merge_dynamic_by_freshness(
                        json_sessions, db_sess_meta,
                        json_rev=json_rev, db_rev=db_rev, json_mtime=json_mtime,
                        use_payload_ts=False,
                    )
                    merged_ops = _merge_dynamic_by_freshness(
                        json_ops, db_ops_meta,
                        json_rev=json_rev, db_rev=db_rev, json_mtime=json_mtime,
                        use_payload_ts=True,
                    )
                    merged_receipts = _merge_dynamic_by_freshness(
                        json_receipts, db_rec_meta,
                        json_rev=json_rev, db_rev=db_rev, json_mtime=json_mtime,
                        use_payload_ts=True,
                    )
                    self._sync_version += 1
                    sync_meta = self.sessions_store.sync_dicts(
                        merged_sessions, merged_ops, merged_receipts,
                        revision=json_rev, sync_version=self._sync_version,
                    )
                    db_sessions, db_ops, db_receipts = merged_sessions, merged_ops, merged_receipts
                    migrated_dynamic = True
                self.state['sessions'] = db_sessions
                if db_ops:
                    self.state['connection_operations'] = db_ops
                elif 'connection_operations' in self.state:
                    self.state['connection_operations'] = {}
                if db_receipts:
                    self.state['connection_receipts'] = db_receipts
                elif 'connection_receipts' in self.state:
                    self.state['connection_receipts'] = {}
                if isinstance(self.state.get('session_relations'), dict):
                    from session_relations import TrackedRelationsDict
                    self.state['session_relations'] = TrackedRelationsDict(self.state['session_relations'])
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
                elif migrated_dynamic:
                    self._save_slim_json(sync_meta={
                        'revision': sync_meta['revision'],
                        'version': sync_meta['sync_version'],
                        'updated_at': sync_meta['updated_at'],
                    })
            else:
                self.state = {'schema_version': 2, 'service_namespace': str(uuid.uuid4()),
                              'revision': 0, 'defaults': dict(DEFAULTS), 'agents': {},
                              'projects': {}, 'sessions': db_sessions, 'connections': {}}
                if db_ops:
                    self.state['connection_operations'] = db_ops
                if db_receipts:
                    self.state['connection_receipts'] = db_receipts
                self.save()
        except BaseException:
            if self.sessions_store is not None:
                self.sessions_store.close()
                self.sessions_store = None
            os.close(self._lock_file)
            raise
        self.mutex = asyncio.Lock()

    def close(self):
        if self.sessions_store is not None:
            self.sessions_store.close()
            self.sessions_store = None
        os.close(self._lock_file)

    def _save_slim_json(self, *, sync_meta=None):
        slim = {k: v for k, v in self.state.items() if k not in _DYNAMIC_STATE_KEYS}
        if sync_meta is not None:
            slim[_SYNC_META_KEY] = sync_meta
        private_write(self.path, json.dumps(slim, ensure_ascii=False, sort_keys=True).encode())

    def save(self):
        if self.sessions_store is not None:
            self._sync_version += 1
            now = time.time()
            meta = self.sessions_store.sync_dicts(
                self.state.get('sessions'),
                self.state.get('connection_operations'),
                self.state.get('connection_receipts'),
                revision=int(self.state.get('revision') or 0),
                sync_version=self._sync_version,
                updated_at=now,
            )
            self._save_slim_json(sync_meta={
                'revision': meta['revision'],
                'version': meta['sync_version'],
                'updated_at': meta['updated_at'],
            })
        else:
            private_write(self.path, json.dumps(self.state, ensure_ascii=False, sort_keys=True).encode())

    def sessions_for_project(self, identifier, *, after_key=None, limit=None, stop_key=None):
        """Return raw session binding keys currently bound to ``identifier`` via SQLite project index."""
        if self.sessions_store is not None:
            if after_key is not None or limit is not None or stop_key is not None:
                return self.sessions_store.sessions_for_project_page(
                    identifier,
                    after_key=after_key or '',
                    limit=int(limit) if limit is not None else 64,
                    stop_key=stop_key,
                )
            return self.sessions_store.sessions_for_project(identifier)
        keys = sorted(raw for raw, bound in self.state.get('sessions', {}).items() if bound == identifier)
        if after_key is not None:
            keys = [k for k in keys if k > after_key]
        if stop_key is not None:
            keys = [k for k in keys if k <= stop_key]
        if limit is not None:
            keys = keys[:int(limit)]
        return keys

    def check_revision(self, expected):
        if type(expected) is not int or expected != self.state['revision']:
            raise PolicyError('revision_conflict', 409)

    def _project_policy_values(self, project, agent_id):
        capture = project_capture_fields(project)
        return effective(
            self.state['defaults'],
            project.get('overrides', {}).get(agent_id, {}),
            protected=is_protected_project(project),
            project_auto_capture=capture['auto_capture'],
            auto_capture_version=capture['auto_capture_version'],
            auto_capture_updated_at=capture['auto_capture_updated_at'],
        )

    def _group_expected_permissions(self, group, agent_id):
        if group.get('kind') == 'common':
            override = (group.get('overrides') or {}).get(agent_id, {})
            if override.get('access') == 'none':
                return set()
            return {'read'}
        return set(self._project_policy_values(group, agent_id)['permissions'])

    def policy(self, project, agent_id):
        if agent_id not in self.state['agents']:
            raise PolicyError('agent_not_registered', 403)
        if agent_id in self.state.get('agent_retirements', {}):
            raise PolicyError('agent_retired', 403)
        values = self._project_policy_values(project, agent_id)
        applied = project['applied'].get(agent_id, {})
        ready = (project['status'] == 'verified' and
                 applied.get('revision') == project['desired_revision'] and applied.get('status') == 'verified')
        return {'project_id': project['id'], 'dataset_id': project['dataset_id'],
                'dataset_name': project['dataset_name'], 'revision': self.state['revision'],
                'desired_revision': project['desired_revision'], 'ready': ready,
                'applied': copy.deepcopy(applied), 'policy': values, 'code_enabled': project.get('code_enabled', False),
                'capture_policy': values['capture_policy'],
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
        allowed = bool(self._group_expected_permissions(common, agent_id))
        return {'dataset_id': common['dataset_id'], 'revision': common['desired_revision'],
                'ready': ready, 'readable': ready and allowed}

    def _can_batch_effective_permissions(self):
        return (
            callable(getattr(self.official, 'effective_permissions_batch', None))
            and 'effective_permissions' not in vars(self.official)
            and type(self.official).effective_permissions is Official.effective_permissions
        )

    async def checked_policy(self, project, agent_id):
        """Read-only recheck before local caches can be used by a hook.

        Out-of-band ACL/role changes must withdraw cached delivery too. This
        never repairs grants or promotes an unconfirmed configuration. Transient
        probe errors fail closed for the current response without poisoning
        durable verified state across agents.
        """
        agent = self.state['agents'].get(agent_id)
        if not agent:
            raise PolicyError('agent_not_registered', 403)
        if agent_id in self.state.get('agent_retirements', {}):
            raise PolicyError('agent_retired', 403)
        groups = [project, *([self.state['common']] if self.state.get('common') else [])]
        verified_groups = [group for group in groups if group['status'] == 'verified']
        changed = False
        transient_errors = {}
        if verified_groups:
            def _expected_permissions(group):
                return self._group_expected_permissions(group, agent_id)

            def _mark_unconfirmed(group, error_code):
                nonlocal changed
                group['status'], group['error_code'] = 'unconfirmed', error_code
                group['applied'][agent_id] = {'status': 'unconfirmed', 'error_code': error_code}
                changed = True

            if self._can_batch_effective_permissions():
                dataset_ids = []
                for group in verified_groups:
                    if group.get('dataset_id'):
                        dataset_ids.append(group['dataset_id'])
                    if group.get('code_dataset_id'):
                        dataset_ids.append(group['code_dataset_id'])
                try:
                    perm_map = await self.official.effective_permissions_batch(agent, dataset_ids)
                except PolicyError as error:
                    if _is_transient_policy_probe_error(error):
                        for group in verified_groups:
                            transient_errors[id(group)] = error.code
                    else:
                        for group in verified_groups:
                            _mark_unconfirmed(group, error.code)
                else:
                    for group in verified_groups:
                        expected = _expected_permissions(group)
                        if perm_map.get(group.get('dataset_id'), set()) != set(expected):
                            _mark_unconfirmed(group, 'effective_acl_changed')
                        elif group.get('code_dataset_id'):
                            expected_code = set(expected) if group.get('code_enabled') else set()
                            if perm_map.get(group['code_dataset_id'], set()) != expected_code:
                                _mark_unconfirmed(group, 'effective_code_acl_changed')
            else:
                for group in verified_groups:
                    expected = _expected_permissions(group)
                    try:
                        actual = await self.official.effective_permissions(agent, group['dataset_id'])
                        if actual != set(expected):
                            raise PolicyError('effective_acl_changed', 503)
                        if group.get('code_dataset_id'):
                            actual = await self.official.effective_permissions(agent, group['code_dataset_id'])
                            if actual != (set(expected) if group.get('code_enabled') else set()):
                                raise PolicyError('effective_code_acl_changed', 503)
                    except PolicyError as error:
                        if _is_transient_policy_probe_error(error):
                            transient_errors[id(group)] = error.code
                        else:
                            _mark_unconfirmed(group, error.code)
        if changed:
            self.state['revision'] += 1
            self.save()
        result = self.policy(project, agent_id)
        if id(project) in transient_errors:
            err_code = transient_errors[id(project)]
            result['ready'] = False
            result['applied'] = {'status': 'unconfirmed', 'error_code': err_code}
            if isinstance(result.get('code'), dict):
                result['code']['ready'] = False
        common = self.state.get('common')
        if common is not None and id(common) in transient_errors and isinstance(result.get('common'), dict):
            result['common']['ready'] = False
            result['common']['readable'] = False
        return result

    def directory(self):
        from tool_graph_policy import settings as tool_graph_settings
        return {'revision': self.state['revision'], 'defaults': copy.deepcopy(self.state['defaults']),
                'tool_graph': tool_graph_settings(self.state),
                'agents': [{'id': key, 'name': value['name'], 'kind': value.get('kind', 'unknown'),
                            'capabilities': copy.deepcopy(value.get('capabilities', {})),
                            'entrypoints': copy.deepcopy(value.get('entrypoints', [])),
                            'installation': _installation_view(value.get('installation'))}
                           for key, value in self.state['agents'].items()],
                'enrollments': {key: {field: value[field] for field in ('status', 'error_code') if field in value}
                                for key, value in self.state.get('enrollments', {}).items()},
                'projects': [{**copy.deepcopy(project), **project_capture_fields(project),
                              'tool_graph': tool_graph_settings(self.state, project['id'])}
                             for project in self.state['projects'].values()],
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
            gateway_url = loopback_origin(control_url) + '/cognee'
            agent['installation'] = prepare(plugin_source, self.path.parent, instance_id, agent,
                                             gateway_url, control_url, host_home)
            self.state['revision'] += 1
            self.save()
            return self.directory()

    def managed_groups(self):
        return [*self.state['projects'].values(), *([self.state['common']] if self.state.get('common') else [])]

    @staticmethod
    def _group_needs_read_only_verify(group):
        return bool(
            isinstance(group, dict)
            and group.get('status') == 'unconfirmed'
            and group.get('error_code') == 'official_result_unknown'
            and group.get('dataset_id')
            and not group.get('import_acl')
        )

    async def _verify_group_locked(self, group):
        # Recovery from an unknown read must not repeat permission
        # mutations or create a missing dataset. Check every live
        # principal before publishing a verified group again.
        applied = {}
        try:
            if group.get('import_acl') or not group.get('dataset_id'):
                raise PolicyError('existing_acl_requires_review', 409)
            use_batch = self._can_batch_effective_permissions()
            is_common = group.get('kind') == 'common'
            for agent_id, agent in self.state['agents'].items():
                if agent_id in self.state.get('agent_retirements', {}):
                    applied[agent_id] = {'status': 'retiring'}
                    continue
                permissions = self._group_expected_permissions(group, agent_id)
                if use_batch and group.get('code_dataset_id'):
                    perm_map = await self.official.effective_permissions_batch(
                        agent, [group['dataset_id'], group['code_dataset_id']])
                    if perm_map.get(group['dataset_id'], set()) != permissions:
                        raise PolicyError('effective_acl_changed', 503)
                    expected_code = permissions if group.get('code_enabled') else set()
                    if perm_map.get(group['code_dataset_id'], set()) != expected_code:
                        raise PolicyError('effective_code_acl_changed', 503)
                else:
                    if await self.official.effective_permissions(agent, group['dataset_id']) != permissions:
                        raise PolicyError('effective_acl_changed', 503)
                    if group.get('code_dataset_id'):
                        expected_code = permissions if group.get('code_enabled') else set()
                        if await self.official.effective_permissions(agent, group['code_dataset_id']) != expected_code:
                            raise PolicyError('effective_code_acl_changed', 503)
                applied[agent_id] = {'status': 'verified', 'revision': group['desired_revision']}
            group['applied'] = applied
            group['status'] = 'verified'
            group.pop('error_code', None)
            return True
        except PolicyError as error:
            group['status'], group['error_code'] = 'unconfirmed', error.code
            return False

    async def verify_unconfirmed_groups(self):
        if not any(self._group_needs_read_only_verify(group) for group in self.managed_groups()):
            return 0
        recovered = 0
        changed = False
        async with self.mutex:
            for group in self.managed_groups():
                if not self._group_needs_read_only_verify(group):
                    continue
                before_error = group.get('error_code')
                if await self._verify_group_locked(group):
                    recovered += 1
                    changed = True
                elif group.get('error_code') != before_error:
                    changed = True
            if changed:
                if recovered:
                    self.state['revision'] += 1
                self.save()
        return recovered

    async def ensure_common(self, expected, *, reconcile=False, verify_only=False):
        if type(verify_only) is not bool:
            raise PolicyError('invalid_verify_only')
        async with self.mutex:
            self.check_revision(expected)
            common = self.state.get('common')
            if common is None:
                self.state['revision'] += 1
                identifier = str(uuid.uuid5(uuid.UUID(self.state['service_namespace']), 'common-knowledge'))
                common_overrides = {
                    aid: {'access': 'none'}
                    for aid, agent in self.state['agents'].items()
                    if agent.get('default_access') == 'none'
                }
                common = {'id': identifier, 'kind': 'common', 'name': '通用记忆',
                          'dataset_id': None, 'dataset_name': 'ml-common-' + identifier,
                          'overrides': common_overrides, 'applied': {}, 'desired_revision': self.state['revision'],
                          'operation_id': secrets.token_hex(16), 'status': 'unconfirmed', 'code_enabled': False}
                self.state['common'] = common
                self.save()
                await self.reconcile_locked(common)
            elif reconcile:
                if verify_only:
                    await self._verify_group_locked(common)
                    self.save()
                else:
                    await self.reconcile_locked(common)
            return self.directory()

    async def reconcile(self, expected, identifier, *, verify_only=False):
        if type(verify_only) is not bool:
            raise PolicyError('invalid_verify_only')
        async with self.mutex:
            self.check_revision(expected)
            project = self.state['projects'].get(identifier)
            if project is None:
                raise PolicyError('project_not_found', 404)
            if verify_only:
                await self._verify_group_locked(project)
                self.save()
            else:
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

    async def _verify_ungranted_agent_locked(self, instance_id):
        """Read-only check confirming a newly provisioned computer-level identity has zero ACLs."""
        agent = self.state['agents'][instance_id]
        groups = [group for group in self.managed_groups() if group.get('status') == 'verified' and group.get('dataset_id')]
        if not groups:
            return
        use_batch = self._can_batch_effective_permissions()
        if use_batch:
            dataset_ids = []
            for group in groups:
                dataset_ids.append(group['dataset_id'])
                if group.get('code_dataset_id'):
                    dataset_ids.append(group['code_dataset_id'])
            try:
                perm_map = await self.official.effective_permissions_batch(agent, dataset_ids)
            except PolicyError as error:
                for group in groups:
                    group['applied'][instance_id] = {'status': 'unconfirmed', 'error_code': error.code}
                return
            for group in groups:
                has_grant = bool(perm_map.get(group['dataset_id'], set())) or bool(
                    group.get('code_dataset_id') and perm_map.get(group['code_dataset_id'], set())
                )
                if has_grant:
                    group['applied'][instance_id] = {'status': 'unconfirmed', 'error_code': 'effective_acl_mismatch'}
                else:
                    group['applied'][instance_id] = {'status': 'verified', 'revision': group['desired_revision']}
            return
        for group in groups:
            try:
                actual = await self.official.effective_permissions(agent, group['dataset_id'])
                if actual:
                    raise PolicyError('effective_acl_mismatch', 503)
                if group.get('code_dataset_id'):
                    actual_code = await self.official.effective_permissions(agent, group['code_dataset_id'])
                    if actual_code:
                        raise PolicyError('effective_acl_mismatch', 503)
                group['applied'][instance_id] = {'status': 'verified', 'revision': group['desired_revision']}
            except PolicyError as error:
                group['applied'][instance_id] = {'status': 'unconfirmed', 'error_code': error.code}

    async def add_agent(self, instance_id, name, principal_id, key_file, expected):
        """Only management can enroll a provisioned official sub-user, never an owner."""
        async with self.mutex:
            self.check_revision(expected)
            if instance_id in self.state['agents']:
                raise PolicyError('agent_already_registered', 409)
            agent = {'name': name, 'principal_id': str(uuid.UUID(principal_id)),
                     'key_file': str(Path(key_file).absolute()), 'default_access': 'none'}
            me = await self.official.request('GET', '/api/v1/users/me', token_file=agent['key_file'])
            owner = await self.official.request('GET', '/api/v1/users/me')
            if me['id'] != agent['principal_id'] or me.get('parent_user_id') != owner['id']:
                raise PolicyError('independent_agent_required', 403)
            if any(a['principal_id'] == agent['principal_id'] for a in self.state['agents'].values()):
                raise PolicyError('principal_already_registered', 409)
            self.state['agents'][instance_id] = agent
            self.state['revision'] += 1
            for group in self.managed_groups():
                group.setdefault('overrides', {}).setdefault(instance_id, {}).setdefault('access', 'none')
            self.save()
            await self._verify_ungranted_agent_locked(instance_id)
            self.save()
            return self.directory()

    async def retire_agent(self, expected, instance_id):
        """Revoke a retired installation's ACL before archiving its identity.

        Never deletes official content, credentials, buffers or source bindings.
        An interrupted retirement stays denied until explicitly resumed.
        """
        async with self.mutex:
            if instance_id in self.state.get('archived_agents', {}):
                return self.directory()
            self.check_revision(expected)
            agent = self.state['agents'].get(instance_id)
            if not agent:
                raise PolicyError('agent_not_registered', 404)
            record = self.state.setdefault('agent_retirements', {}).setdefault(instance_id, {
                'started_at': time.time(), 'status': 'revoking'})
            record.setdefault('prior_applied', {group['id']: copy.deepcopy(group['applied'].get(instance_id))
                                                for group in self.managed_groups()})
            self.state['revision'] += 1
            for group in self.managed_groups():
                group['applied'][instance_id] = {'status': 'retiring'}
            self.save()
            try:
                datasets = {group.get(field) for group in self.managed_groups()
                            for field in ('dataset_id', 'code_dataset_id')}
                datasets.update(await self.official.retirement_datasets(agent))
                datasets.update(record.get('datasets', []))
                record['datasets'] = sorted(datasets - {None, ''})
                self.save()
                for dataset in record['datasets']:
                    await self.official.sync_acl(agent, dataset, set())
            except PolicyError as error:
                record.update(status='unconfirmed', error_code=error.code)
                self.save()
                raise
            archive = copy.deepcopy(agent)
            archive.update(retired_at=time.time(), project_policies={})
            for group in self.managed_groups():
                archive['project_policies'][group['id']] = {
                    'override': group['overrides'].pop(instance_id, None),
                    'applied': record['prior_applied'].get(group['id'])}
                group['applied'].pop(instance_id, None)
            self.state.setdefault('archived_agents', {})[instance_id] = archive
            del self.state['agents'][instance_id]
            record.update(status='complete', completed_at=time.time())
            self.save()
            return self.directory()

    async def provision_agent(self, expected, instance_id, name):
        """Persist a single creation attempt; unknown creates require explicit recovery."""
        if not isinstance(instance_id, str) or not re.fullmatch(r'[a-zA-Z0-9_-]{1,64}', instance_id):
            raise PolicyError('invalid_installation_id')
        if not isinstance(name, str) or not 1 <= len(name.strip()) <= 100:
            raise PolicyError('invalid_agent_name')
        async with self.mutex:
            self.check_revision(expected)
            if instance_id in self.state.get('archived_agents', {}):
                raise PolicyError('retired_agent_requires_explicit_restore', 409)
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
                                                      'key_file': str(key_file), 'control_token_file': str(control_file),
                                                      'default_access': 'none'}
                enrollments[instance_id]['status'] = 'verified'
                # Computer-level provisioning establishes an independent subject
                # without granting project or common memory access.
                for group in self.managed_groups():
                    group.setdefault('overrides', {}).setdefault(instance_id, {}).setdefault('access', 'none')
                self.save()
                await self._verify_ungranted_agent_locked(instance_id)
                self.save()
            except (PolicyError, KeyError, ValueError) as error:
                enrollments[instance_id]['error_code'] = error.code if isinstance(error, PolicyError) else 'agent_creation_result_unknown'
                self.save()
            return self.directory()

    async def recover_agent(self, expected, instance_id):
        """Verify the original saved identity only; never create a replacement."""
        async with self.mutex:
            self.check_revision(expected)
            if instance_id in self.state.get('archived_agents', {}):
                raise PolicyError('retired_agent_requires_explicit_restore', 409)
            enrollment = self.state.get('enrollments', {}).get(instance_id)
            if not enrollment or not enrollment.get('key_file') or instance_id in self.state['agents']:
                raise PolicyError('agent_recovery_requires_original_credentials', 409)
            me = await self.official.request('GET', '/api/v1/users/me', token_file=enrollment['key_file'])
            owner = await self.official.request('GET', '/api/v1/users/me')
            if me['id'] != enrollment['principal_id'] or me.get('parent_user_id') != owner['id']:
                raise PolicyError('independent_agent_required', 503)
            self.state['agents'][instance_id] = {
                **{field: enrollment[field] for field in ('name', 'principal_id', 'key_file', 'control_token_file')},
                'default_access': 'none',
            }
            enrollment['status'] = 'verified'
            enrollment.pop('error_code', None)
            self.state['revision'] += 1
            for group in self.managed_groups():
                group.setdefault('overrides', {}).setdefault(instance_id, {}).setdefault('access', 'none')
            self.save()
            await self._verify_ungranted_agent_locked(instance_id)
            self.save()
            return self.directory()

    async def session_binding(self, agent_id, host_session_key):
        """Read this subject's original binding; never discover or create a project."""
        if not isinstance(host_session_key, str) or not 1 <= len(host_session_key) <= 256:
            raise PolicyError('host_session_key_required')
        async with self.mutex:
            if agent_id not in self.state['agents']:
                raise PolicyError('agent_not_registered', 403)
            identifier = self.state['sessions'].get(json.dumps([agent_id, host_session_key]))
            project = self.state['projects'].get(identifier)
            if project is None:
                raise PolicyError('session_binding_not_found', 404)
            return await self.checked_policy(project, agent_id)

    async def resolve(self, agent_id, workspace, host_session_key, *, explicit=False, host_environment=None):
        if not isinstance(host_session_key, str) or not 1 <= len(host_session_key) <= 256:
            raise PolicyError('host_session_key_required')
        async with self.mutex:
            if agent_id not in self.state['agents']:
                raise PolicyError('agent_not_registered', 403)
            if agent_id in self.state.get('agent_retirements', {}):
                raise PolicyError('agent_retired', 403)
            environment = self.check_host_environment(agent_id, host_environment)
            key = json.dumps([agent_id, host_session_key])
            existing = self.state['sessions'].get(key)
            if existing:
                # Host binding wins over temporary cd, and unconfirmed operations are
                # never silently retried by repeated hook events.
                result = self.policy(self.state['projects'][existing], agent_id)
                self.record_connection_receipt(agent_id, existing, host_session_key, environment)
                return result
            root = workspace_root(workspace, explicit=explicit)
            project = await self.ensure_locked(root)
            self.state['sessions'][key] = project['id']
            if self.sessions_store is not None:
                self.sessions_store.upsert_session(key, project['id'], revision=int(self.state.get('revision') or 0))
            else:
                self.save()
            self.record_connection_receipt(agent_id, project['id'], host_session_key, environment)
            return self.policy(project, agent_id)

    def check_host_environment(self, agent_id, payload):
        if payload is None:
            return {'environment_verified': False}
        allowed = {'instance_id', 'plugin_path', 'plugin_version', 'runtime_profile'}
        if (not isinstance(payload, dict) or set(payload) - allowed
                or any(not isinstance(v, str) or len(v) > 4096 for v in payload.values())
                or payload.get('instance_id') != agent_id):
            raise PolicyError('host_environment_receipt_invalid', 403)
        agent = self.state['agents'][agent_id]
        installation = agent.get('installation') or {}
        profile = agent.get('host_profile')
        if not profile or not installation.get('plugin_dir'):
            return {**payload, 'environment_verified': False}
        plugin = Path(payload.get('plugin_path', ''))
        source = Path(installation['plugin_dir'])
        cache = Path(profile) / 'plugins/cache' / installation['marketplace_name']
        connection = self.state.get('connections', {}).get(agent_id, {})
        native_path = connection.get('native_plugin_dir')
        native_installation = (agent.get('kind') == 'antigravity' and native_path
                               and plugin == Path(native_path) and plugin.parent == Path(profile) / 'plugins')
        if not plugin.is_absolute() or not (plugin == source or cache in plugin.parents or native_installation):
            raise PolicyError('host_environment_receipt_mismatch', 403)
        if payload.get('runtime_profile') and Path(payload['runtime_profile']).resolve() != Path(profile).resolve():
            raise PolicyError('host_environment_receipt_mismatch', 403)
        try:
            marker = json.loads((plugin / '.memlane-installation.json').read_text())
            if marker.get('instance_id') != agent_id:
                raise ValueError()
            manifest = json.loads((plugin / '.memlane-project-access.json').read_text())
            if manifest.get('files') != json.loads((source / '.memlane-project-access.json').read_text()).get('files'):
                raise ValueError()
        except (OSError, ValueError, TypeError):
            raise PolicyError('host_environment_artifact_unconfirmed', 403) from None
        ide_installation = (agent.get('kind') == 'trae_ide' and plugin == source
                            and connection.get('activated')
                            and connection.get('status') == 'configured_pending_new_session'
                            and marker.get('runtime_profile') == profile)
        return {**payload, 'environment_verified': bool(payload.get('runtime_profile') or cache in plugin.parents
                                                         or native_installation or ide_installation),
                'artifact_verified': True, 'profile': profile}

    def record_connection_receipt(self, agent_id, project_id, session_key, environment):
        receipts = self.state.setdefault('connection_receipts', {})
        previous = receipts.get(agent_id, {})
        agent_kind = self.state.get('agents', {}).get(agent_id, {}).get('kind')
        records = 'codex_connections' if agent_kind == 'codex' else 'connections'
        activated_at = self.state.get(records, {}).get(agent_id, {}).get('activated_at', 0)
        now = time.time()
        if (previous.get('host_session_key') == session_key
                and previous.get('environment_verified') == environment.get('environment_verified')
                and previous.get('received_at', 0) >= activated_at
                and now - previous.get('received_at', 0) < 30):
            return
        recent = [{'host_session_key': session_key, 'project_id': project_id, 'received_at': now}]
        seen = {session_key}
        candidates = list(previous.get('recent_sessions') or [])
        if previous.get('host_session_key') and previous.get('project_id'):
            candidates.append({'host_session_key': previous.get('host_session_key'),
                               'project_id': previous.get('project_id'),
                               'received_at': previous.get('received_at', 0)})
        for item in candidates:
            if not isinstance(item, dict):
                continue
            cand_key = item.get('host_session_key')
            cand_project = item.get('project_id')
            if not cand_key or not cand_project or cand_key in seen:
                continue
            seen.add(cand_key)
            recent.append({'host_session_key': cand_key, 'project_id': cand_project,
                           'received_at': item.get('received_at', 0)})
            if len(recent) >= 8:
                break
        receipts[agent_id] = {'received_at': now, 'project_id': project_id,
                              'host_session_key': session_key,
                              'evidence': 'authenticated_host_resolve',
                              'recent_sessions': recent, **environment}
        if self.sessions_store is not None:
            self.sessions_store.upsert_receipt(agent_id, receipts[agent_id], revision=int(self.state.get('revision') or 0))
        else:
            self.save()

    async def ensure_locked(self, root, dataset_id=None, name=None, source=None):
        for project in self.state['projects'].values():
            if root and root in project['aliases']:
                if dataset_id and project['dataset_id'] != dataset_id:
                    raise PolicyError('project_binding_conflict', 409)
                return project
        if dataset_id and any(p['dataset_id'] == dataset_id for p in self.state['projects'].values()):
            raise PolicyError('dataset_already_linked', 409)
        if dataset_id and ((self.state.get('common') or {}).get('dataset_id') == dataset_id
                           or any(group.get('code_dataset_id') == dataset_id for group in self.state['projects'].values())
                           or dataset_id in self.state.get('reserved_datasets', [])):
            raise PolicyError('non_project_dataset_cannot_be_linked', 409)
        if root is None:
            row = await self.official.owned_dataset('', dataset_id)
            name = name or row['name']
        identifier = project_id(self.state['service_namespace'], root if root is not None else 'dataset:' + dataset_id)
        project_name = name or Path(root).name
        dataset_name = 'ml-project-' + identifier
        protected = is_protected_project({'name': project_name, 'dataset_name': dataset_name})
        initial_overrides = {}
        if not dataset_id:
            for aid, agent in self.state['agents'].items():
                if protected or agent.get('default_access') == 'none':
                    initial_overrides[aid] = {'access': 'none'}
        self.state['revision'] += 1
        project = {'id': identifier, 'name': project_name, 'aliases': [root] if root is not None else [],
                   'dataset_id': dataset_id, 'dataset_name': dataset_name,
                   'overrides': initial_overrides, 'applied': {}, 'desired_revision': self.state['revision'],
                   'operation_id': secrets.token_hex(16), 'status': 'unconfirmed',
                   'import_acl': bool(dataset_id),
                   'code_enabled': False, 'auto_capture': True, 'auto_capture_version': 1,
                   'source': source or ('associated' if dataset_id else 'discovered')}
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
                if agent_id in self.state.get('agent_retirements', {}):
                    project['applied'][agent_id] = {'status': 'retiring'}
                    continue
                permissions = self._group_expected_permissions(project, agent_id)
                await self.official.sync_acl(agent, row['id'], permissions)
                if project.get('code_dataset_id'):
                    # The optional group follows project access only while
                    # explicitly enabled, including effective-role verification.
                    await self.official.owned_dataset('', project['code_dataset_id'])
                    await self.official.sync_acl(agent, project['code_dataset_id'],
                                                permissions if project.get('code_enabled') else set())
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

    async def set_auto_capture(self, identifier, enabled):
        if type(enabled) is not bool:
            raise PolicyError('invalid_policy_boolean')
        async with self.mutex:
            project = self.state['projects'].get(identifier)
            if project is None:
                raise PolicyError('project_not_found', 404)
            capture = project_capture_fields(project)
            if enabled != capture['auto_capture']:
                revision = self.state['revision']
                updated = {**project, 'auto_capture': enabled,
                           'auto_capture_version': capture['auto_capture_version'] + 1,
                           'auto_capture_updated_at': datetime.now(timezone.utc).isoformat()}
                self.state['projects'][identifier] = updated
                self.state['revision'] += 1
                try:
                    self.save()
                except (OSError, sqlite3.Error):
                    self.state['projects'][identifier] = project
                    self.state['revision'] = revision
                    raise
                project = updated
            if not enabled and self.backlog is not None:
                self.backlog.on_auto_capture_disabled(identifier)
            capture = project_capture_fields(project)
            policy = effective(self.state['defaults'], {}, protected=is_protected_project(project),
                               project_auto_capture=enabled,
                               auto_capture_version=capture['auto_capture_version'],
                               auto_capture_updated_at=capture['auto_capture_updated_at'])
            return {**copy.deepcopy(project), **capture, 'capture_policy': policy['capture_policy'],
                    'revision': self.state['revision']}

    async def set_policy(self, expected, fields, *, project_id=None, agent_id=None):
        values = validate_fields(fields, allow_reset=project_id is not None)
        async with self.mutex:
            self.check_revision(expected)
            if project_id:
                common = self.state.get('common')
                if common and project_id == common['id']:
                    if agent_id not in self.state['agents']:
                        raise PolicyError('project_or_agent_not_found', 404)
                    if set(values) - {'access'} or values.get('access') == 'maintain':
                        raise PolicyError('common_read_only', 400)
                    overrides = common.setdefault('overrides', {}).setdefault(agent_id, {})
                    if 'access' in values:
                        if values['access'] is None:
                            overrides.pop('access', None)
                        else:
                            overrides['access'] = values['access']
                    affected = [common]
                else:
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

    async def adopt_runtime_projects(self, projects, reserved=()):
        """One-time bootstrap input; the registry owns all later associations."""
        if self.state.get('runtime_catalog_imported'):
            return
        identifiers = set(self.state.get('reserved_datasets', [])) | set(reserved)
        identifiers.update(p['code_dataset_id'] for p in projects if p.get('code_dataset_id'))
        self.state['reserved_datasets'] = sorted(identifiers)
        self.save()
        for project in projects:
            linked = await self.create_project(self.state['revision'], project['path'],
                                               dataset_id=project['dataset_id'], name=project['name'])
            if project.get('code_dataset_id') and not linked.get('code_configured'):
                await self.configure_code(self.state['revision'], linked['id'], project['code_dataset_id'],
                                          project.get('code_enabled', False), apply=linked['status'] == 'verified')
        self.state['runtime_catalog_imported'] = True
        self.save()

    async def create_project(self, expected, workspace=None, *, dataset_id=None, name=None):
        if dataset_id:
            dataset_id = str(uuid.UUID(dataset_id))
        root = None if dataset_id and workspace in (None, '') else workspace_root(workspace, explicit=True)
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
