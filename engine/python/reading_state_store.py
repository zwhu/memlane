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

    def __init__(self, path):
        self.path = Path(path)
        self._lock = threading.RLock()
        self._mutation_rev = 0
        self._unread_cache: dict[tuple[str, str], tuple[int, int, str, set[str]]] = {}
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
        self.db.execute('PRAGMA cache_size=-2048')
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
                    created_at TEXT NOT NULL
                );
            ''')

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
                          enabled_at_ns, read_horizon_at_ns, legacy_file_sha256,
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
            'legacy_file_sha256': str(row[6]),
            'legacy_mtime_ns': int(row[7]),
            'updated_at': str(row[8]),
        }

    def ensure_scope(
        self,
        reading_identity: str,
        project_id: str,
        dataset_id: str,
        *,
        catalog_index=None,
    ) -> dict:
        """Return scope row, initializing first-use baseline read horizon if absent."""
        scope_hash = knowledge_read_scope_hash(reading_identity, project_id, dataset_id)
        existing = self.get_scope(scope_hash)
        if existing is not None and existing['initialized']:
            return existing

        horizon = now_utc_ns()
        if catalog_index is not None:
            max_created = catalog_index.max_knowledge_created_at_ns(dataset_id)
            if max_created and max_created > horizon:
                horizon = max_created
        now_iso = _iso_now()
        with self._lock:
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
                            enabled_at_ns, read_horizon_at_ns, legacy_file_sha256,
                            legacy_mtime_ns, updated_at)
                           VALUES (?,?,?,1,?,?,'',0,?)''',
                        (scope_hash, project_id, dataset_id, horizon, horizon, now_iso),
                    )
                elif int(row[0]) == 0:
                    self.db.execute(
                        '''UPDATE ui_read_scopes
                           SET initialized = 1, enabled_at_ns = ?, read_horizon_at_ns = ?, updated_at = ?
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
        On re-upgrade when ``T_old != T_new``, materializes implicit-read documents in
        ``(min(T_old, T_new), max(T_old, T_new)]`` as explicit ``is_read=1`` before aligning
        ``enabled_at_ns`` so zero phantom unreads are ever produced.
        """
        now_iso = _iso_now()
        existing = self.db.execute(
            '''SELECT initialized, enabled_at_ns, read_horizon_at_ns
               FROM ui_read_scopes WHERE scope_hash = ?''',
            (scope_hash,),
        ).fetchone()

        if existing is None:
            effective_init = 1 if initialized else 0
            self.db.execute(
                '''INSERT INTO ui_read_scopes
                   (scope_hash, project_id, dataset_id, initialized,
                    enabled_at_ns, read_horizon_at_ns, legacy_file_sha256,
                    legacy_mtime_ns, updated_at)
                   VALUES (?,?,?,?,?,?,?,?,?)''',
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

        # Load current explicit items for conflict resolution (explicit true always wins!)
        existing_items = {
            str(r[0]): int(r[1])
            for r in self.db.execute(
                'SELECT doc_id, is_read FROM ui_read_items WHERE scope_hash = ?',
                (scope_hash,),
            ).fetchall()
        }

        final_init = 1 if (old_init or initialized) else 0
        final_enabled_ns = old_enabled_ns
        final_horizon_ns = old_horizon_ns

        # Conflict merge when timestamps diverge: never blindly take min(T1, T2)!
        if old_init and initialized and old_enabled_ns and new_enabled_ns and old_enabled_ns != new_enabled_ns:
            low_ns, high_ns = (old_enabled_ns, new_enabled_ns) if old_enabled_ns < new_enabled_ns else (new_enabled_ns, old_enabled_ns)
            older_was_old = (old_enabled_ns < new_enabled_ns)
            if catalog_index is not None:
                in_between = catalog_index.knowledge_docs_in_range(dataset_id, low_ns, high_ns)
                for did in in_between:
                    # On the side with the LARGER timestamp (high_ns), any doc not explicitly marked unread
                    # was implicitly READ! Preserve that read status explicitly before aligning to low_ns.
                    if older_was_old:
                        # New file had high_ns: if not explicitly False in imported_items, it was read on new side
                        if imported_items.get(did) is not False:
                            imported_items[did] = True
                    else:
                        # Old DB had high_ns: if not explicitly 0 in existing_items, it was read on old side
                        if existing_items.get(did) != 0:
                            imported_items[did] = True
            final_enabled_ns = low_ns
            if final_horizon_ns > low_ns:
                # Re-anchor horizon safely to low_ns; compaction will re-advance it immediately.
                final_horizon_ns = low_ns
        elif not old_init and initialized and new_enabled_ns:
            final_enabled_ns = new_enabled_ns
            final_horizon_ns = new_enabled_ns

        self.db.execute(
            '''UPDATE ui_read_scopes
               SET initialized = ?, enabled_at_ns = ?, read_horizon_at_ns = ?,
                   legacy_file_sha256 = ?, legacy_mtime_ns = ?, updated_at = ?
               WHERE scope_hash = ?''',
            (
                final_init,
                final_enabled_ns,
                final_horizon_ns,
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
        """Single-transaction atomic migration of ``knowledge-read-<hash>.json`` in ``state_dir``."""
        scope_hash = knowledge_read_scope_hash(reading_identity, project_id, dataset_id)
        legacy_path = Path(state_dir) / f'knowledge-read-{scope_hash}.json'
        try:
            info = legacy_path.lstat()
        except FileNotFoundError:
            self.ensure_scope(reading_identity, project_id, dataset_id, catalog_index=catalog_index)
            return {
                'project_id': project_id,
                'dataset_id': dataset_id,
                'scope_hash': scope_hash,
                'status': 'no_legacy_file',
                'imported_items': 0,
            }
        except OSError as exc:
            raise PolicyError('legacy_reading_state_unreadable', 503) from exc

        if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
            raise PolicyError('legacy_reading_state_invalid', 400)
        if info.st_size > MAX_LEGACY_READ_JSON_BYTES:
            raise PolicyError('legacy_reading_state_too_large', 413)

        raw_bytes = legacy_path.read_bytes()
        file_sha256 = hashlib.sha256(raw_bytes).hexdigest()
        existing = self.get_scope(scope_hash)
        if existing is not None and existing['legacy_file_sha256'] == file_sha256:
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
            # Exact match for knowledge_unread.go:54-60: zero enabled_at falls back to file mtime UTC
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

        # Preserve backup without deleting original data
        backup_path = Path(str(legacy_path) + '.migrated.bak')
        try:
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

    def commit_import(self, batch_id: str, *, catalog_index=None) -> dict:
        with self._lock:
            batch = self.db.execute(
                '''SELECT scope_hash, project_id, dataset_id, initialized, enabled_at_ns
                   FROM ui_read_migration_batches WHERE batch_id = ?''',
                (batch_id,),
            ).fetchone()
            if batch is None:
                raise PolicyError('import_batch_not_found', 404)
            scope_hash, project_id, dataset_id, initialized, enabled_ns = (
                str(batch[0]),
                str(batch[1]),
                str(batch[2]),
                bool(batch[3]),
                str(batch[4]),
            )
            staged = {
                str(r[0]): bool(r[1])
                for r in self.db.execute(
                    'SELECT doc_id, is_read FROM ui_read_migration_staging WHERE batch_id = ?',
                    (batch_id,),
                ).fetchall()
            }
            self.db.execute('BEGIN IMMEDIATE')
            try:
                self._merge_imported_scope_locked(
                    scope_hash=scope_hash,
                    project_id=project_id,
                    dataset_id=dataset_id,
                    initialized=initialized,
                    new_enabled_ns=enabled_ns,
                    imported_items=staged,
                    legacy_sha256='',
                    legacy_mtime_ns=0,
                    catalog_index=catalog_index,
                )
                self.db.execute('DELETE FROM ui_read_migration_staging WHERE batch_id = ?', (batch_id,))
                self.db.execute('DELETE FROM ui_read_migration_batches WHERE batch_id = ?', (batch_id,))
                self.db.execute('COMMIT')
                self._bump_mutation_rev_locked()
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise

        if catalog_index is not None:
            self.compact_horizon(scope_hash, dataset_id, catalog_index)
        return {'scope_hash': scope_hash, 'committed': len(staged), 'dataset_id': dataset_id}

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

    def compute_unread_ids(
        self,
        reading_identity: str,
        project_id: str,
        dataset_id: str,
        catalog_index,
    ) -> set[str]:
        """Compute exact unread doc_id set in O(U log N) via 3 sparse indexed branches,
        with generation-keyed O(1) cache & append-only delta updates (zero full-scope RAM cache).
        """
        scope = self.ensure_scope(reading_identity, project_id, dataset_id, catalog_index=catalog_index)
        scope_hash = scope['scope_hash']
        horizon_ns = scope['read_horizon_at_ns']
        cur_gen = int(catalog_index.generation()) if hasattr(catalog_index, 'generation') else -1
        cache_key = (scope_hash, dataset_id)

        with self._lock:
            mut_rev = self._mutation_rev
            cached = self._unread_cache.get(cache_key)
            if cached is not None:
                c_gen, c_mut, c_horizon, c_unread = cached
                if c_mut == mut_rev and c_horizon == horizon_ns and cur_gen >= 0:
                    if c_gen == cur_gen:
                        return set(c_unread)
                    if hasattr(catalog_index, 'get_append_deltas'):
                        deltas = catalog_index.get_append_deltas(c_gen, cur_gen, dataset_id)
                        if deltas is not None:
                            updated = set(c_unread)
                            if deltas:
                                candidate_ids = [
                                    did
                                    for did, cns, c_inv in deltas
                                    if c_inv == 1 or (cns and cns > horizon_ns)
                                ]
                                if candidate_ids:
                                    exp_read = self._explicit_read_subset_locked(scope_hash, candidate_ids)
                                    for did in candidate_ids:
                                        if did not in exp_read:
                                            updated.add(did)
                            self._unread_cache[cache_key] = (cur_gen, mut_rev, horizon_ns, updated)
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

        # Branch B (post-horizon) streamed in bounded 400-row chunks + Branch C (invalid created_at)
        scanned_batches = 0
        if hasattr(catalog_index, 'iter_knowledge_doc_ids_after_horizon'):
            for batch in catalog_index.iter_knowledge_doc_ids_after_horizon(dataset_id, horizon_ns, batch_size=_MAX_IN_BATCH):
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

        if cur_gen >= 0:
            with self._lock:
                if self._mutation_rev == mut_rev:
                    if len(self._unread_cache) >= 64:
                        self._unread_cache.clear()
                    self._unread_cache[cache_key] = (cur_gen, mut_rev, horizon_ns, set(unread))
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
        horizon_ns = scope['read_horizon_at_ns']

        doc_ids = [str(d['doc_id']) for d in page_docs]
        explicit_map = {}
        with self._lock:
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
                flags[did] = bool(cns and cns > horizon_ns)
        return flags

    def compact_horizon(self, scope_hash: str, dataset_id: str, catalog_index) -> str:
        """Advance ``read_horizon_at_ns`` past contiguous read prefixes and prune redundant is_read=1 rows."""
        scope = self.get_scope(scope_hash)
        if scope is None or not scope['initialized']:
            return ''
        cur_horizon = scope['read_horizon_at_ns']
        post_rows = catalog_index.knowledge_docs_after_horizon(dataset_id, cur_horizon)
        if not post_rows:
            return cur_horizon

        # Sort post-horizon docs ascending by created_at_ns so we find the earliest unread boundary
        post_rows_asc = sorted(post_rows, key=lambda pair: (pair[1], pair[0]))
        post_ids = [did for did, _ in post_rows_asc]
        with self._lock:
            explicit_read = set()
            for start in range(0, len(post_ids), _MAX_IN_BATCH):
                batch = post_ids[start : start + _MAX_IN_BATCH]
                ph = ','.join('?' for _ in batch)
                for (did,) in self.db.execute(
                    f'''SELECT doc_id FROM ui_read_items
                        WHERE scope_hash = ? AND is_read = 1 AND doc_id IN ({ph})''',
                    (scope_hash, *batch),
                ):
                    explicit_read.add(str(did))

        # Find the earliest unread timestamp; we can only advance horizon strictly BELOW it
        # (so docs sharing the exact same nanosecond timestamp as an unread doc stay > new_horizon).
        first_unread_ts = None
        for did, cns in post_rows_asc:
            if did not in explicit_read:
                first_unread_ts = cns
                break

        new_horizon = cur_horizon
        for _, cns in post_rows_asc:
            if first_unread_ts is not None and cns >= first_unread_ts:
                break
            if cns > new_horizon:
                new_horizon = cns

        if new_horizon == cur_horizon:
            return cur_horizon

        # Prune redundant is_read=1 rows whose catalog created_at_ns <= new_horizon (and valid).
        # Keep is_read=1 rows for created_at_invalid=1 and orphaned doc_ids not in catalog_docs!
        with self._lock:
            read_ids = [
                str(r[0])
                for r in self.db.execute(
                    'SELECT doc_id FROM ui_read_items WHERE scope_hash = ? AND is_read = 1',
                    (scope_hash,),
                ).fetchall()
            ]
        info_map = catalog_index.doc_created_at_info(dataset_id, read_ids) if read_ids else {}
        prunable = [
            did
            for did in read_ids
            if did in info_map and info_map[did][1] == 0 and info_map[did][0] and info_map[did][0] <= new_horizon
        ]

        now_iso = _iso_now()
        with self._lock:
            self.db.execute('BEGIN IMMEDIATE')
            try:
                self.db.execute(
                    'UPDATE ui_read_scopes SET read_horizon_at_ns = ?, updated_at = ? WHERE scope_hash = ?',
                    (new_horizon, now_iso, scope_hash),
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
                    self.db.execute('COMMIT')
                    self._bump_mutation_rev_locked()
                except Exception:
                    if self.db.in_transaction:
                        self.db.execute('ROLLBACK')
                    raise
        horizon = scope['read_horizon_at_ns']
        unread_count = 0
        if catalog_index is not None:
            horizon = self.compact_horizon(scope_hash, dataset_id, catalog_index)
            unread_count = len(self.compute_unread_ids(reading_identity, project_id, dataset_id, catalog_index))
        return {
            'scope_hash': scope_hash,
            'marked': len(clean_ids),
            'unread_count': unread_count,
            'read_horizon_at_ns': horizon,
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
        new_horizon = max(scope['read_horizon_at_ns'], now_utc_ns())
        invalid_docs = []
        if catalog_index is not None:
            max_created = catalog_index.max_knowledge_created_at_ns(dataset_id)
            if max_created and max_created > new_horizon:
                new_horizon = max_created
            invalid_docs = catalog_index.knowledge_docs_invalid_created(dataset_id)

        now_iso = _iso_now()
        with self._lock:
            self.db.execute('BEGIN IMMEDIATE')
            try:
                # All valid-timestamp docs <= new_horizon are now implicitly read; delete explicit rows
                # for valid docs and keep only explicit is_read=1 markers for invalid-timestamp docs!
                self.db.execute(
                    'UPDATE ui_read_scopes SET read_horizon_at_ns = ?, updated_at = ? WHERE scope_hash = ?',
                    (new_horizon, now_iso, scope_hash),
                )
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
                self.db.execute('COMMIT')
                self._bump_mutation_rev_locked()
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise
        if catalog_index is not None:
            # Prune redundant is_read=1 rows below new_horizon
            with self._lock:
                read_ids = [
                    str(r[0])
                    for r in self.db.execute(
                        'SELECT doc_id FROM ui_read_items WHERE scope_hash = ? AND is_read = 1',
                        (scope_hash,),
                    ).fetchall()
                ]
            info_map = catalog_index.doc_created_at_info(dataset_id, read_ids) if read_ids else {}
            prunable = [
                did for did in read_ids
                if did in info_map and info_map[did][1] == 0 and info_map[did][0] and info_map[did][0] <= new_horizon
            ]
            if prunable:
                with self._lock:
                    self.db.execute('BEGIN IMMEDIATE')
                    try:
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
        return {
            'scope_hash': scope_hash,
            'unread_count': 0,
            'read_horizon_at_ns': new_horizon,
        }
