"""Read-only raw-records catalog & paged session-history endpoints over official SQLite stores.

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

from overview_bootstrap import (
    _is_knowledge_doc,
    _iso_text,
    _parse_json,
    _uuid_hex,
    _uuid_text,
)
from project_policy import PolicyError
from session_relations import descendant_ids_for_session, resolve_session_relations

MAX_RAW_HISTORY_BYTES = 8 * 1024 * 1024
_VALID_KINDS = frozenset({'all', 'session', 'document'})
_DERIVED_NODE_SETS = frozenset({
    'user_sessions_from_cache',
    'agent_trace_feedbacks',
    'session_learnings',
})
_SQL_IN_BATCH = 400


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


def _id_variants(value):
    text = str(value or '').strip()
    if not text:
        return ()
    variants = []
    for candidate in (text, _uuid_text(text), _uuid_hex(text)):
        if candidate and candidate not in variants:
            variants.append(candidate)
    return tuple(variants)


def _is_derived_material(meta):
    if not isinstance(meta, dict):
        return False
    ns = meta.get('node_set')
    if isinstance(ns, str):
        tags = [ns]
    elif isinstance(ns, list):
        tags = [t for t in ns if isinstance(t, str)]
    else:
        return False
    return any(tag in _DERIVED_NODE_SETS for tag in tags)


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
    data = payload if isinstance(payload, dict) else _parse_json(payload)
    if not isinstance(data, dict):
        return None
    q = data.get('question')
    if q:
        return str(q)[:120]
    return None


def _in_clause(variants):
    if len(variants) == 1:
        return '= ?', variants
    return f"IN ({','.join('?' for _ in variants)})", variants


def read_cache_session_history(cache_db, *, owner, session_id, qa_offset=0, trace_offset=0, limit=20):
    """Read bounded QA and trace pages directly from Cognee's read-only SQLite cache.

    Returns a dict with ``conflict``, ``msg_count``, ``tool_calls``, ``label``,
    ``qas``, and ``traces``. Raises ``sqlite3.Error`` on database/schema failures
    so callers can surface 503 or fall back when appropriate.
    """
    owner_variants = _id_variants(owner)
    if not owner_variants:
        raise sqlite3.OperationalError('missing session owner')
    user_sql, user_params = _in_clause(owner_variants)
    bounds = _cache_expiry_bounds()
    now_space = bounds[0]

    con = sqlite3.connect(f'file:{cache_db}?mode=ro', uri=True, timeout=2.0)
    try:
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
            parsed = _parse_json(raw_payload)
            if not isinstance(parsed, dict):
                raise ValueError('corrupt qa payload')
            qas.append(parsed)

        traces = []
        for (raw_payload,) in con.execute(
            f'SELECT payload FROM cache_trace_entries WHERE {tr_where} ORDER BY seq ASC LIMIT ? OFFSET ?',
            (*tr_base_params, limit, trace_offset),
        ):
            parsed = _parse_json(raw_payload)
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


def _hydrate_session_summaries(cache_db, sessions):
    if not cache_db.is_file():
        return False
    bounds = _cache_expiry_bounds()
    now_space = bounds[0]
    try:
        con = sqlite3.connect(f'file:{cache_db}?mode=ro', uri=True, timeout=2.0)
    except sqlite3.Error:
        return False

    try:
        qa_expired = _cache_table_has_expired(con, 'cache_qa_entries', bounds)
        tr_expired = _cache_table_has_expired(con, 'cache_trace_entries', bounds)
        qa_exp_sql, qa_exp_params = _expiry_sql(qa_expired, now_space)
        tr_exp_sql, tr_exp_params = _expiry_sql(tr_expired, now_space)

        by_user = {}
        for row in sessions:
            by_user.setdefault(row['user_id'], []).append(row['session_id'])

        qa_counts = {}
        trace_counts = {}
        min_seqs = []
        min_seq_owner = {}

        for user_id, sids in by_user.items():
            user_variants = _id_variants(user_id)
            user_sql, user_params = _in_clause(user_variants)
            unique_sids = list(dict.fromkeys(sids))
            for i in range(0, len(unique_sids), _SQL_IN_BATCH):
                batch = unique_sids[i:i + _SQL_IN_BATCH]
                sid_placeholders = ','.join('?' for _ in batch)
                for sid, cnt, min_seq in con.execute(
                    f'''SELECT session_id, COUNT(*), MIN(seq)
                        FROM cache_qa_entries
                        WHERE user_id {user_sql} AND session_id IN ({sid_placeholders}){qa_exp_sql}
                        GROUP BY session_id''',
                    (*user_params, *batch, *qa_exp_params),
                ):
                    key = (user_id, str(sid))
                    qa_counts[key] = int(cnt)
                    if min_seq is not None:
                        seq_int = int(min_seq)
                        min_seqs.append(seq_int)
                        min_seq_owner[seq_int] = key

                for sid, cnt in con.execute(
                    f'''SELECT session_id, COUNT(*)
                        FROM cache_trace_entries
                        WHERE user_id {user_sql} AND session_id IN ({sid_placeholders}){tr_exp_sql}
                        GROUP BY session_id''',
                    (*user_params, *batch, *tr_exp_params),
                ):
                    trace_counts[(user_id, str(sid))] = int(cnt)

        labels = {}
        fallback_needed = []
        for i in range(0, len(min_seqs), _SQL_IN_BATCH):
            batch = min_seqs[i:i + _SQL_IN_BATCH]
            seq_placeholders = ','.join('?' for _ in batch)
            for seq_val, raw_payload in con.execute(
                f'SELECT seq, payload FROM cache_qa_entries WHERE seq IN ({seq_placeholders})',
                batch,
            ):
                key = min_seq_owner.get(int(seq_val))
                if key is None:
                    continue
                label = _extract_question_label(raw_payload)
                if label is not None:
                    labels[key] = label
                elif qa_counts.get(key, 0) > 1:
                    fallback_needed.append((key[0], key[1], int(seq_val)))

        for user_id, sid, first_seq in fallback_needed:
            user_variants = _id_variants(user_id)
            user_sql, user_params = _in_clause(user_variants)
            for (raw_payload,) in con.execute(
                f'''SELECT payload FROM cache_qa_entries
                    WHERE user_id {user_sql} AND session_id = ? AND seq > ?{qa_exp_sql}
                    ORDER BY seq ASC''',
                (*user_params, sid, first_seq, *qa_exp_params),
            ):
                label = _extract_question_label(raw_payload)
                if label is not None:
                    labels[(user_id, sid)] = label
                    break

        for row in sessions:
            key = (row['user_id'], row['session_id'])
            row['label'] = labels.get(key, '')
            row['msg_count'] = qa_counts.get(key, 0)
            row['tool_calls'] = trace_counts.get(key, 0)
        return True
    except sqlite3.Error:
        for row in sessions:
            row.pop('label', None)
            row.pop('msg_count', None)
            row.pop('tool_calls', None)
        return False
    finally:
        con.close()


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


def _read_raw_records_catalog(official_data, dataset_id, kind='all', *, registry=None, codex_config=None):
    official_root = Path(official_data).resolve()
    cognee_db = official_root / 'system' / 'databases' / 'cognee_db'
    cache_db = official_root / 'system' / 'databases' / 'cache.db'
    if not cognee_db.is_file():
        raise PolicyError('raw_records_store_unavailable', 503)

    ds_norm = _uuid_text(dataset_id)
    ds_variants = _id_variants(ds_norm)
    ds_sql, ds_params = _in_clause(ds_variants)

    try:
        con = sqlite3.connect(f'file:{cognee_db}?mode=ro', uri=True, timeout=2.0)
    except sqlite3.Error as err:
        raise PolicyError('raw_records_store_unavailable', 503) from err

    documents = []
    sessions = []
    try:
        if kind != 'session':
            for row in con.execute(
                f'''SELECT id, dataset_id, name, label, mime_type, external_metadata,
                           node_set, updated_at, created_at, content_hash
                    FROM data WHERE dataset_id {ds_sql}''',
                ds_params,
            ):
                doc_id = _uuid_text(row[0])
                if not doc_id:
                    continue
                meta = _parse_json(row[5])
                if not isinstance(meta, dict):
                    meta = {}
                else:
                    meta = dict(meta)
                ns = _parse_json(row[6])
                if ns and 'node_set' not in meta:
                    meta['node_set'] = ns
                if _is_knowledge_doc(meta) or _is_derived_material(meta):
                    continue
                documents.append({
                    'id': doc_id,
                    'datasetId': ds_norm,
                    'name': row[2] or '',
                    'label': row[3],
                    'mimeType': row[4] or '',
                    'externalMetadata': meta,
                    'updatedAt': _iso_text(row[7]),
                    'createdAt': _iso_text(row[8]),
                    'contentHash': row[9] or '',
                })

        if kind != 'document':
            for srow in con.execute(
                f'''SELECT session_id, user_id, dataset_id, agent_id, last_activity_at, started_at
                    FROM session_records
                    WHERE dataset_id {ds_sql}
                    ORDER BY last_activity_at DESC''',
                ds_params,
            ):
                sid = str(srow[0] or '')
                if not sid:
                    raise PolicyError('session_identity_missing', 503)
                sessions.append({
                    'session_id': sid,
                    'user_id': _uuid_text(srow[1]),
                    'dataset_id': ds_norm,
                    'agent_id': str(srow[3] or ''),
                    'last_activity_at': _iso_text(srow[4]),
                    'started_at': _iso_text(srow[5]),
                })
    except PolicyError:
        raise
    except sqlite3.Error as err:
        raise PolicyError('raw_records_store_unavailable', 503) from err
    finally:
        con.close()

    cache_ready = False
    if kind != 'document':
        cache_ready = _hydrate_session_summaries(cache_db, sessions) if sessions else cache_db.is_file()
        if sessions and (registry is not None or codex_config):
            relations = resolve_session_relations(
                [row['session_id'] for row in sessions],
                registry=registry,
                codex_config=codex_config,
            )
            for row in sessions:
                _apply_relation_fields(row, relations.get(row['session_id']))

    return {
        'dataset_id': ds_norm,
        'cache_ready': cache_ready,
        'documents': documents,
        'sessions': sessions,
    }


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
    if not cognee_db.is_file():
        raise PolicyError('session_history_unavailable', 503)

    ds_norm = _uuid_text(dataset_id)
    ds_variants = _id_variants(ds_norm)
    ds_sql, ds_params = _in_clause(ds_variants)

    try:
        con = sqlite3.connect(f'file:{cognee_db}?mode=ro', uri=True, timeout=2.0)
    except sqlite3.Error as err:
        raise PolicyError('session_history_unavailable', 503) from err

    ds_sessions = {}
    try:
        if user_id:
            u_variants = _id_variants(user_id)
            u_sql, u_params = _in_clause(u_variants)
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
                        'user_id': _uuid_text(dsrow[1]),
                        'dataset_id': ds_norm,
                        'agent_id': str(dsrow[3] or ''),
                        'last_activity_at': _iso_text(dsrow[4]),
                        'started_at': _iso_text(dsrow[5]),
                    }
    except sqlite3.Error as err:
        raise PolicyError('session_history_unavailable', 503) from err
    finally:
        con.close()

    if srow is None:
        raise PolicyError('session_not_found', 404)

    owner = _uuid_text(srow[1])
    if not owner or not cache_db.is_file():
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
    qas = page['qas']
    traces = page['traces']
    record = {
        'session_id': str(srow[0]),
        'user_id': owner,
        'dataset_id': ds_norm,
        'agent_id': str(srow[3] or ''),
        'last_activity_at': _iso_text(srow[4]),
        'started_at': _iso_text(srow[5]),
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
            if _hydrate_session_summaries(cache_db, [parent_summary_row]) and parent_summary_row.get('label'):
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
                _hydrate_session_summaries(cache_db, subagent_rows)
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
        ds_id = _uuid_text(raw_dataset)
        allowed = {g['dataset_id'] for g in registry.managed_groups() if g.get('dataset_id')}
        if ds_id not in allowed:
            raise PolicyError('dataset_access_denied', 403)
        return ds_id

    async def raw_records(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        if set(request.query) - {'dataset_id', 'kind'}:
            raise PolicyError('invalid_raw_records_query')
        ds_id = require_managed_dataset(request.query.get('dataset_id', ''))
        kind = request.query.get('kind', 'all')
        if kind not in _VALID_KINDS:
            raise PolicyError('invalid_raw_records_query')
        if not official_data:
            raise PolicyError('raw_records_store_unavailable', 503)

        payload = await asyncio.to_thread(
            _read_raw_records_catalog,
            official_data,
            ds_id,
            kind,
            registry=registry,
            codex_config=codex_config,
        )
        return web.json_response(payload)

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
            user_id = _uuid_text(user_id) if user_id else None
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
        web.get('/v1/raw-records', raw_records),
        web.get('/v1/raw-records/session-history', raw_session_history),
    ])
