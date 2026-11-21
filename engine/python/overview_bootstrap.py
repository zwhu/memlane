"""Read-only overview bootstrap aggregation over the official local store.

Serves the desktop Overview in a single authenticated loopback call (~30-50ms)
without modifying upstream Cognee source or keeping a second cache on disk.
"""
import asyncio
from datetime import datetime
import json
from pathlib import Path
import sqlite3
from urllib.parse import unquote, urlparse
import uuid

from aiohttp import web

from project_policy import PolicyError
from provenance_store import _calendar_date, _is_lesson_metadata, _local_calendar_date

_MAX_PREVIEW_DOCS = 30
_MAX_PREVIEW_BYTES = 4096


def _uuid_text(value):
    if not value:
        return ''
    text = str(value).strip()
    if not text:
        return ''
    try:
        return str(uuid.UUID(text))
    except ValueError:
        return text


def _uuid_hex(value):
    try:
        return uuid.UUID(str(value)).hex
    except (ValueError, AttributeError):
        return ''


def _iso_text(value):
    if not value:
        return ''
    text = str(value).strip()
    if len(text) >= 19 and text[10] == ' ':
        return text[:10] + 'T' + text[11:]
    return text


def _parse_json(value):
    if isinstance(value, (dict, list)):
        return value
    if not isinstance(value, str) or not value.strip():
        return None
    try:
        return json.loads(value)
    except ValueError:
        return None


def _is_knowledge_doc(meta):
    if not isinstance(meta, dict):
        return False
    if str(meta.get('common_category') or '').strip():
        return True
    ns = meta.get('node_set')
    if isinstance(ns, str):
        ns = [ns]
    if isinstance(ns, list):
        for tag in ns:
            if tag in ('session_learnings', 'user_context', 'memory'):
                return True
    return False


def _load_provenance_maps(provenance_path, allowed_datasets):
    doc_dates = {}
    key_dates = {}
    if not provenance_path or not provenance_path.is_file() or not allowed_datasets:
        return doc_dates, key_dates
    placeholders = ','.join('?' for _ in allowed_datasets)
    params = tuple(sorted(allowed_datasets))
    try:
        con = sqlite3.connect(f'file:{provenance_path}?mode=ro', uri=True, timeout=2.0)
    except sqlite3.Error:
        return doc_dates, key_dates
    try:
        for ds, doc_id, lt, sk, first_date in con.execute(
            f'''SELECT dataset_id, document_id, lesson_type, state_key, first_source_date
                FROM document_provenance WHERE dataset_id IN ({placeholders})''',
            params,
        ):
            doc_dates[(str(ds), str(doc_id))] = (str(lt or '').strip(), str(sk or '').strip(), str(first_date or '').strip())
        for ds, lt, sk, first_date in con.execute(
            f'''SELECT dataset_id, lesson_type, state_key, first_source_date
                FROM keyed_provenance WHERE dataset_id IN ({placeholders})''',
            params,
        ):
            key_dates[(str(ds), str(lt or '').strip(), str(sk or '').strip())] = str(first_date or '').strip()
    except sqlite3.Error:
        return {}, {}
    finally:
        con.close()
    return doc_dates, key_dates


def _read_local_preview(raw_location, official_root):
    if not raw_location or not official_root:
        return ''
    text = str(raw_location).strip()
    if text.startswith('file://'):
        parsed = urlparse(text)
        if parsed.netloc not in ('', 'localhost'):
            return ''
        path = Path(unquote(parsed.path))
    else:
        path = Path(text)
    try:
        if path.is_symlink():
            return ''
        resolved = path.resolve(strict=True)
        resolved.relative_to(official_root)
        if not resolved.is_file():
            return ''
        with resolved.open('rb') as handle:
            chunk = handle.read(_MAX_PREVIEW_BYTES)
        return chunk.decode('utf-8', errors='replace')
    except (OSError, ValueError):
        return ''


def _effective_knowledge_day(meta, created_at):
    for key in ('first_source_date', 'session_date'):
        day = _calendar_date(meta.get(key))
        if day:
            return day
    return _local_calendar_date(created_at) or ''


