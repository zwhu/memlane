"""Read-only paged session-history endpoint over official SQLite stores.

Runs strictly in ``project_control_server`` (outside upstream Cognee files) without
copying raw bodies or modifying Cognee's ``cognee_db`` / ``cache.db``.
"""
import asyncio
from datetime import datetime, timezone
import json
from pathlib import Path
import sqlite3
import uuid

from aiohttp import web

from _runtime_common import (
    id_variants,
    iso_text,
    parse_json,
    sql_in_clause,
    uuid_text,
    verify_sqlite_schema,
)
from cache_read_v2 import CacheSessionHydrator
from project_policy import PolicyError
from session_relations import descendant_ids_for_session, resolve_session_relations
from sqlite_ro import open_official_sqlite_ro

MAX_RAW_HISTORY_BYTES = 8 * 1024 * 1024
_COGNEE_SESSION_SCHEMA = {
    'session_records': frozenset({
        'session_id', 'user_id', 'dataset_id', 'agent_id', 'last_activity_at', 'started_at',
    }),
}
_CACHE_REQUIRED_SCHEMA = {
    'cache_qa_entries': frozenset({'seq', 'user_id', 'session_id', 'qa_id', 'payload', 'expires_at'}),
    'cache_trace_entries': frozenset({'seq', 'user_id', 'session_id', 'payload', 'expires_at'}),
}


def _is_uuid(value):
    if not isinstance(value, str):
        return False
    try:
        uuid.UUID(value.strip())
        return True
    except (ValueError, AttributeError):
        return False


def _safe_session_id(value):
    if not isinstance(value, str) or not 1 <= len(value) <= 256:
        return False
    if '/' in value or '\\' in value or '..' in value:
        return False
    return not any(ord(ch) < 32 or ord(ch) == 127 for ch in value)


def _cache_expiry_bounds():
    now = datetime.now(timezone.utc)
    return (
        now.strftime('%Y-%m-%d %H:%M:%S.%f'),
        now.strftime('%Y-%m-%dT'),
        now.strftime('%Y-%m-%dT%H:%M:%S.%f'),
    )


def _cache_table_has_expired(con, table, bounds):
    now_space, today_t, now_t = bounds
    row = con.execute(
        f'''SELECT 1 FROM {table}
            WHERE expires_at IS NOT NULL
              AND (expires_at <= ? OR (expires_at >= ? AND expires_at <= ?))
            LIMIT 1''',
        (now_space, today_t, now_t),
    ).fetchone()
    return row is not None


def _expiry_sql(has_expired, now_space):
    if not has_expired:
        return '', ()
    return " AND (expires_at IS NULL OR REPLACE(substr(expires_at, 1, 26), 'T', ' ') > ?)", (now_space,)


def _extract_question_label(payload):
    data = payload if isinstance(payload, dict) else parse_json(payload)
    if not isinstance(data, dict):
        return None
    q = data.get('question')
    if q:
        return str(q)[:120]
    return None


