"""Local recall receipts: identifiers only, never a second knowledge store."""
import asyncio
from concurrent.futures import ThreadPoolExecutor
from contextlib import contextmanager
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import threading
import time
import uuid

from aiohttp import web
from project_policy import PolicyError

_MAX_IN_BATCH = 400


class RecallStore:
    """Private 0600 WAL SQLite store for recall receipts and O(1) materialized counts.

    All writes are serialized through a dedicated single-worker thread executor
    that owns the sole write connection, while reads use thread-local read-only
    connections so concurrent asyncio handlers never block the event loop or
    raise cross-thread ``sqlite3.ProgrammingError``.
    """

    def __init__(self, path):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        os.chmod(self.path.parent, 0o700)
        if self.path.is_symlink():
            raise ValueError('recall database must not be a symlink')
        fd = os.open(self.path, os.O_CREAT | os.O_RDWR | getattr(os, 'O_NOFOLLOW', 0), 0o600)
        os.close(fd)
        os.chmod(self.path, 0o600)

        self._closed = False
        self._writer_db = None
        self._local = threading.local()
        self._readers_lock = threading.Lock()
        self._readers_cond = threading.Condition(self._readers_lock)
        self._active_readers = 0
        self._readers: list[sqlite3.Connection] = []
        self._write_executor = ThreadPoolExecutor(
            max_workers=1,
            thread_name_prefix='memlane-recall-writer',
        )
        try:
            self._write_executor.submit(self._init_on_writer).result()
        except Exception:
            self._write_executor.shutdown(wait=True)
            raise

    def _init_on_writer(self):
        con = sqlite3.connect(self.path, isolation_level=None)
        con.execute('PRAGMA journal_mode=WAL')
        con.execute('PRAGMA journal_size_limit=4194304')
        con.execute('PRAGMA wal_autocheckpoint=1000')
        con.execute('PRAGMA synchronous=NORMAL')
        con.execute('PRAGMA cache_size=-512')
        con.execute('PRAGMA busy_timeout=5000')
        con.execute('PRAGMA foreign_keys=ON')
        con.executescript('''
          CREATE TABLE IF NOT EXISTS recall_meta (
            singleton INTEGER PRIMARY KEY CHECK(singleton=1),
            schema_version INTEGER NOT NULL CHECK(schema_version=1),
            started_at TEXT NOT NULL
          );
          CREATE TABLE IF NOT EXISTS recall_rev_state (
            singleton INTEGER PRIMARY KEY CHECK(singleton=1),
            last_rev INTEGER NOT NULL DEFAULT 0
          );
          CREATE TABLE IF NOT EXISTS recall_event (
            agent_id TEXT NOT NULL,
            event_id TEXT NOT NULL,
            channel TEXT NOT NULL CHECK(channel IN ('automatic','explicit')),
            received_at TEXT NOT NULL,
            payload_hash TEXT NOT NULL,
            PRIMARY KEY(agent_id,event_id)
          );
          CREATE TABLE IF NOT EXISTS recall_hit (
            agent_id TEXT NOT NULL,
            event_id TEXT NOT NULL,
            dataset_id TEXT NOT NULL,
            document_id TEXT NOT NULL,
            PRIMARY KEY(agent_id,event_id,dataset_id,document_id),
            FOREIGN KEY(agent_id,event_id) REFERENCES recall_event(agent_id,event_id)
          );
          CREATE INDEX IF NOT EXISTS recall_hit_document ON recall_hit(dataset_id,document_id);
          CREATE INDEX IF NOT EXISTS idx_recall_event_received ON recall_event(received_at DESC, agent_id ASC, event_id ASC);
          CREATE INDEX IF NOT EXISTS idx_recall_hit_dataset_event ON recall_hit(dataset_id, agent_id, event_id);
          CREATE TABLE IF NOT EXISTS recall_doc_counts (
            dataset_id TEXT NOT NULL,
            document_id TEXT NOT NULL,
            total_hits INTEGER NOT NULL DEFAULT 0,
            auto_hits INTEGER NOT NULL DEFAULT 0,
            explicit_hits INTEGER NOT NULL DEFAULT 0,
            last_recalled_at TEXT,
            rev INTEGER NOT NULL DEFAULT 0,
            PRIMARY KEY(dataset_id, document_id)
          ) WITHOUT ROWID;
          CREATE INDEX IF NOT EXISTS idx_recall_doc_counts_rev ON recall_doc_counts(rev);
          CREATE TABLE IF NOT EXISTS recall_query_stats (
            channel TEXT PRIMARY KEY CHECK(channel IN ('automatic','explicit')),
            total_queries INTEGER NOT NULL DEFAULT 0,
            hit_queries INTEGER NOT NULL DEFAULT 0,
            empty_queries INTEGER NOT NULL DEFAULT 0,
            total_hits INTEGER NOT NULL DEFAULT 0,
            last_query_at TEXT
          );
        ''')
        con.execute('BEGIN IMMEDIATE')
        try:
            con.execute('INSERT OR IGNORE INTO recall_meta VALUES(1,1,?)', (self.now(),))
            con.execute('INSERT OR IGNORE INTO recall_rev_state(singleton, last_rev) VALUES(1, 0)')
            hit_total = int(con.execute('SELECT COUNT(*) FROM recall_hit').fetchone()[0] or 0)
            mat_total = int(con.execute('SELECT COALESCE(SUM(total_hits), 0) FROM recall_doc_counts').fetchone()[0] or 0)
            if hit_total != mat_total:
                rev_row = con.execute('SELECT last_rev FROM recall_rev_state WHERE singleton=1').fetchone()
                new_rev = int(rev_row[0] if rev_row else 0) + 1
                con.execute('DELETE FROM recall_doc_counts')
                con.execute(
                    '''INSERT INTO recall_doc_counts
                       (dataset_id, document_id, total_hits, auto_hits, explicit_hits, last_recalled_at, rev)
                       SELECT
                         h.dataset_id,
                         h.document_id,
                         COUNT(*),
                         SUM(CASE WHEN e.channel = 'automatic' THEN 1 ELSE 0 END),
                         SUM(CASE WHEN e.channel = 'explicit' THEN 1 ELSE 0 END),
                         MAX(e.received_at),
                         ?
                       FROM recall_hit h
                       JOIN recall_event e USING(agent_id, event_id)
                       GROUP BY h.dataset_id, h.document_id''',
                    (new_rev,),
                )
                con.execute('UPDATE recall_rev_state SET last_rev=? WHERE singleton=1', (new_rev,))
            con.execute('COMMIT')
        except Exception:
            if con.in_transaction:
                con.execute('ROLLBACK')
            con.close()
            raise
        self._writer_db = con

    def _reader(self) -> sqlite3.Connection:
        with self._readers_lock:
            if self._closed:
                raise PolicyError('recall_store_closed', 503)
        con = getattr(self._local, 'reader', None)
        if con is not None:
            return con
        resolved = self.path.resolve()
        con = sqlite3.connect(
            f'file:{resolved}?mode=ro',
            uri=True,
            timeout=5.0,
            isolation_level=None,
            check_same_thread=False,
        )
        con.execute('PRAGMA query_only=ON')
        con.execute('PRAGMA cache_size=-512')
        con.execute('PRAGMA busy_timeout=5000')
        con.execute('PRAGMA foreign_keys=ON')
        with self._readers_lock:
            if self._closed:
                con.close()
                raise PolicyError('recall_store_closed', 503)
            self._local.reader = con
            self._readers.append(con)
        return con

    @contextmanager
    def _reader_lease(self):
        with self._readers_lock:
            if self._closed:
                raise PolicyError('recall_store_closed', 503)
            self._active_readers += 1
        try:
            yield self._reader()
        finally:
            with self._readers_lock:
                self._active_readers -= 1
                if self._active_readers == 0:
                    self._readers_cond.notify_all()

    @property
    def db(self) -> sqlite3.Connection:
        """Thread-local read-only connection for legacy inspection callers."""
        return self._reader()

    @staticmethod
    def now():
        return datetime.now(timezone.utc).isoformat(timespec='microseconds')

    @staticmethod
    def identifier(value):
        if not isinstance(value, str):
            raise PolicyError('invalid_recall_identifier')
        try:
            return str(uuid.UUID(value))
        except (ValueError, AttributeError):
            raise PolicyError('invalid_recall_identifier') from None

    def _validate_record_payload(self, payload):
        if not isinstance(payload, dict) or set(payload) != {'event_id', 'channel', 'hits'}:
            raise PolicyError('invalid_recall_fields')
        event = self.identifier(payload['event_id'])
        channel, rows = payload['channel'], payload['hits']
        if channel not in ('automatic', 'explicit') or not isinstance(rows, list) or not 1 <= len(rows) <= 100:
            raise PolicyError('invalid_recall_event')
        hits = set()
        for row in rows:
            if not isinstance(row, dict) or set(row) != {'dataset_id', 'document_id'}:
                raise PolicyError('invalid_recall_hit')
            hits.add((self.identifier(row['dataset_id']), self.identifier(row['document_id'])))
        sorted_hits = sorted(hits)
        fingerprint = hashlib.sha256(json.dumps([channel, sorted_hits]).encode()).hexdigest()
        return event, channel, sorted_hits, fingerprint

    def _record_on_writer(self, agent, event, channel, sorted_hits, fingerprint):
        con = self._writer_db
        now = self.now()
        auto_inc = 1 if channel == 'automatic' else 0
        exp_inc = 1 if channel == 'explicit' else 0
        con.execute('BEGIN IMMEDIATE')
        try:
            old = con.execute(
                'SELECT payload_hash FROM recall_event WHERE agent_id=? AND event_id=?',
                (agent, event),
            ).fetchone()
            if old:
                con.execute('ROLLBACK')
                if old[0] != fingerprint:
                    raise PolicyError('recall_event_conflict', 409)
                return {'recorded': False}
            con.execute(
                'INSERT INTO recall_event VALUES(?,?,?,?,?)',
                (agent, event, channel, now, fingerprint),
            )
            con.executemany(
                'INSERT INTO recall_hit VALUES(?,?,?,?)',
                [(agent, event, ds_id, doc_id) for ds_id, doc_id in sorted_hits],
            )
            rev_row = con.execute('SELECT last_rev FROM recall_rev_state WHERE singleton=1').fetchone()
            new_rev = int(rev_row[0] if rev_row else 0) + 1
            con.execute('UPDATE recall_rev_state SET last_rev=? WHERE singleton=1', (new_rev,))
            con.executemany(
                '''INSERT INTO recall_doc_counts
                   (dataset_id, document_id, total_hits, auto_hits, explicit_hits, last_recalled_at, rev)
                   VALUES(?, ?, 1, ?, ?, ?, ?)
                   ON CONFLICT(dataset_id, document_id) DO UPDATE SET
                     total_hits = total_hits + 1,
                     auto_hits = auto_hits + excluded.auto_hits,
                     explicit_hits = explicit_hits + excluded.explicit_hits,
                     last_recalled_at = CASE
                       WHEN last_recalled_at IS NULL OR excluded.last_recalled_at > last_recalled_at
                       THEN excluded.last_recalled_at
                       ELSE last_recalled_at
                     END,
                     rev = excluded.rev''',
                [(ds_id, doc_id, auto_inc, exp_inc, now, new_rev) for ds_id, doc_id in sorted_hits],
            )
            con.execute('COMMIT')
        except Exception:
            if con.in_transaction:
                con.execute('ROLLBACK')
            raise
        return {'recorded': True}

    def record(self, agent, payload):
        event, channel, sorted_hits, fingerprint = self._validate_record_payload(payload)
        return self._write_executor.submit(
            self._record_on_writer, agent, event, channel, sorted_hits, fingerprint
        ).result()

    async def record_async(self, agent, payload):
        event, channel, sorted_hits, fingerprint = self._validate_record_payload(payload)
        loop = asyncio.get_running_loop()
        return await loop.run_in_executor(
            self._write_executor,
            self._record_on_writer,
            agent,
            event,
            channel,
            sorted_hits,
            fingerprint,
        )

    def started_at(self) -> str:
        with self._reader_lease() as con:
            row = con.execute('SELECT started_at FROM recall_meta WHERE singleton=1').fetchone()
        return str(row[0]) if row and row[0] else ''

    def last_rev(self) -> int:
        with self._reader_lease() as con:
            row = con.execute('SELECT last_rev FROM recall_rev_state WHERE singleton=1').fetchone()
        return int(row[0] or 0) if row else 0

    def stats(self, dataset, document):
        dataset, document = self.identifier(dataset), self.identifier(document)
        with self._reader_lease() as con:
            row = con.execute(
                '''SELECT total_hits, last_recalled_at
                   FROM recall_doc_counts
                   WHERE dataset_id=? AND document_id=?''',
                (dataset, document),
            ).fetchone()
            count = int(row[0]) if row else 0
            last = row[1] if row else None
            started = con.execute('SELECT started_at FROM recall_meta WHERE singleton=1').fetchone()[0]
        return {'count': count, 'last_recalled_at': last, 'started_at': started}

    def counts(self, dataset):
        dataset = self.identifier(dataset)
        with self._reader_lease() as con:
            rows = con.execute(
                '''SELECT document_id, total_hits, last_recalled_at
                   FROM recall_doc_counts
                   WHERE dataset_id=?''',
                (dataset,),
            ).fetchall()
            started = con.execute('SELECT started_at FROM recall_meta WHERE singleton=1').fetchone()[0]
        return {
            'started_at': started,
            'items': [
                {'document_id': document, 'count': int(count), 'last_recalled_at': last}
                for document, count, last in rows
            ],
        }

    def dataset_counts_map(self, allowed_datasets) -> tuple[str, dict[str, dict]]:
        """Return ``(started_at, {'<ds>/<doc>': {'count', 'last_recalled_at', 'started_at'}})``."""
        allowed_sorted = sorted({str(ds) for ds in (allowed_datasets or ()) if ds})
        counts: dict[str, dict] = {}
        with self._reader_lease() as con:
            started_row = con.execute('SELECT started_at FROM recall_meta WHERE singleton=1').fetchone()
            started_at = str(started_row[0]) if started_row and started_row[0] else ''
            for start in range(0, len(allowed_sorted), _MAX_IN_BATCH):
                batch = allowed_sorted[start : start + _MAX_IN_BATCH]
                placeholders = ','.join('?' for _ in batch)
                for ds_id, doc_id, total_hits, last_at in con.execute(
                    f'''SELECT dataset_id, document_id, total_hits, last_recalled_at
                        FROM recall_doc_counts
                        WHERE dataset_id IN ({placeholders})''',
                    batch,
                ):
                    counts[f'{ds_id}/{doc_id}'] = {
                        'count': int(total_hits),
                        'last_recalled_at': last_at or '',
                        'started_at': started_at,
                    }
        return started_at, counts

    def doc_counts_since(self, after_rev: int) -> tuple[list[tuple[str, str, int]], int]:
        """Return ``([(dataset_id, document_id, total_hits)], new_rev)`` from one consistent snapshot."""
        with self._reader_lease() as con:
            con.execute('BEGIN DEFERRED')
            try:
                rows = con.execute(
                    '''SELECT dataset_id, document_id, total_hits, rev
                       FROM recall_doc_counts
                       WHERE rev > ?
                       ORDER BY rev ASC''',
                    (int(after_rev),),
                ).fetchall()
                rev_row = con.execute('SELECT last_rev FROM recall_rev_state WHERE singleton=1').fetchone()
            finally:
                if con.in_transaction:
                    con.execute('ROLLBACK')
        changes = []
        max_rev = int(after_rev)
        for ds_id, doc_id, total_hits, row_rev in rows:
            r_int = int(row_rev or 0)
            if r_int > max_rev:
                max_rev = r_int
            changes.append((str(ds_id), str(doc_id), int(total_hits or 0)))
        if rev_row and int(rev_row[0] or 0) > max_rev:
            max_rev = int(rev_row[0])
        return changes, max_rev

    def event_doc_ids(self, dataset_id: str, agent_id: str, event_id: str) -> set[str]:
        event_uuid = self.identifier(event_id)
        ds_uuid = self.identifier(dataset_id)
        with self._reader_lease() as con:
            rows = con.execute(
                '''SELECT document_id FROM recall_hit
                   WHERE agent_id = ? AND event_id = ? AND dataset_id = ?''',
                (agent_id, event_uuid, ds_uuid),
            ).fetchall()
        return {str(r[0]) for r in rows}

    def counts_for_datasets(self, allowed):
        """Return aggregate per-document recall counts across allowed datasets."""
        with self._reader_lease() as con:
            started_row = con.execute('SELECT started_at FROM recall_meta WHERE singleton=1').fetchone()
            started_at = started_row[0] if started_row else ''
            if not allowed:
                return {'started_at': started_at, 'counts': {}}
            allowed_sorted = sorted({self.identifier(ds) for ds in allowed})
            placeholders = ','.join('?' for _ in allowed_sorted)
            rows = con.execute(
                f'''SELECT dataset_id, document_id, total_hits, last_recalled_at
                    FROM recall_doc_counts WHERE dataset_id IN ({placeholders})''',
                allowed_sorted,
            ).fetchall()
        counts = {
            f'{ds_id}/{doc_id}': {
                'count': int(count),
                'last_recalled_at': last_at or '',
                'started_at': started_at,
            }
            for ds_id, doc_id, count, last_at in rows
        }
        return {'started_at': started_at, 'counts': counts}

    def events(self, allowed, limit, offset):
        if not allowed:
            return {'items': [], 'more': False}
        allowed = sorted(allowed)
        placeholders = ','.join('?' for _ in allowed)
        with self._reader_lease() as con:
            con.execute('BEGIN DEFERRED')
            try:
                rows = con.execute(
                    '''SELECT e.agent_id,e.event_id,e.channel,e.received_at
                       FROM recall_event e INDEXED BY idx_recall_event_received
                       WHERE EXISTS (SELECT 1 FROM recall_hit h
                       WHERE h.agent_id=e.agent_id AND h.event_id=e.event_id AND h.dataset_id IN ('''
                    + placeholders
                    + '''))
                       ORDER BY e.received_at DESC, e.agent_id ASC, e.event_id ASC LIMIT ? OFFSET ?''',
                    (*allowed, limit + 1, offset),
                ).fetchall()
                page_rows = rows[:limit]
                hits_by_key: dict[tuple[str, str], list[dict]] = {}
                if page_rows:
                    events_by_agent: dict[str, list[str]] = {}
                    for r in page_rows:
                        events_by_agent.setdefault(str(r[0]), []).append(str(r[1]))
                    for ag_id, ag_evs in events_by_agent.items():
                        uniq_evs = sorted(set(ag_evs))
                        for start in range(0, len(uniq_evs), _MAX_IN_BATCH):
                            ev_batch = uniq_evs[start : start + _MAX_IN_BATCH]
                            ev_ph = ','.join('?' for _ in ev_batch)
                            for ag, ev, ds_id, doc_id in con.execute(
                                f'''SELECT agent_id, event_id, dataset_id, document_id
                                    FROM recall_hit
                                    WHERE agent_id = ? AND event_id IN ({ev_ph}) AND dataset_id IN ({placeholders})''',
                                (ag_id, *ev_batch, *allowed),
                            ):
                                hits_by_key.setdefault((str(ag), str(ev)), []).append({
                                    'dataset_id': str(ds_id),
                                    'document_id': str(doc_id),
                                })
                    for hit_list in hits_by_key.values():
                        if len(hit_list) > 1:
                            hit_list.sort(key=lambda h: (h['dataset_id'], h['document_id']))
            finally:
                if con.in_transaction:
                    con.execute('ROLLBACK')
        items = []
        for agent, event, channel, received in page_rows:
            items.append({
                'event_id': event,
                'agent_id': agent,
                'channel': channel,
                'received_at': received,
                'hits': hits_by_key.get((str(agent), str(event)), []),
            })
        return {'items': items, 'more': len(rows) > limit}

    def _record_query_on_writer(self, channel: str, hit_count: int) -> dict:
        con = self._writer_db
        now = self.now()
        hit_inc = 1 if hit_count > 0 else 0
        empty_inc = 1 if hit_count == 0 else 0
        con.execute('BEGIN IMMEDIATE')
        try:
            con.execute(
                '''INSERT INTO recall_query_stats(channel, total_queries, hit_queries, empty_queries, total_hits, last_query_at)
                   VALUES(?, 1, ?, ?, ?, ?)
                   ON CONFLICT(channel) DO UPDATE SET
                     total_queries = total_queries + 1,
                     hit_queries = hit_queries + excluded.hit_queries,
                     empty_queries = empty_queries + excluded.empty_queries,
                     total_hits = total_hits + excluded.total_hits,
                     last_query_at = excluded.last_query_at''',
                (channel, hit_inc, empty_inc, hit_count, now),
            )
            con.execute('COMMIT')
        except Exception:
            if con.in_transaction:
                con.execute('ROLLBACK')
            raise
        return {'recorded': True, 'channel': channel, 'hit_count': hit_count}

    def record_query(self, channel: str, hit_count: int) -> dict:
        """Record an aggregate query outcome (including 0-hit denominators) without query text."""
        if channel not in ('automatic', 'explicit') or type(hit_count) is not int or hit_count < 0:
            raise PolicyError('invalid_recall_event')
        return self._write_executor.submit(self._record_query_on_writer, channel, hit_count).result()

    async def record_query_async(self, channel: str, hit_count: int) -> dict:
        if channel not in ('automatic', 'explicit') or type(hit_count) is not int or hit_count < 0:
            raise PolicyError('invalid_recall_event')
        loop = asyncio.get_running_loop()
        return await loop.run_in_executor(self._write_executor, self._record_query_on_writer, channel, hit_count)

    def query_summary(self) -> dict:
        with self._reader_lease() as con:
            started_row = con.execute('SELECT started_at FROM recall_meta WHERE singleton=1').fetchone()
            started_at = started_row[0] if started_row else None
            rows = con.execute(
                'SELECT channel, total_queries, hit_queries, empty_queries, total_hits, last_query_at FROM recall_query_stats'
            ).fetchall()
        by_channel = {
            ch: {'total_queries': 0, 'hit_queries': 0, 'empty_queries': 0, 'total_hits': 0, 'hit_rate': None, 'last_query_at': None}
            for ch in ('automatic', 'explicit')
        }
        total_queries = hit_queries = empty_queries = total_hits = 0
        for channel, total_q, hit_q, empty_q, hits_sum, last_at in rows:
            if channel not in by_channel:
                continue
            by_channel[channel] = {
                'total_queries': int(total_q),
                'hit_queries': int(hit_q),
                'empty_queries': int(empty_q),
                'total_hits': int(hits_sum),
                'hit_rate': round(hit_q / total_q, 4) if total_q > 0 else None,
                'last_query_at': last_at,
            }
            total_queries += int(total_q)
            hit_queries += int(hit_q)
            empty_queries += int(empty_q)
            total_hits += int(hits_sum)
        return {
            'started_at': started_at,
            'total_queries': total_queries,
            'hit_queries': hit_queries,
            'empty_queries': empty_queries,
            'total_hits': total_hits,
            'hit_rate': round(hit_queries / total_queries, 4) if total_queries > 0 else None,
            'by_channel': by_channel,
        }

    def _checkpoint_on_writer(self, mode: str) -> bool:
        try:
            mode_str = str(mode or 'TRUNCATE').upper()
            if mode_str not in ('PASSIVE', 'FULL', 'RESTART', 'TRUNCATE'):
                mode_str = 'TRUNCATE'
            cur = self._writer_db.execute(f'PRAGMA wal_checkpoint({mode_str})')
            row = cur.fetchone()
            return bool(row and row[0] == 0)
        except sqlite3.Error:
            return False

    def checkpoint(self, mode: str = 'TRUNCATE') -> bool:
        if self._closed:
            return False
        return self._write_executor.submit(self._checkpoint_on_writer, mode).result()

    def _close_on_writer(self):
        if self._writer_db is not None:
            try:
                self._writer_db.execute('PRAGMA wal_checkpoint(TRUNCATE)')
            except Exception:
                pass
            try:
                self._writer_db.close()
            finally:
                self._writer_db = None

    def close(self):
        with self._readers_lock:
            if self._closed:
                return
            self._closed = True
            deadline = time.monotonic() + 5.0
            while self._active_readers > 0:
                rem = deadline - time.monotonic()
                if rem <= 0:
                    break
                self._readers_cond.wait(timeout=rem)
            readers = list(self._readers)
            self._readers.clear()
        for con in readers:
            try:
                con.close()
            except Exception:
                pass
        try:
            self._write_executor.submit(self._close_on_writer).result()
        finally:
            self._write_executor.shutdown(wait=True)