def _read_overview_snapshot(official_data, provenance_path, allowed_datasets, activity_limit):
    official_root = Path(official_data).resolve()
    db_path = official_root / 'system' / 'databases' / 'cognee_db'
    if not db_path.is_file():
        raise PolicyError('overview_store_unavailable', 503)

    ds_hex_map = {}
    for ds in allowed_datasets:
        h = _uuid_hex(ds)
        if h:
            ds_hex_map[h] = ds
    doc_prov, key_prov = _load_provenance_maps(provenance_path, allowed_datasets)

    try:
        con = sqlite3.connect(f'file:{db_path}?mode=ro', uri=True, timeout=2.0)
    except sqlite3.Error as err:
        raise PolicyError('overview_store_unavailable', 503) from err

    documents = []
    sessions = []
    pipeline_runs = []
    pipeline_runs_more = False
    knowledge_candidates = []
    try:
        if ds_hex_map:
            hex_list = sorted(ds_hex_map.keys())
            placeholders = ','.join('?' for _ in hex_list)
            for row in con.execute(
                f'''SELECT id, dataset_id, name, label, mime_type, raw_data_location,
                           external_metadata, node_set, updated_at, created_at, content_hash
                    FROM data WHERE dataset_id IN ({placeholders})''',
                hex_list,
            ):
                doc_id = _uuid_text(row[0])
                ds_id = ds_hex_map.get(str(row[1]), _uuid_text(row[1]))
                if not doc_id or not ds_id:
                    continue
                meta = _parse_json(row[6])
                if not isinstance(meta, dict):
                    meta = {}
                else:
                    meta = dict(meta)
                ns = _parse_json(row[7])
                if ns and 'node_set' not in meta:
                    meta['node_set'] = ns
                created_at = _iso_text(row[9])
                updated_at = _iso_text(row[8])
                if _is_lesson_metadata(meta):
                    lt = str(meta.get('lesson_type') or '').strip()
                    sk = str(meta.get('state_key') or '').strip()
                    dates = []
                    prov = doc_prov.get((ds_id, doc_id))
                    if prov:
                        if not lt and prov[0]:
                            lt = prov[0]
                        if not sk and prov[1]:
                            sk = prov[1]
                        if _calendar_date(prov[2]):
                            dates.append(prov[2])
                    if lt and sk:
                        kd = key_prov.get((ds_id, lt, sk))
                        if _calendar_date(kd):
                            dates.append(kd)
                    if not dates:
                        if _calendar_date(meta.get('first_source_date')):
                            dates.append(meta['first_source_date'])
                        elif _calendar_date(meta.get('session_date')):
                            dates.append(meta['session_date'])
                        else:
                            cd = _local_calendar_date(created_at)
                            if cd:
                                dates.append(cd)
                    if dates:
                        meta['first_source_date'] = min(dates)
                doc_obj = {
                    'id': doc_id,
                    'datasetId': ds_id,
                    'name': row[2] or '',
                    'label': row[3],
                    'mimeType': row[4] or '',
                    'externalMetadata': meta,
                    'updatedAt': updated_at,
                    'createdAt': created_at,
                    'contentHash': row[10] or '',
                }
                documents.append(doc_obj)
                if _is_knowledge_doc(meta) and row[5]:
                    day = _effective_knowledge_day(meta, created_at)
                    knowledge_candidates.append((day, f'{ds_id}/{doc_id}', row[5]))

            for srow in con.execute(
                f'''SELECT session_id, user_id, dataset_id, agent_id, last_activity_at, started_at
                    FROM session_records WHERE dataset_id IN ({placeholders})''',
                hex_list,
            ):
                sid = str(srow[0] or '')
                if not sid:
                    continue
                sessions.append({
                    'session_id': sid,
                    'user_id': _uuid_text(srow[1]),
                    'dataset_id': ds_hex_map.get(str(srow[2]), _uuid_text(srow[2])),
                    'agent_id': str(srow[3] or ''),
                    'last_activity_at': _iso_text(srow[4]),
                    'started_at': _iso_text(srow[5]),
                })

        pipe_rows = con.execute(
            '''SELECT id, pipeline_run_id, parent_operation_id, dataset_id,
                      pipeline_name, operation_name, status, outcome, background,
                      error_class, origin, created_at, started_at, ended_at
               FROM pipeline_runs ORDER BY created_at DESC, id DESC LIMIT ?''',
            (activity_limit + 1,),
        ).fetchall()
        pipeline_runs_more = len(pipe_rows) > activity_limit
        for pr in pipe_rows[:activity_limit]:
            pipeline_runs.append({
                'id': _uuid_text(pr[0]),
                'pipeline_run_id': _uuid_text(pr[1]),
                'parent_operation_id': _uuid_text(pr[2]),
                'dataset_id': _uuid_text(pr[3]),
                'kind': 'pipeline' if pr[4] else 'operation',
                'pipeline_name': pr[4] or '',
                'operation_name': pr[5] or '',
                'status': pr[6] or '',
                'outcome': pr[7] or '',
                'background': bool(pr[8]),
                'error_class': pr[9] or '',
                'origin': pr[10] or '',
                'created_at': _iso_text(pr[11]),
                'started_at': _iso_text(pr[12]),
                'ended_at': _iso_text(pr[13]),
            })
    except sqlite3.Error as err:
        raise PolicyError('overview_store_unavailable', 503) from err
    finally:
        con.close()

    # Match Go aggregateOverview sort order (date DESC, key ASC) so top-N previews
    # cover the exact rows visible on the initial Overview screen.
    knowledge_candidates.sort(key=lambda item: item[1])
    knowledge_candidates.sort(key=lambda item: item[0], reverse=True)
    previews = {}
    for _, key, raw_loc in knowledge_candidates[:_MAX_PREVIEW_DOCS]:
        text = _read_local_preview(raw_loc, official_root)
        if text:
            previews[key] = text

    return {
        'documents': documents,
        'sessions': sessions,
        'previews': previews,
        'pipeline_runs': pipeline_runs,
        'pipeline_runs_more': pipeline_runs_more,
    }


