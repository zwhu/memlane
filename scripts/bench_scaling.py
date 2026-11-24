#!/usr/bin/env python3
"""Isolated adversarial scaling benchmark (1k / 10k / 50k) & dual-DB WAL concurrency verifier.

Generates a completely isolated synthetic official store inside
``tempfile.TemporaryDirectory()`` (NEVER reads or writes ``.local/daily``) using ONLY
real upstream Cognee 1.6.7 SQLite schemas & indexes (zero synthetic helper indexes!)
with all 7 adversarial distributions from ``proposal.md §6.1`` and measures every SLA
threshold from ``proposal.md §6.2``:
  1. 10,000 same-day clustered knowledge docs (1k: 200, 10k: 2,000, 50k: 10,000)
  2. 90% zero-recall clustered knowledge docs (50k: 45,000) + 100k recall events
  3. Sparse unread (N-5 read + 5 oldest unread + 3 empty/invalid created_at + ns boundary)
  4. cache.db expiry pollution (80k QA + 400k traces at 50k; 100% have expires_at, oldest 100 expired)
  5. 10,000 sessions (85% subagents + 1 parent with 200 children) + 10,000 raw docs
  6. 6 subtle/adversarial mutations (in-place update, same-length rename with unchanged updated_at,
     NULL/backdated metadata edit, silent DELETE, 1:1 state_key replace, max_rowid delete+reuse)
  7. Cold code project with 3 pipeline_runs buried inside 300,000 pipeline_runs
     (150k on ds_main + 150k on ds_noise)
"""
import argparse
import asyncio
from datetime import datetime, timedelta, timezone
import gc
import hashlib
import json
import os
from pathlib import Path
import resource
import shutil
import sqlite3
import subprocess
import sys
import tempfile
import threading
import time
import types
import uuid

ROOT = Path(__file__).resolve().parents[1]
ENGINE_PY = ROOT / 'engine' / 'python'
if str(ENGINE_PY) not in sys.path:
    sys.path.insert(0, str(ENGINE_PY))

from aiohttp.test_utils import TestClient, TestServer
from catalog_index import CatalogIndex
from knowledge_recall import RecallStore
from project_control_server import create_app
from provenance_store import ProvenanceStore
from reading_state_store import ReadingStateStore
from session_relations import TrackedRelationsDict
from sqlite_ro import uuid_hex

SCALES = {
    '1k': {
        'knowledge_docs': 1000,
        'raw_docs': 100,
        'same_day_cluster': 200,
        'zero_recall_docs': 900,
        'sessions': 200,
        'parent_children': 40,
        'qa_per_session': 2,
        'trace_per_session': 4,
        'recall_events': 500,
        'main_pipeline_runs': 3000,
        'other_pipeline_runs': 3000,
        'deep_offset': 900,
        'raw_deep_pages': 4,
    },
    '10k': {
        'knowledge_docs': 10000,
        'raw_docs': 2000,
        'same_day_cluster': 2000,
        'zero_recall_docs': 9000,
        'sessions': 2000,
        'parent_children': 100,
        'qa_per_session': 4,
        'trace_per_session': 10,
        'recall_events': 10000,
        'main_pipeline_runs': 30000,
        'other_pipeline_runs': 30000,
        'deep_offset': 9000,
        'raw_deep_pages': 20,
    },
    '50k': {
        'knowledge_docs': 50000,
        'raw_docs': 10000,
        'same_day_cluster': 10000,
        'zero_recall_docs': 45000,
        'sessions': 10000,
        'parent_children': 200,
        'qa_per_session': 8,
        'trace_per_session': 40,
        'recall_events': 100000,
        'main_pipeline_runs': 150000,
        'other_pipeline_runs': 150000,
        'deep_offset': 45000,
        'raw_deep_pages': 50,
    },
}


def _peak_rss_mib() -> float:
    val = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    if sys.platform == 'darwin':
        return val / (1024 * 1024)
    return val / 1024.0


def _rss_mib() -> float:
    """Measure live process RSS in MiB (falls back to peak RSS if ps/proc unavailable)."""
    try:
        if sys.platform.startswith('linux'):
            for line in Path('/proc/self/status').read_text(encoding='utf-8').splitlines():
                if line.startswith('VmRSS:'):
                    return int(line.split()[1]) / 1024.0
        out = subprocess.check_output(['ps', '-o', 'rss=', '-p', str(os.getpid())], text=True, timeout=2.0).strip()
        if out:
            return int(out) / 1024.0
    except Exception:
        pass
    return _peak_rss_mib()


def _percentile(samples_ms: list[float], pct: float) -> float:
    if not samples_ms:
        return 0.0
    ordered = sorted(samples_ms)
    idx = min(len(ordered) - 1, max(0, int(round((pct / 100.0) * (len(ordered) - 1)))))
    return round(ordered[idx], 3)


def _create_official_schemas(cognee_db: Path, cache_db: Path):
    """Create official Cognee 1.6.7 tables with ONLY genuine upstream indexes."""
    con = sqlite3.connect(cognee_db)
    con.execute('PRAGMA journal_mode=WAL')
    con.execute('PRAGMA journal_size_limit=4194304')
    con.execute('PRAGMA synchronous=NORMAL')
    con.executescript('''
        CREATE TABLE datasets (
            id CHAR(32) PRIMARY KEY,
            name TEXT,
            owner_id CHAR(32),
            created_at DATETIME
        );
        CREATE TABLE data (
            id CHAR(32) PRIMARY KEY,
            legacy_id CHAR(32),
            dataset_id CHAR(32),
            name TEXT,
            label TEXT,
            raw_data_location TEXT,
            mime_type TEXT,
            external_metadata TEXT,
            system_metadata TEXT,
            node_set TEXT,
            pipeline_status TEXT,
            updated_at DATETIME,
            created_at DATETIME,
            content_hash TEXT
        );
        CREATE INDEX ix_data_legacy_id ON data(legacy_id);
        CREATE INDEX ix_data_dataset_created ON data(dataset_id, created_at);
        CREATE TABLE session_records (
            session_id TEXT NOT NULL,
            user_id CHAR(32) NOT NULL,
            dataset_id CHAR(32) NOT NULL,
            agent_id TEXT,
            last_activity_at DATETIME,
            started_at DATETIME,
            status TEXT,
            PRIMARY KEY(session_id, user_id)
        );
        -- Exact real upstream Cognee 1.6.7 session_records indexes (NO composite (dataset_id, last_activity_at)!)
        CREATE INDEX ix_session_records_dataset_id ON session_records(dataset_id);
        CREATE INDEX ix_session_records_last_activity_at ON session_records(last_activity_at);
        CREATE TABLE pipeline_runs (
            id CHAR(32) PRIMARY KEY,
            pipeline_run_id CHAR(32),
            parent_operation_id CHAR(32),
            dataset_id CHAR(32),
            pipeline_name TEXT,
            operation_name TEXT,
            status TEXT,
            outcome TEXT,
            background INTEGER,
            error_class TEXT,
            origin TEXT,
            created_at DATETIME,
            started_at DATETIME,
            ended_at DATETIME
        );
        CREATE INDEX ix_pipeline_runs_dataset_id ON pipeline_runs(dataset_id);
        CREATE INDEX ix_pipeline_runs_pipeline_run_id ON pipeline_runs(pipeline_run_id);
        CREATE INDEX ix_pipeline_runs_dataset_pipeline_created_at
            ON pipeline_runs(dataset_id, pipeline_name, created_at);
        CREATE INDEX ix_pipeline_runs_created_at_id
            ON pipeline_runs(created_at, id);
    ''')
    con.commit()
    con.close()

    ccon = sqlite3.connect(cache_db)
    ccon.execute('PRAGMA journal_mode=WAL')
    ccon.execute('PRAGMA journal_size_limit=4194304')
    ccon.execute('PRAGMA synchronous=NORMAL')
    ccon.executescript('''
        CREATE TABLE cache_qa_entries (
            seq INTEGER PRIMARY KEY AUTOINCREMENT,
            user_id TEXT NOT NULL,
            session_id TEXT NOT NULL,
            payload TEXT NOT NULL,
            expires_at TEXT
        );
        CREATE INDEX idx_cache_qa_session ON cache_qa_entries(user_id, session_id, seq);
        CREATE TABLE cache_trace_entries (
            seq INTEGER PRIMARY KEY AUTOINCREMENT,
            user_id TEXT NOT NULL,
            session_id TEXT NOT NULL,
            payload TEXT NOT NULL,
            expires_at TEXT
        );
        CREATE INDEX idx_cache_trace_session ON cache_trace_entries(user_id, session_id, seq);
    ''')
    ccon.commit()
    ccon.close()


