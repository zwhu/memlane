"""External SQLite WAL session cross-project drift detection and control API."""
import copy
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import sqlite3
import stat
import threading
import uuid

from aiohttp import web
from project_policy import PolicyError

_WRITE_TOOLS = frozenset({
    'edit_file', 'write_file', 'edit', 'write', 'apply_patch',
    'replace_file_content', 'multi_replace_file_content', 'write_to_file', 'notebookedit',
})
_READ_TOOLS = frozenset({
    'read_file', 'read', 'view_file', 'view_image', 'imageview',
    'list_dir', 'find_by_name', 'grep_search', 'glob', 'grep',
})
_COMMAND_TOOLS = frozenset({'run_command', 'bash', 'exec_command', 'run_shell_command'})
_EXPLICIT_PATH_KEYS = (
    'Path', 'path', 'file_path', 'filePath', 'TargetFile',
    'AbsolutePath', 'SearchPath', 'DirectoryPath', 'cwd', 'Cwd',
)
_PATCH_FILE_RE = re.compile(r'^\*\*\* (?:Update|Add|Delete) File:\s*(/[^\r\n]+)', re.MULTILINE)
_COMMAND_PATH_RE = re.compile(r'(?:^|[\s"\'=:(])(/[A-Za-z0-9._~/@+-]+)')
_MAX_COMMAND_SCAN_CHARS = 16384
_MAX_SESSION_ID_LEN = 512


def _iso_now():
    return datetime.now(timezone.utc).isoformat(timespec='microseconds')


def _uuid_str(value):
    if not isinstance(value, str):
        return None
    try:
        return str(uuid.UUID(value.strip()))
    except (ValueError, AttributeError):
        return None


def _normalize_alias(raw_alias):
    if not isinstance(raw_alias, str) or not raw_alias.startswith('/'):
        return None
    norm = os.path.normpath(raw_alias)
    if norm != '/':
        norm = norm.rstrip('/')
    return norm if norm.startswith('/') else None


def build_project_alias_index(projects_map):
    """Build a longest-prefix-first alias index from registry ``projects``."""
    if not isinstance(projects_map, dict):
        return []
    seen = set()
    entries = []
    for proj in projects_map.values():
        if not isinstance(proj, dict):
            continue
        project_id = str(proj.get('id') or '').strip()
        dataset_id = _uuid_str(proj.get('dataset_id'))
        aliases = proj.get('aliases')
        if not project_id or not dataset_id or not isinstance(aliases, list):
            continue
        name = str(proj.get('name') or project_id)
        for raw_alias in aliases:
            if not isinstance(raw_alias, str) or not raw_alias.startswith('/'):
                continue
            candidates = [_normalize_alias(raw_alias)]
            try:
                candidates.append(_normalize_alias(os.path.realpath(raw_alias)))
            except OSError:
                pass
            for norm in candidates:
                if not norm:
                    continue
                key = (norm, project_id)
                if key in seen:
                    continue
                seen.add(key)
                entries.append((norm, project_id, dataset_id, name))
    entries.sort(key=lambda item: (-len(item[0]), item[0], item[1]))
    return entries


def match_project_for_path(raw_path, alias_index):
    """Return the longest-prefix matched project ID for an absolute path."""
    if not isinstance(raw_path, str) or not raw_path.startswith('/') or not alias_index:
        return None
    norm = os.path.normpath(raw_path)
    for alias, project_id, _dataset_id, _name in alias_index:
        if alias == '/':
            return project_id
        if norm == alias or norm.startswith(alias + '/'):
            return project_id
    return None


