"""Shared incremental (inode, offset) log tailer for diagnostic JSONL streams.

Eliminates repeated 1 MiB–20 MiB rescans on every poll across v1/v2 notice
endpoints while handling log rotation, live-inode truncation, and in-place
rewrites safely.
"""
from collections import deque
import json
import os
from pathlib import Path
import re
import stat
import threading

DEFAULT_TAIL_BYTES = 1024 * 1024
DEFAULT_MAX_ROWS = 4096
_MAX_LINE_BYTES = 8192
_ANCHOR_BYTES = 64
_REQUEST_ID_RE = re.compile(r'[0-9a-f]{32}')


def parse_request_finished_row(line: bytes):
    """Default parser matching ``processing_notices.trace_rows`` schema."""
    if len(line) > _MAX_LINE_BYTES or not line.startswith(b'{'):
        return None
    try:
        row = json.loads(line)
    except (ValueError, TypeError):
        return None
    if (
        isinstance(row, dict)
        and row.get('schema_version') == 1
        and row.get('event') == 'request_finished'
        and _REQUEST_ID_RE.fullmatch(str(row.get('request_id', '')))
    ):
        return row
    return None


class _FileTailState:
    __slots__ = (
        'dev',
        'ino',
        'offset',
        'size',
        'mtime_ns',
        'anchor',
        'rows',
        'pending_rows',
    )

    def __init__(self, *, dev, ino, offset, size, mtime_ns, anchor, rows, pending_rows):
        self.dev = dev
        self.ino = ino
        self.offset = offset
        self.size = size
        self.mtime_ns = mtime_ns
        self.anchor = anchor
        self.rows = rows
        self.pending_rows = pending_rows


class IncrementalLogTailer:
    """Thread-safe incremental log tailer keyed by normalized file path."""

    def __init__(
        self,
        *,
        tail_bytes: int = DEFAULT_TAIL_BYTES,
        max_rows: int = DEFAULT_MAX_ROWS,
        row_parser=parse_request_finished_row,
    ):
        self.tail_bytes = max(4096, int(tail_bytes))
        self.max_rows = max(64, int(max_rows))
        self.row_parser = row_parser
        self._lock = threading.Lock()
        self._states: dict[str, _FileTailState] = {}

    def reset(self, path=None):
        with self._lock:
            if path is None:
                self._states.clear()
            else:
                self._states.pop(str(Path(path)), None)

    def _parse_chunk(self, data: bytes, *, drop_first_line: bool) -> tuple[list, int, list]:
        """Parse complete lines up to the last newline, returning (rows, consumed_bytes, pending_rows)."""
        if not data:
            return [], 0, []
        last_nl = data.rfind(b'\n')
        if last_nl < 0:
            if drop_first_line:
                return [], 0, []
            parsed = self.row_parser(data.rstrip(b'\r'))
            return [], 0, ([parsed] if parsed is not None else [])

        complete = data[: last_nl + 1]
        trailing = data[last_nl + 1 :]
        lines = complete.splitlines()
        if drop_first_line and lines:
            lines = lines[1:]

        rows = []
        for line in lines:
            parsed = self.row_parser(line)
            if parsed is not None:
                rows.append(parsed)

        pending = []
        if trailing:
            parsed_tail = self.row_parser(trailing.rstrip(b'\r'))
            if parsed_tail is not None:
                pending.append(parsed_tail)
        return rows, last_nl + 1, pending

    def _full_tail_read(self, handle, size: int) -> tuple[int, bytes, deque, list]:
        start = max(0, size - self.tail_bytes)
        drop_first_line = False
        if start > 0:
            handle.seek(start - 1)
            prev_byte = handle.read(1)
            drop_first_line = prev_byte != b'\n'
        else:
            handle.seek(0)
        data = handle.read(size - start)
        rows, consumed, pending = self._parse_chunk(data, drop_first_line=drop_first_line)
        new_offset = start + consumed
        anchor_start = max(0, consumed - _ANCHOR_BYTES)
        anchor = data[anchor_start:consumed] if consumed > 0 else b''
        ring = deque(rows[-self.max_rows :], maxlen=self.max_rows)
        return new_offset, anchor, ring, pending

    def read_rows(self, path) -> list:
        target = Path(path)
        key = str(target)
        try:
            info = target.lstat()
        except FileNotFoundError:
            with self._lock:
                self._states.pop(key, None)
            return []
        except OSError:
            raise

        if stat.S_ISLNK(info.st_mode):
            with self._lock:
                self._states.pop(key, None)
            raise OSError('diagnostic_log_symlink')
        if not stat.S_ISREG(info.st_mode):
            with self._lock:
                self._states.pop(key, None)
            return []

        with self._lock:
            state = self._states.get(key)
            if (
                state is not None
                and state.dev == info.st_dev
                and state.ino == info.st_ino
                and state.size == info.st_size
                and state.mtime_ns == info.st_mtime_ns
            ):
                if state.pending_rows:
                    return [*state.rows, *state.pending_rows]
                return list(state.rows)

            flags = os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0)
            try:
                fd = os.open(target, flags)
            except FileNotFoundError:
                self._states.pop(key, None)
                return []

            try:
                finfo = os.fstat(fd)
            except OSError:
                os.close(fd)
                raise

            if not stat.S_ISREG(finfo.st_mode):
                os.close(fd)
                self._states.pop(key, None)
                return []

            dev = finfo.st_dev
            ino = finfo.st_ino
            size = finfo.st_size
            mtime_ns = finfo.st_mtime_ns

            if (
                state is not None
                and state.dev == dev
                and state.ino == ino
                and state.size == size
                and state.mtime_ns == mtime_ns
            ):
                os.close(fd)
                if state.pending_rows:
                    return [*state.rows, *state.pending_rows]
                return list(state.rows)

            with os.fdopen(fd, 'rb') as handle:
                can_increment = (
                    state is not None
                    and state.dev == dev
                    and state.ino == ino
                    and size >= state.offset
                    and (size - state.offset) <= self.tail_bytes
                )
                if can_increment and state.offset > 0 and state.anchor:
                    anchor_pos = state.offset - len(state.anchor)
                    handle.seek(anchor_pos)
                    observed_anchor = handle.read(len(state.anchor))
                    if observed_anchor != state.anchor:
                        can_increment = False

                if can_increment:
                    handle.seek(state.offset)
                    delta = handle.read(size - state.offset)
                    new_rows, consumed, pending = self._parse_chunk(delta, drop_first_line=False)
                    new_offset = state.offset + consumed
                    if consumed > 0:
                        tail_bytes_combined = state.anchor + delta[:consumed]
                        new_anchor = tail_bytes_combined[-_ANCHOR_BYTES:]
                    else:
                        new_anchor = state.anchor
                    ring = state.rows
                    for item in new_rows:
                        ring.append(item)
                else:
                    new_offset, new_anchor, ring, pending = self._full_tail_read(handle, size)

            self._states[key] = _FileTailState(
                dev=dev,
                ino=ino,
                offset=new_offset,
                size=size,
                mtime_ns=mtime_ns,
                anchor=new_anchor,
                rows=ring,
                pending_rows=pending,
            )
            if pending:
                return [*ring, *pending]
            return list(ring)


SHARED_TRACE_TAILER = IncrementalLogTailer()
