"""Efficient read-only cache.db session hydrator with per-session expiry tracking.

Solves C-P1-02 & CR-P0-02:
1. Auto-detects whether cache.db stores 36-char UUIDs only (100% of official Cognee
   stores) so queries use single equality ``user_id = ?`` instead of ``IN (...)``.
2. Tracks per-(table, user_id, session_id) ``min_expires_at`` in memory (invalidated
   on cache.db stat changes) so one expired historical row never forces the entire
   database off its covering index.
3. Extracts first non-empty QA label strictly bound to ``(user_id, session_id)``
   inside a single short read-only transaction (zero ``seq IN (...)`` cross-session
   collision risk).
"""
from collections import OrderedDict
from datetime import datetime, timezone
from pathlib import Path
import sqlite3
import threading

from sqlite_ro import id_variants, official_sqlite_ro, parse_json_value, uuid_text

_MAX_EXPIRY_CACHE = 32768


def _file_stat_sig(path: Path):
    out = []
    for candidate in (path, Path(str(path) + '-wal')):
        try:
            st = candidate.stat()
            out.append((st.st_ino, st.st_mtime_ns, st.st_size))
        except OSError:
            out.append((0, 0, 0))
    return tuple(out)


def _now_space() -> str:
    return datetime.now(timezone.utc).strftime('%Y-%m-%d %H:%M:%S.%f')


def _normalize_expires(value) -> str | None:
    if not isinstance(value, str):
        return None
    text = value.strip()
    if not text:
        return None
    return text[:26].replace('T', ' ')


def _extract_question_label(raw_payload) -> str | None:
    data = raw_payload if isinstance(raw_payload, dict) else parse_json_value(raw_payload)
    if not isinstance(data, dict):
        return None
    question = data.get('question')
    if isinstance(question, str) and question.strip():
        return question.strip()[:120]
    return None