def read_cache_session_history(cache_db, *, owner, session_id, qa_offset=0, trace_offset=0, limit=20):
    """Read bounded QA and trace pages directly from Cognee's read-only SQLite cache.

    Returns a dict with ``conflict``, ``msg_count``, ``tool_calls``, ``label``,
    ``qas``, and ``traces``. Raises ``sqlite3.Error`` on database/schema failures
    so callers can surface 503 or fall back when appropriate.
    """
    cache_path = Path(cache_db)
    if cache_path.is_symlink() or not cache_path.is_file():
        raise sqlite3.OperationalError('cache database unavailable')
    owner_variants = id_variants(owner)
    if not owner_variants:
        raise sqlite3.OperationalError('missing session owner')
    user_sql, user_params = sql_in_clause(owner_variants)
    bounds = _cache_expiry_bounds()
    now_space = bounds[0]

    try:
        con = open_official_sqlite_ro(cache_path, timeout_ms=2000)
    except PolicyError as err:
        raise sqlite3.OperationalError(str(err)) from err
    try:
        verify_sqlite_schema(con, _CACHE_REQUIRED_SCHEMA)
        qa_expired = _cache_table_has_expired(con, 'cache_qa_entries', bounds)
        tr_expired = _cache_table_has_expired(con, 'cache_trace_entries', bounds)
        qa_exp_sql, qa_exp_params = _expiry_sql(qa_expired, now_space)
        tr_exp_sql, tr_exp_params = _expiry_sql(tr_expired, now_space)

        qa_where = f'user_id {user_sql} AND session_id = ?{qa_exp_sql}'
        qa_base_params = (*user_params, session_id, *qa_exp_params)
        tr_where = f'user_id {user_sql} AND session_id = ?{tr_exp_sql}'
        tr_base_params = (*user_params, session_id, *tr_exp_params)

        msg_count = int(
            con.execute(f'SELECT COUNT(*) FROM cache_qa_entries WHERE {qa_where}', qa_base_params).fetchone()[0]
        )
        tool_calls = int(
            con.execute(f'SELECT COUNT(*) FROM cache_trace_entries WHERE {tr_where}', tr_base_params).fetchone()[0]
        )
        if qa_offset > msg_count or trace_offset > tool_calls:
            return {'conflict': True}

        qas = []
        for (raw_payload,) in con.execute(
            f'SELECT payload FROM cache_qa_entries WHERE {qa_where} ORDER BY seq ASC LIMIT ? OFFSET ?',
            (*qa_base_params, limit, qa_offset),
        ):
            parsed = parse_json(raw_payload)
            if not isinstance(parsed, dict):
                raise ValueError('corrupt qa payload')
            qas.append(parsed)

        traces = []
        for (raw_payload,) in con.execute(
            f'SELECT payload FROM cache_trace_entries WHERE {tr_where} ORDER BY seq ASC LIMIT ? OFFSET ?',
            (*tr_base_params, limit, trace_offset),
        ):
            parsed = parse_json(raw_payload)
            if not isinstance(parsed, dict):
                raise ValueError('corrupt trace payload')
            traces.append(parsed)

        label = None
        if qa_offset == 0:
            for qa in qas:
                label = _extract_question_label(qa)
                if label is not None:
                    break
        if label is None and msg_count > 0 and (qa_offset > 0 or len(qas) < msg_count):
            for (raw_payload,) in con.execute(
                f'SELECT payload FROM cache_qa_entries WHERE {qa_where} ORDER BY seq ASC',
                qa_base_params,
            ):
                label = _extract_question_label(raw_payload)
                if label is not None:
                    break

        return {
            'conflict': False,
            'msg_count': msg_count,
            'tool_calls': tool_calls,
            'label': label,
            'qas': qas,
            'traces': traces,
        }
    finally:
        con.close()


def _hydrate_related_sessions(cache_db, rows):
    hydrator = CacheSessionHydrator()
    try:
        return hydrator.hydrate_sessions(cache_db, rows)
    finally:
        hydrator.close()


def _apply_relation_fields(target, rel):
    if not rel:
        return
    target['is_subagent'] = True
    if rel.get('parent_session_id'):
        target['parent_session_id'] = rel['parent_session_id']
    if rel.get('subagent_role'):
        target['subagent_role'] = rel['subagent_role']
    if rel.get('nesting_depth'):
        target['nesting_depth'] = int(rel['nesting_depth'])


