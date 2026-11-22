"""Efficient read-only cache.db session hydrator with batched per-user queries.

Solves C-P1-02 & CR-P0-02:
1. Resolves single-equality ``user_id`` format once per distinct user on the page
   via O(log N) covering-index seeks (never scanning all rows of cache.db).
2. Batches QA and trace counts + min-expiry probes across all sessions on the page
   (<= 50 sessions) inside ONE short read-only WAL snapshot (``ro_snapshot``),
   so concurrent writes to ``cache.db`` never trigger 150+ per-session queries or
   expiry-filter full scans.
3. Extracts first non-empty QA label strictly bound to ``(user_id, session_id)``
   inside the same read snapshot (zero ``seq IN (...)`` cross-session collision).
"""
from collections import OrderedDict
from datetime import datetime, timezone
from pathlib import Path
import sqlite3
import threading

from sqlite_ro import open_official_sqlite_ro, parse_json_value, ro_snapshot, uuid_hex, uuid_text

_MAX_EXPIRY_CACHE = 32768
_MAX_IN_BATCH = 200


def _file_stat_sig(path: Path):
    try:
        st_m = path.stat()
        main_sig = (st_m.st_ino, st_m.st_mtime_ns, st_m.st_size)
    except OSError:
        main_sig = (0, 0, 0)
    try:
        st_w = Path(str(path) + '-wal').stat()
        wal_sig = (st_w.st_ino, st_w.st_mtime_ns, st_w.st_size) if st_w.st_size > 0 else (0, 0, 0)
    except OSError:
        wal_sig = (0, 0, 0)
    return (main_sig, wal_sig)


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
        self._min_expires: OrderedDict[tuple[str, str, str], str | None] = OrderedDict()
        self._local = threading.local()
        self._open_cons: list[sqlite3.Connection] = []
        self._closed = False

    def _get_thread_con(self, cache_db: Path) -> sqlite3.Connection:
        key = str(cache_db)
        try:
            ino = cache_db.stat().st_ino
        except OSError:
            ino = -1
        cached = getattr(self._local, 'cache_con', None)
        if cached is not None:
            c_key, c_ino, c_con = cached
            if c_key == key and c_ino == ino and ino != -1:
                return c_con
            self._local.cache_con = None
            with self._lock:
                if c_con in self._open_cons:
                    self._open_cons.remove(c_con)
            try:
                c_con.close()
            except Exception:
                pass
        con = open_official_sqlite_ro(cache_db)
        with self._lock:
            if self._closed:
                con.close()
                raise sqlite3.OperationalError('cache hydrator closed')
            self._open_cons.append(con)
        self._local.cache_con = (key, ino, con)
        return con

    def _drop_thread_con(self):
        cached = getattr(self._local, 'cache_con', None)
        self._local.cache_con = None
        if cached is not None:
            c_con = cached[2]
            with self._lock:
                if c_con in self._open_cons:
                    self._open_cons.remove(c_con)
            try:
                c_con.close()
            except Exception:
                pass

    def close(self):
        with self._lock:
            self._closed = True
            stale = list(self._open_cons)
            self._open_cons.clear()
        for con in stale:
            try:
                con.close()
            except Exception:
                pass

    def _resolve_user_key_once(
        self,
        con: sqlite3.Connection,
        table: str,
        user_id: str,
        sample_sid: str,
    ) -> str:
        """Resolve single-equality user_id format via O(log N) covering index seek."""
        uid36 = uuid_text(user_id)
        uhex = uuid_hex(user_id)
        primary = uid36 or str(user_id)
        row = con.execute(
            f'SELECT 1 FROM {table} WHERE user_id = ? LIMIT 1',
            (primary,),
        ).fetchone()
        if row is not None:
            return primary
        if uhex and uhex != primary:
            row_hex = con.execute(
                f'SELECT 1 FROM {table} WHERE user_id = ? LIMIT 1',
                (uhex,),
            ).fetchone()
            if row_hex is not None:
                return uhex
        if sample_sid and uhex and uhex != primary:
            row_s = con.execute(
                f'SELECT 1 FROM {table} WHERE user_id = ? AND session_id = ? LIMIT 1',
                (uhex, sample_sid),
            ).fetchone()
            if row_s is not None:
                return uhex
        return primary

    def _hydrate_on_con(self, con: sqlite3.Connection, sessions: list[dict], now_sp: str):
        # Group valid sessions by user_id (typically 1-2 distinct users per page)
        by_uid: dict[str, list[dict]] = {}
        for row in sessions:
            uid = str(row.get('user_id') or '')
            sid = str(row.get('session_id') or '')
            if not uid or not sid:
                row['label'] = ''
                row['msg_count'] = 0
                row['tool_calls'] = 0
                continue
            by_uid.setdefault(uid, []).append(row)

        if not by_uid:
            return

        with ro_snapshot(con):
            for uid, user_rows in by_uid.items():
                uniq_sids = list(dict.fromkeys(str(r['session_id']) for r in user_rows))
                sample_sid = uniq_sids[0]
                qa_user = self._resolve_user_key_once(con, 'cache_qa_entries', uid, sample_sid)
                tr_user = self._resolve_user_key_once(con, 'cache_trace_entries', uid, sample_sid)

                # 1. Batched QA counts + per-session min(expires_at) in ONE query
                qa_counts: dict[str, int] = {}
                qa_needs_filter: set[str] = set()
                for start in range(0, len(uniq_sids), _MAX_IN_BATCH):
                    batch = uniq_sids[start : start + _MAX_IN_BATCH]
                    ph = ','.join('?' for _ in batch)
                    for sid_v, cnt_v, min_exp_v in con.execute(
                        f'''SELECT session_id, COUNT(*),
                                   MIN(CASE WHEN expires_at IS NOT NULL THEN REPLACE(substr(expires_at, 1, 26), 'T', ' ') END)
                            FROM cache_qa_entries
                            WHERE user_id = ? AND session_id IN ({ph})
                            GROUP BY session_id''',
                        (qa_user, *batch),
                    ):
                        s_key = str(sid_v)
                        qa_counts[s_key] = int(cnt_v or 0)
                        norm_exp = _normalize_expires(min_exp_v)
                        if norm_exp and norm_exp <= now_sp:
                            qa_needs_filter.add(s_key)

                # Re-count ONLY sessions that actually contain an expired QA entry
                for s_key in qa_needs_filter:
                    cnt_row = con.execute(
                        '''SELECT COUNT(*)
                           FROM cache_qa_entries
                           WHERE user_id = ? AND session_id = ?
                             AND (expires_at IS NULL OR REPLACE(substr(expires_at, 1, 26), 'T', ' ') > ?)''',
                        (qa_user, s_key, now_sp),
                    ).fetchone()
                    qa_counts[s_key] = int(cnt_row[0] or 0) if cnt_row else 0

                # Extract first non-empty question label per session with msg_count > 0
                qa_labels: dict[str, str] = {}
                for s_key in uniq_sids:
                    if qa_counts.get(s_key, 0) <= 0:
                        continue
                    if s_key in qa_needs_filter:
                        q_sql = (
                            "SELECT payload FROM cache_qa_entries "
                            "WHERE user_id = ? AND session_id = ? "
                            "AND (expires_at IS NULL OR REPLACE(substr(expires_at, 1, 26), 'T', ' ') > ?) "
                            "ORDER BY seq ASC"
                        )
                        q_params = (qa_user, s_key, now_sp)
                    else:
                        q_sql = (
                            'SELECT payload FROM cache_qa_entries '
                            'WHERE user_id = ? AND session_id = ? ORDER BY seq ASC'
                        )
                        q_params = (qa_user, s_key)
                    for (raw_payload,) in con.execute(q_sql, q_params):
                        found = _extract_question_label(raw_payload)
                        if found is not None:
                            qa_labels[s_key] = found
                            break

                # 2. Batched trace counts + per-session min(expires_at) in ONE query
                tr_counts: dict[str, int] = {}
                tr_needs_filter: set[str] = set()
                for start in range(0, len(uniq_sids), _MAX_IN_BATCH):
                    batch = uniq_sids[start : start + _MAX_IN_BATCH]
                    ph = ','.join('?' for _ in batch)
                    for sid_v, cnt_v, min_exp_v in con.execute(
                        f'''SELECT session_id, COUNT(*),
                                   MIN(CASE WHEN expires_at IS NOT NULL THEN REPLACE(substr(expires_at, 1, 26), 'T', ' ') END)
                            FROM cache_trace_entries
                            WHERE user_id = ? AND session_id IN ({ph})
                            GROUP BY session_id''',
                        (tr_user, *batch),
                    ):
                        s_key = str(sid_v)
                        tr_counts[s_key] = int(cnt_v or 0)
                        norm_exp = _normalize_expires(min_exp_v)
                        if norm_exp and norm_exp <= now_sp:
                            tr_needs_filter.add(s_key)

                # Re-count ONLY sessions that actually contain an expired trace entry
                for s_key in tr_needs_filter:
                    cnt_row = con.execute(
                        '''SELECT COUNT(*)
                           FROM cache_trace_entries
                           WHERE user_id = ? AND session_id = ?
                             AND (expires_at IS NULL OR REPLACE(substr(expires_at, 1, 26), 'T', ' ') > ?)''',
                        (tr_user, s_key, now_sp),
                    ).fetchone()
                    tr_counts[s_key] = int(cnt_row[0] or 0) if cnt_row else 0

                for row in user_rows:
                    s_key = str(row['session_id'])
                    row['label'] = qa_labels.get(s_key, '')
                    row['msg_count'] = qa_counts.get(s_key, 0)
                    row['tool_calls'] = tr_counts.get(s_key, 0)

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
        for attempt in range(2):
            try:
                con = self._get_thread_con(cache_db)
                self._hydrate_on_con(con, sessions, now_sp)
                return True
            except Exception:
                self._drop_thread_con()
                if attempt == 0:
                    continue
                for row in sessions:
                    row.setdefault('label', '')
                    row.setdefault('msg_count', 0)
                    row.setdefault('tool_calls', 0)
                return False
        return False


SHARED_CACHE_HYDRATOR = CacheSessionHydrator()
