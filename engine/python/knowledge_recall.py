"""Local recall receipts: identifiers only, never a second knowledge store."""
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import uuid
from datetime import datetime, timezone

from aiohttp import web
from project_policy import PolicyError


class RecallStore:
    def __init__(self, path):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        if self.path.is_symlink():
            raise ValueError('recall database must not be a symlink')
        fd = os.open(self.path, os.O_CREAT | os.O_RDWR, 0o600)
        os.close(fd)
        os.chmod(self.path, 0o600)
        self.db = sqlite3.connect(self.path)
        self.db.execute('PRAGMA foreign_keys=ON')
        self.db.executescript('''
          CREATE TABLE IF NOT EXISTS recall_meta (
            singleton INTEGER PRIMARY KEY CHECK(singleton=1),
            schema_version INTEGER NOT NULL CHECK(schema_version=1),
            started_at TEXT NOT NULL
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
          CREATE TABLE IF NOT EXISTS recall_query_stats (
            channel TEXT PRIMARY KEY CHECK(channel IN ('automatic','explicit')),
            total_queries INTEGER NOT NULL DEFAULT 0,
            hit_queries INTEGER NOT NULL DEFAULT 0,
            empty_queries INTEGER NOT NULL DEFAULT 0,
            total_hits INTEGER NOT NULL DEFAULT 0,
            last_query_at TEXT
          );
        ''')
        with self.db:
            self.db.execute('INSERT OR IGNORE INTO recall_meta VALUES(1,1,?)', (self.now(),))

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

    def record(self, agent, payload):
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
        fingerprint = hashlib.sha256(json.dumps([channel, sorted(hits)]).encode()).hexdigest()
        with self.db:
            old = self.db.execute('SELECT payload_hash FROM recall_event WHERE agent_id=? AND event_id=?', (agent, event)).fetchone()
            if old:
                if old[0] != fingerprint:
                    raise PolicyError('recall_event_conflict', 409)
                return {'recorded': False}
            self.db.execute('INSERT INTO recall_event VALUES(?,?,?,?,?)', (agent, event, channel, self.now(), fingerprint))
            self.db.executemany('INSERT INTO recall_hit VALUES(?,?,?,?)', [(agent, event, *hit) for hit in sorted(hits)])
        return {'recorded': True}

    def stats(self, dataset, document):
        dataset, document = self.identifier(dataset), self.identifier(document)
        count, last = self.db.execute('''SELECT COUNT(*), MAX(e.received_at) FROM recall_hit h
            JOIN recall_event e USING(agent_id,event_id) WHERE dataset_id=? AND document_id=?''', (dataset, document)).fetchone()
        return {'count': count, 'last_recalled_at': last,
                'started_at': self.db.execute('SELECT started_at FROM recall_meta WHERE singleton=1').fetchone()[0]}

    def close(self):
        self.db.close()

    def counts(self, dataset):
        dataset = self.identifier(dataset)
        rows = self.db.execute('''SELECT h.document_id, COUNT(*), MAX(e.received_at)
            FROM recall_hit h JOIN recall_event e USING(agent_id,event_id)
            WHERE h.dataset_id=? GROUP BY h.document_id''', (dataset,)).fetchall()
        return {'started_at': self.db.execute('SELECT started_at FROM recall_meta WHERE singleton=1').fetchone()[0],
                'items': [{'document_id': document, 'count': count, 'last_recalled_at': last}
                          for document, count, last in rows]}

    def events(self, allowed, limit, offset):
        if not allowed:
            return {'items': [], 'more': False}
        allowed = sorted(allowed)
        placeholders = ','.join('?' for _ in allowed)
        rows = self.db.execute('''SELECT e.agent_id,e.event_id,e.channel,e.received_at
            FROM recall_event e WHERE EXISTS (SELECT 1 FROM recall_hit h
            WHERE h.agent_id=e.agent_id AND h.event_id=e.event_id AND h.dataset_id IN ('''+placeholders+'''))
            ORDER BY e.received_at DESC,e.agent_id,e.event_id LIMIT ? OFFSET ?''', (*allowed, limit+1, offset)).fetchall()
        items = []
        for agent, event, channel, received in rows[:limit]:
            hits = self.db.execute('''SELECT dataset_id,document_id FROM recall_hit
                WHERE agent_id=? AND event_id=? AND dataset_id IN ('''+placeholders+''')
                ORDER BY dataset_id,document_id''', (agent,event,*allowed)).fetchall()
            items.append({'event_id': event, 'agent_id': agent, 'channel': channel, 'received_at': received,
                          'hits': [{'dataset_id': dataset, 'document_id': document} for dataset, document in hits]})
        return {'items': items, 'more': len(rows) > limit}

    def record_query(self, channel: str, hit_count: int) -> dict:
        """Record an aggregate query outcome (including 0-hit denominators) without query text."""
        if channel not in ('automatic', 'explicit') or type(hit_count) is not int or hit_count < 0:
            raise PolicyError('invalid_recall_event')
        now = self.now()
        hit_inc = 1 if hit_count > 0 else 0
        empty_inc = 1 if hit_count == 0 else 0
        with self.db:
            self.db.execute(
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
        return {'recorded': True, 'channel': channel, 'hit_count': hit_count}

    def query_summary(self) -> dict:
        started_row = self.db.execute('SELECT started_at FROM recall_meta WHERE singleton=1').fetchone()
        started_at = started_row[0] if started_row else None
        rows = self.db.execute(
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
            result = store.record(identity, payload)
        return web.json_response(result)

    async def stats(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        if set(request.query) != {'dataset_id', 'document_id'} or len(request.query) != 2:
            raise PolicyError('invalid_recall_fields')
        if request.query['dataset_id'] not in {g['dataset_id'] for g in registry.managed_groups()}:
            raise PolicyError('recall_dataset_unknown', 404)
        return web.json_response(store.stats(request.query['dataset_id'], request.query['document_id']))

    async def counts(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        if set(request.query) != {'dataset_id'} or len(request.query) != 1:
            raise PolicyError('invalid_recall_fields')
        if request.query['dataset_id'] not in {g['dataset_id'] for g in registry.managed_groups()}:
            raise PolicyError('recall_dataset_unknown', 404)
        return web.json_response(store.counts(request.query['dataset_id']))

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
        result = store.events({g['dataset_id'] for g in registry.managed_groups()}, limit, offset)
        for item in result['items']:
            agent = registry.state['agents'].get(item['agent_id'], {})
            item['agent_name'] = agent.get('name') or 'Agent 未记录'
            item['agent_kind'] = agent.get('kind') or ''
        return web.json_response(result)

    async def summary(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        return web.json_response(store.query_summary())

    app.add_routes([web.post('/v1/knowledge/recalls', record), web.get('/v1/knowledge/recalls', stats),
                    web.get('/v1/knowledge/recall-counts', counts), web.get('/v1/knowledge/recall-events', events),
                    web.get('/v1/knowledge/recall-summary', summary)])
    async def close(_):
        store.close()
    app.on_cleanup.append(close)
    return store