def generate_adversarial_fixture(root: Path, scale: str = '1k') -> dict:
    """Populate isolated official store + Memlane state with all 7 §6.1 adversarial distributions."""
    spec = SCALES[scale]
    official_data = root / 'official-store'
    db_dir = official_data / 'system' / 'databases'
    data_dir = official_data / 'data'
    state_dir = root / 'state'
    db_dir.mkdir(parents=True, exist_ok=True)
    data_dir.mkdir(parents=True, exist_ok=True)
    state_dir.mkdir(parents=True, exist_ok=True)

    cognee_db = db_dir / 'cognee_db'
    cache_db = db_dir / 'cache.db'
    _create_official_schemas(cognee_db, cache_db)

    ds_main = str(uuid.uuid4())
    ds_code = str(uuid.uuid4())
    ds_common = str(uuid.uuid4())
    ds_noise = str(uuid.uuid4())
    owner_id = str(uuid.uuid4())
    agent_uid = str(uuid.uuid4())

    ds_main_hex = uuid_hex(ds_main)
    ds_code_hex = uuid_hex(ds_code)
    ds_common_hex = uuid_hex(ds_common)
    ds_noise_hex = uuid_hex(ds_noise)
    owner_hex = uuid_hex(owner_id)
    agent_uid_hex = uuid_hex(agent_uid)

    sample_body_file = data_dir / 'sample_knowledge.md'
    sample_body_file.write_text('# Memlane Adversarial Sample\n\nThis is a verified knowledge body stored on disk.\n' * 8, encoding='utf-8')
    sample_raw_file = data_dir / 'sample_raw.txt'
    sample_raw_file.write_text('Raw source document text.\n' * 4, encoding='utf-8')

    n_know = spec['knowledge_docs']
    n_same_day = spec['same_day_cluster']
    n_zero_recall = spec['zero_recall_docs']
    n_raw = spec['raw_docs']

    con = sqlite3.connect(cognee_db)
    con.execute('PRAGMA cache_size = -8192')
    con.execute('BEGIN')
    con.executemany(
        'INSERT INTO datasets(id, name, owner_id, created_at) VALUES(?,?,?,?)',
        [
            (ds_main_hex, 'ml-project-main', owner_hex, '2026-01-01 00:00:00.000000'),
            (ds_code_hex, 'ml-code-main', owner_hex, '2026-01-01 00:00:00.000000'),
            (ds_common_hex, 'ml-common', owner_hex, '2026-01-01 00:00:00.000000'),
            (ds_noise_hex, 'ml-noise', owner_hex, '2026-01-01 00:00:00.000000'),
        ],
    )

    base_dt = datetime(2026, 6, 1, 12, 0, 0, tzinfo=timezone.utc)
    same_day_str = '2026-10-15'
    doc_ids = []
    oldest_unread_ids = []
    invalid_created_ids = []
    recalled_doc_ids = []

    _DATA_INSERT = '''INSERT INTO data(
        id, legacy_id, dataset_id, name, label, raw_data_location, mime_type,
        external_metadata, system_metadata, node_set, pipeline_status,
        updated_at, created_at, content_hash
    ) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)'''

    data_batch = []
    for i in range(n_know):
        did = str(uuid.uuid4())
        doc_ids.append(did)
        did_hex = uuid_hex(did)
        if i < n_same_day:
            eff_day = same_day_str
            created_str = f'2026-10-15 {i // 3600 % 24:02d}:{(i // 60) % 60:02d}:{i % 60:02d}.{(i * 137) % 1000000:06d}'
        elif i >= n_know - 3:
            eff_day = '2026-05-01'
            created_str = '' if i == n_know - 3 else ('not-a-timestamp' if i == n_know - 2 else '2026-02-31T99:99:99Z')
            invalid_created_ids.append(did)
        elif i == n_know - 4:
            eff_day = '2026-05-02'
            created_str = '2026-05-02T00:00:00.123456789Z'
            oldest_unread_ids.append(did)
        else:
            dt = base_dt + timedelta(seconds=i * 19)
            eff_day = dt.strftime('%Y-%m-%d')
            created_str = dt.strftime('%Y-%m-%d %H:%M:%S.%f')
            if i < n_same_day + 4:
                oldest_unread_ids.append(did)

        category = ('lesson', 'decision', 'bugfix', 'pattern')[i % 4]
        sys_meta = json.dumps({
            'lesson_type': category,
            'state_key': f'sk-{i:06d}',
            'first_source_date': eff_day,
            'session_id': f'sess-{i % 50:04d}',
        })
        node_set = json.dumps(json.dumps(['session_learnings'])) if i % 5 == 0 else json.dumps(['session_learnings'])
        label = f'Lesson {i:05d}: {category} optimization needle-{i % 97}'
        data_batch.append((
            did_hex,
            None,
            ds_main_hex,
            f'doc-{i:05d}.md',
            label,
            str(sample_body_file),
            'text/markdown',
            '{}',
            sys_meta,
            node_set,
            '{}',
            created_str or '2026-05-01 00:00:00.000000',
            created_str,
            hashlib.sha256(f'hash-{i}'.encode()).hexdigest()[:32],
        ))
        if i >= n_zero_recall:
            recalled_doc_ids.append(did)
        if len(data_batch) >= 5000:
            con.executemany(_DATA_INSERT, data_batch)
            data_batch.clear()

    # Raw documents (streamed)
    for i in range(n_raw):
        did = str(uuid.uuid4())
        dt = base_dt + timedelta(minutes=i)
        ts = dt.strftime('%Y-%m-%d %H:%M:%S.%f')
        pstatus = json.dumps({'cognify_pipeline': {ds_main: {'status': 'DATA_ITEM_PROCESSING_COMPLETED'}}}) if i % 3 else '{}'
        data_batch.append((
            uuid_hex(did),
            None,
            ds_main_hex,
            f'raw-doc-{i:05d}.txt',
            f'Raw Doc {i:05d}',
            str(sample_raw_file),
            'text/plain',
            '{}',
            None,
            '[]',
            pstatus,
            ts,
            ts,
            f'raw-hash-{i:05d}',
        ))
        if len(data_batch) >= 5000:
            con.executemany(_DATA_INSERT, data_batch)
            data_batch.clear()

    # Common knowledge (30 items)
    for i in range(30):
        did = str(uuid.uuid4())
        ts = (base_dt + timedelta(hours=i)).strftime('%Y-%m-%d %H:%M:%S.%f')
        cat_val = 'preference' if i % 2 == 0 else 'experience'
        sys_meta = json.dumps({'memlane_kind': 'common_knowledge', 'category': cat_val})
        data_batch.append((
            uuid_hex(did),
            None,
            ds_common_hex,
            f'common-{i:03d}.md',
            f'Common {cat_val} #{i}',
            str(sample_body_file),
            'text/markdown',
            '{}',
            sys_meta,
            json.dumps(['user_context']),
            '{}',
            ts,
            ts,
            f'common-hash-{i:03d}',
        ))
    if data_batch:
        con.executemany(_DATA_INSERT, data_batch)
        data_batch.clear()

    # Distribution 5: 85% subagent sessions + 1 parent session with N children
    n_sessions = spec['sessions']
    n_parent_children = spec['parent_children']
    parent_sid = 'sess-parent-root-0001'
    session_meta = []  # (sid, last_at)
    session_relations = {}
    sess_batch = [(
        parent_sid,
        agent_uid_hex,
        ds_main_hex,
        'codex-main',
        '2026-10-20 12:00:00.000000',
        '2026-10-20 10:00:00.000000',
        'active',
    )]
    session_meta.append((parent_sid, '2026-10-20 12:00:00.000000'))

    top_count = max(5, int(n_sessions * 0.15))
    top_sids = [parent_sid]
    for i in range(1, top_count):
        sid = f'sess-top-{i:05d}'
        top_sids.append(sid)
        ts = (base_dt + timedelta(minutes=i * 7)).strftime('%Y-%m-%d %H:%M:%S.%f')
        sess_batch.append((sid, agent_uid_hex, ds_main_hex, 'codex-main', ts, ts, 'active'))
        session_meta.append((sid, ts))

    for i in range(n_parent_children):
        csid = f'sess-child-of-root-{i:05d}'
        ts = (datetime(2026, 10, 20, 10, 0, 0, tzinfo=timezone.utc) + timedelta(seconds=i * 30)).strftime('%Y-%m-%d %H:%M:%S.%f')
        sess_batch.append((csid, agent_uid_hex, ds_main_hex, 'codex-main', ts, ts, 'completed'))
        session_meta.append((csid, ts))
        session_relations[csid] = {
            'is_subagent': True,
            'parent_session_id': parent_sid,
            'subagent_role': 'worker',
            'nesting_depth': 1,
        }

    remaining_subs = max(0, n_sessions - len(session_meta))
    for i in range(remaining_subs):
        csid = f'sess-sub-other-{i:05d}'
        pid = top_sids[1 + (i % (len(top_sids) - 1))]
        ts = (base_dt + timedelta(seconds=i * 45)).strftime('%Y-%m-%d %H:%M:%S.%f')
        sess_batch.append((csid, agent_uid_hex, ds_main_hex, 'codex-main', ts, ts, 'completed'))
        session_meta.append((csid, ts))
        session_relations[csid] = {
            'is_subagent': True,
            'parent_session_id': pid,
            'subagent_role': 'explorer',
            'nesting_depth': 1,
        }

    con.executemany(
        '''INSERT INTO session_records(session_id, user_id, dataset_id, agent_id, last_activity_at, started_at, status)
           VALUES(?,?,?,?,?,?,?)''',
        sess_batch,
    )
    sess_batch.clear()

    # Distribution 7: 3 cold code pipeline_runs buried inside main_pipeline_runs + other_pipeline_runs
    _PR_INSERT = '''INSERT INTO pipeline_runs(
        id, pipeline_run_id, parent_operation_id, dataset_id, pipeline_name,
        operation_name, status, outcome, background, error_class, origin,
        created_at, started_at, ended_at
    ) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)'''
    code_run_rows = [
        (
            uuid_hex(str(uuid.uuid4())),
            uuid_hex(str(uuid.uuid4())),
            None,
            ds_code_hex,
            'code_graph_pipeline',
            '',
            'DATASET_PROCESSING_COMPLETED',
            'success',
            1,
            '',
            'cli',
            f'2026-03-0{j + 1} 08:00:00.000000',
            f'2026-03-0{j + 1} 08:00:01.000000',
            f'2026-03-0{j + 1} 08:05:00.000000',
        )
        for j in range(3)
    ]
    con.executemany(_PR_INSERT, code_run_rows)

    main_runs = spec['main_pipeline_runs']
    other_runs = spec['other_pipeline_runs']
    base_pr_epoch = int(datetime(2026, 4, 1, 0, 0, 0, tzinfo=timezone.utc).timestamp())
    batch_runs = []
    # Interleave ds_main runs (150k) and ds_noise runs (150k, newer timestamps at tail)
    for i in range(main_runs):
        rid = f'1{i + 1:031x}'
        ts = datetime.fromtimestamp(base_pr_epoch + i, tz=timezone.utc).strftime('%Y-%m-%d %H:%M:%S.000000')
        batch_runs.append((
            rid, rid, None, ds_main_hex, 'cognify_pipeline', '',
            'DATASET_PROCESSING_COMPLETED', 'success', 1, '', 'auto', ts, ts, ts,
        ))
        if len(batch_runs) >= 10000:
            con.executemany(_PR_INSERT, batch_runs)
            batch_runs.clear()
    for i in range(other_runs):
        rid = f'2{i + 1:031x}'
        ts = datetime.fromtimestamp(base_pr_epoch + main_runs + i, tz=timezone.utc).strftime('%Y-%m-%d %H:%M:%S.000000')
        batch_runs.append((
            rid, rid, None, ds_noise_hex, 'improve_pipeline', '',
            'DATASET_PROCESSING_COMPLETED', 'success', 1, '', 'auto', ts, ts, ts,
        ))
        if len(batch_runs) >= 10000:
            con.executemany(_PR_INSERT, batch_runs)
            batch_runs.clear()
    if batch_runs:
        con.executemany(_PR_INSERT, batch_runs)
        batch_runs.clear()
    con.commit()
    con.close()

    # Distribution 4: cache.db expiry pollution (80k QA + 400k traces at 50k, streamed in bounded batches)
    ccon = sqlite3.connect(cache_db)
    ccon.execute('PRAGMA cache_size = -8192')
    ccon.execute('BEGIN')
    future_exp = (datetime.now(timezone.utc) + timedelta(days=30)).strftime('%Y-%m-%d %H:%M:%S.%f')
    past_exp = (datetime.now(timezone.utc) - timedelta(days=5)).strftime('%Y-%m-%d %H:%M:%S.%f')
    qa_per = spec['qa_per_session']
    tr_per = spec['trace_per_session']
    qa_batch = []
    tr_batch = []
    expired_left = 100
    for idx, (sid, last_at) in enumerate(session_meta):
        for k in range(qa_per):
            exp = past_exp if expired_left > 0 else future_exp
            if expired_left > 0:
                expired_left -= 1
            qa_batch.append((
                agent_uid,
                sid,
                json.dumps({'question': f'Q{idx}-{k} on {sid}', 'answer': 'ok', 'time': last_at}),
                exp,
            ))
            if len(qa_batch) >= 5000:
                ccon.executemany('INSERT INTO cache_qa_entries(user_id, session_id, payload, expires_at) VALUES(?,?,?,?)', qa_batch)
                qa_batch.clear()
        for k in range(tr_per):
            exp = past_exp if (idx == 0 and k < 2) else future_exp
            tr_batch.append((
                agent_uid,
                sid,
                json.dumps({'origin_function': 'bash', 'time': last_at}),
                exp,
            ))
            if len(tr_batch) >= 10000:
                ccon.executemany('INSERT INTO cache_trace_entries(user_id, session_id, payload, expires_at) VALUES(?,?,?,?)', tr_batch)
                tr_batch.clear()
    if qa_batch:
        ccon.executemany('INSERT INTO cache_qa_entries(user_id, session_id, payload, expires_at) VALUES(?,?,?,?)', qa_batch)
        qa_batch.clear()
    if tr_batch:
        ccon.executemany('INSERT INTO cache_trace_entries(user_id, session_id, payload, expires_at) VALUES(?,?,?,?)', tr_batch)
        tr_batch.clear()
    ccon.commit()
    ccon.close()

    # Populate RecallStore with exact §6.1 recall_events (100,000 events at 50k) using batched SQLite writes
    recall_path = state_dir / 'knowledge-recall.sqlite3'
    rs = RecallStore(recall_path)
    rs.close()
    rcon = sqlite3.connect(recall_path)
    rcon.execute('PRAGMA cache_size = -8192')
    rcon.execute('BEGIN')
    n_rec_events = spec['recall_events']
    rec_pool = recalled_doc_ids or doc_ids[:100]
    pool_len = len(rec_pool)
    ev_batch = []
    hit_batch = []
    base_rec_epoch = int(datetime(2026, 7, 1, 0, 0, 0, tzinfo=timezone.utc).timestamp())
    for i in range(n_rec_events):
        ev_id = str(uuid.UUID(int=(0x10000000000000000000000000000000 + i)))
        ts = datetime.fromtimestamp(base_rec_epoch + i, tz=timezone.utc).isoformat(timespec='microseconds')
        ch = 'explicit' if i % 5 == 0 else 'automatic'
        target_doc = rec_pool[i % pool_len]
        ev_batch.append(('codex-main', ev_id, ch, ts, f'ph-{i:08x}'))
        hit_batch.append(('codex-main', ev_id, ds_main, target_doc))
        if len(ev_batch) >= 10000:
            rcon.executemany('INSERT INTO recall_event VALUES(?,?,?,?,?)', ev_batch)
            rcon.executemany('INSERT INTO recall_hit VALUES(?,?,?,?)', hit_batch)
            ev_batch.clear()
            hit_batch.clear()
    if ev_batch:
        rcon.executemany('INSERT INTO recall_event VALUES(?,?,?,?,?)', ev_batch)
        rcon.executemany('INSERT INTO recall_hit VALUES(?,?,?,?)', hit_batch)
        ev_batch.clear()
        hit_batch.clear()
    # Materialize recall_doc_counts in one SQL aggregate
    rcon.execute(
        '''INSERT INTO recall_doc_counts
           (dataset_id, document_id, total_hits, auto_hits, explicit_hits, last_recalled_at, rev)
           SELECT h.dataset_id, h.document_id, COUNT(*),
                  SUM(CASE WHEN e.channel='automatic' THEN 1 ELSE 0 END),
                  SUM(CASE WHEN e.channel='explicit' THEN 1 ELSE 0 END),
                  MAX(e.received_at), 1
           FROM recall_hit h JOIN recall_event e USING(agent_id, event_id)
           GROUP BY h.dataset_id, h.document_id'''
    )
    rcon.execute('UPDATE recall_rev_state SET last_rev = 1 WHERE singleton = 1')
    rcon.commit()
    rcon.close()

    # Populate ProvenanceStore
    prov_store = ProvenanceStore(state_dir / 'lesson-provenance.sqlite3', cognee_db_path=cognee_db)
    prov_store.record_documents_batch([
        {
            'dataset_id': ds_main,
            'document_id': doc_ids[0],
            'lesson_type': 'lesson',
            'state_key': 'sk-000000',
            'candidate_dates': ['2026-10-15'],
            'explicit_status': 'linked_qa',
        }
    ])
    prov_store.close()

    # Distribution 3: Sparse unread (N-5 read, 5 oldest unread)
    identity = 'http://127.0.0.1:18765/bench-token-sha/'
    proj_id = 'proj-main'
    read_store = ReadingStateStore(state_dir / 'ui-reading-state.sqlite3', state_dir=state_dir)
    unread_set = set(oldest_unread_ids[:5])
    if len(unread_set) < 5:
        for did in doc_ids:
            if did not in unread_set and did not in invalid_created_ids:
                unread_set.add(did)
                if len(unread_set) == 5:
                    break
    read_ids = [did for did in doc_ids if did not in unread_set]
    batch = read_store.begin_import(identity, proj_id, ds_main, initialized=True, enabled_at='2026-01-01T00:00:00Z')
    for s in range(0, len(read_ids), 500):
        chunk = [{'doc_id': did, 'is_read': True} for did in read_ids[s : s + 500]]
        read_store.stage_import_chunk(batch['batch_id'], chunk)
    read_store.commit_import(batch['batch_id'])
    read_store.close()
    del read_ids
    del session_meta

    return {
        'scale': scale,
        'spec': spec,
        'official_data': official_data,
        'cognee_db': cognee_db,
        'cache_db': cache_db,
        'state_dir': state_dir,
        'project_id': proj_id,
        'ds_main': ds_main,
        'ds_code': ds_code,
        'ds_common': ds_common,
        'ds_noise': ds_noise,
        'agent_uid': agent_uid,
        'doc_ids': doc_ids,
        'unread_ids': sorted(unread_set),
        'invalid_created_ids': invalid_created_ids,
        'parent_sid': parent_sid,
        'session_relations': session_relations,
        'identity': identity,
        'sample_body_file': sample_body_file,
    }