def attach_recall(app, registry):
    store = RecallStore(registry.path.parent / 'knowledge-recall.sqlite3')

    async def record(request):
        identity = request['agent']
        if identity is None:
            raise PolicyError('agent_identity_required', 403)
        if request.content_type != 'application/json':
            raise PolicyError('json_required', 415)
        payload = await request.json()
        if not isinstance(payload, dict) or not isinstance(payload.get('hits'), list):
            raise PolicyError('invalid_recall_event')
        # Only already verified readable groups of this authenticated Agent.
        # Metrics must not trigger ACL reconciliation or mutate policy state.
        # The hook reports official document IDs; no query or text is accepted.
        allowed = set()
        async with registry.mutex:
            for group in registry.managed_groups():
                if group.get('kind') == 'common':
                    view = registry.common_policy(identity)
                    if view.get('ready') and view.get('readable'):
                        allowed.add(group['dataset_id'])
                else:
                    policy = registry.policy(group, identity)
                    if policy.get('ready') and 'read' in policy.get('policy', {}).get('permissions', []):
                        allowed.add(group['dataset_id'])
            for hit in payload['hits']:
                if not isinstance(hit, dict) or hit.get('dataset_id') not in allowed:
                    raise PolicyError('recall_dataset_denied', 403)
        result = await store.record_async(identity, payload)
        return web.json_response(result)

    async def stats(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        if set(request.query) != {'dataset_id', 'document_id'} or len(request.query) != 2:
            raise PolicyError('invalid_recall_fields')
        if request.query['dataset_id'] not in {g['dataset_id'] for g in registry.managed_groups()}:
            raise PolicyError('recall_dataset_unknown', 404)
        result = await asyncio.to_thread(store.stats, request.query['dataset_id'], request.query['document_id'])
        return web.json_response(result)

    async def counts(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        if set(request.query) != {'dataset_id'} or len(request.query) != 1:
            raise PolicyError('invalid_recall_fields')
        if request.query['dataset_id'] not in {g['dataset_id'] for g in registry.managed_groups()}:
            raise PolicyError('recall_dataset_unknown', 404)
        result = await asyncio.to_thread(store.counts, request.query['dataset_id'])
        return web.json_response(result)

    async def events(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        if set(request.query) - {'limit', 'offset'}:
            raise PolicyError('invalid_recall_fields')
        try:
            limit, offset = int(request.query.get('limit', '50')), int(request.query.get('offset', '0'))
        except ValueError:
            raise PolicyError('invalid_recall_page') from None
        if not 1 <= limit <= 100 or not 0 <= offset <= 1000000:
            raise PolicyError('invalid_recall_page')
        allowed_ds = {g['dataset_id'] for g in registry.managed_groups()}
        agents_snapshot = {k: dict(v) for k, v in registry.state.get('agents', {}).items()}
        result = await asyncio.to_thread(store.events, allowed_ds, limit, offset)
        for item in result['items']:
            agent = agents_snapshot.get(item['agent_id'], {})
            item['agent_name'] = agent.get('name') or 'Agent 未记录'
            item['agent_kind'] = agent.get('kind') or ''
        return web.json_response(result)

    async def summary(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        result = await asyncio.to_thread(store.query_summary)
        return web.json_response(result)

    app.add_routes([
        web.post('/v1/knowledge/recalls', record),
        web.get('/v1/knowledge/recalls', stats),
        web.get('/v1/knowledge/recall-counts', counts),
        web.get('/v1/knowledge/recall-events', events),
        web.get('/v1/knowledge/recall-summary', summary),
    ])

    async def close(_):
        await asyncio.to_thread(store.close)

    app.on_cleanup.append(close)
    return store