def _read_session_history_page(
    official_data,
    dataset_id,
    session_id,
    *,
    qa_offset=0,
    trace_offset=0,
    limit=20,
    user_id=None,
    registry=None,
    codex_config=None,
):
    official_root = Path(official_data).resolve()
    cognee_db = official_root / 'system' / 'databases' / 'cognee_db'
    cache_db = official_root / 'system' / 'databases' / 'cache.db'
    if cognee_db.is_symlink() or not cognee_db.is_file():
        raise PolicyError('session_history_unavailable', 503)

    ds_norm = uuid_text(dataset_id)
    ds_variants = id_variants(ds_norm)
    ds_sql, ds_params = sql_in_clause(ds_variants)

    try:
        con = open_official_sqlite_ro(cognee_db, timeout_ms=2000)
    except (PolicyError, sqlite3.Error) as err:
        raise PolicyError('session_history_unavailable', 503) from err

    ds_sessions = {}
    try:
        verify_sqlite_schema(con, _COGNEE_SESSION_SCHEMA)
        if user_id:
            u_variants = id_variants(user_id)
            u_sql, u_params = sql_in_clause(u_variants)
            srow = con.execute(
                f'''SELECT session_id, user_id, dataset_id, agent_id, last_activity_at, started_at
                    FROM session_records
                    WHERE session_id = ? AND dataset_id {ds_sql} AND user_id {u_sql}
                    ORDER BY last_activity_at DESC LIMIT 1''',
                (session_id, *ds_params, *u_params),
            ).fetchone()
        else:
            srow = con.execute(
                f'''SELECT session_id, user_id, dataset_id, agent_id, last_activity_at, started_at
                    FROM session_records
                    WHERE session_id = ? AND dataset_id {ds_sql}
                    ORDER BY last_activity_at DESC LIMIT 1''',
                (session_id, *ds_params),
            ).fetchone()
        if srow is not None and (registry is not None or codex_config):
            for dsrow in con.execute(
                f'''SELECT session_id, user_id, dataset_id, agent_id, last_activity_at, started_at
                    FROM session_records
                    WHERE dataset_id {ds_sql}
                    ORDER BY last_activity_at DESC''',
                ds_params,
            ):
                ds_sid = str(dsrow[0] or '')
                if ds_sid and ds_sid not in ds_sessions:
                    ds_sessions[ds_sid] = {
                        'session_id': ds_sid,
                        'user_id': uuid_text(dsrow[1]),
                        'dataset_id': ds_norm,
                        'agent_id': str(dsrow[3] or ''),
                        'last_activity_at': iso_text(dsrow[4]),
                        'started_at': iso_text(dsrow[5]),
                    }
    except sqlite3.Error as err:
        raise PolicyError('session_history_unavailable', 503) from err
    finally:
        con.close()

    if srow is None:
        raise PolicyError('session_not_found', 404)

    owner = uuid_text(srow[1])
    if not owner or cache_db.is_symlink() or not cache_db.is_file():
        raise PolicyError('session_history_unavailable', 503)

    try:
        page = read_cache_session_history(
            cache_db,
            owner=owner,
            session_id=session_id,
            qa_offset=qa_offset,
            trace_offset=trace_offset,
            limit=limit,
        )
    except (sqlite3.Error, ValueError) as err:
        raise PolicyError('session_history_unavailable', 503) from err

    if page['conflict']:
        raise PolicyError('session_history_changed', 409)

    msg_count = page['msg_count']
    tool_calls = page['tool_calls']
    # Never silently serve an empty 200 page for an existing session from the
    # derived SQLite projection; surface 503 so desktop falls back to official
    # Cognee SessionManager HTTP to distinguish a genuinely empty session.
    if msg_count == 0 and tool_calls == 0:
        raise PolicyError('session_history_unavailable', 503)
    qas = page['qas']
    traces = page['traces']
    record = {
        'session_id': str(srow[0]),
        'user_id': owner,
        'dataset_id': ds_norm,
        'agent_id': str(srow[3] or ''),
        'last_activity_at': iso_text(srow[4]),
        'started_at': iso_text(srow[5]),
        'label': page['label'],
        'msg_count': msg_count,
        'tool_calls': tool_calls,
        'history_paging': True,
        'qa_offset': qa_offset,
        'trace_offset': trace_offset,
        'page_limit': limit,
        'qas': qas,
        'traces': traces,
        'qa_more': qa_offset + len(qas) < msg_count,
        'trace_more': trace_offset + len(traces) < tool_calls,
    }
    if ds_sessions:
        relations = resolve_session_relations(
            ds_sessions.keys(),
            registry=registry,
            codex_config=codex_config,
        )
        self_rel = relations.get(session_id)
        _apply_relation_fields(record, self_rel)
        parent_id = self_rel.get('parent_session_id', '') if self_rel else ''
        if parent_id and parent_id in ds_sessions:
            parent_summary_row = dict(ds_sessions[parent_id])
            if _hydrate_related_sessions(cache_db, [parent_summary_row]) and parent_summary_row.get('label'):
                record['parent_label'] = parent_summary_row['label']
        child_ids = descendant_ids_for_session(session_id, relations)
        if child_ids and qa_offset == 0 and trace_offset == 0:
            subagent_rows = []
            for cid in child_ids:
                base = ds_sessions.get(cid)
                if not base:
                    continue
                row = dict(base)
                _apply_relation_fields(row, relations.get(cid))
                subagent_rows.append(row)
            if subagent_rows:
                _hydrate_related_sessions(cache_db, subagent_rows)
                subagent_rows.sort(
                    key=lambda r: (r.get('last_activity_at') or '', r.get('session_id') or ''),
                    reverse=True,
                )
                record['subagents'] = subagent_rows
    encoded = json.dumps(record, ensure_ascii=False).encode('utf-8')
    if len(encoded) > MAX_RAW_HISTORY_BYTES:
        raise PolicyError('session_history_page_too_large', 413)
    return encoded