def _check_eqp_zero_temp_btree(cat: CatalogIndex, fixture: dict) -> dict:
    """Verify EXPLAIN QUERY PLAN for all core production queries has 0 TEMP B-TREE and 0 SCAN TABLE."""
    ds_main = fixture['ds_main']
    ds_code = fixture['ds_code']
    parent_sid = fixture['parent_sid']

    cat_queries = {
        'knowledge_date_keyset': (
            '''SELECT doc_id FROM catalog_docs
               WHERE dataset_id = ? AND is_knowledge = 1
                 AND (day_desc_key, doc_id) > (?, ?)
               ORDER BY day_desc_key ASC, doc_id ASC LIMIT 30''',
            (ds_main, -20261015, '00000000-0000-0000-0000-000000000000'),
        ),
        'knowledge_date_prev_keyset': (
            '''SELECT doc_id FROM catalog_docs
               WHERE dataset_id = ? AND is_knowledge = 1
                 AND (day_desc_key, doc_id) < (?, ?)
               ORDER BY day_desc_key DESC, doc_id DESC LIMIT 30''',
            (ds_main, -20261015, 'ffffffff-ffff-ffff-ffff-ffffffffffff'),
        ),
        'knowledge_recall_keyset': (
            '''SELECT doc_id FROM catalog_docs
               WHERE dataset_id = ? AND is_knowledge = 1
                 AND (recall_desc_key, day_desc_key, doc_id) > (?, ?, ?)
               ORDER BY recall_desc_key ASC, day_desc_key ASC, doc_id ASC LIMIT 30''',
            (ds_main, 0, -20261015, '00000000-0000-0000-0000-000000000000'),
        ),
        'knowledge_category_keyset': (
            '''SELECT doc_id FROM catalog_docs
               WHERE dataset_id = ? AND is_knowledge = 1 AND category = ?
                 AND (day_desc_key, doc_id) > (?, ?)
               ORDER BY day_desc_key ASC, doc_id ASC LIMIT 30''',
            (ds_main, 'lesson', -20261015, '00000000-0000-0000-0000-000000000000'),
        ),
        'raw_docs_keyset': (
            '''SELECT doc_id FROM catalog_docs
               WHERE dataset_id = ? AND kind = 'raw_doc'
                 AND (sort_ts_desc_key, doc_id) > (?, ?)
               ORDER BY sort_ts_desc_key ASC, doc_id ASC LIMIT 30''',
            (ds_main, -1700000000000000000, ''),
        ),
        'day_buckets_range': (
            '''SELECT effective_date, knowledge_count FROM catalog_day_buckets
               WHERE dataset_id = ? AND effective_date >= ?
               ORDER BY effective_date DESC''',
            (ds_main, '2026-01-01'),
        ),
        'late_after_rowid': (
            '''SELECT doc_id FROM catalog_docs
               WHERE dataset_id = ? AND is_knowledge = 1 AND cognee_rowid > ?''',
            (ds_main, 100),
        ),
        'top_level_sessions_keyset': (
            '''SELECT session_id FROM catalog_sessions
               WHERE dataset_id = ? AND is_top_level = 1
                 AND (sort_ts_desc_key, user_id, session_id) > (?, ?, ?)
               ORDER BY sort_ts_desc_key ASC, user_id ASC, session_id ASC LIMIT 30''',
            (ds_main, -1700000000000000000, '', ''),
        ),
        'session_children_root_keyset': (
            '''SELECT session_id FROM catalog_sessions
               WHERE dataset_id = ? AND is_top_level = 0 AND root_session_id = ?
                 AND (sort_ts_desc_key, user_id, session_id) > (?, ?, ?)
               ORDER BY sort_ts_desc_key ASC, user_id ASC, session_id ASC LIMIT 30''',
            (ds_main, parent_sid, -1700000000000000000, '', ''),
        ),
    }

    report = {}
    violations = []

    def _run_eqp(con_obj, name: str, sql: str, params: tuple, *, allow_index_scan: bool = False):
        details = [str(r[3]) for r in con_obj.execute('EXPLAIN QUERY PLAN ' + sql, params).fetchall()]
        has_temp = any('TEMP B-TREE' in d.upper() for d in details)
        has_scan = any(
            d.upper().startswith('SCAN ')
            and not (allow_index_scan and ('USING INDEX' in d.upper() or 'USING COVERING INDEX' in d.upper()))
            for d in details
        )
        report[name] = {'plan': details, 'temp_btree': has_temp, 'full_scan': has_scan}
        if has_temp or has_scan:
            violations.append(f'{name}: {details}')

    con = cat._get_reader()
    for name, (sql, params) in cat_queries.items():
        _run_eqp(con, name, sql, params)

    # RecallStore.events() production query (INDEXED BY idx_recall_event_received -> ordered index walk, 0 TEMP B-TREE)
    rcon = sqlite3.connect(f"file:{fixture['state_dir'] / 'knowledge-recall.sqlite3'}?mode=ro", uri=True)
    try:
        _run_eqp(
            rcon,
            'recall_events_timeline',
            '''SELECT e.agent_id, e.event_id, e.channel, e.received_at
               FROM recall_event e INDEXED BY idx_recall_event_received
               WHERE EXISTS (
                 SELECT 1 FROM recall_hit h
                 WHERE h.agent_id=e.agent_id AND h.event_id=e.event_id AND h.dataset_id IN (?)
               )
               ORDER BY e.received_at DESC, e.agent_id ASC, e.event_id ASC LIMIT 21 OFFSET 0''',
            (ds_main,),
            allow_index_scan=True,
        )
        _run_eqp(
            rcon,
            'recall_hit_by_agent_event',
            '''SELECT agent_id, event_id, dataset_id, document_id
               FROM recall_hit
               WHERE agent_id = ? AND event_id IN (?, ?) AND dataset_id IN (?)''',
            ('codex-main', '10000000-0000-0000-0000-000000000000', '10000000-0000-0000-0000-000000000001', ds_main),
        )
    finally:
        rcon.close()

    # cognee_db production pipeline_runs queries
    ccon = sqlite3.connect(f"file:{fixture['cognee_db']}?mode=ro", uri=True)
    try:
        _run_eqp(
            ccon,
            'pipeline_runs_timeline',
            '''SELECT id FROM pipeline_runs
               ORDER BY created_at DESC, id DESC LIMIT 128''',
            (),
            allow_index_scan=True,
        )
        _run_eqp(
            ccon,
            'pipeline_runs_code_activity',
            '''SELECT id FROM pipeline_runs
               WHERE dataset_id = ? AND pipeline_name = 'code_graph_pipeline'
               ORDER BY created_at DESC LIMIT 200''',
            (uuid_hex(ds_code),),
        )
    finally:
        ccon.close()

    return {'queries': report, 'violations': violations}