def extract_trace_project_hits(entry, alias_index):
    """Extract deduplicated ``(project_id, kind)`` pairs from one trace entry."""
    if not isinstance(entry, dict) or entry.get('type') != 'trace' or not alias_index:
        return set()
    params = entry.get('method_params')
    if not isinstance(params, dict):
        return set()
    fn = str(entry.get('origin_function') or '').strip().lower()
    hits = set()
    explicit_kind = 'write' if fn in _WRITE_TOOLS else 'read'
    for key in _EXPLICIT_PATH_KEYS:
        val = params.get(key)
        if isinstance(val, str) and val.startswith('/'):
            pid = match_project_for_path(val, alias_index)
            if pid:
                hits.add((pid, explicit_kind))
    if fn == 'apply_patch':
        patch_text = params.get('command')
        if isinstance(patch_text, str) and patch_text:
            for raw_path in _PATCH_FILE_RE.findall(patch_text[:_MAX_COMMAND_SCAN_CHARS]):
                pid = match_project_for_path(raw_path.strip(), alias_index)
                if pid:
                    hits.add((pid, 'write'))
    elif fn in _COMMAND_TOOLS:
        cmd_text = params.get('CommandLine')
        if not isinstance(cmd_text, str):
            cmd_text = params.get('command')
        if isinstance(cmd_text, str) and cmd_text:
            for raw_path in _COMMAND_PATH_RE.findall(cmd_text[:_MAX_COMMAND_SCAN_CHARS]):
                pid = match_project_for_path(raw_path, alias_index)
                if pid:
                    hits.add((pid, 'command'))
    return hits


class RegistryProjectIndex:
    """Stat-cached, read-only project alias loader for ``official_service_dev``."""

    def __init__(self, registry_file):
        self.path = Path(registry_file)
        self._lock = threading.Lock()
        self._sig = None
        self._projects = {}
        self._alias_index = []

    def alias_index(self):
        with self._lock:
            try:
                info = self.path.lstat()
                if not stat.S_ISREG(info.st_mode):
                    return self._alias_index
                sig = (info.st_mtime_ns, info.st_size)
                if sig != self._sig:
                    fd = os.open(self.path, os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0))
                    try:
                        raw = os.fdopen(fd, 'rb').read()
                    except Exception:
                        try:
                            os.close(fd)
                        except OSError:
                            pass
                        raise
                    loaded = json.loads(raw.decode('utf-8'))
                    projects = loaded.get('projects') if isinstance(loaded, dict) else None
                    if isinstance(projects, dict):
                        self._projects = copy.deepcopy(projects)
                        self._alias_index = build_project_alias_index(self._projects)
                        self._sig = sig
            except (OSError, UnicodeDecodeError, ValueError, TypeError):
                return self._alias_index
            return list(self._alias_index)


