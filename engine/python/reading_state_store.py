"""Independent persistent user reading state store (ui-reading-state.sqlite3).

Physically isolated from rebuildable ``catalog-index.sqlite3`` so index rebuilds
never lose read/unread state. Preserves exact double-slash ``//`` ``scope_hash``
compatibility with legacy ``knowledge-read-<hash>.json``, 9-digit nanosecond UTC
timestamps, invalid-createdAt unread semantics, single-transaction local atomic
migration, two-stage remote staging import, re-upgrade conflict merge without
phantom unreads, sparse three-branch unread computation, and ``read_horizon_at_ns``
watermark compaction.
"""
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import stat
import threading
import time
import uuid

from catalog_index import normalize_utc_ns
from project_policy import PolicyError

MAX_LEGACY_READ_JSON_BYTES = 64 * 1024 * 1024
_MAX_IN_BATCH = 400
_ZERO_TIME_NS = '0001-01-01T00:00:00.000000000Z'


def knowledge_read_scope_hash(reading_identity: str, project_id: str, dataset_id: str) -> str:
    """Exact byte-for-byte match for Go ``knowledge_unread.go:37``.

    Because Go's ``readingIdentity`` already ends with ``/``, concatenating
    ``identity + "/" + project_id + "/" + dataset_id`` intentionally contains
    a double slash ``//``. This function MUST NEVER strip trailing slashes from
    ``reading_identity``.
    """
    raw = f'{reading_identity}/{project_id}/{dataset_id}'
    return hashlib.sha256(raw.encode('utf-8')).hexdigest()


def now_utc_ns() -> str:
    ns = time.time_ns()
    sec, rem = divmod(ns, 1_000_000_000)
    dt = datetime.fromtimestamp(sec, tz=timezone.utc)
    return dt.strftime('%Y-%m-%dT%H:%M:%S') + f'.{rem:09d}Z'


def epoch_ns_to_utc_ns(epoch_ns: int) -> str:
    sec, rem = divmod(int(epoch_ns), 1_000_000_000)
    dt = datetime.fromtimestamp(sec, tz=timezone.utc)
    return dt.strftime('%Y-%m-%dT%H:%M:%S') + f'.{rem:09d}Z'


def _iso_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec='microseconds')


def _is_zero_or_empty_ts(raw_value) -> bool:
    if not isinstance(raw_value, str) or not raw_value.strip():
        return True
    norm, _, invalid = normalize_utc_ns(raw_value)
    return invalid or norm == _ZERO_TIME_NS or norm.startswith('0001-01-01T00:00:00')