def _verify_subtle_mutations(cat: CatalogIndex, fixture: dict) -> dict:
    """Distribution 6: verify all 6 subtle & adversarial cognee_db mutations are caught."""
    cognee_db = fixture['cognee_db']
    ds_main = fixture['ds_main']
    ds_main_hex = uuid_hex(ds_main)
    allowed = [ds_main, fixture['ds_common']]
    cat.sync(allowed)
    results = {}

    # 6a. Realistic in-place UPDATE label + fresh updated_at (measures incremental_sync_1doc_ms)
    con = sqlite3.connect(cognee_db)
    try:
        target_id = fixture['doc_ids'][10]
        fresh_upd = (datetime.now(timezone.utc) + timedelta(seconds=60)).strftime('%Y-%m-%d %H:%M:%S.%f')
        con.execute(
            'UPDATE data SET label = ?, updated_at = ? WHERE id = ?',
            ('MUTATED-INPLACE-LABEL', fresh_upd, uuid_hex(target_id)),
        )
        con.commit()
    finally:
        con.close()
    t0 = time.perf_counter()
    cat.sync(allowed)
    inc_ms = (time.perf_counter() - t0) * 1000.0
    fetched = cat.fetch_docs_by_ids(ds_main, [target_id])
    results['inplace_update_detected'] = bool(fetched and fetched[0]['doc_label'] == 'MUTATED-INPLACE-LABEL')
    results['incremental_sync_1doc_ms'] = round(inc_ms, 3)

    # 6b. Adversarial same-length rename with UNCHANGED updated_at (catches weak checksums!)
    con = sqlite3.connect(cognee_db)
    try:
        target_same = fixture['doc_ids'][15]
        cur_lbl = con.execute('SELECT label FROM data WHERE id = ?', (uuid_hex(target_same),)).fetchone()[0]
        swapped = ('X' if not cur_lbl.startswith('X') else 'Y') + cur_lbl[1:]
        assert len(swapped) == len(cur_lbl)
        con.execute('UPDATE data SET label = ? WHERE id = ?', (swapped, uuid_hex(target_same)))
        con.commit()
    finally:
        con.close()
    cat.sync(allowed)
    fetched_same = cat.fetch_docs_by_ids(ds_main, [target_same])
    results['same_length_rename_detected'] = bool(fetched_same and fetched_same[0]['doc_label'] == swapped)

    # 6c. Adversarial system_metadata edit with NULL updated_at (catches updated_at > cut holes!)
    con = sqlite3.connect(cognee_db)
    try:
        target_meta = fixture['doc_ids'][16]
        new_meta = json.dumps({'lesson_type': 'decision', 'state_key': 'sk-mutated', 'first_source_date': '2025-01-02'})
        con.execute('UPDATE data SET system_metadata = ?, updated_at = NULL WHERE id = ?', (new_meta, uuid_hex(target_meta)))
        con.commit()
    finally:
        con.close()
    cat.sync(allowed)
    fetched_meta = cat.fetch_docs_by_ids(ds_main, [target_meta])
    results['metadata_null_updated_detected'] = bool(
        fetched_meta
        and fetched_meta[0]['state_key'] == 'sk-mutated'
        and fetched_meta[0]['effective_date'] == '2025-01-02'
    )

    # 6d. Silent DELETE without pipeline_runs
    del_id = fixture['doc_ids'][11]
    con = sqlite3.connect(cognee_db)
    try:
        con.execute('DELETE FROM data WHERE id = ?', (uuid_hex(del_id),))
        con.commit()
    finally:
        con.close()
    cat.sync(allowed)
    results['silent_delete_detected'] = len(cat.fetch_docs_by_ids(ds_main, [del_id])) == 0

    # 6e. 1:1 state_key replace (delete 1 + insert 1 with older updated_at)
    old_id = fixture['doc_ids'][12]
    new_id = str(uuid.uuid4())
    con = sqlite3.connect(cognee_db)
    try:
        con.execute('DELETE FROM data WHERE id = ?', (uuid_hex(old_id),))
        con.execute(
            '''INSERT INTO data(
                 id, legacy_id, dataset_id, name, label, raw_data_location, mime_type,
                 external_metadata, system_metadata, node_set, pipeline_status,
                 updated_at, created_at, content_hash
               ) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)''',
            (
                uuid_hex(new_id), None, ds_main_hex, 'replaced.md', 'REPLACED-1TO1',
                str(fixture['sample_body_file']), 'text/markdown', '{}',
                json.dumps({'lesson_type': 'lesson', 'state_key': 'sk-000012', 'first_source_date': '2026-10-15'}),
                json.dumps(['session_learnings']), '{}',
                '2026-05-01 00:00:00.000000', '2026-05-01 00:00:00.000000', 'replaced-hash',
            ),
        )
        con.commit()
    finally:
        con.close()
    cat.sync(allowed)
    results['replace_1to1_detected'] = (
        len(cat.fetch_docs_by_ids(ds_main, [old_id])) == 0
        and len(cat.fetch_docs_by_ids(ds_main, [new_id])) == 1
    )

    # 6f. max_rowid delete + explicit rowid reuse
    con = sqlite3.connect(cognee_db)
    try:
        max_row = con.execute('SELECT rowid, id FROM data WHERE dataset_id = ? ORDER BY rowid DESC LIMIT 1', (ds_main_hex,)).fetchone()
        max_rid = int(max_row[0])
        reused_id = str(uuid.uuid4())
        con.execute('DELETE FROM data WHERE rowid = ?', (max_rid,))
        con.execute(
            '''INSERT INTO data(
                 rowid, id, legacy_id, dataset_id, name, label, raw_data_location, mime_type,
                 external_metadata, system_metadata, node_set, pipeline_status,
                 updated_at, created_at, content_hash
               ) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)''',
            (
                max_rid, uuid_hex(reused_id), None, ds_main_hex, 'reused.md', 'REUSED-ROWID',
                str(fixture['sample_body_file']), 'text/markdown', '{}',
                json.dumps({'lesson_type': 'lesson', 'state_key': 'sk-reused', 'first_source_date': '2026-10-15'}),
                json.dumps(['session_learnings']), '{}',
                '2026-11-22 02:03:04.000000', '2026-10-15 00:00:00.000000', 'reused-hash',
            ),
        )
        con.commit()
    finally:
        con.close()
    cat.sync(allowed)
    results['rowid_reuse_detected'] = len(cat.fetch_docs_by_ids(ds_main, [reused_id])) == 1

    return results


