"""Reading-state mutations and legacy provenance migration."""
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import stat

from project_policy import PolicyError
from provenance_store import _calendar_date
from sqlite_ro import uuid_text
from v2_api_context import (
    V2ApiContext,
    _has_unconfirmed_groups,
    _require_verified_project,
    _verified_groups,
)


_LEGACY_KEY_RE = re.compile(r'^[0-9a-f]{64}$')


def _archive_legacy_dates_if_unchanged(legacy_path: Path, expected_stat: tuple[int, int, int]) -> tuple[str, bool]:
    """Revalidate and archive under the same sidecar lock used by the desktop writer."""
    lock_path = legacy_path.with_name(legacy_path.name + '.lock')
    try:
        lock_fd = os.open(lock_path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600)
    except OSError:
        return '', False
    try:
        if not stat.S_ISREG(os.fstat(lock_fd).st_mode):
            return '', False
        fcntl.flock(lock_fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        cur_st = os.lstat(legacy_path)
        if (
            not stat.S_ISREG(cur_st.st_mode)
            or (cur_st.st_ino, cur_st.st_size, cur_st.st_mtime_ns) != expected_stat
        ):
            return '', False
        bak = legacy_path.with_name(legacy_path.name + '.migrated.bak')
        os.replace(legacy_path, bak)
        return str(bak), True
    except OSError:
        return '', False
    finally:
        os.close(lock_fd)


async def execute_reading_migrate_local(ctx: V2ApiContext, body: dict) -> dict:
    state_dir = ctx.state_dir
    official_data = ctx.official_data
    registry = ctx.registry
    cat = ctx.cat
    read_store = ctx.read_store

    if set(body) - {'reading_identity', 'projects'} or 'reading_identity' not in body:
        raise PolicyError('invalid_request_fields', 400)
    identity = body['reading_identity']
    if not isinstance(identity, str) or not identity:
        raise PolicyError('invalid_reading_identity', 400)

    req_projects = body.get('projects')
    targets = []
    unconfirmed = []
    if req_projects is None:
        verified_projs, _ = _verified_groups(registry)
        targets = [(p['id'], p['dataset_id']) for p in verified_projs]
    else:
        if not isinstance(req_projects, list):
            raise PolicyError('invalid_request_fields', 400)
        for item in req_projects:
            if not isinstance(item, dict) or set(item) != {'project_id', 'dataset_id'}:
                raise PolicyError('invalid_request_fields', 400)
            pid, ds_id = str(item['project_id']), uuid_text(item['dataset_id'])
            try:
                proj = _require_verified_project(registry, pid)
            except PolicyError as exc:
                if exc.code != 'project_unconfirmed':
                    raise
                unconfirmed.append({
                    'project_id': pid, 'dataset_id': ds_id,
                    'status': 'unconfirmed', 'unread_count': 0,
                })
                continue
            if proj['dataset_id'] != ds_id:
                raise PolicyError('dataset_access_denied', 403)
            targets.append((proj['id'], proj['dataset_id']))

    synced = False
    if official_data:
        try:
            await ctx.ensure_synced(fresh=True)
            synced = True
        except PolicyError:
            synced = False

    def _migrate():
        migrated = []
        for pid, ds_id in targets:
            res = read_store.migrate_local_file(
                state_dir,
                identity,
                pid,
                ds_id,
                catalog_index=cat if synced else None,
            )
            # CRITICAL (PY-P0-04): NEVER call compute_unread_ids when status == 'no_legacy_file',
            # as ensure_scope would pre-create an empty scope before custom-dir import!
            if synced and res.get('status') in ('migrated', 'already_migrated'):
                unread_cnt = len(read_store.compute_unread_ids(identity, pid, ds_id, cat))
            else:
                unread_cnt = 0
            res['unread_count'] = unread_cnt
            migrated.append(res)
        return migrated

    return {'migrated': await ctx.run_db(_migrate) + unconfirmed}


async def execute_reading_import_begin(ctx: V2ApiContext, body: dict) -> dict:
    registry = ctx.registry
    read_store = ctx.read_store

    if set(body) != {'reading_identity', 'project_id', 'dataset_id', 'initialized', 'enabled_at'}:
        raise PolicyError('invalid_request_fields', 400)
    identity = body['reading_identity']
    if not isinstance(identity, str) or not identity:
        raise PolicyError('invalid_reading_identity', 400)
    if type(body['initialized']) is not bool:
        raise PolicyError('invalid_request_fields', 400)
    proj = _require_verified_project(registry, str(body['project_id']))
    if proj['dataset_id'] != uuid_text(body['dataset_id']):
        raise PolicyError('dataset_access_denied', 403)
    res = await ctx.run_db(
        read_store.begin_import,
        identity,
        proj['id'],
        proj['dataset_id'],
        initialized=body['initialized'],
        enabled_at=str(body['enabled_at'] or ''),
    )
    return res


async def execute_reading_import_chunk(ctx: V2ApiContext, body: dict) -> dict:
    read_store = ctx.read_store

    if set(body) != {'batch_id', 'items'}:
        raise PolicyError('invalid_request_fields', 400)
    res = await ctx.run_db(read_store.stage_import_chunk, str(body['batch_id']), body['items'])
    return res


async def execute_reading_import_commit(ctx: V2ApiContext, body: dict) -> dict:
    official_data = ctx.official_data
    registry = ctx.registry
    cat = ctx.cat
    read_store = ctx.read_store

    if set(body) - {'batch_id', 'expected_items'} or 'batch_id' not in body:
        raise PolicyError('invalid_request_fields', 400)
    expected_items = None
    if 'expected_items' in body and body['expected_items'] is not None:
        if type(body['expected_items']) is not int or body['expected_items'] < 0:
            raise PolicyError('invalid_request_fields', 400)
        expected_items = body['expected_items']

    def _verify_project(pid: str, ds_id: str):
        proj = _require_verified_project(registry, pid)
        if proj['dataset_id'] != uuid_text(ds_id):
            raise PolicyError('dataset_access_denied', 403)

    synced = False
    if official_data:
        try:
            await ctx.ensure_synced(fresh=True)
            synced = True
        except PolicyError:
            synced = False
    res = await ctx.run_db(
        read_store.commit_import,
        str(body['batch_id']),
        catalog_index=cat if synced else None,
        expected_items=expected_items,
        verify_project=_verify_project,
    )
    return {
        'scope_hash': res['scope_hash'],
        'committed': res['committed'],
        'unread_count': int(res.get('unread_count', 0)),
        'state_rev': int(res.get('state_rev', 0)),
    }


async def execute_reading_mark_read(ctx: V2ApiContext, body: dict) -> dict:
    official_data = ctx.official_data
    registry = ctx.registry
    cat = ctx.cat
    read_store = ctx.read_store

    if set(body) != {'reading_identity', 'project_id', 'dataset_id', 'doc_ids'}:
        raise PolicyError('invalid_request_fields', 400)
    identity = body['reading_identity']
    if not isinstance(identity, str) or not identity:
        raise PolicyError('invalid_reading_identity', 400)
    doc_ids = body['doc_ids']
    if not isinstance(doc_ids, list) or not 1 <= len(doc_ids) <= 200:
        raise PolicyError('invalid_doc_ids', 400)
    proj = _require_verified_project(registry, str(body['project_id']))
    if proj['dataset_id'] != uuid_text(body['dataset_id']):
        raise PolicyError('dataset_access_denied', 403)
    synced = False
    if official_data:
        try:
            await ctx.ensure_synced(fresh=True)
            synced = True
        except PolicyError:
            synced = False
    res = await ctx.run_db(
        read_store.mark_read,
        identity,
        proj['id'],
        proj['dataset_id'],
        doc_ids,
        catalog_index=cat if synced else None,
    )
    return res


async def execute_reading_mark_all_read(ctx: V2ApiContext, body: dict) -> dict:
    official_data = ctx.official_data
    registry = ctx.registry
    cat = ctx.cat
    read_store = ctx.read_store

    if set(body) != {'reading_identity', 'project_id', 'dataset_id'}:
        raise PolicyError('invalid_request_fields', 400)
    identity = body['reading_identity']
    if not isinstance(identity, str) or not identity:
        raise PolicyError('invalid_reading_identity', 400)
    proj = _require_verified_project(registry, str(body['project_id']))
    if proj['dataset_id'] != uuid_text(body['dataset_id']):
        raise PolicyError('dataset_access_denied', 403)
    synced = False
    if official_data:
        try:
            await ctx.ensure_synced(fresh=True)
            synced = True
        except PolicyError:
            synced = False
    res = await ctx.run_db(
        read_store.mark_all_read,
        identity,
        proj['id'],
        proj['dataset_id'],
        catalog_index=cat if synced else None,
    )
    return res


async def execute_provenance_backfill_legacy_dates(ctx: V2ApiContext, body: dict) -> dict:
    state_dir = ctx.state_dir
    registry = ctx.registry
    manager_token = ctx.manager_token
    cat = ctx.cat

    if set(body) - {'reading_identity', 'days'} or 'reading_identity' not in body:
        raise PolicyError('invalid_request_fields', 400)
    identity = body['reading_identity']
    if not isinstance(identity, str) or not identity:
        raise PolicyError('invalid_reading_identity', 400)

    has_unconfirmed = _has_unconfirmed_groups(registry)
    legacy_path = None
    legacy_stat_sig = None
    if 'days' in body:
        raw_days = body['days']
        if not isinstance(raw_days, dict) or len(raw_days) > 200000:
            raise PolicyError('invalid_legacy_dates', 400)
        days = {}
        for k, v in raw_days.items():
            if not isinstance(k, str) or not _LEGACY_KEY_RE.match(k):
                raise PolicyError('invalid_legacy_dates', 400)
            day = _calendar_date(v)
            if not day:
                raise PolicyError('invalid_legacy_dates', 400)
            days[k] = day
    else:
        digest = hashlib.sha256(identity.encode('utf-8')).hexdigest()
        legacy_path = state_dir / f'knowledge-source-dates-{digest}.json'
        if not legacy_path.exists():
            return {
                'status': 'absent',
                'matched': 0,
                'imported': 0,
                'skipped': 0,
                'unmapped': 0,
                'has_unconfirmed_projects': has_unconfirmed,
                'backup_path': '',
            }
        try:
            st = os.lstat(legacy_path)
        except OSError as exc:
            raise PolicyError('invalid_legacy_dates', 400) from exc
        if legacy_path.is_symlink() or (st.st_mode & 0o077) != 0 or st.st_size > 8 * 1024 * 1024:
            raise PolicyError('invalid_legacy_dates', 400)
        legacy_stat_sig = (st.st_ino, st.st_size, st.st_mtime_ns)
        try:
            parsed = json.loads(legacy_path.read_text(encoding='utf-8'))
        except (OSError, ValueError) as exc:
            raise PolicyError('invalid_legacy_dates', 400) from exc
        if (
            not isinstance(parsed, dict)
            or parsed.get('schema_version') != 1
            or not isinstance(parsed.get('days'), dict)
            or len(parsed['days']) > 200000
        ):
            raise PolicyError('invalid_legacy_dates', 400)
        days = {}
        for k, v in parsed['days'].items():
            if not isinstance(k, str) or not _LEGACY_KEY_RE.match(k):
                raise PolicyError('invalid_legacy_dates', 400)
            day = _calendar_date(v)
            if not day:
                raise PolicyError('invalid_legacy_dates', 400)
            days[k] = day

    if not days:
        backup_path = ''
        archived_ok = True
        if legacy_path is not None and not has_unconfirmed:
            backup_path, archived_ok = _archive_legacy_dates_if_unchanged(legacy_path, legacy_stat_sig)
        return {
            'status': 'empty' if archived_ok else 'partial',
            'matched': 0,
            'imported': 0,
            'skipped': 0,
            'unmapped': 0,
            'has_unconfirmed_projects': has_unconfirmed,
            'backup_path': backup_path,
        }

    official_client = getattr(registry, 'official', None)
    if official_client is None:
        raise PolicyError('provenance_backfill_unavailable', 503)

    projects, common = await ctx.ensure_synced(fresh=True)
    allowed_ds = [p['dataset_id'] for p in projects]
    if common is not None:
        allowed_ds.append(common['dataset_id'])

    def _resolve_entries():
        entries = []
        matched_keys = set()
        if not allowed_ds:
            return entries, len(days)
        for ds_s, doc_s, lt_s, sk_s in cat.iter_knowledge_provenance_keys(allowed_ds):
            stable = f'{lt_s}/{sk_s}' if sk_s else doc_s
            primary_key = hashlib.sha256(f'{ds_s}/{stable}'.encode('utf-8')).hexdigest()
            found = []
            if primary_key in days:
                found.append(days[primary_key])
                matched_keys.add(primary_key)
            if sk_s:
                fallback_key = hashlib.sha256(f'{ds_s}/{doc_s}'.encode('utf-8')).hexdigest()
                if fallback_key != primary_key and fallback_key in days:
                    found.append(days[fallback_key])
                    matched_keys.add(fallback_key)
            if found:
                entries.append({
                    'dataset_id': ds_s,
                    'document_id': doc_s,
                    'lesson_type': lt_s,
                    'state_key': sk_s,
                    'first_source_date': min(found),
                })
        return entries, len(days) - len(matched_keys)

    entries, unmapped_count = await ctx.run_db(_resolve_entries)

    total_imported = 0
    total_server_skipped = 0
    chunk_size = 500
    for start in range(0, len(entries), chunk_size):
        chunk = entries[start : start + chunk_size]
        try:
            resp = await official_client.request(
                'POST',
                '/api/v1/memlane/provenance/backfill-legacy-dates',
                {'entries': chunk},
                adapter_token=manager_token,
            )
        except PolicyError as exc:
            raise PolicyError('provenance_backfill_unavailable', 503) from exc
        if not isinstance(resp, dict):
            raise PolicyError('provenance_backfill_unavailable', 503)
        total_imported += int(resp.get('imported') or 0)
        total_server_skipped += int(resp.get('skipped') or 0)

    # Contract #6 (GO-P1-07 / CC-P1-04): NEVER rename legacy .json to .migrated.bak while
    # unmapped entries remain, any project/common group is still unconfirmed, or file stat drifted!
    backup_path = ''
    archived_ok = True
    if legacy_path is not None and unmapped_count == 0 and not has_unconfirmed:
        backup_path, archived_ok = _archive_legacy_dates_if_unchanged(legacy_path, legacy_stat_sig)

    return {
        'status': 'migrated' if (unmapped_count == 0 and not has_unconfirmed and archived_ok) else 'partial',
        'matched': len(entries),
        'imported': total_imported,
        'skipped': unmapped_count + total_server_skipped,
        'unmapped': unmapped_count,
        'has_unconfirmed_projects': has_unconfirmed,
        'backup_path': backup_path,
    }
