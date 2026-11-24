"""Reconcile catalog metadata against a pinned Cognee snapshot.

The caller owns the connections, write transaction, and publication lock order.
Stages share only the state of the current sync.
"""
from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass, field
from datetime import date, datetime, timezone, tzinfo
from functools import lru_cache
import calendar
import hashlib
import os
from pathlib import Path
import re
import sqlite3
import time
from typing import ContextManager, Protocol
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

from project_policy import PolicyError
from provenance_store import _calendar_date
from sqlite_ro import extract_trusted_metadata, ro_snapshot, uuid_hex, uuid_text

_MAX_IN_BATCH = 400
_CHUNK_SPAN = 256
_CHUNK_SHIFT = 8  # 1 << 8 == 256
_TS_FRAC_RE = re.compile(
    r'^(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2}:\d{2})(?:\.(\d+))?(Z|[+-]\d{2}:\d{2})?$'
)
_FAST_SQLITE_TS_MATCH = re.compile(
    r'^(\d{4}-\d{2}-\d{2})[T ](\d{2}):(\d{2}):(\d{2})\.(\d{6})$'
).match



def _tzinfo_for_name(tz_name: str) -> tzinfo:
    if tz_name:
        try:
            return ZoneInfo(tz_name)
        except (ZoneInfoNotFoundError, ValueError):
            pass
    return datetime.now().astimezone().tzinfo or timezone.utc


@lru_cache(maxsize=4096)
def _cached_calendar_date(raw_val: str):
    return _calendar_date(raw_val)


@lru_cache(maxsize=4096)
def _ymd_epoch_seconds(date_part: str):
    try:
        y = int(date_part[0:4])
        m = int(date_part[5:7])
        d = int(date_part[8:10])
        date(y, m, d)
        return calendar.timegm((y, m, d, 0, 0, 0))
    except ValueError:
        return None


@lru_cache(maxsize=4096)
def _day_index_to_ymd(day_index: int) -> str:
    return date.fromordinal(day_index + 719163).isoformat()


@lru_cache(maxsize=4096)
def _utc_day_constant_offset(date_part: str, tz_info):
    """Return constant UTC offset seconds for ``date_part`` in ``tz_info``, or ``None`` on DST transition days."""
    try:
        y = int(date_part[0:4])
        m = int(date_part[5:7])
        d = int(date_part[8:10])
        o0 = datetime(y, m, d, 0, 0, 0, tzinfo=timezone.utc).astimezone(tz_info).utcoffset()
        o1 = datetime(y, m, d, 23, 59, 59, tzinfo=timezone.utc).astimezone(tz_info).utcoffset()
        if o0 is not None and o0 == o1:
            return int(o0.total_seconds())
    except (ValueError, OverflowError):
        pass
    return None


def normalize_utc_ns(raw_value) -> tuple[str, int, bool]:
    """Normalize an ISO-8601/SQLite timestamp to fixed-width 9-digit UTC nanoseconds.

    Returns ``(utc_ns_iso, epoch_ns, is_invalid)``.
    Empty or unparseable timestamps return ``('', 0, True)``.
    """
    if not isinstance(raw_value, str) or not raw_value:
        return '', 0, True
    text = raw_value if (raw_value[0] > ' ' and raw_value[-1] > ' ') else raw_value.strip()
    n = len(text)
    if n == 26:
        fm = _FAST_SQLITE_TS_MATCH(text)
        if fm is not None:
            date_part, hh_s, mm_s, ss_s, frac_part = fm.groups()
            day_sec = _ymd_epoch_seconds(date_part)
            if day_sec is None:
                return '', 0, True
            hh = int(hh_s)
            mm = int(mm_s)
            ss = int(ss_s)
            if hh > 23 or mm > 59 or ss > 59:
                return '', 0, True
            epoch_ns = (day_sec + hh * 3600 + mm * 60 + ss) * 1_000_000_000 + int(frac_part) * 1000
            return f'{date_part}T{hh_s}:{mm_s}:{ss_s}.{frac_part}000Z', epoch_ns, False
    if n < 19:
        return '', 0, True
    if (
        text[4] == '-'
        and text[7] == '-'
        and (text[10] == 'T' or text[10] == ' ')
        and text[13] == ':'
        and text[16] == ':'
        and '0' <= text[11] <= '2'
        and '0' <= text[12] <= '9'
        and '0' <= text[14] <= '5'
        and '0' <= text[15] <= '9'
        and '0' <= text[17] <= '5'
        and '0' <= text[18] <= '9'
    ):
        tail = text[19:]
        frac_part = None
        if n == 26 and text[19] == '.':
            cand = text[20:]
            if cand.isascii() and cand.isdigit():
                frac_part = cand
        elif not tail or tail == 'Z' or tail == '+00:00' or tail == '-00:00':
            frac_part = ''
        elif tail[0] == '.':
            if tail[-1] == 'Z':
                cand = tail[1:-1]
            elif tail.endswith('+00:00') or tail.endswith('-00:00'):
                cand = tail[1:-6]
            elif tail[1:].isdigit():
                cand = tail[1:]
            else:
                cand = None
            if cand and cand.isascii() and cand.isdigit():
                frac_part = cand
        if frac_part is not None:
            date_part = text[0:10]
            day_sec = _ymd_epoch_seconds(date_part)
            if day_sec is None:
                return '', 0, True
            hh = int(text[11:13])
            if hh > 23:
                return '', 0, True
            mm = int(text[14:16])
            ss = int(text[17:19])
            epoch_sec = day_sec + hh * 3600 + mm * 60 + ss
            fl = len(frac_part)
            if fl == 0:
                return f'{date_part}T{text[11:19]}.000000000Z', epoch_sec * 1_000_000_000, False
            if fl == 6:
                frac_ns = int(frac_part) * 1000
                return f'{date_part}T{text[11:19]}.{frac_part}000Z', epoch_sec * 1_000_000_000 + frac_ns, False
            if fl == 9:
                frac_ns = int(frac_part)
                return f'{date_part}T{text[11:19]}.{frac_part}Z', epoch_sec * 1_000_000_000 + frac_ns, False
            padded = (frac_part + '000000000')[:9]
            return f'{date_part}T{text[11:19]}.{padded}Z', epoch_sec * 1_000_000_000 + int(padded), False

    match = _TS_FRAC_RE.match(text)
    if match is None:
        return '', 0, True
    date_part, time_part, frac_part, offset_part = match.groups()
    frac_ns = int((frac_part or '').ljust(9, '0')[:9]) if frac_part else 0
    if offset_part in (None, 'Z', '+00:00', '-00:00'):
        day_sec = _ymd_epoch_seconds(date_part)
        if day_sec is None:
            return '', 0, True
        hh = int(time_part[0:2])
        mm = int(time_part[3:5])
        ss = int(time_part[6:8])
        if hh > 23 or mm > 59 or ss > 59:
            return '', 0, True
        epoch_sec = day_sec + hh * 3600 + mm * 60 + ss
        epoch_ns = epoch_sec * 1_000_000_000 + frac_ns
        return f'{date_part}T{time_part}.{frac_ns:09d}Z', epoch_ns, False
    try:
        base_dt = datetime.fromisoformat(f'{date_part}T{time_part}{offset_part}').astimezone(timezone.utc)
    except ValueError:
        return '', 0, True
    epoch_sec = int(base_dt.timestamp())
    epoch_ns = epoch_sec * 1_000_000_000 + frac_ns
    utc_iso = base_dt.strftime('%Y-%m-%dT%H:%M:%S') + f'.{frac_ns:09d}Z'
    return utc_iso, epoch_ns, False


