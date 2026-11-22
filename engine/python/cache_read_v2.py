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
        self._cache_db_key = ''
        self._cache_sig = None
        self._cache_ino = -1
        self._session_cache: OrderedDict[tuple[str, str], tuple[str, int, int, str | None]] = OrderedDict()
        # Per-session append/delete covering-index caches keyed by (uid, sid) -> (cnt, min_seq, max_seq, ...)
        self._qa_seq_cache: OrderedDict[tuple[str, str], tuple[int, int, int, str, int, str | None]] = OrderedDict()
        self._tr_seq_cache: OrderedDict[tuple[str, str], tuple[int, int, int, int, str | None]] = OrderedDict()
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

    def _hydrate_on_con(
        self,
        con: sqlite3.Connection,
        sessions: list[dict],
        now_sp: str,
    ) -> dict[tuple[str, str], tuple[str, int, int, str | None]]:
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
            return {}

        computed: dict[tuple[str, str], tuple[str, int, int, str | None]] = {}
        qa_seq_updates: dict[tuple[str, str], tuple[int, int, int, str, int, str | None]] = {}
        tr_seq_updates: dict[tuple[str, str], tuple[int, int, int, int, str | None]] = {}
        with self._lock:
            qa_seq_snap = dict(self._qa_seq_cache)
            tr_seq_snap = dict(self._tr_seq_cache)

        with ro_snapshot(con):
            for uid, user_rows in by_uid.items():
                uniq_sids = list(dict.fromkeys(str(r['session_id']) for r in user_rows))
                sample_sid = uniq_sids[0]
                qa_user = self._resolve_user_key_once(con, 'cache_qa_entries', uid, sample_sid)
                tr_user = self._resolve_user_key_once(con, 'cache_trace_entries', uid, sample_sid)

                # 1. Covering-index QA summary (0 table-page reads!) to detect ONLY sessions whose QA rows changed
                qa_cov: dict[str, tuple[int, int, int]] = {}
                for start in range(0, len(uniq_sids), _MAX_IN_BATCH):
                    batch = uniq_sids[start : start + _MAX_IN_BATCH]
                    ph = ','.join('?' for _ in batch)
                    for sid_v, cnt_v, min_seq_v, max_seq_v in con.execute(
                        f'''SELECT session_id, COUNT(*), COALESCE(MIN(seq), 0), COALESCE(MAX(seq), 0)
                            FROM cache_qa_entries
                            WHERE user_id = ? AND session_id IN ({ph})
                            GROUP BY session_id''',
                        (qa_user, *batch),
                    ):
                        qa_cov[str(sid_v)] = (int(cnt_v or 0), int(min_seq_v or 0), int(max_seq_v or 0))

                qa_counts: dict[str, int] = {}
                qa_labels: dict[str, str] = {}
                qa_min_exp: dict[str, str | None] = {}
                qa_dirty_sids: list[str] = []
                for s_key in uniq_sids:
                    cov = qa_cov.get(s_key)
                    if cov is None or cov[0] <= 0:
                        qa_counts[s_key] = 0
                        qa_labels[s_key] = ''
                        qa_min_exp[s_key] = None
                        qa_seq_updates[(uid, s_key)] = (0, 0, 0, '', 0, None)
                        continue
                    prev_q = qa_seq_snap.get((uid, s_key))
                    if (
                        prev_q is not None
                        and prev_q[0] == cov[0]
                        and prev_q[1] == cov[1]
                        and prev_q[2] == cov[2]
                        and (prev_q[5] is None or prev_q[5] > now_sp)
                    ):
                        qa_labels[s_key] = prev_q[3]
                        qa_counts[s_key] = prev_q[4]
                        qa_min_exp[s_key] = prev_q[5]
                    else:
                        qa_dirty_sids.append(s_key)

                if qa_dirty_sids:
                    qa_needs_filter: set[str] = set()
                    for start in range(0, len(qa_dirty_sids), _MAX_IN_BATCH):
                        batch = qa_dirty_sids[start : start + _MAX_IN_BATCH]
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
                            qa_min_exp[s_key] = norm_exp
                            if norm_exp and norm_exp <= now_sp:
                                qa_needs_filter.add(s_key)

                    for s_key in qa_needs_filter:
                        cnt_row = con.execute(
                            '''SELECT COUNT(*),
                                      MIN(CASE WHEN expires_at IS NOT NULL THEN REPLACE(substr(expires_at, 1, 26), 'T', ' ') END)
                               FROM cache_qa_entries
                               WHERE user_id = ? AND session_id = ?
                                 AND (expires_at IS NULL OR REPLACE(substr(expires_at, 1, 26), 'T', ' ') > ?)''',
                            (qa_user, s_key, now_sp),
                        ).fetchone()
                        qa_counts[s_key] = int(cnt_row[0] or 0) if cnt_row else 0
                        qa_min_exp[s_key] = _normalize_expires(cnt_row[1]) if cnt_row else None

                    # 1 batched PK lookup for dirty non-expired sessions
                    fast_seqs = [
                        qa_cov[s_key][1]
                        for s_key in qa_dirty_sids
                        if qa_counts.get(s_key, 0) > 0 and s_key not in qa_needs_filter and qa_cov[s_key][1] > 0
                    ]
                    for start in range(0, len(fast_seqs), _MAX_IN_BATCH):
                        seq_batch = fast_seqs[start : start + _MAX_IN_BATCH]
                        ph = ','.join('?' for _ in seq_batch)
                        for sid_v, raw_payload in con.execute(
                            f'SELECT session_id, payload FROM cache_qa_entries WHERE seq IN ({ph})',
                            seq_batch,
                        ):
                            found = _extract_question_label(raw_payload)
                            if found is not None:
                                qa_labels[str(sid_v)] = found

                    for s_key in qa_dirty_sids:
                        if s_key in qa_labels:
                            continue
                        q_cnt = qa_counts.get(s_key, 0)
                        if q_cnt <= 0:
                            continue
                        if s_key in qa_needs_filter:
                            q_sql = (
                                "SELECT payload FROM cache_qa_entries "
                                "WHERE user_id = ? AND session_id = ? "
                                "AND (expires_at IS NULL OR REPLACE(substr(expires_at, 1, 26), 'T', ' ') > ?) "
                                "ORDER BY seq ASC"
                            )
                            q_params = (qa_user, s_key, now_sp)
                        elif q_cnt > 1 and qa_cov[s_key][1] > 0:
                            q_sql = (
                                'SELECT payload FROM cache_qa_entries '
                                'WHERE user_id = ? AND session_id = ? AND seq > ? ORDER BY seq ASC'
                            )
                            q_params = (qa_user, s_key, qa_cov[s_key][1])
                        else:
                            continue
                        for (raw_payload,) in con.execute(q_sql, q_params):
                            found = _extract_question_label(raw_payload)
                            if found is not None:
                                qa_labels[s_key] = found
                                break

                    for s_key in qa_dirty_sids:
                        cov = qa_cov[s_key]
                        qa_seq_updates[(uid, s_key)] = (
                            cov[0],
                            cov[1],
                            cov[2],
                            qa_labels.get(s_key, ''),
                            qa_counts.get(s_key, 0),
                            qa_min_exp.get(s_key),
                        )

                # 2. Covering-index trace summary (0 table-page reads!) to skip unchanged sessions' 7,200+ trace rows
                tr_cov: dict[str, tuple[int, int, int]] = {}
                for start in range(0, len(uniq_sids), _MAX_IN_BATCH):
                    batch = uniq_sids[start : start + _MAX_IN_BATCH]
                    ph = ','.join('?' for _ in batch)
                    for sid_v, cnt_v, min_seq_v, max_seq_v in con.execute(
                        f'''SELECT session_id, COUNT(*), COALESCE(MIN(seq), 0), COALESCE(MAX(seq), 0)
                            FROM cache_trace_entries
                            WHERE user_id = ? AND session_id IN ({ph})
                            GROUP BY session_id''',
                        (tr_user, *batch),
                    ):
                        tr_cov[str(sid_v)] = (int(cnt_v or 0), int(min_seq_v or 0), int(max_seq_v or 0))

                tr_counts: dict[str, int] = {}
                tr_min_exp: dict[str, str | None] = {}
                tr_dirty_sids: list[str] = []
                for s_key in uniq_sids:
                    cov = tr_cov.get(s_key)
                    if cov is None or cov[0] <= 0:
                        tr_counts[s_key] = 0
                        tr_min_exp[s_key] = None
                        tr_seq_updates[(uid, s_key)] = (0, 0, 0, 0, None)
                        continue
                    prev_t = tr_seq_snap.get((uid, s_key))
                    if (
                        prev_t is not None
                        and prev_t[0] == cov[0]
                        and prev_t[1] == cov[1]
                        and prev_t[2] == cov[2]
                        and (prev_t[4] is None or prev_t[4] > now_sp)
                    ):
                        tr_counts[s_key] = prev_t[3]
                        tr_min_exp[s_key] = prev_t[4]
                    else:
                        tr_dirty_sids.append(s_key)

                if tr_dirty_sids:
                    tr_needs_filter: set[str] = set()
                    for start in range(0, len(tr_dirty_sids), _MAX_IN_BATCH):
                        batch = tr_dirty_sids[start : start + _MAX_IN_BATCH]
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
                            tr_min_exp[s_key] = norm_exp
                            if norm_exp and norm_exp <= now_sp:
                                tr_needs_filter.add(s_key)

                    for s_key in tr_needs_filter:
                        cnt_row = con.execute(
                            '''SELECT COUNT(*),
                                      MIN(CASE WHEN expires_at IS NOT NULL THEN REPLACE(substr(expires_at, 1, 26), 'T', ' ') END)
                               FROM cache_trace_entries
                               WHERE user_id = ? AND session_id = ?
                                 AND (expires_at IS NULL OR REPLACE(substr(expires_at, 1, 26), 'T', ' ') > ?)''',
                            (tr_user, s_key, now_sp),
                        ).fetchone()
                        tr_counts[s_key] = int(cnt_row[0] or 0) if cnt_row else 0
                        tr_min_exp[s_key] = _normalize_expires(cnt_row[1]) if cnt_row else None

                    for s_key in tr_dirty_sids:
                        cov = tr_cov[s_key]
                        tr_seq_updates[(uid, s_key)] = (
                            cov[0],
                            cov[1],
                            cov[2],
                            tr_counts.get(s_key, 0),
                            tr_min_exp.get(s_key),
                        )

                for row in user_rows:
                    s_key = str(row['session_id'])
                    lbl = qa_labels.get(s_key, '')
                    mc = qa_counts.get(s_key, 0)
                    tc = tr_counts.get(s_key, 0)
                    row['label'] = lbl
                    row['msg_count'] = mc
                    row['tool_calls'] = tc
                    e1 = qa_min_exp.get(s_key)
                    e2 = tr_min_exp.get(s_key)
                    if e1 and e2:
                        next_exp = e1 if e1 < e2 else e2
                    else:
                        next_exp = e1 or e2
                    computed[(uid, s_key)] = (lbl, mc, tc, next_exp)

        if qa_seq_updates or tr_seq_updates:
            with self._lock:
                for k, v in qa_seq_updates.items():
                    self._qa_seq_cache[k] = v
                    self._qa_seq_cache.move_to_end(k)
                while len(self._qa_seq_cache) > 4096:
                    self._qa_seq_cache.popitem(last=False)
                for k, v in tr_seq_updates.items():
                    self._tr_seq_cache[k] = v
                    self._tr_seq_cache.move_to_end(k)
                while len(self._tr_seq_cache) > 4096:
                    self._tr_seq_cache.popitem(last=False)
        return computed

    def _fill_from_cache_locked(
        self,
        db_key: str,
        sig,
        sessions: list[dict],
        now_sp: str,
    ) -> list[dict]:
        db_ino = sig[0][0] if sig and sig[0] else -1
        if self._cache_db_key != db_key or self._cache_ino != db_ino:
            self._session_cache.clear()
            self._qa_seq_cache.clear()
            self._tr_seq_cache.clear()
            self._cache_db_key = db_key
            self._cache_ino = db_ino
            self._cache_sig = sig
        elif self._cache_sig != sig:
            self._session_cache.clear()
            self._cache_sig = sig
        missing: list[dict] = []
        cache = self._session_cache
        for row in sessions:
            uid = str(row.get('user_id') or '')
            sid = str(row.get('session_id') or '')
            if not uid or not sid:
                row['label'] = ''
                row['msg_count'] = 0
                row['tool_calls'] = 0
                continue
            key = (uid, sid)
            entry = cache.get(key)
            if entry is not None and (entry[3] is None or entry[3] > now_sp):
                row['label'] = entry[0]
                row['msg_count'] = entry[1]
                row['tool_calls'] = entry[2]
                cache.move_to_end(key)
            else:
                missing.append(row)
        return missing

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

        db_key = str(cache_db)
        now_sp = _now_space()
        sig = _file_stat_sig(cache_db)
        with self._lock:
            missing = self._fill_from_cache_locked(db_key, sig, sessions, now_sp)
        if not missing:
            return True

        # Hydrate missing sessions on this thread's own read-only WAL connection without
        # serializing concurrent readers behind a global mutex.
        for attempt in range(2):
            try:
                con = self._get_thread_con(cache_db)
                computed = self._hydrate_on_con(con, missing, now_sp)
                sig_after = _file_stat_sig(cache_db)
                if sig_after == sig and computed:
                    with self._lock:
                        if self._cache_db_key == db_key and self._cache_sig == sig:
                            cache = self._session_cache
                            for k, v in computed.items():
                                cache[k] = v
                                cache.move_to_end(k)
                            while len(cache) > 4096:
                                cache.popitem(last=False)
                return True
            except Exception:
                self._drop_thread_con()
                if attempt == 0:
                    continue
                for row in missing:
                    row.setdefault('label', '')
                    row.setdefault('msg_count', 0)
                    row.setdefault('tool_calls', 0)
                return False
        return False


SHARED_CACHE_HYDRATOR = CacheSessionHydrator()