def _read_recall_summary(recall_store, allowed_datasets, agents_state, recall_limit):
    if recall_store is None or not allowed_datasets:
        return {'recall_events': [], 'recall_events_more': False, 'recall_started_at': '', 'recall_counts': {}}
    events_page = recall_store.events(allowed_datasets, recall_limit, 0)
    for item in events_page['items']:
        agent = agents_state.get(item['agent_id'], {})
        item['agent_name'] = agent.get('name') or 'Agent 未记录'
        item['agent_kind'] = agent.get('kind') or ''
    allowed_sorted = sorted(allowed_datasets)
    placeholders = ','.join('?' for _ in allowed_sorted)
    started_row = recall_store.db.execute('SELECT started_at FROM recall_meta WHERE singleton=1').fetchone()
    started_at = started_row[0] if started_row else ''
    counts = {}
    for ds_id, doc_id, count, last_at in recall_store.db.execute(
        f'''SELECT h.dataset_id, h.document_id, COUNT(*), MAX(e.received_at)
            FROM recall_hit h JOIN recall_event e USING(agent_id,event_id)
            WHERE h.dataset_id IN ({placeholders})
            GROUP BY h.dataset_id, h.document_id''',
        allowed_sorted,
    ):
        counts[f'{ds_id}/{doc_id}'] = {
            'count': int(count),
            'last_recalled_at': last_at or '',
            'started_at': started_at,
        }
    return {
        'recall_events': events_page['items'],
        'recall_events_more': bool(events_page['more']),
        'recall_started_at': started_at,
        'recall_counts': counts,
    }


def attach_overview_bootstrap(app, registry, *, recall_store=None, codex_config=None):
    official_data = (codex_config or {}).get('official_data')
    provenance_path = registry.path.parent / 'lesson-provenance.sqlite3'

    async def overview_bootstrap(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        if set(request.query) - {'activity_limit', 'recall_limit'}:
            raise PolicyError('invalid_overview_query')
        try:
            activity_limit = int(request.query.get('activity_limit', '250'))
            recall_limit = int(request.query.get('recall_limit', '50'))
        except ValueError:
            raise PolicyError('invalid_overview_query') from None
        if not 1 <= activity_limit <= 500 or not 1 <= recall_limit <= 100:
            raise PolicyError('invalid_overview_query')
        if not official_data:
            raise PolicyError('overview_store_unavailable', 503)

        allowed = {g['dataset_id'] for g in registry.managed_groups() if g.get('dataset_id')}
        agents_snapshot = {k: dict(v) for k, v in registry.state.get('agents', {}).items()}

        snapshot = await asyncio.to_thread(
            _read_overview_snapshot,
            official_data,
            provenance_path,
            allowed,
            activity_limit,
        )
        recalls = _read_recall_summary(recall_store, allowed, agents_snapshot, recall_limit)
        snapshot.update(recalls)
        return web.json_response(snapshot)

    app.add_routes([web.get('/v1/overview/bootstrap', overview_bootstrap)])
