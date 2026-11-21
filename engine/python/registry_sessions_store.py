"""Private 0600 SQLite WAL store for high-churn registry session & connection state.

Splits dynamic ``sessions``, ``connection_operations``, and ``connection_receipts``
out of ``project-registry.json`` so ``/v1/project/resolve`` and connection receipt
updates execute O(1) single-row SQLite upserts (<0.3ms) instead of rewriting the
entire registry JSON on every hook call.
"""
from __future__ import annotations

import json
import os
from pathlib import Path
import sqlite3
import threading
import time

from project_policy import PolicyError


class RegistrySessionsStore:
    """Thread-safe 0600 WAL SQLite store for dynamic registry tables."""

    def __init__(self, path):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        os.chmod(self.path.parent, 0o700)
        if self.path.is_symlink():
            raise PolicyError('private_registry_required', 503)
        if self.path.exists() and (self.path.stat().st_mode & 0o077):
            raise PolicyError('private_registry_required', 503)
        fd = os.open(self.path, os.O_CREAT | os.O_RDWR | getattr(os, 'O_NOFOLLOW', 0), 0o600)
        os.close(fd)
        os.chmod(self.path, 0o600)

        self._lock = threading.RLock()
        self._closed = False
        self.db = sqlite3.connect(self.path, isolation_level=None, check_same_thread=False)
        self.db.execute('PRAGMA journal_mode=WAL')
        self.db.execute('PRAGMA journal_size_limit=4194304')
        self.db.execute('PRAGMA wal_autocheckpoint=1000')
        self.db.execute('PRAGMA synchronous=FULL')
        self.db.execute('PRAGMA busy_timeout=5000')
        self.db.executescript('''
            CREATE TABLE IF NOT EXISTS registry_sessions (
                session_key TEXT PRIMARY KEY,
                project_id TEXT NOT NULL,
                updated_at REAL NOT NULL
            ) WITHOUT ROWID;
            CREATE INDEX IF NOT EXISTS idx_registry_sessions_project
                ON registry_sessions(project_id);

            CREATE TABLE IF NOT EXISTS registry_connection_operations (
                operation_id TEXT PRIMARY KEY,
                payload_json TEXT NOT NULL,
                updated_at REAL NOT NULL
            ) WITHOUT ROWID;

            CREATE TABLE IF NOT EXISTS registry_connection_receipts (
                agent_id TEXT PRIMARY KEY,
                payload_json TEXT NOT NULL,
                updated_at REAL NOT NULL
            ) WITHOUT ROWID;
        ''')

    def load_all(self) -> tuple[dict[str, str], dict[str, dict], dict[str, dict]]:
        """Load persisted ``(sessions, connection_operations, connection_receipts)``."""
        with self._lock:
            sessions = {
                str(k): str(v)
                for k, v in self.db.execute(
                    'SELECT session_key, project_id FROM registry_sessions'
                ).fetchall()
            }
            operations = {}
            for op_id, raw in self.db.execute(
                'SELECT operation_id, payload_json FROM registry_connection_operations'
            ).fetchall():
                try:
                    parsed = json.loads(raw)
                except ValueError:
                    continue
                if isinstance(parsed, dict):
                    operations[str(op_id)] = parsed
            receipts = {}
            for agent_id, raw in self.db.execute(
                'SELECT agent_id, payload_json FROM registry_connection_receipts'
            ).fetchall():
                try:
                    parsed = json.loads(raw)
                except ValueError:
                    continue
                if isinstance(parsed, dict):
                    receipts[str(agent_id)] = parsed
            return sessions, operations, receipts

    def upsert_session(self, session_key: str, project_id: str) -> None:
        now = time.time()
        with self._lock:
            self.db.execute('BEGIN IMMEDIATE')
            try:
                self.db.execute(
                    '''INSERT INTO registry_sessions(session_key, project_id, updated_at)
                       VALUES(?, ?, ?)
                       ON CONFLICT(session_key) DO UPDATE SET
                         project_id = excluded.project_id,
                         updated_at = excluded.updated_at''',
                    (str(session_key), str(project_id), now),
                )
                self.db.execute('COMMIT')
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise

    def upsert_operation(self, operation_id: str, payload: dict) -> None:
        now = time.time()
        encoded = json.dumps(payload, ensure_ascii=False, sort_keys=True)
        with self._lock:
            self.db.execute('BEGIN IMMEDIATE')
            try:
                self.db.execute(
                    '''INSERT INTO registry_connection_operations(operation_id, payload_json, updated_at)
                       VALUES(?, ?, ?)
                       ON CONFLICT(operation_id) DO UPDATE SET
                         payload_json = excluded.payload_json,
                         updated_at = excluded.updated_at''',
                    (str(operation_id), encoded, now),
                )
                self.db.execute('COMMIT')
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise

    def upsert_receipt(self, agent_id: str, payload: dict) -> None:
        now = time.time()
        encoded = json.dumps(payload, ensure_ascii=False, sort_keys=True)
        with self._lock:
            self.db.execute('BEGIN IMMEDIATE')
            try:
                self.db.execute(
                    '''INSERT INTO registry_connection_receipts(agent_id, payload_json, updated_at)
                       VALUES(?, ?, ?)
                       ON CONFLICT(agent_id) DO UPDATE SET
                         payload_json = excluded.payload_json,
                         updated_at = excluded.updated_at''',
                    (str(agent_id), encoded, now),
                )
                self.db.execute('COMMIT')
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise

    def sessions_for_project(self, project_id: str) -> list[str]:
        with self._lock:
            rows = self.db.execute(
                'SELECT session_key FROM registry_sessions WHERE project_id = ? ORDER BY session_key ASC',
                (str(project_id),),
            ).fetchall()
        return [str(r[0]) for r in rows]

    def sync_dicts(
        self,
        sessions: dict | None,
        operations: dict | None,
        receipts: dict | None,
    ) -> None:
        """Synchronize in-memory dynamic dicts into SQLite in a single transaction."""
        now = time.time()
        sess_map = sessions if isinstance(sessions, dict) else {}
        ops_map = operations if isinstance(operations, dict) else {}
        rec_map = receipts if isinstance(receipts, dict) else {}
        with self._lock:
            self.db.execute('BEGIN IMMEDIATE')
            try:
                existing_sess = {
                    str(k): str(v)
                    for k, v in self.db.execute('SELECT session_key, project_id FROM registry_sessions').fetchall()
                }
                for stale in set(existing_sess) - set(sess_map):
                    self.db.execute('DELETE FROM registry_sessions WHERE session_key = ?', (stale,))
                sess_upserts = [
                    (str(k), str(v), now)
                    for k, v in sess_map.items()
                    if existing_sess.get(str(k)) != str(v)
                ]
                if sess_upserts:
                    self.db.executemany(
                        '''INSERT INTO registry_sessions(session_key, project_id, updated_at)
                           VALUES(?, ?, ?)
                           ON CONFLICT(session_key) DO UPDATE SET
                             project_id = excluded.project_id,
                             updated_at = excluded.updated_at''',
                        sess_upserts,
                    )

                existing_ops = {
                    str(k): str(v)
                    for k, v in self.db.execute(
                        'SELECT operation_id, payload_json FROM registry_connection_operations'
                    ).fetchall()
                }
                for stale in set(existing_ops) - set(ops_map):
                    self.db.execute('DELETE FROM registry_connection_operations WHERE operation_id = ?', (stale,))
                op_upserts = []
                for k, v in ops_map.items():
                    enc = json.dumps(v, ensure_ascii=False, sort_keys=True)
                    if existing_ops.get(str(k)) != enc:
                        op_upserts.append((str(k), enc, now))
                if op_upserts:
                    self.db.executemany(
                        '''INSERT INTO registry_connection_operations(operation_id, payload_json, updated_at)
                           VALUES(?, ?, ?)
                           ON CONFLICT(operation_id) DO UPDATE SET
                             payload_json = excluded.payload_json,
                             updated_at = excluded.updated_at''',
                        op_upserts,
                    )

                existing_rec = {
                    str(k): str(v)
                    for k, v in self.db.execute(
                        'SELECT agent_id, payload_json FROM registry_connection_receipts'
                    ).fetchall()
                }
                for stale in set(existing_rec) - set(rec_map):
                    self.db.execute('DELETE FROM registry_connection_receipts WHERE agent_id = ?', (stale,))
                rec_upserts = []
                for k, v in rec_map.items():
                    enc = json.dumps(v, ensure_ascii=False, sort_keys=True)
                    if existing_rec.get(str(k)) != enc:
                        rec_upserts.append((str(k), enc, now))
                if rec_upserts:
                    self.db.executemany(
                        '''INSERT INTO registry_connection_receipts(agent_id, payload_json, updated_at)
                           VALUES(?, ?, ?)
                           ON CONFLICT(agent_id) DO UPDATE SET
                             payload_json = excluded.payload_json,
                             updated_at = excluded.updated_at''',
                        rec_upserts,
                    )
                self.db.execute('COMMIT')
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise

    def checkpoint(self, mode: str = 'TRUNCATE') -> bool:
        with self._lock:
            if self._closed:
                return False
            try:
                mode_str = str(mode or 'TRUNCATE').upper()
                if mode_str not in ('PASSIVE', 'FULL', 'RESTART', 'TRUNCATE'):
                    mode_str = 'TRUNCATE'
                row = self.db.execute(f'PRAGMA wal_checkpoint({mode_str})').fetchone()
                return bool(row and row[0] == 0)
            except sqlite3.Error:
                return False

    def close(self) -> None:
        with self._lock:
            if self._closed:
                return
            self._closed = True
            try:
                self.db.execute('PRAGMA wal_checkpoint(TRUNCATE)')
            except Exception:
                pass
            self.db.close()