class SessionDriftStore:
    """Private ``0600`` SQLite WAL ledger tracking cross-project session drift."""

    def __init__(self, path):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        os.chmod(self.path.parent, 0o700)
        if self.path.is_symlink():
            raise ValueError('session drift database must not be a symlink')
        fd = os.open(self.path, os.O_CREAT | os.O_RDWR | getattr(os, 'O_NOFOLLOW', 0), 0o600)
        os.close(fd)
        os.chmod(self.path, 0o600)
        self._lock = threading.Lock()
        self.db = sqlite3.connect(self.path, isolation_level=None, check_same_thread=False)
        self.db.execute('PRAGMA journal_mode=WAL')
        self.db.execute('PRAGMA journal_size_limit=4194304')
        self.db.execute('PRAGMA wal_autocheckpoint=1000')
        self.db.execute('PRAGMA synchronous=NORMAL')
        self.db.execute('PRAGMA busy_timeout=5000')
        self.db.executescript('''
          CREATE TABLE IF NOT EXISTS drift_meta (
            singleton INTEGER PRIMARY KEY CHECK(singleton=1),
            schema_version INTEGER NOT NULL CHECK(schema_version=1),
            started_at TEXT NOT NULL
          );
          CREATE TABLE IF NOT EXISTS session_project_hits (
            bound_dataset_id TEXT NOT NULL,
            session_id TEXT NOT NULL,
            target_project_id TEXT NOT NULL,
            read_count INTEGER NOT NULL DEFAULT 0,
            write_count INTEGER NOT NULL DEFAULT 0,
            command_count INTEGER NOT NULL DEFAULT 0,
            updated_at TEXT NOT NULL,
            PRIMARY KEY(bound_dataset_id, session_id, target_project_id)
          );
          CREATE TABLE IF NOT EXISTS session_drift_seen (
            bound_dataset_id TEXT NOT NULL,
            session_id TEXT NOT NULL,
            message_id TEXT NOT NULL,
            recorded_at TEXT NOT NULL,
            PRIMARY KEY(bound_dataset_id, session_id, message_id)
          );
          CREATE TABLE IF NOT EXISTS session_drift_disposition (
            bound_dataset_id TEXT NOT NULL,
            session_id TEXT NOT NULL,
            status TEXT NOT NULL CHECK(status IN ('dismissed')),
            target_project_id TEXT NOT NULL,
            updated_at TEXT NOT NULL,
            PRIMARY KEY(bound_dataset_id, session_id)
          );
        ''')
        with self._lock:
            self.db.execute('BEGIN IMMEDIATE')
            try:
                self.db.execute('INSERT OR IGNORE INTO drift_meta VALUES(1,1,?)', (_iso_now(),))
                self.db.execute('COMMIT')
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise

    def observe_trace(self, bound_dataset_id, session_id, entry, alias_index, *, message_id=None):
        dataset = _uuid_str(bound_dataset_id)
        if not dataset or not isinstance(session_id, str) or not 1 <= len(session_id) <= _MAX_SESSION_ID_LEN:
            return False
        hits = extract_trace_project_hits(entry, alias_index)
        if not hits:
            return False
        deltas = {}
        for pid, kind in hits:
            read_inc, write_inc, cmd_inc = deltas.get(pid, (0, 0, 0))
            if kind == 'write':
                write_inc += 1
            elif kind == 'command':
                cmd_inc += 1
            else:
                read_inc += 1
            deltas[pid] = (read_inc, write_inc, cmd_inc)
        now = _iso_now()
        msg = str(message_id).strip() if isinstance(message_id, str) and message_id.strip() else None
        if msg and len(msg) > _MAX_SESSION_ID_LEN:
            msg = msg[:_MAX_SESSION_ID_LEN]
        with self._lock:
            self.db.execute('BEGIN IMMEDIATE')
            try:
                if msg is not None:
                    cur = self.db.execute(
                        '''INSERT OR IGNORE INTO session_drift_seen
                           (bound_dataset_id, session_id, message_id, recorded_at)
                           VALUES(?,?,?,?)''',
                        (dataset, session_id, msg, now),
                    )
                    if cur.rowcount == 0:
                        self.db.execute('COMMIT')
                        return False
                for pid, (read_inc, write_inc, cmd_inc) in deltas.items():
                    self.db.execute(
                        '''INSERT INTO session_project_hits
                           (bound_dataset_id, session_id, target_project_id,
                            read_count, write_count, command_count, updated_at)
                           VALUES(?,?,?,?,?,?,?)
                           ON CONFLICT(bound_dataset_id, session_id, target_project_id) DO UPDATE SET
                             read_count = read_count + excluded.read_count,
                             write_count = write_count + excluded.write_count,
                             command_count = command_count + excluded.command_count,
                             updated_at = excluded.updated_at''',
                        (dataset, session_id, pid, read_inc, write_inc, cmd_inc, now),
                    )
                self.db.execute('COMMIT')
                return True
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise

    def evaluate_project_drifts(self, projects_map, *, project_id=None, include_dismissed=False, limit=20):
        by_id = {}
        by_dataset = {}
        if isinstance(projects_map, dict):
            for key, proj in projects_map.items():
                if not isinstance(proj, dict):
                    continue
                pid = str(proj.get('id') or key or '').strip()
                ds = _uuid_str(proj.get('dataset_id'))
                if not pid or not ds:
                    continue
                normalized = {
                    'id': pid,
                    'name': str(proj.get('name') or pid),
                    'dataset_id': ds,
                }
                by_id[pid] = normalized
                by_dataset[ds] = normalized
        bound_filter = None
        if project_id is not None:
            proj = by_id.get(project_id)
            if proj is None:
                with self._lock:
                    started_at = self.db.execute('SELECT started_at FROM drift_meta WHERE singleton=1').fetchone()[0]
                return {'started_at': started_at, 'items': []}
            bound_filter = proj['dataset_id']

        with self._lock:
            started_at = self.db.execute('SELECT started_at FROM drift_meta WHERE singleton=1').fetchone()[0]
            if bound_filter is not None:
                hit_rows = self.db.execute(
                    '''SELECT bound_dataset_id, session_id, target_project_id,
                              read_count, write_count, command_count, updated_at
                       FROM session_project_hits WHERE bound_dataset_id=?''',
                    (bound_filter,),
                ).fetchall()
                disp_rows = self.db.execute(
                    '''SELECT bound_dataset_id, session_id, status
                       FROM session_drift_disposition WHERE bound_dataset_id=?''',
                    (bound_filter,),
                ).fetchall()
            else:
                hit_rows = self.db.execute(
                    '''SELECT bound_dataset_id, session_id, target_project_id,
                              read_count, write_count, command_count, updated_at
                       FROM session_project_hits'''
                ).fetchall()
                disp_rows = self.db.execute(
                    '''SELECT bound_dataset_id, session_id, status
                       FROM session_drift_disposition'''
                ).fetchall()

        dismissed = {(bds, sid) for bds, sid, status in disp_rows if status == 'dismissed'}
        grouped = {}
        for bds, sid, tid, r_cnt, w_cnt, c_cnt, updated_at in hit_rows:
            if bds not in by_dataset or tid not in by_id:
                continue
            bucket = grouped.setdefault((bds, sid), {'counts': {}, 'updated_at': updated_at})
            bucket['counts'][tid] = (int(r_cnt), int(w_cnt), int(c_cnt))
            if updated_at > bucket['updated_at']:
                bucket['updated_at'] = updated_at

        items = []
        for (bds, sid), info in grouped.items():
            if not include_dismissed and (bds, sid) in dismissed:
                continue
            bound_proj = by_dataset[bds]
            bound_id = bound_proj['id']
            counts = info['counts']
            all_known = sum(r + w + c for r, w, c in counts.values())
            if all_known <= 0:
                continue
            bound_r, bound_w, bound_c = counts.get(bound_id, (0, 0, 0))
            bound_total = bound_r + bound_w + bound_c
            candidates = []
            for tid, (r_cnt, w_cnt, c_cnt) in counts.items():
                if tid == bound_id:
                    continue
                total = r_cnt + w_cnt + c_cnt
                if total <= 0:
                    continue
                candidates.append((1 if w_cnt > 0 else 0, total, w_cnt, tid, r_cnt, w_cnt, c_cnt))
            if not candidates:
                continue
            candidates.sort(key=lambda row: (row[0], row[1], row[2], row[3]), reverse=True)
            _, target_total, _, target_id, target_r, target_w, target_c = candidates[0]
            share = target_total / all_known
            reason = None
            if target_w >= 1 and bound_w == 0 and target_total >= 2 and share >= 0.75:
                reason = 'write_drift'
            elif target_total >= 4 and bound_total == 0:
                reason = 'exclusive_other_project'
            elif target_total >= 6 and bound_w == 0 and share >= 0.85:
                reason = 'dominant_other_project'
            if reason is None:
                continue
            target_proj = by_id[target_id]
            items.append({
                'session_id': sid,
                'bound_project_id': bound_id,
                'bound_project_name': bound_proj['name'],
                'bound_dataset_id': bds,
                'target_project_id': target_id,
                'target_project_name': target_proj['name'],
                'target_dataset_id': target_proj['dataset_id'],
                'target_read_count': target_r,
                'target_write_count': target_w,
                'target_command_count': target_c,
                'bound_total_count': bound_total,
                'updated_at': info['updated_at'],
                'reason': reason,
            })

        items.sort(key=lambda row: row['session_id'])
        items.sort(key=lambda row: row['updated_at'], reverse=True)
        if isinstance(limit, int) and limit >= 0:
            items = items[:limit]
        return {'started_at': started_at, 'items': items}

    def dismiss(self, bound_dataset_id, session_id, target_project_id):
        dataset = _uuid_str(bound_dataset_id)
        if not dataset:
            raise PolicyError('invalid_dataset_id')
        if (not isinstance(session_id, str) or not session_id.strip() or len(session_id) > _MAX_SESSION_ID_LEN
                or not isinstance(target_project_id, str) or not target_project_id.strip()
                or len(target_project_id) > _MAX_SESSION_ID_LEN):
            raise PolicyError('invalid_request_fields')
        now = _iso_now()
        with self._lock:
            self.db.execute('BEGIN IMMEDIATE')
            try:
                self.db.execute(
                    '''INSERT OR REPLACE INTO session_drift_disposition
                       (bound_dataset_id, session_id, status, target_project_id, updated_at)
                       VALUES(?,?,?,?,?)''',
                    (dataset, session_id.strip(), 'dismissed', target_project_id.strip(), now),
                )
                self.db.execute('COMMIT')
            except Exception:
                if self.db.in_transaction:
                    self.db.execute('ROLLBACK')
                raise

    def checkpoint(self, mode: str = 'TRUNCATE') -> bool:
        """Run WAL checkpoint to truncate WAL and prevent unbounded growth, safely handling locks."""
        with self._lock:
            try:
                mode_str = str(mode or 'TRUNCATE').upper()
                if mode_str not in ('PASSIVE', 'FULL', 'RESTART', 'TRUNCATE'):
                    mode_str = 'TRUNCATE'
                cur = self.db.execute(f'PRAGMA wal_checkpoint({mode_str})')
                row = cur.fetchone()
                return bool(row and row[0] == 0)
            except (sqlite3.OperationalError, sqlite3.Error):
                return False
            except Exception:
                return False

    def close(self):
        with self._lock:
            try:
                self.db.execute('PRAGMA wal_checkpoint(TRUNCATE)')
            except Exception:
                pass
            self.db.close()