class ReadingStateStore:
    """Private ``0600`` SQLite WAL authoritative store for user knowledge read/unread state."""

    def __init__(self, path, *, state_dir=None):
        self.path = Path(path)
        self.state_dir = Path(state_dir) if state_dir is not None else self.path.parent
        self._lock = threading.RLock()
        self._mutation_rev = 0
        self._unread_cache: dict[tuple[str, str], tuple[int, int, int, str, int, set[str]]] = {}
        # Cache earliest unread created_at_ns per (scope_hash, dataset_id) to skip O(N) compaction rescans
        self._compact_floor_cache: dict[tuple[str, str], tuple[int, int, str | None]] = {}
        self.path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        os.chmod(self.path.parent, 0o700)
        if self.path.is_symlink():
            raise ValueError('reading state database must not be a symlink')
        fd = os.open(self.path, os.O_CREAT | os.O_RDWR | getattr(os, 'O_NOFOLLOW', 0), 0o600)
        os.close(fd)
        os.chmod(self.path, 0o600)
        self.db = sqlite3.connect(self.path, isolation_level=None, check_same_thread=False)
        self.db.execute('PRAGMA journal_mode=WAL')
        self.db.execute('PRAGMA journal_size_limit=4194304')
        self.db.execute('PRAGMA wal_autocheckpoint=1000')
        self.db.execute('PRAGMA synchronous=NORMAL')
        self.db.execute('PRAGMA cache_size=-1024')
        self.db.execute('PRAGMA foreign_keys=ON')
        self.db.execute('PRAGMA busy_timeout=5000')
        with self._lock:
            self.db.executescript('''
                CREATE TABLE IF NOT EXISTS ui_read_scopes (
                    scope_hash TEXT PRIMARY KEY,
                    project_id TEXT NOT NULL,
                    dataset_id TEXT NOT NULL,
                    initialized INTEGER NOT NULL CHECK (initialized IN (0, 1)),
                    enabled_at_ns TEXT NOT NULL,
                    read_horizon_at_ns TEXT NOT NULL,
                    horizon_max_rowid INTEGER NOT NULL DEFAULT 0,
                    state_rev INTEGER NOT NULL DEFAULT 1,
                    is_auto_baseline INTEGER NOT NULL DEFAULT 0 CHECK (is_auto_baseline IN (0, 1)),
                    legacy_file_sha256 TEXT NOT NULL DEFAULT '',
                    legacy_mtime_ns INTEGER NOT NULL DEFAULT 0,
                    updated_at TEXT NOT NULL
                );

                CREATE TABLE IF NOT EXISTS ui_read_items (
                    scope_hash TEXT NOT NULL,
                    doc_id TEXT NOT NULL,
                    is_read INTEGER NOT NULL CHECK (is_read IN (0, 1)),
                    updated_at TEXT NOT NULL,
                    PRIMARY KEY (scope_hash, doc_id),
                    FOREIGN KEY (scope_hash) REFERENCES ui_read_scopes(scope_hash) ON DELETE CASCADE
                ) WITHOUT ROWID;

                CREATE INDEX IF NOT EXISTS idx_read_items_state
                    ON ui_read_items(scope_hash, is_read, doc_id);

                CREATE TABLE IF NOT EXISTS ui_read_migration_staging (
                    batch_id TEXT NOT NULL,
                    scope_hash TEXT NOT NULL,
                    doc_id TEXT NOT NULL,
                    is_read INTEGER NOT NULL CHECK (is_read IN (0, 1)),
                    PRIMARY KEY (batch_id, scope_hash, doc_id)
                ) WITHOUT ROWID;

                CREATE TABLE IF NOT EXISTS ui_read_migration_batches (
                    batch_id TEXT PRIMARY KEY,
                    scope_hash TEXT NOT NULL,
                    project_id TEXT NOT NULL,
                    dataset_id TEXT NOT NULL,
                    initialized INTEGER NOT NULL CHECK (initialized IN (0, 1)),
                    enabled_at_ns TEXT NOT NULL,
                    created_at TEXT NOT NULL,
                    committed_at TEXT NOT NULL DEFAULT '',
                    committed_count INTEGER NOT NULL DEFAULT 0
                );
            ''')
            # Backward-compatible online schema migration for existing databases
            scope_cols = {str(r[1]) for r in self.db.execute('PRAGMA table_info(ui_read_scopes)').fetchall()}
            if 'horizon_max_rowid' not in scope_cols:
                self.db.execute('ALTER TABLE ui_read_scopes ADD COLUMN horizon_max_rowid INTEGER NOT NULL DEFAULT 0')
            if 'state_rev' not in scope_cols:
                self.db.execute('ALTER TABLE ui_read_scopes ADD COLUMN state_rev INTEGER NOT NULL DEFAULT 1')
            if 'is_auto_baseline' not in scope_cols:
                self.db.execute('ALTER TABLE ui_read_scopes ADD COLUMN is_auto_baseline INTEGER NOT NULL DEFAULT 0')
            batch_cols = {str(r[1]) for r in self.db.execute('PRAGMA table_info(ui_read_migration_batches)').fetchall()}
            if 'committed_at' not in batch_cols:
                self.db.execute("ALTER TABLE ui_read_migration_batches ADD COLUMN committed_at TEXT NOT NULL DEFAULT ''")
            if 'committed_count' not in batch_cols:
                self.db.execute('ALTER TABLE ui_read_migration_batches ADD COLUMN committed_count INTEGER NOT NULL DEFAULT 0')

    def close(self):
        with self._lock:
            try:
                self.db.execute('PRAGMA wal_checkpoint(PASSIVE)')
            except sqlite3.Error:
                pass
            self.db.close()

    def get_scope(self, scope_hash: str) -> dict | None:
        with self._lock:
            row = self.db.execute(
                '''SELECT scope_hash, project_id, dataset_id, initialized,
                          enabled_at_ns, read_horizon_at_ns, horizon_max_rowid,
                          state_rev, is_auto_baseline, legacy_file_sha256,
                          legacy_mtime_ns, updated_at
                   FROM ui_read_scopes WHERE scope_hash = ?''',
                (scope_hash,),
            ).fetchone()
        if row is None:
            return None
        return {
            'scope_hash': str(row[0]),
            'project_id': str(row[1]),
            'dataset_id': str(row[2]),
            'initialized': bool(row[3]),
            'enabled_at_ns': str(row[4]),
            'read_horizon_at_ns': str(row[5]),
            'horizon_max_rowid': int(row[6] or 0),
            'state_rev': int(row[7] or 1),
            'is_auto_baseline': bool(row[8]),
            'legacy_file_sha256': str(row[9]),
            'legacy_mtime_ns': int(row[10]),
            'updated_at': str(row[11]),
        }

    def ensure_scope(
        self,
        reading_identity: str,
        project_id: str,
        dataset_id: str,
        *,
        catalog_index=None,
    ) -> dict:
        """Return scope row, auto-migrating any unmigrated legacy JSON in ``state_dir`` FIRST.

        Never overwrites an unmigrated ``knowledge-read-<scope_hash>.json`` with an
        empty ``NOW`` baseline.
        """
        scope_hash = knowledge_read_scope_hash(reading_identity, project_id, dataset_id)
        with self._lock:
            existing = self.get_scope(scope_hash)
            if existing is not None and existing['initialized']:
                return existing

            # Before initializing any blank scope, atomically migrate an existing local legacy JSON!
            if self.state_dir is not None:
                legacy_path = self.state_dir / f'knowledge-read-{scope_hash}.json'
                try:
                    info = legacy_path.lstat()
                    has_legacy = not stat.S_ISLNK(info.st_mode) and stat.S_ISREG(info.st_mode)
                except OSError:
                    has_legacy = False
                if has_legacy:
                    try:
                        self.migrate_local_file(
                            self.state_dir,
                            reading_identity,
                            project_id,
                            dataset_id,
                            catalog_index=catalog_index,
                        )
                        migrated = self.get_scope(scope_hash)
                        if migrated is not None and migrated['initialized']:
                            return migrated
                    except PolicyError:
                        # Corrupt/oversized legacy JSON must not silently drop into a blank NOW baseline
                        raise

            horizon = now_utc_ns()
            if catalog_index is not None:
                max_created = catalog_index.max_knowledge_created_at_ns(dataset_id)
                if max_created and max_created > horizon:
                    horizon = max_created
            now_iso = _iso_now()
            self.db.execute('BEGIN IMMEDIATE')
            try:
                row = self.db.execute(
                    'SELECT initialized FROM ui_read_scopes WHERE scope_hash = ?',
                    (scope_hash,),
                ).fetchone()
                if row is None:
                    self.db.execute(
                        '''INSERT INTO ui_read_scopes
                           (scope_hash, project_id, dataset_id, initialized,
                            enabled_at_ns, read_horizon_at_ns, horizon_max_rowid,
                            state_rev, is_auto_baseline, legacy_file_sha256,
                            legacy_mtime_ns, updated_at)
                           VALUES (?,?,?,1,?,?,0,1,1,'',0,?)''',
                        (scope_hash, project_id, dataset_id, horizon, horizon, now_iso),
                    )
                elif int(row[0]) == 0:
                    self.db.execute(
                        '''UPDATE ui_read_scopes
                           SET initialized = 1, enabled_at_ns = ?, read_horizon_at_ns = ?,
                               horizon_max_rowid = 0, state_rev = state_rev + 1,
                               is_auto_baseline = 1, updated_at = ?
                           WHERE scope_hash = ?''',
                        (horizon, horizon, now_iso, scope_hash),
                    )
                self.db.execute('COMMIT')
                self._bump_mutation_rev_locked()
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise
            return self.get_scope(scope_hash)

    def _merge_imported_scope_locked(
        self,
        *,
        scope_hash: str,
        project_id: str,
        dataset_id: str,
        initialized: bool,
        new_enabled_ns: str,
        imported_items: dict[str, bool],
        legacy_sha256: str,
        legacy_mtime_ns: int,
        catalog_index=None,
    ):
        """Merge imported read state inside an active BEGIN IMMEDIATE transaction.

        Never drops explicit ``is_read=1`` keys even if not yet present in ``catalog_index``.
        Preserves compacted reads across the full ``(low_ns, max(old_enabled, old_horizon, new_enabled)]``
        interval so re-upgrades never manufacture phantom unreads or flip compacted reads back to unread.
        """
        now_iso = _iso_now()
        existing = self.db.execute(
            '''SELECT initialized, enabled_at_ns, read_horizon_at_ns, horizon_max_rowid,
                      state_rev, is_auto_baseline
               FROM ui_read_scopes WHERE scope_hash = ?''',
            (scope_hash,),
        ).fetchone()

        if existing is None:
            effective_init = 1 if initialized else 0
            self.db.execute(
                '''INSERT INTO ui_read_scopes
                   (scope_hash, project_id, dataset_id, initialized,
                    enabled_at_ns, read_horizon_at_ns, horizon_max_rowid,
                    state_rev, is_auto_baseline, legacy_file_sha256,
                    legacy_mtime_ns, updated_at)
                   VALUES (?,?,?,?,?,?,0,1,0,?,?,?)''',
                (
                    scope_hash,
                    project_id,
                    dataset_id,
                    effective_init,
                    new_enabled_ns,
                    new_enabled_ns,
                    legacy_sha256,
                    int(legacy_mtime_ns),
                    now_iso,
                ),
            )
            if imported_items:
                self.db.executemany(
                    '''INSERT INTO ui_read_items(scope_hash, doc_id, is_read, updated_at)
                       VALUES (?,?,?,?)
                       ON CONFLICT(scope_hash, doc_id) DO UPDATE SET
                         is_read = CASE
                           WHEN ui_read_items.is_read = 1 OR excluded.is_read = 1 THEN 1
                           ELSE 0
                         END,
                         updated_at = excluded.updated_at''',
                    [(scope_hash, str(did), 1 if is_r else 0, now_iso) for did, is_r in imported_items.items()],
                )
            return

        old_init = bool(existing[0])
        old_enabled_ns = str(existing[1])
        old_horizon_ns = str(existing[2])
        old_horizon_max_rowid = int(existing[3] or 0)
        old_is_auto_baseline = bool(existing[5])

        existing_items = {
            str(r[0]): int(r[1])
            for r in self.db.execute(
                'SELECT doc_id, is_read FROM ui_read_items WHERE scope_hash = ?',
                (scope_hash,),
            ).fetchall()
        }

        # If existing scope was an untouched auto-baseline created before custom-dir migration arrived,
        # adopt the imported baseline cleanly without wiping historical unreads!
        if old_is_auto_baseline and not existing_items:
            final_init = 1 if initialized else 0
            self.db.execute(
                '''UPDATE ui_read_scopes
                   SET initialized = ?, enabled_at_ns = ?, read_horizon_at_ns = ?,
                       horizon_max_rowid = 0, state_rev = state_rev + 1,
                       is_auto_baseline = 0, legacy_file_sha256 = ?,
                       legacy_mtime_ns = ?, updated_at = ?
                   WHERE scope_hash = ?''',
                (
                    final_init,
                    new_enabled_ns,
                    new_enabled_ns,
                    legacy_sha256,
                    int(legacy_mtime_ns),
                    now_iso,
                    scope_hash,
                ),
            )
            if imported_items:
                self.db.executemany(
                    '''INSERT INTO ui_read_items(scope_hash, doc_id, is_read, updated_at)
                       VALUES (?,?,?,?)
                       ON CONFLICT(scope_hash, doc_id) DO UPDATE SET
                         is_read = CASE
                           WHEN ui_read_items.is_read = 1 OR excluded.is_read = 1 THEN 1
                           ELSE 0
                         END,
                         updated_at = excluded.updated_at''',
                    [(scope_hash, str(did), 1 if is_r else 0, now_iso) for did, is_r in imported_items.items()],
                )
            return

        final_init = 1 if (old_init or initialized) else 0
        final_enabled_ns = old_enabled_ns
        final_horizon_ns = old_horizon_ns
        final_horizon_max_rowid = old_horizon_max_rowid

        # Preserve ALL implicit reads from BOTH sides (including compacted horizon > enabled_at!)
        if old_init and initialized and old_enabled_ns and new_enabled_ns:
            low_ns = min(old_enabled_ns, new_enabled_ns)
            high_span = max(old_enabled_ns, old_horizon_ns, new_enabled_ns)
            if high_span > low_ns:
                if catalog_index is not None:
                    # Fetch (doc_id, created_at_ns, cognee_rowid) across the FULL span (low_ns, high_span]
                    if hasattr(catalog_index, 'knowledge_docs_in_range_with_rowid'):
                        span_docs = catalog_index.knowledge_docs_in_range_with_rowid(dataset_id, low_ns, high_span)
                    else:
                        span_docs = [(did, high_span, 0) for did in catalog_index.knowledge_docs_in_range(dataset_id, low_ns, high_span)]
                    for did, cns, rid in span_docs:
                        # Was this doc implicitly read on the OLD DB side?
                        read_on_old = (
                            existing_items.get(did) == 1
                            or (
                                existing_items.get(did) != 0
                                and (
                                    cns <= old_enabled_ns
                                    or (
                                        cns <= old_horizon_ns
                                        and (old_horizon_max_rowid == 0 or rid <= old_horizon_max_rowid)
                                    )
                                )
                            )
                        )
                        # Was this doc implicitly read on the NEW imported JSON side?
                        read_on_new = (
                            imported_items.get(did) is True
                            or (imported_items.get(did) is not False and cns <= new_enabled_ns)
                        )
                        if read_on_old or read_on_new:
                            imported_items[did] = True
                    final_enabled_ns = low_ns
                    final_horizon_ns = low_ns
                    final_horizon_max_rowid = 0
                else:
                    # Without catalog_index we cannot safely materialize compacted reads across (low_ns, high_span];
                    # Fail-Closed so commit_import rolls back cleanly and keeps the staging batch retryable!
                    raise PolicyError('catalog_index_rebuilding', 503)
        elif not old_init and initialized and new_enabled_ns:
            final_enabled_ns = new_enabled_ns
            final_horizon_ns = new_enabled_ns
            final_horizon_max_rowid = 0

        self.db.execute(
            '''UPDATE ui_read_scopes
               SET initialized = ?, enabled_at_ns = ?, read_horizon_at_ns = ?,
                   horizon_max_rowid = ?, state_rev = state_rev + 1,
                   is_auto_baseline = 0, legacy_file_sha256 = ?,
                   legacy_mtime_ns = ?, updated_at = ?
               WHERE scope_hash = ?''',
            (
                final_init,
                final_enabled_ns,
                final_horizon_ns,
                final_horizon_max_rowid,
                legacy_sha256,
                int(legacy_mtime_ns),
                now_iso,
                scope_hash,
            ),
        )
        if imported_items:
            self.db.executemany(
                '''INSERT INTO ui_read_items(scope_hash, doc_id, is_read, updated_at)
                   VALUES (?,?,?,?)
                   ON CONFLICT(scope_hash, doc_id) DO UPDATE SET
                     is_read = CASE
                       WHEN ui_read_items.is_read = 1 OR excluded.is_read = 1 THEN 1
                       ELSE 0
                     END,
                     updated_at = excluded.updated_at''',
                [(scope_hash, str(did), 1 if is_r else 0, now_iso) for did, is_r in imported_items.items()],
            )

    def migrate_local_file(
        self,
        state_dir: Path,
        reading_identity: str,
        project_id: str,
        dataset_id: str,
        *,
        catalog_index=None,
    ) -> dict:
        """Single-transaction atomic migration of ``knowledge-read-<hash>.json`` in ``state_dir``.

        NEVER pre-creates a blank scope when the local legacy file is absent so custom-dir
        clients can cleanly proceed with ``import/begin -> chunk -> commit``.
        """
        scope_hash = knowledge_read_scope_hash(reading_identity, project_id, dataset_id)
        legacy_path = Path(state_dir) / f'knowledge-read-{scope_hash}.json'
        o_nofollow = getattr(os, 'O_NOFOLLOW', 0)
        o_cloexec = getattr(os, 'O_CLOEXEC', 0)
        fd = -1
        try:
            fd = os.open(str(legacy_path), os.O_RDONLY | o_nofollow | o_cloexec)
            info = os.fstat(fd)
            if not stat.S_ISREG(info.st_mode):
                raise PolicyError('legacy_reading_state_invalid', 400)
            if info.st_size > MAX_LEGACY_READ_JSON_BYTES:
                raise PolicyError('legacy_reading_state_too_large', 413)
            buf = bytearray()
            while True:
                chunk = os.read(fd, 65536)
                if not chunk:
                    break
                buf.extend(chunk)
                if len(buf) > MAX_LEGACY_READ_JSON_BYTES:
                    raise PolicyError('legacy_reading_state_too_large', 413)
            raw_bytes = bytes(buf)
        except FileNotFoundError:
            existing = self.get_scope(scope_hash)
            if existing is not None and existing['legacy_file_sha256']:
                return {
                    'project_id': project_id,
                    'dataset_id': dataset_id,
                    'scope_hash': scope_hash,
                    'status': 'already_migrated',
                    'imported_items': 0,
                }
            # Critical: DO NOT call ensure_scope() here; return cleanly so client triggers staging import!
            return {
                'project_id': project_id,
                'dataset_id': dataset_id,
                'scope_hash': scope_hash,
                'status': 'no_legacy_file',
                'imported_items': 0,
            }
        except PolicyError:
            raise
        except OSError as exc:
            # ELOOP (symlink) -> invalid; other OS errors -> unreadable
            if getattr(exc, 'errno', None) == getattr(os, 'ELOOP', 62):
                raise PolicyError('legacy_reading_state_invalid', 400) from exc
            try:
                st = legacy_path.lstat()
                if stat.S_ISLNK(st.st_mode) or not stat.S_ISREG(st.st_mode):
                    raise PolicyError('legacy_reading_state_invalid', 400) from exc
            except OSError:
                pass
            raise PolicyError('legacy_reading_state_unreadable', 503) from exc
        finally:
            if fd >= 0:
                os.close(fd)

        file_sha256 = hashlib.sha256(raw_bytes).hexdigest()
        backup_path = Path(str(legacy_path) + '.migrated.bak')
        existing = self.get_scope(scope_hash)
        if existing is not None and existing['legacy_file_sha256'] == file_sha256:
            # Complete any previously interrupted backup handoff when file content is unchanged
            try:
                cur_st = legacy_path.lstat()
                if cur_st.st_ino == info.st_ino and cur_st.st_size == info.st_size and cur_st.st_mtime_ns == info.st_mtime_ns:
                    os.replace(legacy_path, backup_path)
                    os.chmod(backup_path, 0o600)
            except OSError:
                pass
            return {
                'project_id': project_id,
                'dataset_id': dataset_id,
                'scope_hash': scope_hash,
                'status': 'already_migrated',
                'imported_items': 0,
            }

        try:
            payload = json.loads(raw_bytes.decode('utf-8'))
        except (UnicodeDecodeError, ValueError) as exc:
            raise PolicyError('legacy_reading_state_invalid', 400) from exc

        if (
            not isinstance(payload, dict)
            or payload.get('version') != 1
            or not isinstance(payload.get('knowledge_ids'), dict)
        ):
            raise PolicyError('legacy_reading_state_invalid', 400)

        initialized = bool(payload.get('initialized', False))
        raw_enabled = payload.get('enabled_at', '')
        if initialized and _is_zero_or_empty_ts(raw_enabled):
            enabled_ns = epoch_ns_to_utc_ns(info.st_mtime_ns)
        elif not _is_zero_or_empty_ts(raw_enabled):
            enabled_ns, _, _ = normalize_utc_ns(raw_enabled)
        else:
            enabled_ns = now_utc_ns()

        imported_items = {}
        for doc_id, read_val in payload['knowledge_ids'].items():
            if isinstance(doc_id, str) and doc_id and type(read_val) is bool:
                imported_items[doc_id] = read_val

        with self._lock:
            self.db.execute('BEGIN IMMEDIATE')
            try:
                self._merge_imported_scope_locked(
                    scope_hash=scope_hash,
                    project_id=project_id,
                    dataset_id=dataset_id,
                    initialized=initialized,
                    new_enabled_ns=enabled_ns,
                    imported_items=imported_items,
                    legacy_sha256=file_sha256,
                    legacy_mtime_ns=info.st_mtime_ns,
                    catalog_index=catalog_index,
                )
                self.db.execute('COMMIT')
                self._bump_mutation_rev_locked()
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise

        if catalog_index is not None:
            self.compact_horizon(scope_hash, dataset_id, catalog_index)

        # Preserve backup only if the legacy file wasn't concurrently rewritten while migrating
        try:
            cur_st = legacy_path.lstat()
            if cur_st.st_ino == info.st_ino and cur_st.st_size == info.st_size and cur_st.st_mtime_ns == info.st_mtime_ns:
                os.replace(legacy_path, backup_path)
                os.chmod(backup_path, 0o600)
        except OSError:
            pass

        return {
            'project_id': project_id,
            'dataset_id': dataset_id,
            'scope_hash': scope_hash,
            'status': 'migrated',
            'imported_items': len(imported_items),
        }

    # ============ Two-stage staging import for remote / custom-dir clients ============

    def begin_import(
        self,
        reading_identity: str,
        project_id: str,
        dataset_id: str,
        *,
        initialized: bool,
        enabled_at: str,
    ) -> dict:
        scope_hash = knowledge_read_scope_hash(reading_identity, project_id, dataset_id)
        if _is_zero_or_empty_ts(enabled_at):
            enabled_ns = now_utc_ns()
        else:
            enabled_ns, _, invalid = normalize_utc_ns(enabled_at)
            if invalid:
                raise PolicyError('invalid_enabled_at', 400)
        batch_id = str(uuid.uuid4())
        with self._lock:
            self.db.execute('BEGIN IMMEDIATE')
            try:
                self.db.execute(
                    '''INSERT INTO ui_read_migration_batches
                       (batch_id, scope_hash, project_id, dataset_id, initialized, enabled_at_ns, created_at)
                       VALUES (?,?,?,?,?,?,?)''',
                    (batch_id, scope_hash, project_id, dataset_id, 1 if initialized else 0, enabled_ns, _iso_now()),
                )
                self.db.execute('COMMIT')
                self._bump_mutation_rev_locked()
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise
        return {'batch_id': batch_id, 'scope_hash': scope_hash}

    def stage_import_chunk(self, batch_id: str, items: list[dict]) -> dict:
        if not isinstance(items, list) or len(items) > 2000:
            raise PolicyError('invalid_import_chunk', 400)
        with self._lock:
            batch = self.db.execute(
                'SELECT scope_hash FROM ui_read_migration_batches WHERE batch_id = ?',
                (batch_id,),
            ).fetchone()
            if batch is None:
                raise PolicyError('import_batch_not_found', 404)
            scope_hash = str(batch[0])
            rows = []
            for entry in items:
                if not isinstance(entry, dict):
                    raise PolicyError('invalid_import_chunk', 400)
                did = entry.get('doc_id')
                is_r = entry.get('is_read')
                if not isinstance(did, str) or not did or type(is_r) is not bool:
                    raise PolicyError('invalid_import_chunk', 400)
                rows.append((batch_id, scope_hash, did, 1 if is_r else 0))
            self.db.execute('BEGIN IMMEDIATE')
            try:
                self.db.executemany(
                    '''INSERT INTO ui_read_migration_staging(batch_id, scope_hash, doc_id, is_read)
                       VALUES (?,?,?,?)
                       ON CONFLICT(batch_id, scope_hash, doc_id) DO UPDATE SET
                         is_read = CASE
                           WHEN ui_read_migration_staging.is_read = 1 OR excluded.is_read = 1 THEN 1
                           ELSE 0
                         END''',
                    rows,
                )
                self.db.execute('COMMIT')
                self._bump_mutation_rev_locked()
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise
        return {'batch_id': batch_id, 'staged': len(rows)}

    def commit_import(
        self,
        batch_id: str,
        *,
        catalog_index=None,
        expected_items: int | None = None,
        verify_project=None,
    ) -> dict:
        with self._lock:
            batch = self.db.execute(
                '''SELECT scope_hash, project_id, dataset_id, initialized, enabled_at_ns,
                          committed_at, committed_count
                   FROM ui_read_migration_batches WHERE batch_id = ?''',
                (batch_id,),
            ).fetchone()
            if batch is None:
                raise PolicyError('import_batch_not_found', 404)
            scope_hash, project_id, dataset_id, initialized, enabled_ns, committed_at, committed_count = (
                str(batch[0]),
                str(batch[1]),
                str(batch[2]),
                bool(batch[3]),
                str(batch[4]),
                str(batch[5] or ''),
                int(batch[6] or 0),
            )
            if verify_project is not None:
                verify_project(project_id, dataset_id)
            # Idempotent retry support: already committed batch returns consistent receipt
            if committed_at:
                sc = self.get_scope(scope_hash)
                unread_cnt = (
                    len(self._compute_unread_ids_for_scope(scope_hash, dataset_id, sc, catalog_index))
                    if (catalog_index is not None and sc is not None)
                    else 0
                )
                return {
                    'scope_hash': scope_hash,
                    'committed': committed_count,
                    'dataset_id': dataset_id,
                    'unread_count': unread_cnt,
                    'state_rev': sc['state_rev'] if sc else 1,
                }
            staged = {
                str(r[0]): bool(r[1])
                for r in self.db.execute(
                    'SELECT doc_id, is_read FROM ui_read_migration_staging WHERE batch_id = ?',
                    (batch_id,),
                ).fetchall()
            }
            if expected_items is not None and len(staged) != int(expected_items):
                raise PolicyError('import_batch_incomplete', 409)
            now_iso = _iso_now()
            self.db.execute('BEGIN IMMEDIATE')
            try:
                self._merge_imported_scope_locked(
                    scope_hash=scope_hash,
                    project_id=project_id,
                    dataset_id=dataset_id,
                    initialized=initialized,
                    new_enabled_ns=enabled_ns,
                    imported_items=dict(staged),
                    legacy_sha256='',
                    legacy_mtime_ns=0,
                    catalog_index=catalog_index,
                )
                self.db.execute('DELETE FROM ui_read_migration_staging WHERE batch_id = ?', (batch_id,))
                self.db.execute(
                    'UPDATE ui_read_migration_batches SET committed_at = ?, committed_count = ? WHERE batch_id = ?',
                    (now_iso, len(staged), batch_id),
                )
                self.db.execute('COMMIT')
                self._bump_mutation_rev_locked()
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise

        if catalog_index is not None:
            self.compact_horizon(scope_hash, dataset_id, catalog_index)
            unread_ids, state_rev = self._compute_unread_snapshot_for_scope(scope_hash, dataset_id, catalog_index)
            unread_cnt = len(unread_ids)
        else:
            sc_after = self.get_scope(scope_hash)
            unread_cnt = 0
            state_rev = sc_after['state_rev'] if sc_after else 1
        return {
            'scope_hash': scope_hash,
            'committed': len(staged),
            'dataset_id': dataset_id,
            'unread_count': unread_cnt,
            'state_rev': state_rev,
        }

    def export_scope(self, scope_hash: str) -> dict | None:
        """Export scope back to legacy JSON format for lossless rollback."""
        scope = self.get_scope(scope_hash)
        if scope is None:
            return None
        with self._lock:
            items = {
                str(r[0]): bool(r[1])
                for r in self.db.execute(
                    'SELECT doc_id, is_read FROM ui_read_items WHERE scope_hash = ?',
                    (scope_hash,),
                ).fetchall()
            }
        return {
            'version': 1,
            'initialized': scope['initialized'],
            'enabled_at': scope['enabled_at_ns'],
            'knowledge_ids': items,
        }

    # ============ Sparse 3-branch unread computation & watermark compaction ============

    def _bump_mutation_rev_locked(self):
        self._mutation_rev += 1
        self._unread_cache.clear()
        self._compact_floor_cache.clear()

    def _explicit_read_subset_locked(self, scope_hash: str, doc_ids: list[str]) -> set[str]:
        """O(K log N) PK lookup on ui_read_items without ever loading the full scope into RAM."""
        if not doc_ids:
            return set()
        found: set[str] = set()
        for start in range(0, len(doc_ids), _MAX_IN_BATCH):
            batch = doc_ids[start : start + _MAX_IN_BATCH]
            ph = ','.join('?' for _ in batch)
            for (did,) in self.db.execute(
                f'''SELECT doc_id FROM ui_read_items
                    WHERE scope_hash = ? AND is_read = 1 AND doc_id IN ({ph})''',
                (scope_hash, *batch),
            ):
                found.add(str(did))
        return found

    def _compute_unread_ids_for_scope(self, scope_hash: str, dataset_id: str, scope: dict, catalog_index) -> set[str]:
        horizon_ns = scope['read_horizon_at_ns']
        enabled_ns = scope['enabled_at_ns']
        horizon_max_rowid = int(scope.get('horizon_max_rowid') or 0)
        state_rev = int(scope.get('state_rev') or 0)
        cur_gen = int(catalog_index.generation()) if hasattr(catalog_index, 'generation') else -1
        cache_key = (scope_hash, dataset_id)

        with self._lock:
            mut_rev = self._mutation_rev
            cached = self._unread_cache.get(cache_key)
            if cached is not None:
                c_gen, c_mut, c_rev, c_horizon, c_hrowid, c_unread = cached
                if (c_mut, c_rev, c_horizon, c_hrowid) == (mut_rev, state_rev, horizon_ns, horizon_max_rowid) and cur_gen >= 0:
                    if c_gen == cur_gen:
                        return set(c_unread)
                    if hasattr(catalog_index, 'get_append_deltas'):
                        deltas = catalog_index.get_append_deltas(c_gen, cur_gen, dataset_id)
                        if deltas is not None:
                            updated = set(c_unread)
                            if deltas:
                                candidate_ids = []
                                for entry in deltas:
                                    did, cns, c_inv = entry[0], entry[1], entry[2]
                                    rid = int(entry[3]) if len(entry) > 3 else 0
                                    if c_inv == 1:
                                        candidate_ids.append(did)
                                    elif cns:
                                        if cns > horizon_ns:
                                            candidate_ids.append(did)
                                        elif horizon_max_rowid > 0 and rid > horizon_max_rowid and cns > enabled_ns:
                                            candidate_ids.append(did)
                                if candidate_ids:
                                    exp_read = self._explicit_read_subset_locked(scope_hash, candidate_ids)
                                    for did in candidate_ids:
                                        if did not in exp_read:
                                            updated.add(did)
                            self._unread_cache[cache_key] = (cur_gen, mut_rev, state_rev, horizon_ns, horizon_max_rowid, updated)
                            return set(updated)

        # Branch A: explicit is_read=0
        with self._lock:
            explicit_unread = [
                str(r[0])
                for r in self.db.execute(
                    'SELECT doc_id FROM ui_read_items WHERE scope_hash = ? AND is_read = 0',
                    (scope_hash,),
                )
            ]
        unread = set()
        if explicit_unread:
            unread.update(catalog_index.existing_knowledge_doc_ids(dataset_id, explicit_unread))

        # Branch B1 (post-horizon created_at_ns > horizon_ns)
        # Small post-horizon (<400 docs): single O(K log N) PK lookup.
        # Large uncompacted post-horizon (e.g. oldest docs unread): single covering-index scan
        # of ui_read_items (when <=100k read markers) instead of hundreds of random PK batches.
        scanned_batches = 0
        if hasattr(catalog_index, 'iter_knowledge_doc_ids_after_horizon'):
            batch_iter = iter(
                catalog_index.iter_knowledge_doc_ids_after_horizon(dataset_id, horizon_ns, batch_size=_MAX_IN_BATCH)
            )
            first_batch = next(batch_iter, None)
            if first_batch:
                scanned_batches = 1
                if len(first_batch) < _MAX_IN_BATCH:
                    with self._lock:
                        exp_read = self._explicit_read_subset_locked(scope_hash, first_batch)
                    for did in first_batch:
                        if did not in exp_read:
                            unread.add(did)
                else:
                    exp_read_all: set[str] | None = None
                    with self._lock:
                        rc_row = self.db.execute(
                            'SELECT COUNT(*) FROM ui_read_items WHERE scope_hash = ? AND is_read = 1',
                            (scope_hash,),
                        ).fetchone()
                        read_cnt = int(rc_row[0] or 0) if rc_row else 0
                        if read_cnt == 0:
                            exp_read_all = set()
                        elif read_cnt <= 100_000:
                            exp_read_all = {
                                str(r[0])
                                for r in self.db.execute(
                                    'SELECT doc_id FROM ui_read_items WHERE scope_hash = ? AND is_read = 1',
                                    (scope_hash,),
                                )
                            }
                    if exp_read_all is not None:
                        for did in first_batch:
                            if did not in exp_read_all:
                                unread.add(did)
                        for batch in batch_iter:
                            scanned_batches += 1
                            for did in batch:
                                if did not in exp_read_all:
                                    unread.add(did)
                        del exp_read_all
                    else:
                        with self._lock:
                            exp_read = self._explicit_read_subset_locked(scope_hash, first_batch)
                        for did in first_batch:
                            if did not in exp_read:
                                unread.add(did)
                        for batch in batch_iter:
                            scanned_batches += 1
                            with self._lock:
                                exp_read = self._explicit_read_subset_locked(scope_hash, batch)
                            for did in batch:
                                if did not in exp_read:
                                    unread.add(did)
        else:
            post_horizon_ids = [doc_id for doc_id, _ in catalog_index.knowledge_docs_after_horizon(dataset_id, horizon_ns)]
            for start in range(0, len(post_horizon_ids), _MAX_IN_BATCH):
                scanned_batches += 1
                batch = post_horizon_ids[start : start + _MAX_IN_BATCH]
                with self._lock:
                    exp_read = self._explicit_read_subset_locked(scope_hash, batch)
                for did in batch:
                    if did not in exp_read:
                        unread.add(did)

        # Branch B2 (late-arriving docs with cognee_rowid > horizon_max_rowid and enabled_at_ns < created_at_ns <= horizon_ns)
        if horizon_max_rowid > 0 and horizon_ns > enabled_ns and hasattr(catalog_index, 'knowledge_doc_ids_late_after_rowid'):
            late_ids = catalog_index.knowledge_doc_ids_late_after_rowid(
                dataset_id, horizon_max_rowid, enabled_ns, horizon_ns
            )
            if late_ids:
                with self._lock:
                    exp_read_late = self._explicit_read_subset_locked(scope_hash, late_ids)
                for did in late_ids:
                    if did not in exp_read_late:
                        unread.add(did)

        if scanned_batches >= 10:
            with self._lock:
                self.db.execute('PRAGMA shrink_memory')

        invalid_docs = catalog_index.knowledge_docs_invalid_created(dataset_id)
        if invalid_docs:
            with self._lock:
                exp_read_inv = self._explicit_read_subset_locked(scope_hash, invalid_docs)
            for did in invalid_docs:
                if did not in exp_read_inv:
                    unread.add(did)

        if cur_gen >= 0 and catalog_index.generation() == cur_gen:
            with self._lock:
                if self._mutation_rev == mut_rev:
                    if len(self._unread_cache) >= 64:
                        self._unread_cache.clear()
                    self._unread_cache[cache_key] = (cur_gen, mut_rev, state_rev, horizon_ns, horizon_max_rowid, set(unread))
        return unread

    def _compute_unread_snapshot_for_scope(
        self,
        scope_hash: str,
        dataset_id: str,
        catalog_index,
    ) -> tuple[set[str], int]:
        """Return ``(unread_ids, state_rev)`` guaranteed to originate from the exact same scope snapshot."""
        generation = getattr(catalog_index, 'generation', lambda: -1)
        for _ in range(3):
            with self._lock:
                mut_before = self._mutation_rev
                scope = self.get_scope(scope_hash)
            if scope is None:
                return set(), 0
            state_rev = int(scope.get('state_rev') or 0)
            catalog_gen = generation()
            unread = self._compute_unread_ids_for_scope(scope_hash, dataset_id, scope, catalog_index)
            if generation() != catalog_gen:
                continue
            with self._lock:
                current_scope = self.get_scope(scope_hash)
                if self._mutation_rev == mut_before and current_scope == scope:
                    return unread, state_rev
        raise PolicyError('reading_state_conflict', 409)

    def compute_unread_snapshot(
        self,
        reading_identity: str,
        project_id: str,
        dataset_id: str,
        catalog_index,
    ) -> tuple[set[str], int]:
        """Compute exact ``(unread_ids, state_rev)`` from a single consistent scope snapshot."""
        scope = self.ensure_scope(reading_identity, project_id, dataset_id, catalog_index=catalog_index)
        return self._compute_unread_snapshot_for_scope(scope['scope_hash'], dataset_id, catalog_index)

    def compute_unread_ids(
        self,
        reading_identity: str,
        project_id: str,
        dataset_id: str,
        catalog_index,
    ) -> set[str]:
        """Compute exact unread doc_id set in O(U log N) via sparse indexed branches."""
        unread, _ = self.compute_unread_snapshot(reading_identity, project_id, dataset_id, catalog_index)
        return unread

    def page_unread_flags(
        self,
        reading_identity: str,
        project_id: str,
        dataset_id: str,
        page_docs: list[dict],
        *,
        catalog_index=None,
    ) -> dict[str, bool]:
        """O(K) per-page unread flag resolution for <= 50 items on the current page."""
        if not page_docs:
            return {}
        scope = self.ensure_scope(reading_identity, project_id, dataset_id, catalog_index=catalog_index)
        scope_hash = scope['scope_hash']

        doc_ids = [str(d['doc_id']) for d in page_docs]
        explicit_map = {}
        with self._lock:
            cur_scope = self.get_scope(scope_hash) or scope
            horizon_ns = cur_scope['read_horizon_at_ns']
            enabled_ns = cur_scope['enabled_at_ns']
            horizon_max_rowid = int(cur_scope.get('horizon_max_rowid') or 0)
            for start in range(0, len(doc_ids), _MAX_IN_BATCH):
                batch = doc_ids[start : start + _MAX_IN_BATCH]
                ph = ','.join('?' for _ in batch)
                for did, is_r in self.db.execute(
                    f'SELECT doc_id, is_read FROM ui_read_items WHERE scope_hash = ? AND doc_id IN ({ph})',
                    (scope_hash, *batch),
                ):
                    explicit_map[str(did)] = bool(is_r)

        flags = {}
        for doc in page_docs:
            did = str(doc['doc_id'])
            if did in explicit_map:
                flags[did] = not explicit_map[did]
            elif int(doc.get('created_at_invalid', 0)) == 1:
                flags[did] = True
            else:
                cns = str(doc.get('created_at_ns') or '')
                if not cns:
                    flags[did] = True
                elif cns > horizon_ns:
                    flags[did] = True
                elif horizon_max_rowid > 0 and cns > enabled_ns:
                    seq = int(doc['doc_seq']) if 'doc_seq' in doc else int(doc.get('cognee_rowid') or 0)
                    flags[did] = bool(seq > horizon_max_rowid)
                else:
                    flags[did] = False
        return flags

    def compact_horizon(
        self,
        scope_hash: str,
        dataset_id: str,
        catalog_index,
        *,
        touched_doc_ids: list[str] | None = None,
    ) -> str:
        """Advance ``read_horizon_at_ns`` past contiguous read prefixes with late-arrival preservation."""
        with self._lock:
            scope = self.get_scope(scope_hash)
            if scope is None or not scope['initialized']:
                return ''
            enabled_ns = scope['enabled_at_ns']
            cur_horizon = scope['read_horizon_at_ns']
            cur_state_rev = scope['state_rev']
            cur_hrowid = int(scope.get('horizon_max_rowid') or 0)
            cur_gen = int(catalog_index.generation()) if hasattr(catalog_index, 'generation') else -1

            floor_key = (scope_hash, dataset_id)
            cached_floor = self._compact_floor_cache.get(floor_key)
            if (
                cached_floor is not None
                and cur_gen >= 0
                and cached_floor[0] == cur_gen
                and touched_doc_ids is not None
                and cached_floor[2] is not None
                and cached_floor[2] not in set(touched_doc_ids)
            ):
                return cur_horizon

            post_rows = catalog_index.knowledge_docs_after_horizon(dataset_id, cur_horizon)
            if not post_rows:
                return cur_horizon

            post_rows_asc = sorted(post_rows, key=lambda pair: (pair[1], pair[0]))
            post_ids = [did for did, _ in post_rows_asc]
            explicit_read = self._explicit_read_subset_locked(scope_hash, post_ids)

            first_unread_ts = None
            first_unread_doc = None
            for did, cns in post_rows_asc:
                if did not in explicit_read:
                    first_unread_ts = cns
                    first_unread_doc = did
                    break

            new_horizon = cur_horizon
            for _, cns in post_rows_asc:
                if first_unread_ts is not None and cns >= first_unread_ts:
                    break
                if cns > new_horizon:
                    new_horizon = cns

            if new_horizon <= cur_horizon:
                if cur_gen >= 0:
                    self._compact_floor_cache[floor_key] = (cur_gen, cur_state_rev, first_unread_doc)
                return cur_horizon

            max_seq_now = (
                int(catalog_index.max_knowledge_rowid(dataset_id))
                if hasattr(catalog_index, 'max_knowledge_rowid')
                else 0
            )
            new_hrowid = max(cur_hrowid, max_seq_now)

            # CRITICAL (R2-PY-P0-02 / CC2-P0-01): before advancing horizon_max_rowid, materialize ALL
            # late-arriving unread docs (doc_seq > cur_hrowid, enabled_at_ns < created_at_ns <= new_horizon)
            # as explicit is_read=0 inside the SAME transaction so they are never silently marked read!
            late_unread: list[str] = []
            if (
                cur_hrowid > 0
                and new_horizon > enabled_ns
                and hasattr(catalog_index, 'knowledge_doc_ids_late_after_rowid')
            ):
                late_ids = catalog_index.knowledge_doc_ids_late_after_rowid(
                    dataset_id, cur_hrowid, enabled_ns, new_horizon
                )
                if late_ids:
                    exp_read_late = self._explicit_read_subset_locked(scope_hash, late_ids)
                    late_unread = [did for did in late_ids if did not in exp_read_late]
            late_unread_set = set(late_unread)

            read_ids = [
                str(r[0])
                for r in self.db.execute(
                    'SELECT doc_id FROM ui_read_items WHERE scope_hash = ? AND is_read = 1',
                    (scope_hash,),
                ).fetchall()
            ]
            info_map = catalog_index.doc_created_at_info(dataset_id, read_ids) if read_ids else {}
            prunable = []
            for did in read_ids:
                if did in late_unread_set:
                    continue
                info = info_map.get(did)
                if not info:
                    continue
                cns_i, inv_i = info[0], info[1]
                seq_i = int(info[2]) if len(info) > 2 else 0
                if (
                    inv_i == 0
                    and cns_i
                    and cns_i <= new_horizon
                    and (new_hrowid == 0 or (seq_i > 0 and seq_i <= new_hrowid))
                ):
                    prunable.append(did)

            now_iso = _iso_now()
            self.db.execute('BEGIN IMMEDIATE')
            try:
                cur_row = self.db.execute(
                    'SELECT read_horizon_at_ns, state_rev FROM ui_read_scopes WHERE scope_hash = ?',
                    (scope_hash,),
                ).fetchone()
                if cur_row is None or int(cur_row[1] or 0) != cur_state_rev or str(cur_row[0]) >= new_horizon:
                    self.db.execute('ROLLBACK')
                    return str(cur_row[0]) if cur_row else cur_horizon
                if late_unread:
                    self.db.executemany(
                        '''INSERT OR IGNORE INTO ui_read_items(scope_hash, doc_id, is_read, updated_at)
                           VALUES (?,?,0,?)''',
                        [(scope_hash, did, now_iso) for did in late_unread],
                    )
                self.db.execute(
                    '''UPDATE ui_read_scopes
                       SET read_horizon_at_ns = ?, horizon_max_rowid = ?,
                           state_rev = state_rev + 1, updated_at = ?
                       WHERE scope_hash = ? AND state_rev = ?''',
                    (new_horizon, new_hrowid, now_iso, scope_hash, cur_state_rev),
                )
                for start in range(0, len(prunable), _MAX_IN_BATCH):
                    batch = prunable[start : start + _MAX_IN_BATCH]
                    ph = ','.join('?' for _ in batch)
                    self.db.execute(
                        f'DELETE FROM ui_read_items WHERE scope_hash = ? AND is_read = 1 AND doc_id IN ({ph})',
                        (scope_hash, *batch),
                    )
                self.db.execute('COMMIT')
                self._bump_mutation_rev_locked()
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise
            if cur_gen >= 0:
                self._compact_floor_cache[floor_key] = (cur_gen, cur_state_rev + 1, first_unread_doc)
            return new_horizon

    def mark_read(
        self,
        reading_identity: str,
        project_id: str,
        dataset_id: str,
        doc_ids: list[str],
        *,
        catalog_index=None,
    ) -> dict:
        scope = self.ensure_scope(reading_identity, project_id, dataset_id, catalog_index=catalog_index)
        scope_hash = scope['scope_hash']
        clean_ids = list(dict.fromkeys(str(d).strip() for d in doc_ids if isinstance(d, str) and d.strip()))
        now_iso = _iso_now()
        state_rev = int(scope.get('state_rev') or 0)
        if clean_ids:
            with self._lock:
                self.db.execute('BEGIN IMMEDIATE')
                try:
                    self.db.executemany(
                        '''INSERT INTO ui_read_items(scope_hash, doc_id, is_read, updated_at)
                           VALUES (?,?,1,?)
                           ON CONFLICT(scope_hash, doc_id) DO UPDATE SET
                             is_read = 1,
                             updated_at = excluded.updated_at''',
                        [(scope_hash, did, now_iso) for did in clean_ids],
                    )
                    self.db.execute(
                        '''UPDATE ui_read_scopes
                           SET state_rev = state_rev + 1,
                               is_auto_baseline = 0,
                               updated_at = ?
                           WHERE scope_hash = ?''',
                        (now_iso, scope_hash),
                    )
                    rev_row = self.db.execute(
                        'SELECT state_rev FROM ui_read_scopes WHERE scope_hash = ?',
                        (scope_hash,),
                    ).fetchone()
                    if rev_row is not None:
                        state_rev = int(rev_row[0] or 0)
                    self.db.execute('COMMIT')
                    self._bump_mutation_rev_locked()
                except Exception:
                    if self.db.in_transaction:
                        self.db.execute('ROLLBACK')
                    raise
        horizon = scope['read_horizon_at_ns']
        unread_count = 0
        unread_count_known = False
        if catalog_index is not None:
            horizon = self.compact_horizon(
                scope_hash,
                dataset_id,
                catalog_index,
                touched_doc_ids=clean_ids,
            )
            unread_ids, state_rev = self.compute_unread_snapshot(
                reading_identity, project_id, dataset_id, catalog_index
            )
            unread_count = len(unread_ids)
            unread_count_known = True
        return {
            'scope_hash': scope_hash,
            'marked': len(clean_ids),
            'unread_count': unread_count,
            'unread_count_known': unread_count_known,
            'read_horizon_at_ns': horizon,
            'state_rev': state_rev,
        }

    def mark_all_read(
        self,
        reading_identity: str,
        project_id: str,
        dataset_id: str,
        *,
        catalog_index=None,
    ) -> dict:
        scope = self.ensure_scope(reading_identity, project_id, dataset_id, catalog_index=catalog_index)
        scope_hash = scope['scope_hash']
        new_horizon = scope['read_horizon_at_ns']
        state_rev = int(scope.get('state_rev') or 0)
        committed = False
        unread_count = 0

        for _attempt in range(3):
            with self._lock:
                cur_scope = self.get_scope(scope_hash)
                if cur_scope is None:
                    cur_scope = scope
                expected_rev = int(cur_scope.get('state_rev') or 0)
                cur_horizon = cur_scope['read_horizon_at_ns']
                cur_hseq = int(cur_scope.get('horizon_max_rowid') or 0)
                existing_read_ids = (
                    [
                        str(r[0])
                        for r in self.db.execute(
                            'SELECT doc_id FROM ui_read_items WHERE scope_hash = ? AND is_read = 1',
                            (scope_hash,),
                        ).fetchall()
                    ]
                    if catalog_index is not None
                    else []
                )

            new_horizon = max(cur_horizon, now_utc_ns())
            new_horizon_seq = cur_hseq
            invalid_docs: list[str] = []
            prunable: list[str] = []
            if catalog_index is not None:
                max_created = catalog_index.max_knowledge_created_at_ns(dataset_id)
                if max_created and max_created > new_horizon:
                    new_horizon = max_created
                if hasattr(catalog_index, 'max_knowledge_rowid'):
                    max_seq = int(catalog_index.max_knowledge_rowid(dataset_id) or 0)
                    if max_seq > new_horizon_seq:
                        new_horizon_seq = max_seq
                invalid_docs = catalog_index.knowledge_docs_invalid_created(dataset_id)
                invalid_set = set(invalid_docs)
                if existing_read_ids:
                    info_map = catalog_index.doc_created_at_info(dataset_id, existing_read_ids)
                    for did in existing_read_ids:
                        if did in invalid_set:
                            continue
                        info = info_map.get(did)
                        if not info:
                            continue
                        cns_i, inv_i = info[0], info[1]
                        seq_i = int(info[2]) if len(info) > 2 else 0
                        if (
                            inv_i == 0
                            and cns_i
                            and cns_i <= new_horizon
                            and (new_horizon_seq == 0 or (seq_i > 0 and seq_i <= new_horizon_seq))
                        ):
                            prunable.append(did)

            now_iso = _iso_now()
            # Single atomic BEGIN IMMEDIATE transaction with state_rev CAS (horizon + delete is_read=0 +
            # invalid_docs is_read=1 + prune redundant is_read=1).
            with self._lock:
                self.db.execute('BEGIN IMMEDIATE')
                try:
                    cur = self.db.execute(
                        '''UPDATE ui_read_scopes
                           SET read_horizon_at_ns = CASE
                                 WHEN ? > read_horizon_at_ns THEN ?
                                 ELSE read_horizon_at_ns
                               END,
                               horizon_max_rowid = CASE
                                 WHEN ? > horizon_max_rowid THEN ?
                                 ELSE horizon_max_rowid
                               END,
                               state_rev = state_rev + 1,
                               is_auto_baseline = 0,
                               updated_at = ?
                           WHERE scope_hash = ? AND state_rev = ?''',
                        (
                            new_horizon,
                            new_horizon,
                            new_horizon_seq,
                            new_horizon_seq,
                            now_iso,
                            scope_hash,
                            expected_rev,
                        ),
                    )
                    if cur.rowcount != 1:
                        self.db.execute('ROLLBACK')
                        continue
                    self.db.execute(
                        'DELETE FROM ui_read_items WHERE scope_hash = ? AND is_read = 0',
                        (scope_hash,),
                    )
                    if invalid_docs:
                        self.db.executemany(
                            '''INSERT INTO ui_read_items(scope_hash, doc_id, is_read, updated_at)
                               VALUES (?,?,1,?)
                               ON CONFLICT(scope_hash, doc_id) DO UPDATE SET
                                 is_read = 1, updated_at = excluded.updated_at''',
                            [(scope_hash, did, now_iso) for did in invalid_docs],
                        )
                    for start in range(0, len(prunable), _MAX_IN_BATCH):
                        batch = prunable[start : start + _MAX_IN_BATCH]
                        ph = ','.join('?' for _ in batch)
                        self.db.execute(
                            f'DELETE FROM ui_read_items WHERE scope_hash = ? AND is_read = 1 AND doc_id IN ({ph})',
                            (scope_hash, *batch),
                        )
                    rev_row = self.db.execute(
                        'SELECT read_horizon_at_ns, state_rev FROM ui_read_scopes WHERE scope_hash = ?',
                        (scope_hash,),
                    ).fetchone()
                    if rev_row is not None:
                        new_horizon = str(rev_row[0])
                        state_rev = int(rev_row[1] or 0)
                    self._bump_mutation_rev_locked()
                    if catalog_index is not None:
                        # Count against the pending reading-state transaction and retry
                        # the catalog scan if its generation moves during the read.
                        unread, state_rev = self._compute_unread_snapshot_for_scope(scope_hash, dataset_id, catalog_index)
                        unread_count = len(unread)
                    self.db.execute('COMMIT')
                    committed = True
                    break
                except Exception:
                    if self.db.in_transaction:
                        self.db.execute('ROLLBACK')
                        self._bump_mutation_rev_locked()
                    raise

        if not committed:
            raise PolicyError('reading_state_conflict', 409)

        return {
            'scope_hash': scope_hash,
            'unread_count': unread_count,
            'unread_count_known': catalog_index is not None,
            'read_horizon_at_ns': new_horizon,
            'state_rev': state_rev,
        }
