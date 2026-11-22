"""Unit tests for shared IncrementalLogTailer rotation, truncation, and delta reads."""
import json
from pathlib import Path
import tempfile
import unittest

from log_tailer import IncrementalLogTailer


def _trace_line(req_hex: str, status: int = 502) -> str:
    return json.dumps({
        'schema_version': 1,
        'event': 'request_finished',
        'component': 'generation_http',
        'request_id': req_hex * 32,
        'status': status,
    }) + '\n'


class IncrementalLogTailerTests(unittest.TestCase):
    def test_incremental_append_truncation_rotation_and_inplace_rewrite(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            log_path = root / 'official_service_dev.log'
            tailer = IncrementalLogTailer(tail_bytes=8192, max_rows=100)

            # Missing file returns empty list
            self.assertEqual(tailer.read_rows(log_path), [])

            # Initial write
            log_path.write_text(_trace_line('a', 502), encoding='utf-8')
            rows1 = tailer.read_rows(log_path)
            self.assertEqual([r['request_id'] for r in rows1], ['a' * 32])
            offset_after_1 = tailer._states[str(log_path)].offset

            # Unchanged poll hits cache without moving offset
            rows1_cached = tailer.read_rows(log_path)
            self.assertEqual(rows1_cached, rows1)
            self.assertEqual(tailer._states[str(log_path)].offset, offset_after_1)

            # Append second line -> incremental read advances offset
            with log_path.open('a', encoding='utf-8') as handle:
                handle.write(_trace_line('b', 503))
            rows2 = tailer.read_rows(log_path)
            self.assertEqual([r['request_id'] for r in rows2], ['a' * 32, 'b' * 32])
            offset_after_2 = tailer._states[str(log_path)].offset
            self.assertGreater(offset_after_2, offset_after_1)

            # Truncation (size < offset) resets state cleanly
            log_path.write_text(_trace_line('c', 500), encoding='utf-8')
            rows3 = tailer.read_rows(log_path)
            self.assertEqual([r['request_id'] for r in rows3], ['c' * 32])

            # In-place overwrite growing LARGER than previous offset (same inode) resets via anchor mismatch
            bigger_payload = _trace_line('d', 504) + _trace_line('e', 502)
            log_path.write_text(bigger_payload, encoding='utf-8')
            rows4 = tailer.read_rows(log_path)
            self.assertEqual([r['request_id'] for r in rows4], ['d' * 32, 'e' * 32])

            # Log rotation via rename + new file (new inode) resets cleanly
            rotated = root / 'official_service_dev.log.1'
            log_path.rename(rotated)
            log_path.write_text(_trace_line('f', 502) + _trace_line('1', 503) + _trace_line('2', 504), encoding='utf-8')
            rows5 = tailer.read_rows(log_path)
            self.assertEqual([r['request_id'] for r in rows5], ['f' * 32, '1' * 32, '2' * 32])

    def test_partial_trailing_line_and_symlink_rejection(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            log_path = root / 'stream.log'
            tailer = IncrementalLogTailer()

            # Write partial line without trailing newline
            partial = _trace_line('a', 502).rstrip('\n')
            log_path.write_text(partial, encoding='utf-8')
            rows_partial = tailer.read_rows(log_path)
            self.assertEqual([r['request_id'] for r in rows_partial], ['a' * 32])
            self.assertEqual(tailer._states[str(log_path)].offset, 0)

            # Finish newline and append another line
            with log_path.open('a', encoding='utf-8') as handle:
                handle.write('\n' + _trace_line('b', 503))
            rows_complete = tailer.read_rows(log_path)
            self.assertEqual([r['request_id'] for r in rows_complete], ['a' * 32, 'b' * 32])
            self.assertGreater(tailer._states[str(log_path)].offset, 0)

            # Symlink must fail closed with OSError
            outside = root / 'outside.log'
            outside.write_text(_trace_line('c', 502), encoding='utf-8')
            sym = root / 'symlink.log'
            sym.symlink_to(outside)
            with self.assertRaises(OSError):
                tailer.read_rows(sym)

    def test_tail_read_preserves_first_line_on_exact_line_boundary_and_drops_partial_midline(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            tailer = IncrementalLogTailer(tail_bytes=4096, max_rows=100)

            # Case A: file tail start (size - 4096) lands EXACTLY after '\n' on first byte of line_b.
            line_b = _trace_line('b', 502)
            line_c = _trace_line('c', 503)
            tail_content = (line_b + line_c).encode('utf-8')
            pad_len = 4096 - len(tail_content)
            # Fill tail window to exact 4096 bytes after prefix ending with '\n'.
            prefix = ('x' * 500 + '\n').encode('utf-8')
            filler_line = ('z' * (pad_len - 1) + '\n').encode('utf-8')
            # Place line_b RIGHT at offset = size - 4096!
            log_boundary = root / 'boundary.log'
            body_a = prefix + line_b.encode('utf-8') + filler_line + line_c.encode('utf-8')
            # Adjust filler_line so len(line_b + filler_line + line_c) == 4096 exactly!
            filler_exact = ('z' * (4096 - len(line_b.encode('utf-8')) - len(line_c.encode('utf-8')) - 1) + '\n').encode('utf-8')
            body_a = prefix + line_b.encode('utf-8') + filler_exact + line_c.encode('utf-8')
            self.assertEqual(len(body_a) - 4096, len(prefix))
            self.assertEqual(body_a[len(prefix) - 1:len(prefix)], b'\n')
            log_boundary.write_bytes(body_a)

            rows_a = tailer.read_rows(log_boundary)
            self.assertEqual([r['request_id'] for r in rows_a], ['b' * 32, 'c' * 32])

            # Case B: start (size - 4096) lands mid-line (byte before start is NOT '\n') -> drops truncated first line.
            log_mid = root / 'midline.log'
            body_b = ('x' * 500).encode('utf-8') + line_b.encode('utf-8') + filler_exact + line_c.encode('utf-8')
            # Offset len(body_b) - 4096 lands right at line_b[0], preceded by 'x' (NOT '\n'), so line_b is a suffix of a cut line.
            self.assertNotEqual(body_b[len(body_b) - 4096 - 1:len(body_b) - 4096], b'\n')
            log_mid.write_bytes(body_b)

            rows_b = tailer.read_rows(log_mid)
            self.assertEqual([r['request_id'] for r in rows_b], ['c' * 32])

    def test_rotation_between_lstat_and_open_uses_fstat_identity(self):
        from unittest.mock import patch

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            log_path = root / 'rotate-race.log'
            log_path.write_text(_trace_line('1', 502), encoding='utf-8')
            tailer = IncrementalLogTailer(tail_bytes=8192, max_rows=100)
            self.assertEqual([r['request_id'] for r in tailer.read_rows(log_path)], ['1' * 32])

            # Append to old file so lstat misses cache, then swap file right after lstat returns!
            with log_path.open('a', encoding='utf-8') as h:
                h.write(_trace_line('2', 502))
            old_lstat = Path.lstat
            rotated = [False]

            def racing_lstat(path_self):
                info = old_lstat(path_self)
                if path_self == log_path and not rotated[0]:
                    rotated[0] = True
                    path_self.rename(root / 'rotate-race.log.1')
                    path_self.write_text(_trace_line('9', 504), encoding='utf-8')
                return info

            with patch.object(Path, 'lstat', racing_lstat):
                rows = tailer.read_rows(log_path)

            new_stat = log_path.stat()
            state = tailer._states[str(log_path)]
            self.assertEqual([r['request_id'] for r in rows], ['9' * 32])
            self.assertEqual(state.ino, new_stat.st_ino)
            self.assertEqual(state.size, new_stat.st_size)


if __name__ == '__main__':
    unittest.main()
