"""External SQLite WAL capture receipt ledger and read-only session reconciliation.

Replaces source edits on Cognee's ``SessionManager`` / ``SqlCacheAdapter``. Stores
only capture scope hashes, SHA-256 digests, status, and official entry IDs
(plus an optional integer ``baseline_count`` for QA crash reconciliation), never
message bodies or conversation text.
"""
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import sqlite3

from capture_receipts import CaptureConflict

CAPTURE_MARKER_KEY = '_memlane_capture'
_DATE_PREFIX_RE = re.compile(r'^\d{4}-\d{2}-\d{2}$')


def _iso_now():
    return datetime.now(timezone.utc).isoformat(timespec='microseconds')


def _same_timestamp(lhs, rhs):
    if lhs is None or rhs is None:
        return False
    if str(lhs) == str(rhs):
        return True
    try:
        dl = datetime.fromisoformat(str(lhs).replace('Z', '+00:00'))
        dr = datetime.fromisoformat(str(rhs).replace('Z', '+00:00'))
        if dl.tzinfo is None:
            dl = dl.replace(tzinfo=timezone.utc)
        if dr.tzinfo is None:
            dr = dr.replace(tzinfo=timezone.utc)
        return dl == dr
    except (ValueError, TypeError):
        return False


def _field(row, name):
    return row.get(name) if isinstance(row, dict) else getattr(row, name, None)


class ExternalCaptureStore:
    """Private ``0600`` SQLite WAL ledger tracking idempotent capture receipts."""

    def __init__(self, path):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        os.chmod(self.path.parent, 0o700)
        if self.path.is_symlink():
            raise ValueError('capture ledger database must not be a symlink')
        fd = os.open(self.path, os.O_CREAT | os.O_RDWR | getattr(os, 'O_NOFOLLOW', 0), 0o600)
        os.close(fd)
        os.chmod(self.path, 0o600)
        self.db = sqlite3.connect(self.path, isolation_level=None, check_same_thread=False)
        self.db.execute('PRAGMA journal_mode=WAL')
        self.db.execute('PRAGMA journal_size_limit=4194304')
        self.db.execute('PRAGMA wal_autocheckpoint=1000')
        self.db.execute('PRAGMA synchronous=FULL')
        self.db.execute('PRAGMA busy_timeout=5000')
        self.db.executescript('''
          CREATE TABLE IF NOT EXISTS capture_receipts (
            capture_key TEXT PRIMARY KEY,
            user_id TEXT NOT NULL,
            dataset_id TEXT NOT NULL,
            session_id TEXT NOT NULL,
            message_id TEXT NOT NULL,
            payload_sha256 TEXT NOT NULL,
            entry_type TEXT NOT NULL,
            entry_id TEXT,
            baseline_count INTEGER,
            status TEXT NOT NULL CHECK(status IN ('pending','saved')),
            updated_at TEXT NOT NULL
          );
        ''')

    @staticmethod
    def _row_to_dict(row):
        (
            capture_key, user_id, dataset_id, session_id, message_id,
            payload_sha256, entry_type, entry_id, baseline_count, status, updated_at,
        ) = row
        info = {
            'capture_key': capture_key,
            'user_id': user_id,
            'dataset_id': dataset_id,
            'session_id': session_id,
            'message_id': message_id,
            'payload_sha256': payload_sha256,
            'entry_type': entry_type,
            'entry_id': entry_id,
            'baseline_count': baseline_count,
            'status': status,
            'updated_at': updated_at,
        }
        if status == 'saved' and entry_id:
            info['receipt'] = {
                'protocol': 1,
                'message_id': message_id,
                'payload_sha256': payload_sha256,
                'entry_id': entry_id,
                'entry_type': entry_type,
                'session_id': session_id,
                'dataset_id': dataset_id,
                'status': 'saved',
            }
        return info

    def get(self, capture_key):
        row = self.db.execute(
            '''SELECT capture_key,user_id,dataset_id,session_id,message_id,
                      payload_sha256,entry_type,entry_id,baseline_count,status,updated_at
               FROM capture_receipts WHERE capture_key=?''',
            (capture_key,),
        ).fetchone()
        return self._row_to_dict(row) if row is not None else None

    def reserve(self, capture, *, baseline_count=None):
        now = _iso_now()
        self.db.execute('BEGIN IMMEDIATE')
        try:
            row = self.db.execute(
                '''SELECT capture_key,user_id,dataset_id,session_id,message_id,
                          payload_sha256,entry_type,entry_id,baseline_count,status,updated_at
                   FROM capture_receipts WHERE capture_key=?''',
                (capture.key,),
            ).fetchone()
            if row is None:
                self.db.execute(
                    '''INSERT INTO capture_receipts
                       (capture_key,user_id,dataset_id,session_id,message_id,
                        payload_sha256,entry_type,entry_id,baseline_count,status,updated_at)
                       VALUES(?,?,?,?,?,?,?,?,?,?,?)''',
                    (
                        capture.key, capture.user, capture.dataset, capture.session,
                        capture.message, capture.digest, capture.kind,
                        None, baseline_count, 'pending', now,
                    ),
                )
                created = self.db.execute(
                    '''SELECT capture_key,user_id,dataset_id,session_id,message_id,
                              payload_sha256,entry_type,entry_id,baseline_count,status,updated_at
                       FROM capture_receipts WHERE capture_key=?''',
                    (capture.key,),
                ).fetchone()
                self.db.execute('COMMIT')
                return 'reserved', self._row_to_dict(created)
            info = self._row_to_dict(row)
            if (info['payload_sha256'] != capture.digest or info['entry_type'] != capture.kind
                    or info['dataset_id'] != capture.dataset or info['session_id'] != capture.session
                    or info['user_id'] != capture.user or info['message_id'] != capture.message):
                raise CaptureConflict('message ID already used for another payload')
            self.db.execute('COMMIT')
            return info['status'], info
        except Exception:
            if self.db.in_transaction:
                self.db.execute('ROLLBACK')
            raise

    def mark_saved(self, capture, entry_id):
        now = _iso_now()
        self.db.execute('BEGIN IMMEDIATE')
        try:
            self.db.execute(
                '''UPDATE capture_receipts SET status='saved', entry_id=?, updated_at=?
                   WHERE capture_key=?''',
                (str(entry_id), now, capture.key),
            )
            self.db.execute('COMMIT')
        except Exception:
            if self.db.in_transaction:
                self.db.execute('ROLLBACK')
            raise
        return capture.receipt(entry_id=str(entry_id))

    def abort_pending(self, capture):
        self.db.execute('BEGIN IMMEDIATE')
        try:
            self.db.execute(
                "DELETE FROM capture_receipts WHERE capture_key=? AND status='pending'",
                (capture.key,),
            )
            self.db.execute('COMMIT')
        except Exception:
            if self.db.in_transaction:
                self.db.execute('ROLLBACK')
            raise

    def checkpoint(self, mode: str = 'TRUNCATE') -> bool:
        """Run WAL checkpoint to truncate WAL and prevent unbounded growth, safely handling locks."""
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
        try:
            self.checkpoint('TRUNCATE')
        except Exception:
            pass
        self.db.close()