def attach_session_drift(app, registry):
    """Attach read-only/dismissal session drift endpoints and return the shared store."""
    store = SessionDriftStore(registry.path.parent / 'session-drift.sqlite3')

    async def cleanup(_):
        store.close()

    app.on_cleanup.append(cleanup)

    async def project_drifts(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        async with registry.mutex:
            project = registry.state['projects'].get(request.match_info['id'])
            if project is None:
                raise PolicyError('project_not_found', 404)
            projects_map = copy.deepcopy(registry.state['projects'])
        return web.json_response(store.evaluate_project_drifts(projects_map, project_id=project['id']))

    async def dismiss_drift(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        if request.content_type != 'application/json':
            raise PolicyError('json_required', 415)
        payload = await request.json()
        if (not isinstance(payload, dict) or set(payload) != {'session_id', 'target_project_id'}
                or not isinstance(payload.get('session_id'), str) or not payload['session_id'].strip()
                or not isinstance(payload.get('target_project_id'), str) or not payload['target_project_id'].strip()):
            raise PolicyError('invalid_request_fields')
        async with registry.mutex:
            project = registry.state['projects'].get(request.match_info['id'])
            if project is None:
                raise PolicyError('project_not_found', 404)
            bound_dataset_id = project.get('dataset_id')
            if not bound_dataset_id:
                raise PolicyError('project_not_found', 404)
            projects_map = copy.deepcopy(registry.state['projects'])
        store.dismiss(bound_dataset_id, payload['session_id'], payload['target_project_id'])
        return web.json_response(store.evaluate_project_drifts(projects_map, project_id=project['id']))

    async def all_drifts(request):
        if not request['manager']:
            raise PolicyError('management_required', 403)
        async with registry.mutex:
            projects_map = copy.deepcopy(registry.state['projects'])
        return web.json_response(store.evaluate_project_drifts(projects_map))

    app.router.add_get('/v1/projects/{id}/session-drift', project_drifts)
    app.router.add_post('/v1/projects/{id}/session-drift/dismiss', dismiss_drift)
    app.router.add_get('/v1/session-drift', all_drifts)
    return store