async def run_benchmark(scale: str = '1k', concurrency_seconds: float = 2.0) -> dict:
    disk_free_gib = shutil.disk_usage(tempfile.gettempdir()).free / (1024 ** 3)
    rss_start = _rss_mib()
    with tempfile.TemporaryDirectory(prefix=f'memlane-bench-{scale}-') as tmp:
        root = Path(tmp)
        t_gen = time.perf_counter()
        fixture = generate_adversarial_fixture(root, scale=scale)
        gen_ms = (time.perf_counter() - t_gen) * 1000.0
        gc.collect()
        rss_after_fixture = _rss_mib()
        peak_after_fixture = _peak_rss_mib()

        ds_main = fixture['ds_main']
        ds_code = fixture['ds_code']
        ds_common = fixture['ds_common']
        proj_id = fixture['project_id']
        identity = fixture['identity']

        registry = types.SimpleNamespace(
            path=fixture['state_dir'] / 'project-registry.json',
            state={
                'projects': {
                    proj_id: {
                        'id': proj_id,
                        'name': 'Main Bench Project',
                        'dataset_id': ds_main,
                        'code_dataset_id': ds_code,
                        'code_enabled': True,
                        'status': 'verified',
                        'aliases': [str(root)],
                        'overrides': {},
                        'applied': {},
                        'desired_revision': 1,
                    },
                },
                'common': {
                    'id': 'common',
                    'kind': 'common',
                    'dataset_id': ds_common,
                    'status': 'verified',
                    'overrides': {},
                    'applied': {},
                    'desired_revision': 1,
                },
                'agents': {'codex-main': {'name': 'Codex', 'kind': 'codex'}},
                'sessions': {},
                'session_relations': TrackedRelationsDict(fixture['session_relations']),
            },
            mutex=asyncio.Lock(),
            official=types.SimpleNamespace(
                authorized_datasets=lambda: {ds_main, ds_code, ds_common},
            ),
        )
        registry.managed_groups = lambda: [registry.state['projects'][proj_id], registry.state['common']]
        manager_token = 'm' * 40
        headers = {
            'Authorization': 'Bearer ' + manager_token,
            'X-Memlane-Reading-Contract': 'catalog-cursor-v1',
            'X-Memlane-Reading-Identity': identity,
        }
        codex_config = {'official_data': str(fixture['official_data'])}
        app = create_app(registry, manager_token, {'codex-main': 'a' * 40}, codex_config=codex_config)
        handles = app['v2_api_handles']
        cat = handles.catalog_index

        # 1. Measure catalog-index cold rebuild (including sessions), stat short-circuit, EQP, deep keyset page
        rel_res = getattr(handles, 'relations_resolver', None)
        t0 = time.perf_counter()
        cat.sync([ds_main, ds_common], relations_resolver=rel_res)
        cold_rebuild_ms = (time.perf_counter() - t0) * 1000.0

        stat_samples = []
        for _ in range(30):
            ts = time.perf_counter()
            cat.sync([ds_main, ds_common], relations_resolver=rel_res)
            stat_samples.append((time.perf_counter() - ts) * 1000.0)

        # Deep keyset page check (at offset ~deep_offset in recall=0 cluster)
        deep_offset = fixture['spec']['deep_offset']
        con_r = cat._get_reader()
        anchor_row = con_r.execute(
            '''SELECT recall_desc_key, day_desc_key, doc_id FROM catalog_docs
               WHERE dataset_id = ? AND is_knowledge = 1
               ORDER BY recall_desc_key ASC, day_desc_key ASC, doc_id ASC
               LIMIT 1 OFFSET ?''',
            (ds_main, deep_offset),
        ).fetchone()
        deep_samples = []
        deep_anchor_doc_id = str(anchor_row[2]) if anchor_row is not None else fixture['doc_ids'][0]
        if anchor_row is not None:
            after_key = (int(anchor_row[0]), int(anchor_row[1]), str(anchor_row[2]))
            for _ in range(20):
                ts = time.perf_counter()
                page_res = cat.query_project_knowledge(ds_main, sort='recall', limit=30, after_key=after_key)
                deep_samples.append((time.perf_counter() - ts) * 1000.0)
                assert len(page_res['items']) > 0

        # 2. HTTP /v1/* latency & payload bytes
        client = TestClient(TestServer(app))
        await client.start_server()
        try:
            async def _bench_get(url: str, n: int = 12):
                lats = []
                max_bytes = 0
                last_body = None
                for _ in range(n):
                    ts = time.perf_counter()
                    async with client.session.get(client.make_url(url), headers=headers) as resp:
                        raw = await resp.read()
                        status = resp.status
                    lats.append((time.perf_counter() - ts) * 1000.0)
                    assert status == 200, (url, status, raw[:200])
                    if len(raw) > max_bytes:
                        max_bytes = len(raw)
                    last_body = json.loads(raw)
                return {
                    'p50_ms': _percentile(lats, 50),
                    'p95_ms': _percentile(lats, 95),
                    'p99_ms': _percentile(lats, 99),
                    'max_bytes': max_bytes,
                    'sample': last_body,
                }

            # Warm session projection once via overview bootstrap
            b_overview = await _bench_get('/v1/overview/bootstrap')
            # Run full production EQP checks now that catalog_sessions is also synced
            eqp = _check_eqp_zero_temp_btree(cat, fixture)

            b_knowledge = await _bench_get(f'/v1/projects/{proj_id}/knowledge?sort=date&limit=30')
            b_anchor = await _bench_get(f'/v1/projects/{proj_id}/knowledge?sort=recall&limit=30&anchor_doc_id={deep_anchor_doc_id}')
            b_unread = await _bench_get(f'/v1/projects/{proj_id}/knowledge?unread_only=1&limit=30')
            b_body = await _bench_get(f'/v1/datasets/{ds_main}/documents/{fixture["doc_ids"][0]}/body')
            b_raw = await _bench_get(f'/v1/projects/{proj_id}/raw-records?limit=30')
            b_children = await _bench_get(
                f'/v1/projects/{proj_id}/raw-records/sessions/{fixture["parent_sid"]}/children?limit=30'
            )
            b_code_act = await _bench_get(f'/v1/projects/{proj_id}/code-activity')
            b_msg_summary = await _bench_get(f'/v1/projects/{proj_id}/message-summary')

            # Walk deep page N (50 at 50k!) on /v1/projects/{id}/raw-records
            raw_cursor = b_raw['sample'].get('next_cursor') or ''
            raw_deep_target = fixture['spec']['raw_deep_pages']
            pages_walked = 1
            deep_raw_url = f'/v1/projects/{proj_id}/raw-records?limit=30'
            while raw_cursor and pages_walked < raw_deep_target:
                deep_raw_url = f'/v1/projects/{proj_id}/raw-records?limit=30&cursor={raw_cursor}'
                async with client.session.get(client.make_url(deep_raw_url), headers=headers) as r_step:
                    assert r_step.status == 200, (pages_walked, r_step.status)
                    d_step = await r_step.json()
                raw_cursor = d_step.get('next_cursor') or ''
                pages_walked += 1
            b_raw_deep = await _bench_get(deep_raw_url, n=10)

            # Verify Contract #2 (total_raw_count = sessions + raw_docs) and PY-P0-05 (no ds_noise runs)
            ov_s = b_overview['sample']
            expected_raw_total = fixture['spec']['sessions'] + fixture['spec']['raw_docs']
            assert ov_s['total_raw_count'] == expected_raw_total, (ov_s['total_raw_count'], expected_raw_total)
            assert ov_s['projects'][0]['session_count'] == fixture['spec']['sessions']
            assert all(r['dataset_id'] != fixture['ds_noise'] for r in ov_s['pipeline_runs']), 'leaked noise pipeline_run!'
            # Verify anchor jump returned anchor_doc_id with bidirectional cursors
            assert any(it['doc_id'] == deep_anchor_doc_id for it in b_anchor['sample']['items'])
            assert b_anchor['sample']['has_prev'] is True
            # Verify sparse unread found exact 5 oldest unread docs
            assert len(b_unread['sample']['items']) == 5, len(b_unread['sample']['items'])
            # Verify parent session children paginated out of N children
            assert b_children['sample']['total_count'] == fixture['spec']['parent_children']
            # Verify code-activity found cold code dataset buried under 300k other pipeline_runs
            assert b_code_act['sample']['status'] == 'DATASET_PROCESSING_COMPLETED'
            # Verify message-summary < 16 KiB and pending_documents carries full minimal Document fields
            assert b_msg_summary['max_bytes'] <= 16 * 1024, b_msg_summary['max_bytes']
            if b_msg_summary['sample']['pending_documents']:
                pd0 = b_msg_summary['sample']['pending_documents'][0]
                for req_k in ('id', 'dataset_id', 'name', 'label', 'title', 'date', 'kind', 'created_at'):
                    assert req_k in pd0, (req_k, pd0)

            # 3. Dual-DB WAL mixed concurrency: 1 writer writing cognee_db + cache.db, 4 async readers
            stop_ev = threading.Event()
            write_errors = []
            written_doc_ids = []
            cache_writes_done = [0]
            initial_k_count = cat.dataset_sync_info(ds_main)['knowledge_count']

            def _writer_loop():
                wcon = sqlite3.connect(fixture['cognee_db'], timeout=5.0)
                ccon_w = sqlite3.connect(fixture['cache_db'], timeout=5.0)
                try:
                    idx = 0
                    future_exp = (datetime.now(timezone.utc) + timedelta(days=30)).strftime('%Y-%m-%d %H:%M:%S.%f')
                    while not stop_ev.is_set():
                        did = str(uuid.uuid4())
                        ts_now = datetime.now(timezone.utc).strftime('%Y-%m-%d %H:%M:%S.%f')
                        wcon.execute(
                            '''INSERT INTO data(
                                 id, legacy_id, dataset_id, name, label, raw_data_location, mime_type,
                                 external_metadata, system_metadata, node_set, pipeline_status,
                                 updated_at, created_at, content_hash
                               ) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)''',
                            (
                                uuid_hex(did), None, uuid_hex(ds_main), f'live-{idx}.md', f'Live {idx}',
                                str(fixture['sample_body_file']), 'text/markdown', '{}',
                                json.dumps({'lesson_type': 'lesson', 'state_key': f'live-{idx}', 'first_source_date': '2026-11-22'}),
                                json.dumps(['session_learnings']), '{}', ts_now, ts_now, f'live-{idx}',
                            ),
                        )
                        wcon.commit()
                        written_doc_ids.append(did)
                        ccon_w.execute(
                            'INSERT INTO cache_qa_entries(user_id, session_id, payload, expires_at) VALUES(?,?,?,?)',
                            (
                                fixture['agent_uid'],
                                fixture['parent_sid'],
                                json.dumps({'question': f'Live Q {idx}', 'answer': 'ok', 'time': ts_now}),
                                future_exp,
                            ),
                        )
                        ccon_w.commit()
                        cache_writes_done[0] += 1
                        idx += 1
                        time.sleep(0.05)
                except Exception as exc:
                    write_errors.append(repr(exc))
                finally:
                    wcon.close()
                    ccon_w.close()

            reader_lats = []
            reader_errors = []

            async def _reader_worker(worker_id: int):
                endpoints = [
                    '/v1/overview/bootstrap',
                    f'/v1/projects/{proj_id}/knowledge?sort=date&limit=30',
                    f'/v1/datasets/{ds_main}/documents/{fixture["doc_ids"][worker_id]}/body',
                    f'/v1/projects/{proj_id}/raw-records?limit=30',
                    f'/v1/projects/{proj_id}/message-summary',
                    f'/v1/projects/{proj_id}/code-activity',
                ]
                deadline = time.monotonic() + concurrency_seconds
                step = 0
                while time.monotonic() < deadline:
                    url = endpoints[(worker_id + step) % len(endpoints)]
                    step += 1
                    ts = time.perf_counter()
                    try:
                        async with client.session.get(client.make_url(url), headers=headers) as resp:
                            await resp.read()
                            status = resp.status
                        reader_lats.append((time.perf_counter() - ts) * 1000.0)
                        if status != 200:
                            reader_errors.append(f'{url}: status={status}')
                    except Exception as exc:
                        reader_errors.append(f'{url}: {exc!r}')

            gen_before_wal = cat.generation()
            writer_thread = threading.Thread(target=_writer_loop, daemon=True)
            writer_thread.start()
            try:
                await asyncio.gather(*[_reader_worker(i) for i in range(4)])
            finally:
                stop_ev.set()
                writer_thread.join(timeout=5.0)
            gen_after_wal_loop = cat.generation()
            k_count_after_wal_loop = cat.dataset_sync_info(ds_main)['knowledge_count']
            bg_docs_ingested = max(0, k_count_after_wal_loop - initial_k_count)
            bg_gen_delta = max(0, gen_after_wal_loop - gen_before_wal)
            bg_caught_up = (
                bg_gen_delta >= 2
                and bg_docs_ingested >= max(1, len(written_doc_ids) // 2)
            )

            # Final content reconciliation after concurrent WAL writes
            cat.sync([ds_main, ds_common])
            final_k_count = cat.dataset_sync_info(ds_main)['knowledge_count']
            found_live = cat.fetch_docs_by_ids(ds_main, written_doc_ids)
            wal_reconciled = (
                final_k_count == initial_k_count + len(written_doc_ids)
                and len(found_live) == len(written_doc_ids)
            )

            # 4. Distribution 6: verify all 6 subtle & adversarial mutations
            mutations = _verify_subtle_mutations(cat, fixture)
            gc.collect()
            rss_end = _rss_mib()
            peak_end = _peak_rss_mib()
        finally:
            await client.close()

    report = {
        'scale': scale,
        'disk_free_gib': round(disk_free_gib, 2),
        'fixture_gen_ms': round(gen_ms, 1),
        'catalog_cold_rebuild_ms': round(cold_rebuild_ms, 2),
        'catalog_stat_shortcircuit_p95_ms': _percentile(stat_samples, 95),
        'catalog_incremental_1doc_ms': mutations['incremental_sync_1doc_ms'],
        'deep_keyset_page_p95_ms': _percentile(deep_samples, 95),
        'eqp_violations': eqp['violations'],
        'overview_bootstrap': {k: v for k, v in b_overview.items() if k != 'sample'},
        'project_knowledge': {k: v for k, v in b_knowledge.items() if k != 'sample'},
        'anchor_jump': {k: v for k, v in b_anchor.items() if k != 'sample'},
        'sparse_unread': {
            'p95_ms': b_unread['p95_ms'],
            'count': len(b_unread['sample']['items']),
        },
        'document_body': {k: v for k, v in b_body.items() if k != 'sample'},
        'raw_records': {k: v for k, v in b_raw.items() if k != 'sample'},
        'raw_records_deep': {
            'pages_walked': pages_walked,
            **{k: v for k, v in b_raw_deep.items() if k != 'sample'},
        },
        'session_children': {k: v for k, v in b_children.items() if k != 'sample'},
        'code_activity': {k: v for k, v in b_code_act.items() if k != 'sample'},
        'message_summary': {k: v for k, v in b_msg_summary.items() if k != 'sample'},
        'mutations': mutations,
        'wal_concurrency': {
            'duration_s': concurrency_seconds,
            'writes_committed': len(written_doc_ids),
            'cache_writes_committed': cache_writes_done[0],
            'reads_completed': len(reader_lats),
            'read_p95_ms': _percentile(reader_lats, 95),
            'read_p99_ms': _percentile(reader_lats, 99),
            'bg_gen_delta': bg_gen_delta,
            'bg_docs_ingested': bg_docs_ingested,
            'bg_caught_up': bg_caught_up,
            'reconciled': wal_reconciled,
            'write_errors': write_errors,
            'reader_errors': reader_errors,
        },
        'rss_start_mib': round(rss_start, 2),
        'rss_after_fixture_mib': round(rss_after_fixture, 2),
        'rss_end_mib': round(rss_end, 2),
        'rss_delta_mib': round(max(0.0, rss_end - rss_start), 2),
        'rss_serving_delta_mib': round(max(0.0, rss_end - rss_after_fixture), 2),
        'rss_peak_serving_delta_mib': round(max(0.0, peak_end - peak_after_fixture), 2),
    }
    return report


def check_sla(report: dict) -> list[str]:
    """Check strict SLA thresholds from proposal.md §6.2."""
    failures = []
    if report['eqp_violations']:
        failures.append(f"EQP violations: {report['eqp_violations']}")
    if report['catalog_cold_rebuild_ms'] > 1200.0:
        failures.append(f"catalog_cold_rebuild_ms={report['catalog_cold_rebuild_ms']} > 1200")
    if report['catalog_incremental_1doc_ms'] > 60.0:
        failures.append(f"catalog_incremental_1doc_ms={report['catalog_incremental_1doc_ms']} > 60")
    if report['catalog_stat_shortcircuit_p95_ms'] > 0.5:
        failures.append(f"catalog_stat_shortcircuit_p95_ms={report['catalog_stat_shortcircuit_p95_ms']} > 0.5")
    if report['overview_bootstrap']['p95_ms'] > 25.0:
        failures.append(f"overview_bootstrap p95={report['overview_bootstrap']['p95_ms']} > 25")
    if report['overview_bootstrap']['max_bytes'] > 128 * 1024:
        failures.append(f"overview_bootstrap bytes={report['overview_bootstrap']['max_bytes']} > 131072")
    if report['project_knowledge']['p95_ms'] > 15.0:
        failures.append(f"project_knowledge p95={report['project_knowledge']['p95_ms']} > 15")
    if report['project_knowledge']['max_bytes'] > 256 * 1024:
        failures.append(f"project_knowledge bytes={report['project_knowledge']['max_bytes']} > 262144")
    if report['anchor_jump']['p95_ms'] > 15.0:
        failures.append(f"anchor_jump p95={report['anchor_jump']['p95_ms']} > 15")
    if report['document_body']['p95_ms'] > 5.0:
        failures.append(f"document_body p95={report['document_body']['p95_ms']} > 5")
    if report['raw_records']['p95_ms'] > 25.0:
        failures.append(f"raw_records p95={report['raw_records']['p95_ms']} > 25")
    if report['raw_records_deep']['p95_ms'] > 25.0:
        failures.append(f"raw_records_deep p95={report['raw_records_deep']['p95_ms']} > 25")
    if report['code_activity']['p95_ms'] > 20.0:
        failures.append(f"code_activity p95={report['code_activity']['p95_ms']} > 20")
    if report['message_summary']['p95_ms'] > 25.0:
        failures.append(f"message_summary p95={report['message_summary']['p95_ms']} > 25")
    if report['message_summary']['max_bytes'] > 16 * 1024:
        failures.append(f"message_summary bytes={report['message_summary']['max_bytes']} > 16384")
    if report['sparse_unread']['count'] != 5:
        failures.append(f"sparse_unread count={report['sparse_unread']['count']} != 5")
    mut = report['mutations']
    for key in (
        'inplace_update_detected',
        'same_length_rename_detected',
        'metadata_null_updated_detected',
        'silent_delete_detected',
        'replace_1to1_detected',
        'rowid_reuse_detected',
    ):
        if not mut.get(key):
            failures.append(f'mutation check failed: {key}')
    wc = report['wal_concurrency']
    if wc['write_errors'] or wc['reader_errors']:
        failures.append(f"WAL concurrency errors: writes={wc['write_errors']} reads={wc['reader_errors']}")
    if not wc.get('reconciled'):
        failures.append('WAL concurrency final content reconciliation failed')
    if wc['writes_committed'] >= 5 and not wc.get('bg_caught_up'):
        failures.append('WAL concurrency background SWR catch-up did not advance catalog generation')
    if wc['writes_committed'] < 5 or wc['cache_writes_committed'] < 5 or wc['reads_completed'] < 20:
        failures.append(
            f"WAL concurrency throughput too low: writes={wc['writes_committed']} "
            f"cache_writes={wc['cache_writes_committed']} reads={wc['reads_completed']}"
        )
    if wc['read_p99_ms'] > 80.0:
        failures.append(f"WAL concurrency read_p99_ms={wc['read_p99_ms']} > 80")
    if report['rss_serving_delta_mib'] > 80.0:
        failures.append(f"rss_serving_delta_mib={report['rss_serving_delta_mib']} > 80")
    if report['rss_peak_serving_delta_mib'] > 80.0:
        failures.append(f"rss_peak_serving_delta_mib={report['rss_peak_serving_delta_mib']} > 80")
    return failures


def main():
    parser = argparse.ArgumentParser(description='Memlane isolated adversarial scaling benchmark')
    parser.add_argument('--scale', choices=('1k', '10k', '50k'), default='1k')
    parser.add_argument('--concurrency-seconds', type=float, default=3.0)
    parser.add_argument('--json', action='store_true')
    parser.add_argument('--enforce-sla', action='store_true')
    args = parser.parse_args()

    report = asyncio.run(run_benchmark(scale=args.scale, concurrency_seconds=args.concurrency_seconds))
    sla_failures = check_sla(report)
    report['sla_passed'] = not sla_failures
    report['sla_failures'] = sla_failures
    if args.json:
        print(json.dumps(report, ensure_ascii=False, indent=2))
    else:
        print(f"=== Memlane Adversarial Scaling Benchmark ({args.scale}) ===")
        print(f"  catalog cold rebuild      : {report['catalog_cold_rebuild_ms']} ms (SLA < 1200 ms)")
        print(f"  catalog stat short-circuit: {report['catalog_stat_shortcircuit_p95_ms']} ms P95 (SLA < 0.5 ms)")
        print(f"  catalog incremental 1-doc : {report['catalog_incremental_1doc_ms']} ms (SLA < 60 ms)")
        print(f"  deep keyset page          : {report['deep_keyset_page_p95_ms']} ms P95")
        print(f"  EQP violations            : {len(report['eqp_violations'])}")
        print(f"  /v1/overview/bootstrap    : P95={report['overview_bootstrap']['p95_ms']} ms, bytes={report['overview_bootstrap']['max_bytes']} (SLA <25ms, <128KiB)")
        print(f"  /v1/projects/../knowledge : P95={report['project_knowledge']['p95_ms']} ms, bytes={report['project_knowledge']['max_bytes']} (SLA <15ms, <256KiB)")
        print(f"  /v1/.../knowledge?anchor  : P95={report['anchor_jump']['p95_ms']} ms (SLA <15ms)")
        print(f"  /v1/.../body              : P95={report['document_body']['p95_ms']} ms (SLA <5ms)")
        print(f"  /v1/.../raw-records (p1)  : P95={report['raw_records']['p95_ms']} ms (SLA <25ms)")
        print(f"  /v1/.../raw-records (p{report['raw_records_deep']['pages_walked']}) : P95={report['raw_records_deep']['p95_ms']} ms (SLA <25ms)")
        print(f"  /v1/.../code-activity     : P95={report['code_activity']['p95_ms']} ms (SLA <20ms)")
        print(f"  /v1/.../message-summary   : P95={report['message_summary']['p95_ms']} ms, bytes={report['message_summary']['max_bytes']} (SLA <25ms, <16KiB)")
        print(f"  sparse unread             : count={report['sparse_unread']['count']} P95={report['sparse_unread']['p95_ms']} ms")
        mut_ok = all(
            report['mutations'][k]
            for k in (
                'inplace_update_detected',
                'same_length_rename_detected',
                'metadata_null_updated_detected',
                'silent_delete_detected',
                'replace_1to1_detected',
                'rowid_reuse_detected',
            )
        )
        print(f"  subtle mutations (6/6)    : {mut_ok}")
        wc = report['wal_concurrency']
        print(
            f"  WAL 1W+4R concurrency     : writes={wc['writes_committed']} cache_w={wc['cache_writes_committed']} "
            f"reads={wc['reads_completed']} P99={wc['read_p99_ms']} ms reconciled={wc['reconciled']} "
            f"errs={len(wc['write_errors'])+len(wc['reader_errors'])} (SLA <80ms)"
        )
        print(
            f"  RSS serving delta         : live={report['rss_serving_delta_mib']} MiB "
            f"(after_fixture={report['rss_after_fixture_mib']} -> live={report['rss_end_mib']}; "
            f"peak_delta={report['rss_peak_serving_delta_mib']} MiB; SLA <80 MiB)"
        )
        print(f"  SLA passed                : {report['sla_passed']}")
        if sla_failures:
            for item in sla_failures:
                print(f"  [SLA FAIL] {item}")
    if args.enforce_sla and sla_failures:
        sys.exit(1)


if __name__ == '__main__':
    main()