def attach_raw_records_read(app, registry, *, codex_config=None):
    official_data = (codex_config or {}).get('official_data')

    def require_managed_dataset(raw_dataset):
        if not _is_uuid(raw_dataset):
            raise PolicyError('dataset_access_denied', 403)
        ds_id = uuid_text(raw_dataset)
        allowed = {g['dataset_id'] for g in registry.managed_groups() if g.get('dataset_id')}
        if ds_id not in allowed:
            raise PolicyError('dataset_access_denied', 403)
        return ds_id

    async def raw_session_history(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        if set(request.query) - {'dataset_id', 'session_id', 'qa_offset', 'trace_offset', 'limit', 'user_id'}:
            raise PolicyError('invalid_raw_records_query')
        ds_id = require_managed_dataset(request.query.get('dataset_id', ''))
        session_id = request.query.get('session_id', '')
        if not _safe_session_id(session_id):
            raise PolicyError('invalid_raw_records_query')
        user_id = request.query.get('user_id')
        if user_id is not None:
            user_id = user_id.strip()
            if user_id and not _is_uuid(user_id):
                raise PolicyError('invalid_raw_records_query')
            user_id = uuid_text(user_id) if user_id else None
        try:
            qa_offset = int(request.query.get('qa_offset', '0'))
            trace_offset = int(request.query.get('trace_offset', '0'))
            limit = int(request.query.get('limit', '20'))
        except ValueError:
            raise PolicyError('invalid_raw_records_query') from None
        if qa_offset < 0 or trace_offset < 0 or not 1 <= limit <= 20:
            raise PolicyError('invalid_raw_records_query')
        if not official_data:
            raise PolicyError('session_history_unavailable', 503)

        body_bytes = await asyncio.to_thread(
            _read_session_history_page,
            official_data,
            ds_id,
            session_id,
            qa_offset=qa_offset,
            trace_offset=trace_offset,
            limit=limit,
            user_id=user_id,
            registry=registry,
            codex_config=codex_config,
        )
        return web.Response(body=body_bytes, content_type='application/json')

    app.add_routes([
        web.get('/v1/raw-records/session-history', raw_session_history),
    ])