class CacheSessionHydrator:
    """Stat-aware bounded read-only cache.db hydrator for <= 50 sessions per page."""

    def __init__(self):
        self._lock = threading.Lock()
        self._cache_sig = None
        self._has_hex_user_ids = False
        # (table, user_key, session_id) -> normalized min_expires_at str or None
        self._min_expires: OrderedDict[tuple[str, str, str], str | None] = OrderedDict()

    def _sync_stat_locked(self, cache_db: Path, con: sqlite3.Connection):
        sig = (str(cache_db), _file_stat_sig(cache_db))
        if self._cache_sig == sig:
            return
        self._cache_sig = sig
        self._min_expires.clear()
        # Probe once per cache.db stat whether any 32-char hex user_id exists.
        has_hex = False
        for tbl in ('cache_qa_entries', 'cache_trace_entries'):
            try:
                row = con.execute(f'SELECT 1 FROM {tbl} WHERE length(user_id) = 32 LIMIT 1').fetchone()
                if row is not None:
                    has_hex = True
                    break
            except sqlite3.Error:
                continue
        self._has_hex_user_ids = has_hex

    def _user_predicate(self, user_id: str) -> tuple[str, tuple[str, ...]]:
        uid36 = uuid_text(user_id)
        if not self._has_hex_user_ids and uid36:
            return '= ?', (uid36,)
        variants = id_variants(user_id)
        if len(variants) == 1:
            return '= ?', variants
        return f"IN ({','.join('?' for _ in variants)})", variants

    def _get_min_expires(
        self,
        con: sqlite3.Connection,
        table: str,
        user_id: str,
        session_id: str,
        user_sql: str,
        user_params: tuple[str, ...],
    ) -> str | None:
        key = (table, uuid_text(user_id), session_id)
        with self._lock:
            if key in self._min_expires:
                self._min_expires.move_to_end(key)
                return self._min_expires[key]

        row = con.execute(
            f'''SELECT MIN(REPLACE(substr(expires_at, 1, 26), 'T', ' '))
                FROM {table}
                WHERE user_id {user_sql} AND session_id = ? AND expires_at IS NOT NULL''',
            (*user_params, session_id),
        ).fetchone()
        val = _normalize_expires(row[0]) if row and row[0] else None
        with self._lock:
            self._min_expires[key] = val
            self._min_expires.move_to_end(key)
            while len(self._min_expires) > _MAX_EXPIRY_CACHE:
                self._min_expires.popitem(last=False)
        return val

    def hydrate_sessions(self, cache_db: Path, sessions: list[dict]) -> bool:
        """Hydrate ``label``, ``msg_count``, and ``tool_calls`` for the current page's sessions."""
        if not sessions:
            return bool(cache_db and cache_db.is_file() and not cache_db.is_symlink())
        if not cache_db or not cache_db.is_file() or cache_db.is_symlink():
            for row in sessions:
                row.setdefault('label', '')
                row.setdefault('msg_count', 0)
                row.setdefault('tool_calls', 0)
            return False

        now_sp = _now_space()
        try:
            with official_sqlite_ro(cache_db) as con:
                with self._lock:
                    self._sync_stat_locked(cache_db, con)

                for row in sessions:
                    uid = str(row.get('user_id') or '')
                    sid = str(row.get('session_id') or '')
                    if not uid or not sid:
                        row['label'] = ''
                        row['msg_count'] = 0
                        row['tool_calls'] = 0
                        continue
                    user_sql, user_params = self._user_predicate(uid)

                    # QA count & label (per-session expiry check avoids full-DB expiry penalty)
                    qa_min_exp = self._get_min_expires(con, 'cache_qa_entries', uid, sid, user_sql, user_params)
                    qa_needs_filter = bool(qa_min_exp and qa_min_exp <= now_sp)
                    if qa_needs_filter:
                        qa_where = (
                            f"user_id {user_sql} AND session_id = ? "
                            f"AND (expires_at IS NULL OR REPLACE(substr(expires_at, 1, 26), 'T', ' ') > ?)"
                        )
                        qa_params = (*user_params, sid, now_sp)
                    else:
                        qa_where = f'user_id {user_sql} AND session_id = ?'
                        qa_params = (*user_params, sid)

                    msg_count = int(
                        con.execute(f'SELECT COUNT(*) FROM cache_qa_entries WHERE {qa_where}', qa_params).fetchone()[0]
                    )
                    label = ''
                    if msg_count > 0:
                        # Strictly bound to (user_id, session_id): zero cross-session seq reuse bug!
                        for (raw_payload,) in con.execute(
                            f'SELECT payload FROM cache_qa_entries WHERE {qa_where} ORDER BY seq ASC',
                            qa_params,
                        ):
                            found = _extract_question_label(raw_payload)
                            if found is not None:
                                label = found
                                break

                    # Trace count (per-session expiry check)
                    tr_min_exp = self._get_min_expires(con, 'cache_trace_entries', uid, sid, user_sql, user_params)
                    tr_needs_filter = bool(tr_min_exp and tr_min_exp <= now_sp)
                    if tr_needs_filter:
                        tr_where = (
                            f"user_id {user_sql} AND session_id = ? "
                            f"AND (expires_at IS NULL OR REPLACE(substr(expires_at, 1, 26), 'T', ' ') > ?)"
                        )
                        tr_params = (*user_params, sid, now_sp)
                    else:
                        tr_where = f'user_id {user_sql} AND session_id = ?'
                        tr_params = (*user_params, sid)

                    tool_calls = int(
                        con.execute(f'SELECT COUNT(*) FROM cache_trace_entries WHERE {tr_where}', tr_params).fetchone()[0]
                    )

                    row['label'] = label
                    row['msg_count'] = msg_count
                    row['tool_calls'] = tool_calls
            return True
        except Exception:
            for row in sessions:
                row.setdefault('label', '')
                row.setdefault('msg_count', 0)
                row.setdefault('tool_calls', 0)
            return False


SHARED_CACHE_HYDRATOR = CacheSessionHydrator()