def _local_day_from_utc_ns(utc_ns_iso: str, tz_info, epoch_ns: int | None = None) -> str:
    if not utc_ns_iso:
        return ''
    if tz_info is not None and len(utc_ns_iso) >= 19:
        date_part = utc_ns_iso[:10]
        off = _utc_day_constant_offset(date_part, tz_info)
        if off is not None:
            if epoch_ns is not None:
                local_sec = (epoch_ns // 1_000_000_000) + off
            else:
                day_sec = _ymd_epoch_seconds(date_part)
                if day_sec is None:
                    return ''
                local_sec = (
                    day_sec
                    + int(utc_ns_iso[11:13]) * 3600
                    + int(utc_ns_iso[14:16]) * 60
                    + int(utc_ns_iso[17:19])
                    + off
                )
            return _day_index_to_ymd(local_sec // 86400)
    try:
        dt = datetime.fromisoformat(utc_ns_iso[:19] + '+00:00').astimezone(tz_info)
        return dt.strftime('%Y-%m-%d')
    except ValueError:
        return ''


def day_to_desc_key(effective_date: str) -> int:
    """Convert ``YYYY-MM-DD`` to same-direction ASC key ``-YYYYMMDD`` (missing -> 0)."""
    day = _calendar_date(effective_date)
    if not day:
        return 0
    return -int(day.replace('-', ''))


def _file_stat_sig(path: Path) -> tuple[tuple[int, int, int], tuple[int, int, int]]:
    try:
        st_main = path.stat()
        main_sig = (st_main.st_ino, st_main.st_mtime_ns, st_main.st_size)
    except OSError:
        main_sig = (0, 0, 0)
    try:
        st_wal = Path(str(path) + '-wal').stat()
        # A 0-byte -wal file contains zero frames (created even by read-only WAL opens).
        # Normalizing size==0 to (0, 0, 0) prevents spurious resyncs after cold RO opens
        # while still detecting any frame write (size>0) or checkpoint truncation (main_sig changes).
        wal_sig = (st_wal.st_ino, st_wal.st_mtime_ns, st_wal.st_size) if st_wal.st_size > 0 else (0, 0, 0)
    except OSError:
        wal_sig = (0, 0, 0)
    return main_sig, wal_sig


def _iso_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec='microseconds')




def _fold_chunk_digests(chunk_map: dict[int, bytes]) -> str:
    h = hashlib.blake2b(digest_size=16)
    for bkt in sorted(chunk_map):
        h.update(f'{bkt}:'.encode('ascii'))
        h.update(chunk_map[bkt])
        h.update(b'\n')
    return h.hexdigest()


DatasetFingerprint = tuple[int, int, int, str, str]
FileStatSignature = tuple[tuple[int, int, int], tuple[int, int, int]]


class _CatalogSyncOwner(Protocol):
    """Catalog operations and publication state accessed while the writer owns the run."""

    provenance_path: Path | None
    recall_path: Path | None
    tz_provider: Callable[[], str]
    _write_con: sqlite3.Connection | None
    _state_lock: ContextManager
    _chunk_digests: dict[str, dict[int, bytes]]
    _prov_sync_incomplete: bool
    _recall_sync_incomplete: bool
    _last_prov_stat: FileStatSignature | None
    _last_recall_stat: FileStatSignature | None
    _last_synced_cog_sig: FileStatSignature | None
    _last_synced_pin_ts_ns_pending: int
    _last_sync_doc_count_pending: int
    _sessions_generation: int
    _recent_appends: dict[int, tuple[str, list[tuple[str, str, int, int]]]]
    _cat_counts_cache: dict[tuple[int, str], dict[str, int]]
    _common_cat_counts_cache: dict[tuple[int, str], dict[str, int]]
    _pending_raw_cache: dict[tuple[int, str, int], tuple[list[dict], int]]
    _children_preview_cache: dict[tuple[int, str, str, str, int], tuple[list[dict], bool]]

    def _get_writer_ro(self, db_path: Path) -> sqlite3.Connection: ...
    def _load_provenance_maps(self, allowed_datasets: list[str]) -> tuple[dict, dict, int, bool]: ...
    def _load_provenance_delta(self, after_seq: int, allowed_set: set[str]) -> tuple[list, int, bool, bool]: ...
    def _load_recall_counts(self, allowed_datasets: list[str]) -> tuple[dict, int, bool]: ...
    def _load_recall_delta(self, after_rev: int, allowed_set: set[str]) -> tuple[list, int, bool, bool]: ...
    def _scan_dataset_chunks(self, *args, **kwargs) -> tuple[dict[int, bytes], int, int, int, str] | None: ...
    def _project_cognee_row(self, *args, **kwargs) -> tuple | None: ...
    def _rebuild_day_buckets_and_state_locked(self, *args) -> None: ...


@dataclass
class SyncRunState:
    """Inputs, accumulated changes, and watermarks for a single sync."""

    allowed: list[str]
    allowed_set: set[str]
    ds_hex_to_uuid: dict[str, str]
    sync_pin_ts_ns: int
    tz_name: str
    tz_info: tzinfo
    cur_gen: int
    last_prov_seq: int
    last_recall_rev: int
    initial_doc_seq: int
    preserved_seqs: dict[tuple[str, str], int]
    next_doc_seq: int
    tz_changed: bool
    existing_states: dict[str, DatasetFingerprint]
    live_fingerprints: dict[str, DatasetFingerprint]
    prov_stat_now: FileStatSignature | None
    recall_stat_now: FileStatSignature | None
    is_cold_build: bool
    is_temp_rebuild: bool
    stream_cold_bulk: bool
    stream_cold_sessions: bool
    removed_datasets: list[str]
    changed_datasets: list[str] = field(default_factory=list)
    changed_buckets_by_ds: dict[str, list[int]] = field(default_factory=dict)
    removed_buckets_by_ds: dict[str, list[int]] = field(default_factory=dict)
    new_chunk_digests: dict[str, dict[int, bytes]] = field(default_factory=dict)
    dataset_diffs: dict[str, tuple[list[tuple], list[str]]] = field(default_factory=dict)
    incremental_bucket_deltas: dict[str, tuple[dict[str, int], int, int, int]] = field(default_factory=dict)
    cold_bucket_summaries: dict[str, tuple[dict[str, int], int, int, int, dict[str, int]]] = field(default_factory=dict)
    proven_pure_append_ds: set[str] = field(default_factory=set)
    doc_prov: dict[tuple[str, str], tuple[str, str, str]] = field(default_factory=dict)
    key_prov: dict[tuple[str, str, str], str] = field(default_factory=dict)
    recall_counts: dict[tuple[str, str], int] = field(default_factory=dict)
    prov_changes: list[tuple[str, str, str, str, str]] = field(default_factory=list)
    recall_changes: list[tuple[str, str, int]] = field(default_factory=list)
    new_prov_seq: int = 0
    new_recall_rev: int = 0
    need_full_prov: bool = False
    need_full_recall: bool = False
    loaded_full_prov_for_changed: bool = False
    loaded_full_recall_for_changed: bool = False
    prov_ok: bool = True
    recall_ok: bool = True

    def with_doc_seq(self, ds_id: str, proj26: tuple, existing_seq: int | None = None) -> tuple:
        if existing_seq is not None and existing_seq > 0:
            if existing_seq > self.next_doc_seq:
                self.next_doc_seq = existing_seq
            return proj26 + (existing_seq,)
        pseq = self.preserved_seqs.get((ds_id, proj26[1]))
        if pseq is not None and pseq > 0:
            if pseq > self.next_doc_seq:
                self.next_doc_seq = pseq
            return proj26 + (pseq,)
        self.next_doc_seq += 1
        return proj26 + (self.next_doc_seq,)


def init_sync_state(
    index: _CatalogSyncOwner, con: sqlite3.Connection, managed_datasets: list[str], *, force_full: bool,
    preserved_doc_seqs: dict[tuple[str, str], int] | None, min_last_doc_seq: int,
    min_generation: int,
) -> SyncRunState:
    allowed = sorted({uuid_text(ds) for ds in managed_datasets if ds})
    allowed_set = set(allowed)
    ds_hex_to_uuid = {}
    for ds in allowed:
        h = uuid_hex(ds)
        if h:
            ds_hex_to_uuid[h] = ds
        ds_hex_to_uuid[ds] = ds

    sync_pin_ts_ns = time.monotonic_ns()
    tz_name = index.tz_provider()
    tz_info = _tzinfo_for_name(tz_name)
    meta_row = con.execute(
        'SELECT generation, local_tz_name, last_provenance_seq, last_recall_rev, last_doc_seq FROM catalog_meta WHERE singleton=1'
    ).fetchone()
    cur_gen = max(int(meta_row[0]) if meta_row else 0, int(min_generation))
    old_tz = str(meta_row[1]) if meta_row else tz_name
    last_prov_seq = 0 if force_full else (int(meta_row[2]) if meta_row else 0)
    last_recall_rev = 0 if force_full else (int(meta_row[3]) if meta_row else 0)
    initial_doc_seq = max(int(meta_row[4]) if meta_row else 0, int(min_last_doc_seq))
    preserved_seqs = dict(preserved_doc_seqs) if preserved_doc_seqs else {}
    if preserved_seqs:
        max_pseq = max(preserved_seqs.values())
        if max_pseq > initial_doc_seq:
            initial_doc_seq = max_pseq
    next_doc_seq = initial_doc_seq
    tz_changed = (old_tz != tz_name)

    existing_states: dict[str, tuple[int, int, int, str, str]] = {}
    all_existing_ds: list[str] = []
    for row in con.execute(
        'SELECT dataset_id, doc_count, max_rowid, sum_rowid, max_updated_at, data_hash FROM dataset_sync_state'
    ).fetchall():
        ds_k = str(row[0])
        all_existing_ds.append(ds_k)
        if not force_full:
            existing_states[ds_k] = (
                int(row[1]),
                int(row[2]),
                int(row[3]),
                str(row[4] or ''),
                str(row[5] or ''),
            )

    is_temp_rebuild = (con is not index._write_con)
    if force_full and not is_temp_rebuild and not preserved_seqs:
        for ds_s, doc_s, seq_s in con.execute('SELECT dataset_id, doc_id, doc_seq FROM catalog_docs').fetchall():
            si = int(seq_s or 0)
            if si > 0:
                preserved_seqs[(str(ds_s), str(doc_s))] = si
                if si > next_doc_seq:
                    next_doc_seq = si

    live_fingerprints: dict[str, tuple[int, int, int, str, str]] = {ds: (0, 0, 0, '', '') for ds in allowed}
    prov_stat_now = _file_stat_sig(index.provenance_path) if index.provenance_path else None
    recall_stat_now = _file_stat_sig(index.recall_path) if index.recall_path else None
    is_cold_build = force_full or not existing_states
    # Bulk journal_mode=OFF + deferred index build is ONLY permitted on private temp rebuild files!
    stream_cold_bulk = bool(
        is_temp_rebuild and is_cold_build and con.execute('SELECT 1 FROM catalog_docs LIMIT 1').fetchone() is None
    )
    stream_cold_sessions = bool(
        stream_cold_bulk and con.execute('SELECT 1 FROM catalog_sessions LIMIT 1').fetchone() is None
    )
    removed_datasets = [ds for ds in all_existing_ds if ds not in allowed_set]
    return SyncRunState(
        allowed=allowed,
        allowed_set=allowed_set,
        ds_hex_to_uuid=ds_hex_to_uuid,
        sync_pin_ts_ns=sync_pin_ts_ns,
        tz_name=tz_name,
        tz_info=tz_info,
        cur_gen=cur_gen,
        last_prov_seq=last_prov_seq,
        last_recall_rev=last_recall_rev,
        initial_doc_seq=initial_doc_seq,
        preserved_seqs=preserved_seqs,
        next_doc_seq=next_doc_seq,
        tz_changed=tz_changed,
        existing_states=existing_states,
        live_fingerprints=live_fingerprints,
        prov_stat_now=prov_stat_now,
        recall_stat_now=recall_stat_now,
        is_cold_build=is_cold_build,
        is_temp_rebuild=is_temp_rebuild,
        stream_cold_bulk=stream_cold_bulk,
        stream_cold_sessions=stream_cold_sessions,
        removed_datasets=removed_datasets,
    )


def build_cognee_data_columns(cog_con: sqlite3.Connection) -> tuple[dict[str, str], str]:
    data_cols = {str(r[1]) for r in cog_con.execute('PRAGMA table_info(data)').fetchall()}
    col_exprs = {
        'id': 'id' if 'id' in data_cols else 'NULL',
        'name': 'name' if 'name' in data_cols else 'NULL',
        'label': 'label' if 'label' in data_cols else 'NULL',
        'raw_data_location': 'raw_data_location' if 'raw_data_location' in data_cols else 'NULL',
        'mime_type': 'mime_type' if 'mime_type' in data_cols else 'NULL',
        'system_metadata': 'system_metadata' if 'system_metadata' in data_cols else 'NULL',
        'external_metadata': 'external_metadata' if 'external_metadata' in data_cols else 'NULL',
        'node_set': 'node_set' if 'node_set' in data_cols else 'NULL',
        'pipeline_status': 'pipeline_status' if 'pipeline_status' in data_cols else 'NULL',
        'updated_at': 'updated_at' if 'updated_at' in data_cols else 'NULL',
        'created_at': 'created_at' if 'created_at' in data_cols else 'NULL',
        'content_hash': 'content_hash' if 'content_hash' in data_cols else 'NULL',
    }
    select_full_cols = (
        f"{col_exprs['id']}, rowid, {col_exprs['name']}, {col_exprs['label']}, "
        f"{col_exprs['raw_data_location']}, {col_exprs['system_metadata']}, "
        f"{col_exprs['external_metadata']}, {col_exprs['node_set']}, {col_exprs['pipeline_status']}, "
        f"{col_exprs['updated_at']}, {col_exprs['created_at']}, {col_exprs['content_hash']}, "
        f"{col_exprs['mime_type']}"
    )
    return col_exprs, select_full_cols


def scan_changed_datasets(
    index: _CatalogSyncOwner, cog_con: sqlite3.Connection, cognee_db: Path, state: SyncRunState,
    col_exprs: dict[str, str], cog_snap_sig: FileStatSignature, *, background: bool,
) -> FileStatSignature:
    allowed = state.allowed
    existing_states = state.existing_states
    is_cold_build = state.is_cold_build

    # Background catch-up hashes on the pinned writer connection to leave CPU for readers.
    # Foreground scans can use the hash pool and retry if its WAL read marks diverge.
    if is_cold_build:
        state.changed_datasets = list(allowed)
        return cog_snap_sig

    for _snap_attempt in range(3):
        changed_datasets = []
        changed_buckets_by_ds = {}
        removed_buckets_by_ds = {}
        new_chunk_digests = {}
        live_fingerprints = {ds: (0, 0, 0, '', '') for ds in allowed}
        snap_stale = False
        g_min_row = cog_con.execute('SELECT rowid FROM data ORDER BY rowid ASC LIMIT 1').fetchone()
        g_max_row = cog_con.execute('SELECT rowid FROM data ORDER BY rowid DESC LIMIT 1').fetchone()
        global_min_rid = int(g_min_row[0] or 0) if g_min_row else 0
        global_max_rid = int(g_max_row[0] or 0) if g_max_row else 0

        for ds in allowed:
            prev_fp = existing_states.get(ds)
            ds_vars = list(dict.fromkeys(v for v in (uuid_hex(ds), ds) if v))
            if global_max_rid == 0:
                s1_min_rid = 0
                s1_max_rid = 0
            elif prev_fp is not None and prev_fp[0] > 2000:
                # O(log N) global PK bounds avoid scanning 60k covering-index entries + TEMP B-TREE!
                s1_min_rid = global_min_rid
                s1_max_rid = global_max_rid
            else:
                s1_min_rid = 0
                s1_max_rid = 0
                for v in ds_vars:
                    mm = cog_con.execute(
                        'SELECT MIN(rowid), MAX(rowid) FROM data WHERE dataset_id = ?',
                        (v,),
                    ).fetchone()
                    if mm and mm[0] is not None:
                        lo_v = int(mm[0])
                        hi_v = int(mm[1] or 0)
                        if lo_v > 0 and (s1_min_rid == 0 or lo_v < s1_min_rid):
                            s1_min_rid = lo_v
                        if hi_v > s1_max_rid:
                            s1_max_rid = hi_v

            if s1_max_rid == 0:
                live_fingerprints[ds] = (0, 0, 0, '', _fold_chunk_digests({}))
                new_chunk_digests[ds] = {}
                if prev_fp is None or prev_fp[0] != 0 or prev_fp[4] != live_fingerprints[ds][4]:
                    changed_datasets.append(ds)
                    old_b = index._chunk_digests.get(ds)
                    if old_b:
                        removed_buckets_by_ds[ds] = sorted(old_b.keys())
                continue

            if prev_fp is None:
                changed_datasets.append(ds)
                continue

            # Full-13-column cryptographic chunk digest scan (zero undetected mutations!)
            scan_res = index._scan_dataset_chunks(
                cog_con,
                cognee_db,
                ds_vars,
                col_exprs,
                None,
                s1_min_rid,
                s1_max_rid,
                None,
                cog_snap_sig=cog_snap_sig,
                est_cnt=prev_fp[0],
                allow_stale_signal=(_snap_attempt < 2),
                verify_global_max_rid=global_max_rid,
                max_workers=2 if background else 6,
            )
            if scan_res is None:
                snap_stale = True
                break
            chunk_map, c_cnt, c_max_rid, c_sum_rid, c_max_upd = scan_res
            data_hash = _fold_chunk_digests(chunk_map)
            live_fingerprints[ds] = (c_cnt, c_max_rid, c_sum_rid, c_max_upd, data_hash)
            new_chunk_digests[ds] = chunk_map

            if prev_fp == live_fingerprints[ds]:
                continue

            changed_datasets.append(ds)
            old_chunks = index._chunk_digests.get(ds)
            if old_chunks is not None:
                diff_bkts = [b for b, dig in chunk_map.items() if old_chunks.get(b) != dig]
                del_bkts = [b for b in old_chunks if b not in chunk_map]
                changed_buckets_by_ds[ds] = sorted(diff_bkts)
                removed_buckets_by_ds[ds] = sorted(del_bkts)

        if snap_stale:
            cog_con.execute('ROLLBACK')
            cog_snap_sig = _file_stat_sig(cognee_db)
            cog_con.execute('BEGIN DEFERRED')
            cog_con.execute('SELECT name FROM sqlite_master LIMIT 1').fetchone()
            continue
        break
    state.changed_datasets = changed_datasets
    state.changed_buckets_by_ds = changed_buckets_by_ds
    state.removed_buckets_by_ds = removed_buckets_by_ds
    state.new_chunk_digests = new_chunk_digests
    state.live_fingerprints = live_fingerprints
    return cog_snap_sig


def load_provenance_and_recall_inputs(index: _CatalogSyncOwner, state: SyncRunState) -> None:
    allowed = state.allowed
    allowed_set = state.allowed_set
    last_prov_seq = state.last_prov_seq
    last_recall_rev = state.last_recall_rev
    prov_stat_now = state.prov_stat_now
    recall_stat_now = state.recall_stat_now
    is_cold_build = state.is_cold_build

    # Load provenance & recall deltas using single-snapshot readers (never swallow failures!)
    if is_cold_build:
        prov_changes, new_prov_seq, need_full_prov = [], last_prov_seq, True
        doc_prov, key_prov, full_prov_seq, prov_ok = index._load_provenance_maps(allowed)
        if prov_ok and full_prov_seq > new_prov_seq:
            new_prov_seq = full_prov_seq
        recall_changes, new_recall_rev, need_full_recall = [], last_recall_rev, True
        recall_counts, full_recall_rev, recall_ok = index._load_recall_counts(allowed)
        if recall_ok and full_recall_rev > new_recall_rev:
            new_recall_rev = full_recall_rev
    else:
        if not index._prov_sync_incomplete and index._last_prov_stat is not None and prov_stat_now == index._last_prov_stat:
            prov_changes, new_prov_seq, prov_needs_full, prov_ok = [], last_prov_seq, False, True
        elif index._prov_sync_incomplete:
            prov_changes, new_prov_seq, prov_needs_full, prov_ok = [], last_prov_seq, True, True
        else:
            prov_changes, new_prov_seq, prov_needs_full, prov_ok = index._load_provenance_delta(
                last_prov_seq, allowed_set
            )
        need_full_prov = prov_needs_full and prov_ok
        if need_full_prov:
            doc_prov, key_prov, full_prov_seq, prov_ok = index._load_provenance_maps(allowed)
            if prov_ok and full_prov_seq > new_prov_seq:
                new_prov_seq = full_prov_seq
        else:
            doc_prov, key_prov = {}, {}

        if not index._recall_sync_incomplete and index._last_recall_stat is not None and recall_stat_now == index._last_recall_stat:
            recall_changes, new_recall_rev, recall_needs_full, recall_ok = [], last_recall_rev, False, True
        elif index._recall_sync_incomplete:
            recall_changes, new_recall_rev, recall_needs_full, recall_ok = [], last_recall_rev, True, True
        else:
            recall_changes, new_recall_rev, recall_needs_full, recall_ok = index._load_recall_delta(
                last_recall_rev, allowed_set
            )
        need_full_recall = recall_needs_full and recall_ok
        if need_full_recall:
            recall_counts, full_recall_rev, recall_ok = index._load_recall_counts(allowed)
            if recall_ok and full_recall_rev > new_recall_rev:
                new_recall_rev = full_recall_rev
        else:
            recall_counts = {}

    state.doc_prov = doc_prov
    state.key_prov = key_prov
    state.recall_counts = recall_counts
    state.prov_changes = prov_changes
    state.recall_changes = recall_changes
    state.new_prov_seq = new_prov_seq
    state.new_recall_rev = new_recall_rev
    state.need_full_prov = need_full_prov
    state.need_full_recall = need_full_recall
    state.loaded_full_prov_for_changed = need_full_prov
    state.loaded_full_recall_for_changed = need_full_recall
    state.prov_ok = prov_ok
    state.recall_ok = recall_ok


def ensure_changed_maps(
    index: _CatalogSyncOwner, state: SyncRunState, target_datasets: list[str],
    specific_doc_ids: list[str] | None = None,
    specific_lesson_keys: set[tuple[str, str, str]] | None = None,
) -> None:
    allowed = state.allowed
    doc_prov = state.doc_prov
    key_prov = state.key_prov
    recall_counts = state.recall_counts

    if specific_doc_ids is not None and len(specific_doc_ids) <= 2048 and not state.loaded_full_prov_for_changed:
        if index.provenance_path and index.provenance_path.is_file() and not index.provenance_path.is_symlink():
            try:
                pcon = index._get_writer_ro(index.provenance_path)
                with ro_snapshot(pcon):
                    ph_ds = ','.join('?' for _ in target_datasets)
                    for start in range(0, len(specific_doc_ids), _MAX_IN_BATCH):
                        d_batch = specific_doc_ids[start : start + _MAX_IN_BATCH]
                        ph_d = ','.join('?' for _ in d_batch)
                        for r_ds, r_doc, r_lt, r_sk, r_fd in pcon.execute(
                            f'''SELECT dataset_id, document_id, lesson_type, state_key, first_source_date
                                FROM document_provenance
                                WHERE dataset_id IN ({ph_ds}) AND document_id IN ({ph_d})''',
                            (*target_datasets, *d_batch),
                        ):
                            lt_s = str(r_lt or '').strip()
                            sk_s = str(r_sk or '').strip()
                            doc_prov[(str(r_ds), str(r_doc))] = (
                                lt_s,
                                sk_s,
                                str(r_fd or '').strip(),
                            )
                            if specific_lesson_keys is not None and lt_s and sk_s:
                                specific_lesson_keys.add((str(r_ds), lt_s, sk_s))
                    if specific_lesson_keys is not None:
                        keys_by_ds: dict[str, set[str]] = {}
                        for k_ds, _k_lt, k_sk in specific_lesson_keys:
                            keys_by_ds.setdefault(k_ds, set()).add(k_sk)
                        for k_ds, sk_set in keys_by_ds.items():
                            sk_list = sorted(sk_set)
                            for start in range(0, len(sk_list), _MAX_IN_BATCH):
                                sk_batch = sk_list[start : start + _MAX_IN_BATCH]
                                ph_sk = ','.join('?' for _ in sk_batch)
                                for r_lt, r_sk, r_fd in pcon.execute(
                                    f'''SELECT lesson_type, state_key, first_source_date
                                        FROM keyed_provenance
                                        WHERE dataset_id = ? AND state_key IN ({ph_sk})''',
                                    (k_ds, *sk_batch),
                                ):
                                    if r_fd:
                                        key_prov[(k_ds, str(r_lt or '').strip(), str(r_sk or '').strip())] = str(r_fd).strip()
                    else:
                        for r_ds, r_lt, r_sk, r_fd in pcon.execute(
                            f'''SELECT dataset_id, lesson_type, state_key, first_source_date
                                FROM keyed_provenance
                                WHERE dataset_id IN ({ph_ds})''',
                            target_datasets,
                        ):
                            key_prov[(str(r_ds), str(r_lt or '').strip(), str(r_sk or '').strip())] = str(r_fd or '').strip()
            except (PolicyError, sqlite3.Error):
                state.prov_ok = False
    elif not state.loaded_full_prov_for_changed:
        dp, kp, fseq, ok = index._load_provenance_maps(allowed)
        if ok:
            doc_prov.update(dp)
            key_prov.update(kp)
            if fseq > state.new_prov_seq:
                state.new_prov_seq = fseq
            state.loaded_full_prov_for_changed = True
        else:
            state.prov_ok = False

    if specific_doc_ids is not None and len(specific_doc_ids) <= 2048 and not state.loaded_full_recall_for_changed:
        if index.recall_path and index.recall_path.is_file() and not index.recall_path.is_symlink():
            try:
                rcon = index._get_writer_ro(index.recall_path)
                with ro_snapshot(rcon):
                    has_mat = rcon.execute(
                        "SELECT 1 FROM sqlite_master WHERE type='table' AND name='recall_doc_counts' LIMIT 1"
                    ).fetchone()
                    if has_mat:
                        ph_ds = ','.join('?' for _ in target_datasets)
                        for start in range(0, len(specific_doc_ids), _MAX_IN_BATCH):
                            d_batch = specific_doc_ids[start : start + _MAX_IN_BATCH]
                            ph_d = ','.join('?' for _ in d_batch)
                            for r_ds, r_doc, r_hits in rcon.execute(
                                f'''SELECT dataset_id, document_id, total_hits
                                    FROM recall_doc_counts
                                    WHERE dataset_id IN ({ph_ds}) AND document_id IN ({ph_d})''',
                                (*target_datasets, *d_batch),
                            ):
                                recall_counts[(str(r_ds), str(r_doc))] = int(r_hits or 0)
            except (PolicyError, sqlite3.Error):
                state.recall_ok = False
    elif not state.loaded_full_recall_for_changed:
        rc, frev, ok = index._load_recall_counts(allowed)
        if ok:
            recall_counts.update(rc)
            if frev > state.new_recall_rev:
                state.new_recall_rev = frev
            state.loaded_full_recall_for_changed = True
        else:
            state.recall_ok = False


def reconcile_cold_bulk(
    index: _CatalogSyncOwner, con: sqlite3.Connection, cog_con: sqlite3.Connection, cognee_db: Path,
    state: SyncRunState, col_exprs: dict[str, str], select_full_cols: str, *, background: bool,
    catalog_index_names: tuple[str, ...], session_index_names: tuple[str, ...],
    bulk_insert_docs: Callable[[sqlite3.Connection, list[tuple]], None],
) -> tuple[bool, bool]:
    """Stream an empty private rebuild after the caller sets temporary PRAGMAs."""
    ds_hex_to_uuid = state.ds_hex_to_uuid
    tz_info = state.tz_info
    preserved_seqs = state.preserved_seqs
    live_fingerprints = state.live_fingerprints
    stream_cold_sessions = state.stream_cold_sessions
    changed_datasets = state.changed_datasets
    new_chunk_digests = state.new_chunk_digests
    dataset_diffs = state.dataset_diffs
    cold_bucket_summaries = state.cold_bucket_summaries
    doc_prov = state.doc_prov
    key_prov = state.key_prov
    recall_counts = state.recall_counts

    for idx_name in catalog_index_names:
        con.execute(f'DROP INDEX IF EXISTS {idx_name}')
    if stream_cold_sessions:
        for sidx_name in session_index_names:
            con.execute(f'DROP INDEX IF EXISTS {sidx_name}')

    cold_stats: dict[str, list] = {
        ds: [0, 0, 0, 0, '', {}, {}, 0, 0, 0]
        for ds in changed_datasets
    }
    upserts: list[tuple] = []
    query_variants = sorted(ds_hex_to_uuid)
    has_preserved_seqs = bool(preserved_seqs)
    if query_variants and len(query_variants) <= _MAX_IN_BATCH:
        ph_all = ','.join('?' for _ in query_variants)
        cold_cursor = cog_con.execute(
            f'SELECT dataset_id, {select_full_cols} FROM data WHERE dataset_id IN ({ph_all})',
            query_variants,
        )
    else:
        cold_cursor = cog_con.execute(f'SELECT dataset_id, {select_full_cols} FROM data')
    project_row = index._project_cognee_row
    next_doc_seq = state.next_doc_seq
    preserved_seq_for = preserved_seqs.get
    for row_with_ds in cold_cursor:
        canon_ds = ds_hex_to_uuid.get(str(row_with_ds[0]))
        if canon_ds is None:
            continue
        st = cold_stats.get(canon_ds)
        if st is None:
            continue
        full_row = row_with_ds[1:]
        rid_val = int(full_row[1] or 0)
        st[0] += 1
        if st[1] == 0 or (rid_val > 0 and rid_val < st[1]):
            st[1] = rid_val
        if rid_val > st[2]:
            st[2] = rid_val
        st[3] += rid_val
        upd_raw = full_row[9]
        if isinstance(upd_raw, str) and upd_raw and '0' <= upd_raw[0] <= '9' and upd_raw > st[4]:
            st[4] = upd_raw

        projected = project_row(
            full_row,
            canon_ds,
            doc_prov=doc_prov,
            key_prov=key_prov,
            recall_counts=recall_counts,
            tz_info=tz_info,
        )
        if projected is not None:
            if has_preserved_seqs:
                doc_seq = preserved_seq_for((canon_ds, projected[1]))
                if doc_seq is not None and doc_seq > 0:
                    if doc_seq > next_doc_seq:
                        next_doc_seq = doc_seq
                else:
                    next_doc_seq += 1
                    doc_seq = next_doc_seq
                upserts.append(projected + (doc_seq,))
            else:
                next_doc_seq += 1
                upserts.append(projected + (next_doc_seq,))
            p_kind = projected[3]
            if projected[4]:
                st[7] += 1
                p_day = projected[18]
                if p_day:
                    c_days = st[5]
                    c_days[p_day] = c_days.get(p_day, 0) + 1
                p_cat = projected[8]
                if p_cat:
                    c_cats = st[6]
                    c_cats[p_cat] = c_cats.get(p_cat, 0) + 1
            elif p_kind == 'raw_doc':
                st[8] += 1
            if p_kind == 'common':
                st[9] += 1
            if len(upserts) >= 2000:
                bulk_insert_docs(con, upserts)
                upserts.clear()
    state.next_doc_seq = next_doc_seq
    if upserts:
        bulk_insert_docs(con, upserts)
        upserts.clear()

    for ds in changed_datasets:
        (
            c_cnt, c_min_rid, c_max_rid, c_sum_rid, c_max_upd,
            cold_days, cold_cats, cold_k, cold_raw, cold_com,
        ) = cold_stats[ds]
        ds_vars = list(dict.fromkeys(v for v in (uuid_hex(ds), ds) if v))
        bkt_digests, _, _, _, _ = index._scan_dataset_chunks(
            cog_con, cognee_db, ds_vars, col_exprs, c_cnt, c_min_rid, c_max_rid, c_sum_rid,
            max_workers=2 if background else 6,
        )
        data_hash = _fold_chunk_digests(bkt_digests)
        live_fingerprints[ds] = (c_cnt, c_max_rid, c_sum_rid, c_max_upd, data_hash)
        new_chunk_digests[ds] = bkt_digests
        cold_bucket_summaries[ds] = (cold_days, cold_k, cold_raw, cold_com, cold_cats)
        dataset_diffs[ds] = ([], [])
    return con.in_transaction, stream_cold_sessions and con.in_transaction


def reconcile_dataset_incremental_buckets(
    index: _CatalogSyncOwner, con: sqlite3.Connection, cog_con: sqlite3.Connection, state: SyncRunState,
    ds: str, ds_vars: list[str], ds_filter: str, select_full_cols: str,
) -> None:
    changed_buckets_by_ds = state.changed_buckets_by_ds
    removed_buckets_by_ds = state.removed_buckets_by_ds
    prov_changes = state.prov_changes
    recall_changes = state.recall_changes
    need_full_prov = state.need_full_prov
    need_full_recall = state.need_full_recall

    diff_bkts = changed_buckets_by_ds[ds]
    del_bkts = removed_buckets_by_ds.get(ds, [])
    all_touched_bkts = sorted(set(diff_bkts) | set(del_bkts))
    if not all_touched_bkts:
        return

    # Fetch ONLY live rows inside the changed buckets (up to 8 buckets)
    live_rows_in_bkts: list[tuple] = []
    for bkt in diff_bkts:
        lo = bkt << _CHUNK_SHIFT
        hi = lo + _CHUNK_SPAN - 1
        live_rows_in_bkts.extend(
            cog_con.execute(
                f'SELECT {select_full_cols} FROM data WHERE rowid BETWEEN ? AND ? AND {ds_filter} ORDER BY rowid ASC',
                (lo, hi, *ds_vars),
            ).fetchall()
        )

    # Fetch existing catalog rows inside ALL touched buckets (for exact diff + bucket deltas)
    existing_in_bkts: dict[str, tuple] = {}
    existing_seqs_in_bkts: dict[str, int] = {}
    for bkt in all_touched_bkts:
        lo = bkt << _CHUNK_SHIFT
        hi = lo + _CHUNK_SPAN - 1
        for crow in con.execute(
            '''SELECT dataset_id, doc_id, cognee_rowid, kind, is_knowledge,
                      common_category, lesson_type, state_key, category, session_id,
                      doc_name, doc_label, raw_data_location, content_hash,
                      created_at_raw, created_at_ns, created_at_invalid, updated_at_ns,
                      effective_date, effective_date_source, day_desc_key, sort_ts_desc_key,
                      recall_count, recall_desc_key, pipeline_status, source_updated_at, doc_seq
               FROM catalog_docs
               WHERE dataset_id = ? AND cognee_rowid BETWEEN ? AND ?''',
            (ds, lo, hi),
        ).fetchall():
            did_k = str(crow[1])
            existing_in_bkts[did_k] = crow[:26]
            existing_seqs_in_bkts[did_k] = int(crow[26] or 0)

    query_doc_ids = []
    specific_keys: set[tuple[str, str, str]] = set()
    fast_existing_maps = (
        not prov_changes
        and not recall_changes
        and not need_full_prov
        and not need_full_recall
    )
    for r in live_rows_in_bkts:
        did_c = uuid_text(r[0])
        if not did_c:
            continue
        old_c = existing_in_bkts.get(did_c)
        if old_c is None:
            query_doc_ids.append(did_c)
            m_c = extract_trusted_metadata(r[5], r[6], r[7])
            lt_c = str(m_c.get('lesson_type') or '').strip()
            sk_c = str(m_c.get('state_key') or '').strip()
            if lt_c and sk_c:
                specific_keys.add((ds, lt_c, sk_c))
        elif not fast_existing_maps:
            query_doc_ids.append(did_c)
            if old_c[6] and old_c[7]:
                specific_keys.add((ds, str(old_c[6]), str(old_c[7])))
    if query_doc_ids:
        ensure_changed_maps(
            index, state, [ds],
            specific_doc_ids=query_doc_ids if len(query_doc_ids) <= 2048 else None,
            specific_lesson_keys=specific_keys if len(query_doc_ids) <= 2048 else None,
        )
    _reconcile_bucket_rows(
        index, con, state, ds, live_rows_in_bkts, existing_in_bkts,
        existing_seqs_in_bkts, fast_existing_maps,
    )


def _reconcile_bucket_rows(
    index: _CatalogSyncOwner, con: sqlite3.Connection, state: SyncRunState, ds: str, live_rows_in_bkts: list[tuple],
    existing_in_bkts: dict[str, tuple], existing_seqs_in_bkts: dict[str, int],
    fast_existing_maps: bool,
) -> None:
    tz_info = state.tz_info
    existing_states = state.existing_states
    dataset_diffs = state.dataset_diffs
    incremental_bucket_deltas = state.incremental_bucket_deltas
    proven_pure_append_ds = state.proven_pure_append_ds
    doc_prov = state.doc_prov
    key_prov = state.key_prov
    recall_counts = state.recall_counts
    with_doc_seq = state.with_doc_seq

    upserts = []
    deleted_ids = []
    seen_live_dids: set[str] = set()
    day_delta: dict[str, int] = {}
    dk = dr = dc = 0
    all_strictly_new_appends = True
    prev_fp = existing_states.get(ds)
    old_max_rid = prev_fp[1] if prev_fp is not None else 0

    for full_row in live_rows_in_bkts:
        did = uuid_text(full_row[0])
        if not did:
            continue
        seen_live_dids.add(did)
        old_row = existing_in_bkts.get(did)
        projected = index._project_cognee_row(
            full_row,
            ds,
            doc_prov=doc_prov,
            key_prov=key_prov,
            recall_counts=recall_counts,
            tz_info=tz_info,
        )
        if old_row is not None and fast_existing_maps and projected is not None:
            if projected[6] != old_row[6] or projected[7] != old_row[7]:
                extra_keys = (
                    {(ds, str(projected[6]), str(projected[7]))}
                    if projected[6] and projected[7]
                    else set()
                )
                ensure_changed_maps(
                    index, state, [ds],
                    specific_doc_ids=[did],
                    specific_lesson_keys=extra_keys,
                )
                projected = index._project_cognee_row(
                    full_row,
                    ds,
                    doc_prov=doc_prov,
                    key_prov=key_prov,
                    recall_counts=recall_counts,
                    tz_info=tz_info,
                )
            elif old_row[19] == 'provenance' and projected[19] != 'provenance':
                projected = (
                    projected[:18]
                    + (old_row[18], 'provenance', old_row[20])
                    + projected[21:]
                )
        if old_row is None:
            if projected is None:
                continue
            # Check if this doc already exists in another bucket (e.g. rowid moved!)
            if int(full_row[1] or 0) <= old_max_rid:
                all_strictly_new_appends = False
            moved_old = con.execute(
                'SELECT kind, is_knowledge, effective_date, doc_seq FROM catalog_docs WHERE dataset_id = ? AND doc_id = ?',
                (ds, did),
            ).fetchone()
            moved_seq = None
            if moved_old is not None:
                all_strictly_new_appends = False
                moved_seq = int(moved_old[3] or 0) or None
                if int(moved_old[1]):
                    dk -= 1
                    if moved_old[2]:
                        day_delta[str(moved_old[2])] = day_delta.get(str(moved_old[2]), 0) - 1
                if moved_old[0] == 'raw_doc':
                    dr -= 1
                if moved_old[0] == 'common':
                    dc -= 1
            upserts.append(with_doc_seq(ds, projected, existing_seq=moved_seq))
            if projected[4]:
                dk += 1
                if projected[18]:
                    day_delta[projected[18]] = day_delta.get(projected[18], 0) + 1
            if projected[3] == 'raw_doc':
                dr += 1
            if projected[3] == 'common':
                dc += 1
        else:
            # Preserve existing recall_count if recall_counts map didn't load this doc
            if projected is not None and (ds, did) not in recall_counts and int(old_row[22]) > 0:
                rc_old = int(old_row[22])
                projected = projected[:22] + (rc_old, -rc_old) + projected[24:]
            if projected == old_row:
                continue
            all_strictly_new_appends = False
            # Subtract old row contribution
            if int(old_row[4]):
                dk -= 1
                if old_row[18]:
                    day_delta[str(old_row[18])] = day_delta.get(str(old_row[18]), 0) - 1
            if old_row[3] == 'raw_doc':
                dr -= 1
            if old_row[3] == 'common':
                dc -= 1
            if projected is not None:
                upserts.append(with_doc_seq(ds, projected, existing_seq=existing_seqs_in_bkts.get(did)))
                if projected[4]:
                    dk += 1
                    if projected[18]:
                        day_delta[projected[18]] = day_delta.get(projected[18], 0) + 1
                if projected[3] == 'raw_doc':
                    dr += 1
                if projected[3] == 'common':
                    dc += 1
            else:
                deleted_ids.append(did)

    for old_did, old_row in existing_in_bkts.items():
        if old_did not in seen_live_dids:
            all_strictly_new_appends = False
            deleted_ids.append(old_did)
            if int(old_row[4]):
                dk -= 1
                if old_row[18]:
                    day_delta[str(old_row[18])] = day_delta.get(str(old_row[18]), 0) - 1
            if old_row[3] == 'raw_doc':
                dr -= 1
            if old_row[3] == 'common':
                dc -= 1

    dataset_diffs[ds] = (upserts, deleted_ids)
    incremental_bucket_deltas[ds] = (day_delta, dk, dr, dc)
    if all_strictly_new_appends and upserts and not deleted_ids:
        proven_pure_append_ds.add(ds)


def reconcile_dataset_full(
    index: _CatalogSyncOwner, con: sqlite3.Connection, cog_con: sqlite3.Connection, cognee_db: Path, state: SyncRunState,
    ds: str, ds_vars: list[str], ds_filter: str, col_exprs: dict[str, str], select_full_cols: str, *,
    force_full: bool, background: bool,
) -> None:
    tz_info = state.tz_info
    existing_states = state.existing_states
    changed_datasets = state.changed_datasets
    dataset_diffs = state.dataset_diffs
    doc_prov = state.doc_prov
    key_prov = state.key_prov
    recall_counts = state.recall_counts
    with_doc_seq = state.with_doc_seq

    # Full dataset reconciliation (force_full on non-empty catalog, post-restart diff, or >8 changed chunks)
    ensure_changed_maps(index, state, changed_datasets)

    if force_full or ds not in existing_states:
        _reconcile_new_dataset(
            index, cog_con, cognee_db, state, ds, ds_vars,
            ds_filter, col_exprs, select_full_cols, background=background,
        )
        return

    # Full diff against existing catalog_docs (post-restart or bulk change)
    existing_rows: dict[str, tuple] = {}
    existing_row_seqs: dict[str, int] = {}
    for crow in con.execute(
        '''SELECT dataset_id, doc_id, cognee_rowid, kind, is_knowledge,
                  common_category, lesson_type, state_key, category, session_id,
                  doc_name, doc_label, raw_data_location, content_hash,
                  created_at_raw, created_at_ns, created_at_invalid, updated_at_ns,
                  effective_date, effective_date_source, day_desc_key, sort_ts_desc_key,
                  recall_count, recall_desc_key, pipeline_status, source_updated_at, doc_seq
           FROM catalog_docs WHERE dataset_id = ?''',
        (ds,),
    ):
        did_k = str(crow[1])
        existing_rows[did_k] = crow[:26]
        existing_row_seqs[did_k] = int(crow[26] or 0)

    upserts = []
    deleted_ids = []
    for full_row in cog_con.execute(
        f'SELECT {select_full_cols} FROM data WHERE {ds_filter} ORDER BY rowid ASC',
        ds_vars,
    ):
        did = uuid_text(full_row[0])
        if not did:
            continue
        old_row = existing_rows.pop(did, None)
        old_seq = existing_row_seqs.get(did)
        projected = index._project_cognee_row(
            full_row,
            ds,
            doc_prov=doc_prov,
            key_prov=key_prov,
            recall_counts=recall_counts,
            tz_info=tz_info,
        )
        if projected is not None:
            if old_row is None or projected != old_row:
                upserts.append(with_doc_seq(ds, projected, existing_seq=old_seq))
        elif old_row is not None:
            deleted_ids.append(did)
    deleted_ids.extend(existing_rows.keys())
    existing_rows.clear()
    dataset_diffs[ds] = (upserts, deleted_ids)


def _reconcile_new_dataset(
    index: _CatalogSyncOwner, cog_con: sqlite3.Connection, cognee_db: Path, state: SyncRunState, ds: str, ds_vars: list[str], ds_filter: str,
    col_exprs: dict[str, str], select_full_cols: str, *, background: bool,
) -> None:
    tz_info = state.tz_info
    live_fingerprints = state.live_fingerprints
    new_chunk_digests = state.new_chunk_digests
    dataset_diffs = state.dataset_diffs
    cold_bucket_summaries = state.cold_bucket_summaries
    doc_prov = state.doc_prov
    key_prov = state.key_prov
    recall_counts = state.recall_counts
    with_doc_seq = state.with_doc_seq

    upserts = []
    c_cnt = c_min_rid = c_max_rid = c_sum_rid = 0
    c_max_upd = ''
    cold_days: dict[str, int] = {}
    cold_cats: dict[str, int] = {}
    cold_k = cold_raw = cold_com = 0
    for full_row in cog_con.execute(
        f'SELECT {select_full_cols} FROM data WHERE {ds_filter} ORDER BY rowid ASC',
        ds_vars,
    ):
        rid_val = int(full_row[1] or 0)
        c_cnt += 1
        if c_min_rid == 0 or (rid_val > 0 and rid_val < c_min_rid):
            c_min_rid = rid_val
        if rid_val > c_max_rid:
            c_max_rid = rid_val
        c_sum_rid += rid_val
        upd_raw = full_row[9]
        if isinstance(upd_raw, str) and upd_raw and '0' <= upd_raw[0] <= '9' and upd_raw > c_max_upd:
            c_max_upd = upd_raw

        projected = index._project_cognee_row(
            full_row,
            ds,
            doc_prov=doc_prov,
            key_prov=key_prov,
            recall_counts=recall_counts,
            tz_info=tz_info,
        )
        if projected is not None:
            upserts.append(with_doc_seq(ds, projected))
            p_kind = projected[3]
            if projected[4]:
                cold_k += 1
                p_day = projected[18]
                if p_day:
                    cold_days[p_day] = cold_days.get(p_day, 0) + 1
                p_cat = projected[8]
                if p_cat:
                    cold_cats[p_cat] = cold_cats.get(p_cat, 0) + 1
            elif p_kind == 'raw_doc':
                cold_raw += 1
            if p_kind == 'common':
                cold_com += 1
    bkt_digests, _, _, _, _ = index._scan_dataset_chunks(
        cog_con, cognee_db, ds_vars, col_exprs, c_cnt, c_min_rid, c_max_rid, c_sum_rid,
        max_workers=2 if background else 6,
    )
    data_hash = _fold_chunk_digests(bkt_digests)
    live_fingerprints[ds] = (c_cnt, c_max_rid, c_sum_rid, c_max_upd, data_hash)
    new_chunk_digests[ds] = bkt_digests
    cold_bucket_summaries[ds] = (cold_days, cold_k, cold_raw, cold_com, cold_cats)
    dataset_diffs[ds] = (upserts, [])


def apply_doc_diffs_tx(
    con: sqlite3.Connection, state: SyncRunState, *, force_full: bool, use_bulk_index_build: bool,
    catalog_index_names: tuple[str, ...], catalog_index_ddl: str,
    bulk_insert_docs: Callable[[sqlite3.Connection, list[tuple]], None], upsert_doc_sql: str,
) -> bool:
    is_temp_rebuild = state.is_temp_rebuild
    stream_cold_bulk = state.stream_cold_bulk
    removed_datasets = state.removed_datasets
    dataset_diffs = state.dataset_diffs

    total_upserts = sum(len(u) for u, _ in dataset_diffs.values())
    if (
        is_temp_rebuild
        and not use_bulk_index_build
        and total_upserts >= 5000
        and con.execute('SELECT 1 FROM catalog_docs LIMIT 1').fetchone() is None
    ):
        use_bulk_index_build = True
        for idx_name in catalog_index_names:
            con.execute(f'DROP INDEX IF EXISTS {idx_name}')

    for ds in removed_datasets:
        con.execute('DELETE FROM catalog_docs WHERE dataset_id = ?', (ds,))
        con.execute('DELETE FROM catalog_day_buckets WHERE dataset_id = ?', (ds,))
        con.execute('DELETE FROM catalog_sessions WHERE dataset_id = ?', (ds,))
        con.execute('DELETE FROM dataset_sync_state WHERE dataset_id = ?', (ds,))

    for ds, (upserts, deleted_ids) in dataset_diffs.items():
        if force_full and not stream_cold_bulk:
            # Explicitly purge any stale/deleted source rows on force_full!
            con.execute('DELETE FROM catalog_docs WHERE dataset_id = ?', (ds,))
        elif deleted_ids:
            for start in range(0, len(deleted_ids), _MAX_IN_BATCH):
                batch = deleted_ids[start : start + _MAX_IN_BATCH]
                ph = ','.join('?' for _ in batch)
                con.execute(
                    f'DELETE FROM catalog_docs WHERE dataset_id = ? AND doc_id IN ({ph})',
                    (ds, *batch),
                )
        if upserts:
            if use_bulk_index_build or (force_full and not stream_cold_bulk):
                bulk_insert_docs(con, upserts)
            else:
                con.executemany(upsert_doc_sql, upserts)

    if use_bulk_index_build:
        for stmt in catalog_index_ddl.strip().split(';'):
            s = stmt.strip()
            if s:
                con.execute(s)
    return use_bulk_index_build


def apply_provenance_updates_tx(con: sqlite3.Connection, state: SyncRunState, dirty_datasets: set[str]) -> None:
    allowed = state.allowed
    incremental_bucket_deltas = state.incremental_bucket_deltas
    cold_bucket_summaries = state.cold_bucket_summaries
    proven_pure_append_ds = state.proven_pure_append_ds
    doc_prov = state.doc_prov
    key_prov = state.key_prov
    prov_changes = state.prov_changes
    need_full_prov = state.need_full_prov
    prov_ok = state.prov_ok

    # Apply full provenance recovery write-back (when provenance recovered from transient failure or lacks sync_seq)
    # AND incremental provenance date updates + sibling propagation across ALL affected datasets.
    prov_mutated_datasets: set[str] = set()
    if need_full_prov and prov_ok and (doc_prov or key_prov):
        # Streamed rows already use keyed provenance. Legacy unkeyed entries
        # need a second pass using the keys resolved from the catalog rows.
        legacy_prov_datasets = {ds for (ds, _), (lt, sk, _) in doc_prov.items() if not lt or not sk}
        for ds in allowed:
            if ds in cold_bucket_summaries and ds not in legacy_prov_datasets:
                continue
            ds_rows = con.execute(
                '''SELECT doc_id, lesson_type, state_key, effective_date, effective_date_source
                   FROM catalog_docs
                   WHERE dataset_id = ? AND is_knowledge = 1''',
                (ds,),
            ).fetchall()
            # Pass 1: aggregate earliest valid provenance date across every (lesson_type, state_key)
            # sibling group even when keyed_provenance is empty and only document_provenance exists.
            group_earliest: dict[tuple[str, str], str] = {}
            resolved_rows: list[tuple[str, str, str, str, str, tuple | None]] = []
            for r_doc_id, r_lt, r_sk, r_day, r_src in ds_rows:
                did_s = str(r_doc_id)
                lt_s = str(r_lt or '').strip()
                sk_s = str(r_sk or '').strip()
                prov_e = doc_prov.get((ds, did_s))
                if prov_e is not None:
                    if not lt_s and prov_e[0]:
                        lt_s = prov_e[0]
                    if not sk_s and prov_e[1]:
                        sk_s = prov_e[1]
                resolved_rows.append((did_s, lt_s, sk_s, str(r_day or ''), str(r_src or ''), prov_e))
                if lt_s and sk_s:
                    g_key = (lt_s, sk_s)
                    for raw_cand in (
                        prov_e[2] if prov_e is not None else '',
                        key_prov.get((ds, lt_s, sk_s), ''),
                    ):
                        if isinstance(raw_cand, str) and raw_cand:
                            cd = _cached_calendar_date(raw_cand)
                            if cd:
                                prev_g = group_earliest.get(g_key)
                                if not prev_g or cd < prev_g:
                                    group_earliest[g_key] = cd
            # Pass 2: propagate earliest provenance date to both directly linked rows and siblings.
            full_prov_updates = []
            for did_s, lt_s, sk_s, cur_day, cur_src, prov_e in resolved_rows:
                cand_dates = []
                if prov_e is not None and isinstance(prov_e[2], str):
                    cd = _cached_calendar_date(prov_e[2])
                    if cd:
                        cand_dates.append(cd)
                if lt_s and sk_s:
                    g_day = group_earliest.get((lt_s, sk_s))
                    if g_day:
                        cand_dates.append(g_day)
                if not cand_dates:
                    continue
                best_day = min(cand_dates)
                if cur_src != 'provenance' or not cur_day or best_day < cur_day:
                    full_prov_updates.append((best_day, day_to_desc_key(best_day), ds, did_s))
            if full_prov_updates:
                con.executemany(
                    '''UPDATE catalog_docs
                       SET effective_date = ?, effective_date_source = 'provenance', day_desc_key = ?
                       WHERE dataset_id = ? AND doc_id = ?''',
                    full_prov_updates,
                )
                dirty_datasets.add(ds)
                prov_mutated_datasets.add(ds)

    for ds, doc_id, lt, sk, fdate in prov_changes:
        valid_day = _calendar_date(fdate)
        if not valid_day:
            continue
        row = con.execute(
            'SELECT effective_date, effective_date_source, lesson_type, state_key FROM catalog_docs WHERE dataset_id = ? AND doc_id = ?',
            (ds, doc_id),
        ).fetchone()
        eff_lt = lt
        eff_sk = sk
        if row is not None:
            cur_day, cur_src = str(row[0] or ''), str(row[1] or '')
            if not eff_lt and row[2]:
                eff_lt = str(row[2]).strip()
            if not eff_sk and row[3]:
                eff_sk = str(row[3]).strip()
            if cur_src != 'provenance' or not cur_day or valid_day < cur_day:
                con.execute(
                    '''UPDATE catalog_docs
                       SET effective_date = ?, effective_date_source = 'provenance', day_desc_key = ?
                       WHERE dataset_id = ? AND doc_id = ?''',
                    (valid_day, day_to_desc_key(valid_day), ds, doc_id),
                )
                dirty_datasets.add(ds)
                prov_mutated_datasets.add(ds)
        if eff_lt and eff_sk:
            sib_updates = []
            for sib_id, s_day, s_src in con.execute(
                '''SELECT doc_id, effective_date, effective_date_source
                   FROM catalog_docs
                   WHERE dataset_id = ? AND is_knowledge = 1 AND lesson_type = ? AND state_key = ?''',
                (ds, eff_lt, eff_sk),
            ).fetchall():
                s_day_s = str(s_day or '')
                s_src_s = str(s_src or '')
                if s_src_s != 'provenance' or not s_day_s or valid_day < s_day_s:
                    sib_updates.append((valid_day, day_to_desc_key(valid_day), ds, str(sib_id)))
            if sib_updates:
                con.executemany(
                    '''UPDATE catalog_docs
                       SET effective_date = ?, effective_date_source = 'provenance', day_desc_key = ?
                       WHERE dataset_id = ? AND doc_id = ?''',
                    sib_updates,
                )
                dirty_datasets.add(ds)
                prov_mutated_datasets.add(ds)

    for pds in prov_mutated_datasets:
        incremental_bucket_deltas.pop(pds, None)
        cold_bucket_summaries.pop(pds, None)
        proven_pure_append_ds.discard(pds)


def apply_recall_and_tz_updates_tx(
    con: sqlite3.Connection, state: SyncRunState, dirty_datasets: set[str], *, force_full: bool,
) -> None:
    allowed = state.allowed
    tz_info = state.tz_info
    tz_changed = state.tz_changed
    incremental_bucket_deltas = state.incremental_bucket_deltas
    cold_bucket_summaries = state.cold_bucket_summaries
    proven_pure_append_ds = state.proven_pure_append_ds
    recall_counts = state.recall_counts
    recall_changes = state.recall_changes
    need_full_recall = state.need_full_recall
    recall_ok = state.recall_ok

    # Sync recall counts: O(delta) via recall_doc_counts.rev when possible, else full diff
    if need_full_recall and recall_ok:
        for ds in allowed:
            if ds in cold_bucket_summaries:
                continue
            current_recalls = {}
            for doc_id, rcnt in con.execute(
                'SELECT doc_id, recall_count FROM catalog_docs WHERE dataset_id = ? AND recall_count > 0',
                (ds,),
            ).fetchall():
                current_recalls[str(doc_id)] = int(rcnt)

            ds_live_recalls = {
                doc_id: cnt
                for (r_ds, doc_id), cnt in recall_counts.items()
                if r_ds == ds and cnt > 0
            }
            updates = []
            for doc_id, new_cnt in ds_live_recalls.items():
                if current_recalls.get(doc_id, 0) != new_cnt:
                    updates.append((new_cnt, -new_cnt, ds, doc_id))
            for doc_id in current_recalls:
                if doc_id not in ds_live_recalls:
                    updates.append((0, 0, ds, doc_id))
            if updates:
                con.executemany(
                    '''UPDATE catalog_docs
                       SET recall_count = ?, recall_desc_key = ?
                       WHERE dataset_id = ? AND doc_id = ?''',
                    updates,
                )
                dirty_datasets.add(ds)
    elif recall_changes and recall_ok:
        delta_updates = []
        for ds, doc_id, new_cnt in recall_changes:
            delta_updates.append((new_cnt, -new_cnt, ds, doc_id))
            dirty_datasets.add(ds)
        con.executemany(
            '''UPDATE catalog_docs
               SET recall_count = ?, recall_desc_key = ?
               WHERE dataset_id = ? AND doc_id = ?''',
            delta_updates,
        )

    # Timezone change: recompute effective_date ONLY for created_at-fallback docs (never overwrite provenance or meta!)
    if tz_changed and not force_full:
        for ds in allowed:
            tz_updates = []
            for doc_id, created_ns, cur_day in con.execute(
                '''SELECT doc_id, created_at_ns, effective_date
                   FROM catalog_docs
                   WHERE dataset_id = ? AND is_knowledge = 1
                     AND created_at_invalid = 0
                     AND effective_date_source = 'created_at' ''',
                (ds,),
            ).fetchall():
                recomputed = _local_day_from_utc_ns(str(created_ns), tz_info)
                if recomputed and recomputed != cur_day:
                    tz_updates.append((recomputed, day_to_desc_key(recomputed), ds, doc_id))
            if tz_updates:
                con.executemany(
                    '''UPDATE catalog_docs
                       SET effective_date = ?, day_desc_key = ?
                       WHERE dataset_id = ? AND doc_id = ?''',
                    tz_updates,
                )
                dirty_datasets.add(ds)
                incremental_bucket_deltas.pop(ds, None)
                cold_bucket_summaries.pop(ds, None)
                proven_pure_append_ds.discard(ds)


def commit_day_buckets_and_meta_tx(
    index: _CatalogSyncOwner, con: sqlite3.Connection, state: SyncRunState, dirty_datasets: set[str],
    sessions_mutated: bool, now_iso: str, *, force_full: bool,
) -> tuple[int, bool]:
    tz_name = state.tz_name
    cur_gen = state.cur_gen
    last_prov_seq = state.last_prov_seq
    last_recall_rev = state.last_recall_rev
    initial_doc_seq = state.initial_doc_seq
    next_doc_seq = state.next_doc_seq
    tz_changed = state.tz_changed
    live_fingerprints = state.live_fingerprints
    removed_datasets = state.removed_datasets
    incremental_bucket_deltas = state.incremental_bucket_deltas
    cold_bucket_summaries = state.cold_bucket_summaries
    new_prov_seq = state.new_prov_seq
    new_recall_rev = state.new_recall_rev
    prov_ok = state.prov_ok
    recall_ok = state.recall_ok

    mutated = bool(
        dirty_datasets
        or removed_datasets
        or tz_changed
        or (prov_ok and new_prov_seq != last_prov_seq)
        or (recall_ok and new_recall_rev != last_recall_rev)
        or sessions_mutated
        or force_full
    )
    next_gen = cur_gen + 1 if mutated else cur_gen
    for ds in sorted(dirty_datasets):
        if ds in cold_bucket_summaries and not tz_changed:
            cold_days, k_cnt, raw_cnt, com_cnt, _cold_cats = cold_bucket_summaries[ds]
            con.execute('DELETE FROM catalog_day_buckets WHERE dataset_id = ?', (ds,))
            if cold_days:
                con.executemany(
                    'INSERT INTO catalog_day_buckets(dataset_id, effective_date, knowledge_count) VALUES (?,?,?)',
                    [(ds, day_k, cnt_v) for day_k, cnt_v in cold_days.items()],
                )
            doc_count, max_rowid, sum_rowid, max_updated, data_hash = live_fingerprints[ds]
            ver_raw = f'{next_gen}|{ds}|{doc_count}|{sum_rowid}|{max_updated}|{data_hash}|{new_prov_seq}|{k_cnt}'
            catalog_version = hashlib.sha256(ver_raw.encode('utf-8')).hexdigest()[:24]
            con.execute(
                '''INSERT INTO dataset_sync_state
                   (dataset_id, dataset_hex, doc_count, max_rowid, sum_rowid, max_updated_at, id_checksum,
                    data_hash, knowledge_count, raw_doc_count, common_count, catalog_version, synced_at)
                   VALUES (?,?,?,?,?,?,0,?,?,?,?,?,?)
                   ON CONFLICT(dataset_id) DO UPDATE SET
                     dataset_hex=excluded.dataset_hex,
                     doc_count=excluded.doc_count,
                     max_rowid=excluded.max_rowid,
                     sum_rowid=excluded.sum_rowid,
                     max_updated_at=excluded.max_updated_at,
                     data_hash=excluded.data_hash,
                     knowledge_count=excluded.knowledge_count,
                     raw_doc_count=excluded.raw_doc_count,
                     common_count=excluded.common_count,
                     catalog_version=excluded.catalog_version,
                     synced_at=excluded.synced_at''',
                (
                    ds, uuid_hex(ds), doc_count, max_rowid, sum_rowid, max_updated,
                    data_hash, k_cnt, raw_cnt, com_cnt, catalog_version, now_iso,
                ),
            )
            continue
        if ds in incremental_bucket_deltas and not tz_changed:
            day_delta, dk, dr, dc = incremental_bucket_deltas[ds]
            for day_str, delta_cnt in day_delta.items():
                if delta_cnt == 0:
                    continue
                cur_b = con.execute(
                    'SELECT knowledge_count FROM catalog_day_buckets WHERE dataset_id = ? AND effective_date = ?',
                    (ds, day_str),
                ).fetchone()
                new_b = (int(cur_b[0]) if cur_b else 0) + delta_cnt
                if new_b <= 0:
                    con.execute(
                        'DELETE FROM catalog_day_buckets WHERE dataset_id = ? AND effective_date = ?',
                        (ds, day_str),
                    )
                else:
                    con.execute(
                        '''INSERT INTO catalog_day_buckets(dataset_id, effective_date, knowledge_count)
                           VALUES(?, ?, ?)
                           ON CONFLICT(dataset_id, effective_date) DO UPDATE SET
                             knowledge_count = excluded.knowledge_count''',
                        (ds, day_str, new_b),
                    )
            prev_counts = con.execute(
                'SELECT knowledge_count, raw_doc_count, common_count FROM dataset_sync_state WHERE dataset_id = ?',
                (ds,),
            ).fetchone()
            if prev_counts is not None:
                k_cnt = max(0, int(prev_counts[0]) + dk)
                raw_cnt = max(0, int(prev_counts[1]) + dr)
                com_cnt = max(0, int(prev_counts[2]) + dc)
                doc_count, max_rowid, sum_rowid, max_updated, data_hash = live_fingerprints[ds]
                ver_raw = f'{next_gen}|{ds}|{doc_count}|{sum_rowid}|{max_updated}|{data_hash}|{new_prov_seq}|{k_cnt}'
                catalog_version = hashlib.sha256(ver_raw.encode('utf-8')).hexdigest()[:24]
                con.execute(
                    '''UPDATE dataset_sync_state
                       SET doc_count=?, max_rowid=?, sum_rowid=?, max_updated_at=?, data_hash=?,
                           knowledge_count=?, raw_doc_count=?, common_count=?,
                           catalog_version=?, synced_at=?
                       WHERE dataset_id=?''',
                    (
                        doc_count, max_rowid, sum_rowid, max_updated, data_hash,
                        k_cnt, raw_cnt, com_cnt, catalog_version, now_iso, ds,
                    ),
                )
                continue
        index._rebuild_day_buckets_and_state_locked(
            con, ds, live_fingerprints[ds], next_gen, new_prov_seq, now_iso
        )

    if mutated or next_doc_seq != initial_doc_seq:
        persisted_prov_seq = new_prov_seq if prov_ok else last_prov_seq
        persisted_recall_rev = new_recall_rev if recall_ok else last_recall_rev
        con.execute(
            '''UPDATE catalog_meta
               SET generation = ?, local_tz_name = ?, last_provenance_seq = ?,
                   last_recall_rev = ?, last_doc_seq = ?, built_at = ?
               WHERE singleton = 1''',
            (next_gen, tz_name, persisted_prov_seq, persisted_recall_rev, next_doc_seq, now_iso),
        )
    con.execute('COMMIT')
    return next_gen, mutated


def build_in_memory_state_publisher(
    index: _CatalogSyncOwner, state: SyncRunState, dirty_datasets: set[str], sessions_mutated: bool,
    next_gen: int, mutated: bool, cog_snap_sig: FileStatSignature, *, force_full: bool,
) -> Callable[[], None]:
    sync_pin_ts_ns = state.sync_pin_ts_ns
    cur_gen = state.cur_gen
    tz_changed = state.tz_changed
    live_fingerprints = state.live_fingerprints
    prov_stat_now = state.prov_stat_now
    recall_stat_now = state.recall_stat_now
    removed_datasets = state.removed_datasets
    new_chunk_digests = state.new_chunk_digests
    dataset_diffs = state.dataset_diffs
    cold_bucket_summaries = state.cold_bucket_summaries
    proven_pure_append_ds = state.proven_pure_append_ds
    prov_changes = state.prov_changes
    recall_changes = state.recall_changes
    need_full_prov = state.need_full_prov
    need_full_recall = state.need_full_recall
    prov_ok = state.prov_ok
    recall_ok = state.recall_ok

    doc_count_total = sum(int(fp[0]) for fp in live_fingerprints.values())

    def _publish_in_memory_state():
        for ds in removed_datasets:
            index._chunk_digests.pop(ds, None)
        for ds, cmap in new_chunk_digests.items():
            index._chunk_digests[ds] = cmap

        if prov_ok:
            index._last_prov_stat = prov_stat_now
            index._prov_sync_incomplete = False
        else:
            index._last_prov_stat = None
            index._prov_sync_incomplete = True
        if recall_ok:
            index._last_recall_stat = recall_stat_now
            index._recall_sync_incomplete = False
        else:
            index._last_recall_stat = None
            index._recall_sync_incomplete = True

        # Maintain O(1) pure-append reading deltas & category_counts caches ONLY when proven strictly pure-append!
        pure_append = (
            mutated
            and len(dirty_datasets) == 1
            and not removed_datasets
            and not tz_changed
            and not prov_changes
            and not recall_changes
            and not need_full_prov
            and not need_full_recall
            and not sessions_mutated
            and not force_full
        )
        if pure_append:
            only_ds = next(iter(dirty_datasets))
            if only_ds in proven_pure_append_ds:
                upserts_only, del_only = dataset_diffs.get(only_ds, ([], []))
                if upserts_only and not del_only:
                    appended = [
                        (str(p[1]), str(p[15]), int(p[16]), int(p[26]))
                        for p in upserts_only
                        if int(p[4]) == 1
                    ]
                    with index._state_lock:
                        index._recent_appends[next_gen] = (only_ds, appended)
                        if len(index._recent_appends) > 64:
                            oldest_k = min(index._recent_appends)
                            index._recent_appends.pop(oldest_k, None)
                        prev_cats = index._cat_counts_cache.get((cur_gen, only_ds))
                        if prev_cats is not None:
                            updated_cats = dict(prev_cats)
                            for p in upserts_only:
                                if int(p[4]) == 1 and p[8]:
                                    c_lbl = str(p[8])
                                    updated_cats[c_lbl] = updated_cats.get(c_lbl, 0) + 1
                            index._cat_counts_cache[(next_gen, only_ds)] = updated_cats
                        for (g_k, ds_k), cats_v in list(index._cat_counts_cache.items()):
                            if g_k == cur_gen and ds_k != only_ds:
                                index._cat_counts_cache[(next_gen, ds_k)] = cats_v
                        if len(index._cat_counts_cache) > 64:
                            index._cat_counts_cache = {
                                k: v for k, v in index._cat_counts_cache.items() if k[0] >= next_gen - 2
                            }
                        index._common_cat_counts_cache.pop((cur_gen, only_ds), None)
                        if not any(p[3] == 'raw_doc' for p in upserts_only):
                            for (g_k, ds_k, lim_k), pv in list(index._pending_raw_cache.items()):
                                if g_k == cur_gen:
                                    index._pending_raw_cache[(next_gen, ds_k, lim_k)] = pv
                            if len(index._pending_raw_cache) > 32:
                                index._pending_raw_cache = {
                                    k: v for k, v in index._pending_raw_cache.items() if k[0] >= next_gen - 2
                                }
                        else:
                            index._pending_raw_cache.clear()
                else:
                    with index._state_lock:
                        index._recent_appends.clear()
                        index._cat_counts_cache.clear()
                        index._common_cat_counts_cache.clear()
                        index._pending_raw_cache.clear()
            else:
                with index._state_lock:
                    index._recent_appends.clear()
                    index._cat_counts_cache.clear()
                    index._common_cat_counts_cache.clear()
                    index._pending_raw_cache.clear()
        elif mutated:
            with index._state_lock:
                index._recent_appends.clear()
                index._cat_counts_cache.clear()
                index._common_cat_counts_cache.clear()
                index._pending_raw_cache.clear()
                if sessions_mutated or removed_datasets or force_full:
                    index._sessions_generation += 1
                    index._children_preview_cache.clear()
                for ds_c, (_days, _k, _r, _c, cold_cats) in cold_bucket_summaries.items():
                    index._cat_counts_cache[(next_gen, ds_c)] = dict(cold_cats)
        index._last_synced_cog_sig = cog_snap_sig
        index._last_synced_pin_ts_ns_pending = sync_pin_ts_ns
        index._last_sync_doc_count_pending = doc_count_total
    return _publish_in_memory_state
