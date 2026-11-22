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
        self._last_sessions: dict[str, str] | None = None
        self._last_ops_enc: dict[str, str] | None = None
        self._last_rec_enc: dict[str, str] | None = None
        self.db = sqlite3.connect(self.path, isolation_level=None, check_same_thread=False)
        self.db.execute('PRAGMA journal_mode=WAL')
        self.db.execute('PRAGMA journal_size_limit=4194304')
        self.db.execute('PRAGMA wal_autocheckpoint=1000')
        self.db.execute('PRAGMA synchronous=FULL')
        self.db.execute('PRAGMA busy_timeout=5000')
        self.db.executescript('''
            CREATE TABLE IF NOT EXISTS registry_meta (
                key TEXT PRIMARY KEY,
                value TEXT NOT NULL
            ) WITHOUT ROWID;

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

    def _read_meta_locked(self) -> dict:
        raw = {
            str(k): str(v)
            for k, v in self.db.execute('SELECT key, value FROM registry_meta').fetchall()
        }
        try:
            rev = int(raw['revision']) if 'revision' in raw else -1
        except ValueError:
            rev = -1
        try:
            ver = int(raw['sync_version']) if 'sync_version' in raw else 0
        except ValueError:
            ver = 0
        try:
            ts = float(raw['updated_at']) if 'updated_at' in raw else 0.0
        except ValueError:
            ts = 0.0
        return {'revision': rev, 'sync_version': ver, 'updated_at': ts}

    def _write_meta_locked(
        self,
        *,
        revision: int | None = None,
        sync_version: int | None = None,
        updated_at: float | None = None,
    ) -> dict:
        entries = []
        if revision is not None:
            entries.append(('revision', str(int(revision))))
        if sync_version is not None:
            entries.append(('sync_version', str(int(sync_version))))
        if updated_at is not None:
            entries.append(('updated_at', repr(float(updated_at))))
        if entries:
            self.db.executemany(
                '''INSERT INTO registry_meta(key, value) VALUES(?, ?)
                   ON CONFLICT(key) DO UPDATE SET value = excluded.value''',
                entries,
            )
        return self._read_meta_locked()

    def get_meta(self) -> dict:
        with self._lock:
            return self._read_meta_locked()

    def load_all_with_meta(
        self,
    ) -> tuple[
        dict[str, tuple[str, float]],
        dict[str, tuple[dict, float]],
        dict[str, tuple[dict, float]],
        dict,
    ]:
        """Load persisted dynamic tables with per-row ``updated_at`` and store metadata in one snapshot."""
        with self._lock:
            self.db.execute('BEGIN DEFERRED')
            try:
                sessions: dict[str, tuple[str, float]] = {}
                last_sessions: dict[str, str] = {}
                for k, v, ts in self.db.execute(
                    'SELECT session_key, project_id, updated_at FROM registry_sessions'
                ).fetchall():
                    ks, vs = str(k), str(v)
                    sessions[ks] = (vs, float(ts or 0.0))
                    last_sessions[ks] = vs

                operations: dict[str, tuple[dict, float]] = {}
                last_ops_enc: dict[str, str] = {}
                for op_id, raw, ts in self.db.execute(
                    'SELECT operation_id, payload_json, updated_at FROM registry_connection_operations'
                ).fetchall():
                    op_s = str(op_id)
                    raw_s = str(raw)
                    last_ops_enc[op_s] = raw_s
                    try:
                        parsed = json.loads(raw_s)
                    except ValueError:
                        continue
                    if isinstance(parsed, dict):
                        operations[op_s] = (parsed, float(ts or 0.0))

                receipts: dict[str, tuple[dict, float]] = {}
                last_rec_enc: dict[str, str] = {}
                for agent_id, raw, ts in self.db.execute(
                    'SELECT agent_id, payload_json, updated_at FROM registry_connection_receipts'
                ).fetchall():
                    aid_s = str(agent_id)
                    raw_s = str(raw)
                    last_rec_enc[aid_s] = raw_s
                    try:
                        parsed = json.loads(raw_s)
                    except ValueError:
                        continue
                    if isinstance(parsed, dict):
                        receipts[aid_s] = (parsed, float(ts or 0.0))

                meta = self._read_meta_locked()
            finally:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
            self._last_sessions = last_sessions
            self._last_ops_enc = last_ops_enc
            self._last_rec_enc = last_rec_enc
            return sessions, operations, receipts, meta

    def load_all(self) -> tuple[dict[str, str], dict[str, dict], dict[str, dict]]:
        """Load persisted ``(sessions, connection_operations, connection_receipts)``."""
        sess_meta, ops_meta, rec_meta, _ = self.load_all_with_meta()
        return (
            {k: v for k, (v, _) in sess_meta.items()},
            {k: v for k, (v, _) in ops_meta.items()},
            {k: v for k, (v, _) in rec_meta.items()},
        )

    def upsert_session(self, session_key: str, project_id: str, *, revision: int | None = None) -> None:
        now = time.time()
        ks, vs = str(session_key), str(project_id)
        with self._lock:
            self.db.execute('BEGIN IMMEDIATE')
            try:
                self.db.execute(
                    '''INSERT INTO registry_sessions(session_key, project_id, updated_at)
                       VALUES(?, ?, ?)
                       ON CONFLICT(session_key) DO UPDATE SET
                         project_id = excluded.project_id,
                         updated_at = excluded.updated_at''',
                    (ks, vs, now),
                )
                self._write_meta_locked(revision=revision, updated_at=now)
                self.db.execute('COMMIT')
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise
            if self._last_sessions is not None:
                self._last_sessions[ks] = vs

    def upsert_operation(self, operation_id: str, payload: dict, *, revision: int | None = None) -> None:
        now = time.time()
        op_s = str(operation_id)
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
                    (op_s, encoded, now),
                )
                self._write_meta_locked(revision=revision, updated_at=now)
                self.db.execute('COMMIT')
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise
            if self._last_ops_enc is not None:
                self._last_ops_enc[op_s] = encoded

    def upsert_receipt(self, agent_id: str, payload: dict, *, revision: int | None = None) -> None:
        now = time.time()
        aid_s = str(agent_id)
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
                    (aid_s, encoded, now),
                )
                self._write_meta_locked(revision=revision, updated_at=now)
                self.db.execute('COMMIT')
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise
            if self._last_rec_enc is not None:
                self._last_rec_enc[aid_s] = encoded

    def sessions_for_project(self, project_id: str) -> list[str]:
        with self._lock:
            rows = self.db.execute(
                'SELECT session_key FROM registry_sessions WHERE project_id = ? ORDER BY session_key ASC',
                (str(project_id),),
            ).fetchall()
        return [str(r[0]) for r in rows]

    def sessions_for_project_page(
        self,
        project_id: str,
        *,
        after_key: str = '',
        limit: int = 32,
        stop_key: str | None = None,
    ) -> list[str]:
        """Bounded covering-index keyset page over ``idx_registry_sessions_project``."""
        bounded = max(1, int(limit))
        pid = str(project_id)
        after = str(after_key or '')
        with self._lock:
            if stop_key is None:
                rows = self.db.execute(
                    '''SELECT session_key FROM registry_sessions
                       WHERE project_id = ? AND session_key > ?
                       ORDER BY session_key ASC
                       LIMIT ?''',
                    (pid, after, bounded),
                ).fetchall()
            else:
                rows = self.db.execute(
                    '''SELECT session_key FROM registry_sessions
                       WHERE project_id = ? AND session_key > ? AND session_key <= ?
                       ORDER BY session_key ASC
                       LIMIT ?''',
                    (pid, after, str(stop_key), bounded),
                ).fetchall()
        return [str(r[0]) for r in rows]

    def sync_dicts(
        self,
        sessions: dict | None,
        operations: dict | None,
        receipts: dict | None,
        *,
        revision: int | None = None,
        sync_version: int | None = None,
        updated_at: float | None = None,
    ) -> dict:
        """Synchronize in-memory dynamic dicts and metadata into SQLite in one transaction."""
        now = float(time.time() if updated_at is None else updated_at)
        sess_map = {str(k): str(v) for k, v in sessions.items()} if isinstance(sessions, dict) else {}
        ops_map = operations if isinstance(operations, dict) else {}
        rec_map = receipts if isinstance(receipts, dict) else {}
        ops_enc = {str(k): json.dumps(v, ensure_ascii=False, sort_keys=True) for k, v in ops_map.items()}
        rec_enc = {str(k): json.dumps(v, ensure_ascii=False, sort_keys=True) for k, v in rec_map.items()}

        with self._lock:
            self.db.execute('BEGIN IMMEDIATE')
            try:
                if self._last_sessions is None:
                    existing_sess = {
                        str(k): str(v)
                        for k, v in self.db.execute('SELECT session_key, project_id FROM registry_sessions').fetchall()
                    }
                else:
                    existing_sess = self._last_sessions
                if sess_map != existing_sess:
                    stale_sess = [(stale,) for stale in (set(existing_sess) - set(sess_map))]
                    if stale_sess:
                        self.db.executemany('DELETE FROM registry_sessions WHERE session_key = ?', stale_sess)
                    sess_upserts = [
                        (k, v, now)
                        for k, v in sess_map.items()
                        if existing_sess.get(k) != v
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

                if self._last_ops_enc is None:
                    existing_ops = {
                        str(k): str(v)
                        for k, v in self.db.execute(
                            'SELECT operation_id, payload_json FROM registry_connection_operations'
                        ).fetchall()
                    }
                else:
                    existing_ops = self._last_ops_enc
                if ops_enc != existing_ops:
                    stale_ops = [(stale,) for stale in (set(existing_ops) - set(ops_enc))]
                    if stale_ops:
                        self.db.executemany(
                            'DELETE FROM registry_connection_operations WHERE operation_id = ?', stale_ops
                        )
                    op_upserts = [
                        (k, enc, now)
                        for k, enc in ops_enc.items()
                        if existing_ops.get(k) != enc
                    ]
                    if op_upserts:
                        self.db.executemany(
                            '''INSERT INTO registry_connection_operations(operation_id, payload_json, updated_at)
                               VALUES(?, ?, ?)
                               ON CONFLICT(operation_id) DO UPDATE SET
                                 payload_json = excluded.payload_json,
                                 updated_at = excluded.updated_at''',
                            op_upserts,
                        )

                if self._last_rec_enc is None:
                    existing_rec = {
                        str(k): str(v)
                        for k, v in self.db.execute(
                            'SELECT agent_id, payload_json FROM registry_connection_receipts'
                        ).fetchall()
                    }
                else:
                    existing_rec = self._last_rec_enc
                if rec_enc != existing_rec:
                    stale_rec = [(stale,) for stale in (set(existing_rec) - set(rec_enc))]
                    if stale_rec:
                        self.db.executemany(
                            'DELETE FROM registry_connection_receipts WHERE agent_id = ?', stale_rec
                        )
                    rec_upserts = [
                        (k, enc, now)
                        for k, enc in rec_enc.items()
                        if existing_rec.get(k) != enc
                    ]
                    if rec_upserts:
                        self.db.executemany(
                            '''INSERT INTO registry_connection_receipts(agent_id, payload_json, updated_at)
                               VALUES(?, ?, ?)
                               ON CONFLICT(agent_id) DO UPDATE SET
                                 payload_json = excluded.payload_json,
                                 updated_at = excluded.updated_at''',
                            rec_upserts,
                        )

                meta = self._write_meta_locked(
                    revision=revision,
                    sync_version=sync_version,
                    updated_at=now,
                )
                self.db.execute('COMMIT')
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise
            self._last_sessions = sess_map
            self._last_ops_enc = ops_enc
            self._last_rec_enc = rec_enc
            return meta

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