def attach_capture_marker(entry, capture):
    """Attach an opaque dict capture key onto TraceEntry ``method_params``; leave QAEntry untouched."""
    if getattr(entry, 'type', None) == 'trace':
        existing = dict(entry.method_params) if isinstance(entry.method_params, dict) else {}
        existing[CAPTURE_MARKER_KEY] = {'key': capture.key}
        return entry.model_copy(update={'method_params': existing})
    return entry


def _qa_local_date(entry):
    raw_ts = _field(entry, 'prompt_time') or _field(entry, 'time')
    if isinstance(raw_ts, datetime):
        dt = raw_ts if raw_ts.tzinfo is not None else raw_ts.replace(tzinfo=timezone.utc)
        return dt.astimezone().strftime('%Y-%m-%d')
    if isinstance(raw_ts, str) and raw_ts.strip():
        text = raw_ts.strip()
        if _DATE_PREFIX_RE.match(text):
            return text
        try:
            dt = datetime.fromisoformat(text.replace('Z', '+00:00'))
            if dt.tzinfo is None:
                dt = dt.replace(tzinfo=timezone.utc)
            return dt.astimezone().strftime('%Y-%m-%d')
        except ValueError:
            pass
    return datetime.now().astimezone().strftime('%Y-%m-%d')


def ensure_qa_context_date(entry):
    """Populate ``context.date`` externally on QA entries when absent without touching Cognee."""
    if _field(entry, 'type') != 'qa':
        return entry
    context = _field(entry, 'context')
    day = _qa_local_date(entry)
    updated_context = None
    if context is None or (isinstance(context, str) and not context.strip()):
        updated_context = json.dumps({'date': day}, ensure_ascii=False)
    elif isinstance(context, dict):
        if isinstance(context.get('date'), str) and context['date'].strip():
            return entry
        updated_context = {**context, 'date': day}
    elif isinstance(context, str):
        stripped = context.strip()
        if not stripped.startswith('{'):
            return entry
        try:
            parsed = json.loads(stripped)
        except ValueError:
            return entry
        if not isinstance(parsed, dict):
            return entry
        if isinstance(parsed.get('date'), str) and parsed['date'].strip():
            return entry
        parsed['date'] = day
        updated_context = json.dumps(parsed, ensure_ascii=False)
    else:
        return entry
    if isinstance(entry, dict):
        return {**entry, 'context': updated_context}
    return entry.model_copy(update={'context': updated_context})


async def find_committed_capture(session_manager, capture, entry=None, *, baseline_count=None, alt_contexts=()):
    """Read-only reconciliation checking whether an ambiguous remember already committed."""
    if not getattr(session_manager, 'is_available', False):
        return None
    try:
        if capture.kind == 'trace':
            traces = await session_manager.get_agent_trace_session(
                user_id=capture.user, session_id=capture.session,
            )
            if not isinstance(traces, list):
                return None
            for row in traces:
                mp = _field(row, 'method_params')
                marker = mp.get(CAPTURE_MARKER_KEY) if isinstance(mp, dict) else None
                if isinstance(marker, dict) and marker.get('key') == capture.key:
                    found_id = _field(row, 'trace_id') or _field(row, 'id')
                    return str(found_id) if found_id else capture.entry_id
            return None
        if capture.kind == 'qa' and entry is not None:
            qas = await session_manager.get_session(
                user_id=capture.user, session_id=capture.session, formatted=False,
            )
            if not isinstance(qas, list):
                return None
            allowed_contexts = {(entry.context or ''), *((c or '') for c in (alt_contexts or ()))}
            candidates = qas[baseline_count:] if isinstance(baseline_count, int) and 0 <= baseline_count <= len(qas) else qas
            for row in candidates:
                if (_field(row, 'question') == entry.question
                        and _field(row, 'answer') == entry.answer
                        and (_field(row, 'context') or '') in allowed_contexts):
                    if entry.time is not None and not _same_timestamp(_field(row, 'time'), entry.time):
                        continue
                    if entry.prompt_time is not None and not _same_timestamp(_field(row, 'prompt_time'), entry.prompt_time):
                        continue
                    found_id = _field(row, 'qa_id') or _field(row, 'id')
                    return str(found_id) if found_id else capture.entry_id
            return None
    except Exception:
        return None
    return None
